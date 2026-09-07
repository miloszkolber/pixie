import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { SESSION_LIVENESS_EVENT, Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";
import { makeProvider } from "./provider-fixture.ts";

const cleanups: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

async function fixture(extension: ExtensionFactory) {
	const dir = await mkdtemp(join(tmpdir(), "pixie-lifecycle-"));
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const events: Record<string, unknown>[] = [];
	const sessions = new Sessions(
		dir,
		[makeProvider(), extension],
		(_id, event) => {
			events.push(event as Record<string, unknown>);
		},
		{ maxIdle: 0, idleMs: 0 },
	);
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(dir);
	const model = entry.modelRuntime.getModel("fixture", "echo");
	if (!model) throw new Error("Missing fixture model");
	await entry.session.setModel(model);
	const id = entry.session.sessionId;
	const prompt = (text: string) =>
		sessions.call("session.prompt", {
			sessionId: id,
			content: [{ type: "text", text }],
		});
	return { dir, sessions, entry, id, events, prompt };
}

async function until(check: () => boolean) {
	for (let i = 0; i < 200 && !check(); i++) await Bun.sleep(5);
	expect(check()).toBe(true);
}

for (const mode of ["wait-for-ui", "reject", "stall"] as const) {
	test(`prompt context dialog is cancelled before abort: ${mode}`, async () => {
		let dismissed = false;
		let entered = false;
		const { sessions, entry, id, events, prompt } = await fixture((pi) => {
			pi.on("context", async (_event, ctx) => {
				entered = true;
				expect(await ctx.ui.confirm("Context", "Continue?")).toBe(false);
				dismissed = true;
			});
		});
		const run = prompt("Ask in context");
		void run.catch(() => {});
		const originalAbort = entry.session.abort.bind(entry.session);
		try {
			await until(() => entered && events.some((e) => e.type === "pixie:ui:request"));
			expect(entry.run).toBeDefined();
			entry.session.abort = async () => {
				// Assert at abort invocation, before yielding to the context continuation.
				expect(sessions.snapshot(entry).pendingDialogs).toEqual([]);
				expect(events.filter((e) => e.type === "pixie:ui:cancel")).toHaveLength(1);
				if (mode === "reject") throw new Error("fixture abort rejection");
				if (mode === "stall") return new Promise<void>(() => {});
				await until(() => dismissed);
				await originalAbort();
			};
			const started = Date.now();
			const cancel = sessions.call("session.cancel", { sessionId: id });
			if (mode === "reject") await expect(cancel).rejects.toThrow("fixture abort rejection");
			else expect(await cancel).toEqual({ ok: true, aborted: mode !== "stall" });
			expect(Date.now() - started).toBeLessThan(mode === "stall" ? 15_000 : 2_000);
			await until(() => dismissed);
			expect(sessions.snapshot(entry).pendingDialogs).toEqual([]);
			const request = events.find((e) => e.type === "pixie:ui:request");
			if (!request) throw new Error("Missing dialog request");
			await expect(
				sessions.resolveUiResponse({ sessionId: id, requestId: request.requestId, value: true }),
			).rejects.toThrow("Unknown or settled");
			if (mode === "stall") expect(events.some((e) => e.type === "run_abort_timeout")).toBe(true);
		} finally {
			entry.session.abort = originalAbort;
			const requestId = events.find((e) => e.type === "pixie:ui:request")?.requestId;
			if (requestId) await sessions.cancelUiRequest({ sessionId: id, requestId });
			await originalAbort();
			await run.catch(() => {});
		}
	}, 20_000);
}

test("pending UI protects a prompt-idle native session from release and sweep until answered", async () => {
	let answer: Promise<string | undefined> | undefined;
	const { dir, sessions, entry, id, prompt } = await fixture((pi) => {
		pi.registerCommand("dialog", {
			description: "Leave an extension-owned dialog pending",
			handler: async (_args, ctx) => {
				answer = ctx.ui.input("Background input");
			},
		});
	});
	await prompt("/dialog");
	expect(entry.run).toBeUndefined();
	expect(entry.refs).toBe(0);
	expect(entry.session.isIdle).toBe(true);
	const other = await sessions.create(dir);
	await sessions.release(id);
	await sessions.sweep(Date.now() + 600000);
	expect(sessions.entries.get(id)).toBe(entry);
	expect(sessions.entries.has(other.session.sessionId)).toBe(false);
	const [request] = sessions.snapshot(entry).pendingDialogs as { requestId: string }[];
	await sessions.resolveUiResponse({ sessionId: id, requestId: request.requestId, value: "done" });
	expect(await answer).toBe("done");
	await sessions.sweep(Date.now() + 600000);
	expect(sessions.entries.has(id)).toBe(false);
	expect((await sessions.get(id)).session.sessionId).toBe(id);
});

test("native event-bus work pins only its session and releases independently of prompt settlement", async () => {
	let finish!: () => void;
	let work: Promise<void> | undefined;
	const gate = new Promise<void>((resolve) => {
		finish = resolve;
	});
	const { dir, sessions, entry, id, prompt } = await fixture((pi) => {
		pi.registerCommand("work", {
			description: "Start extension-owned work",
			handler: async () => {
				pi.events.emit(SESSION_LIVENESS_EVENT, { key: "fixture:work", active: true });
				pi.events.emit(SESSION_LIVENESS_EVENT, { key: "fixture:work", active: true });
				work = gate.finally(() => {
					pi.events.emit(SESSION_LIVENESS_EVENT, { key: "fixture:work", active: false });
				});
			},
		});
	});
	try {
		await prompt("/work");
		expect(entry.run).toBeUndefined();
		expect(entry.refs).toBe(0);
		expect(entry.session.isIdle).toBe(true);
		const other = await sessions.create(dir);
		await sessions.release(id);
		await sessions.sweep(Date.now() + 600000);
		expect(sessions.entries.get(id)).toBe(entry);
		expect(sessions.entries.has(other.session.sessionId)).toBe(false);
	} finally {
		finish();
		await work;
	}
	await sessions.release(id);
	expect(sessions.entries.has(id)).toBe(false);
});

test("explicit close bypasses UI/work pins and runs shutdown and disposal when abort rejects", async () => {
	let answer: Promise<boolean> | undefined;
	let shutdown = 0;
	const { sessions, entry, prompt } = await fixture((pi) => {
		pi.on("session_shutdown", async () => {
			shutdown++;
		});
		pi.registerCommand("hold", {
			description: "Hold residence",
			handler: async (_args, ctx) => {
				pi.events.emit(SESSION_LIVENESS_EVENT, { key: "fixture:hold", active: true });
				answer = ctx.ui.confirm("Close", "Waiting");
			},
		});
	});
	await prompt("/hold");
	let disposed = false;
	const dispose = entry.session.dispose.bind(entry.session);
	entry.session.dispose = () => {
		disposed = true;
		dispose();
	};
	entry.session.abort = async () => {
		throw new Error("close abort rejection");
	};
	await expect(entry.close()).rejects.toThrow("close abort rejection");
	expect(await answer).toBe(false);
	expect(shutdown).toBe(1);
	expect(disposed).toBe(true);
	expect(sessions.snapshot(entry).pendingDialogs).toEqual([]);
	await expect(entry.close()).rejects.toThrow("close abort rejection");
	expect(shutdown).toBe(1);
});

test("manual compaction summarizes an adequate transcript and the session stays usable across reopen", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-lifecycle-compact-"));
	cleanups.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"), cwd = join(root, "project");
	const { mkdir, writeFile } = await import("node:fs/promises");
	const { ProjectTrustStore } = await import("@earendil-works/pi-coding-agent");
	const rpivTodo = (await import("@juicesharp/rpiv-todo")).default;
	await mkdir(join(cwd, ".pi"), { recursive: true });
	// Tiny keep-recent window so a bounded deterministic transcript compacts.
	await writeFile(join(cwd, ".pi", "settings.json"), JSON.stringify({ compaction: { keepRecentTokens: 400, reserveTokens: 1024 } }));
	new ProjectTrustStore(agentDir).set(cwd, true);
	const events: Record<string, unknown>[] = [];
	const sessions = new Sessions(agentDir, [makeProvider(), rpivTodo], (_id, event) => {
		events.push(event as Record<string, unknown>);
	});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const model = entry.modelRuntime.getModel("fixture", "echo");
	if (!model) throw new Error("Missing fixture model");
	await entry.session.setModel(model);
	const id = entry.session.sessionId;
	const signal = new AbortController().signal;
	const todo = entry.session.agent.state.tools.find((tool) => tool.name === "todo")!;
	const created = await todo.execute("compact-todo-create", { action: "create", subject: "Surviving task" }, signal);
	expect(created.details.tasks).toMatchObject([{ id: 1, subject: "Surviving task" }]);
	const paragraph = "Compaction fodder. ".repeat(100);
	for (let i = 0; i < 6; i++) {
		await sessions.call("session.prompt", {
			sessionId: id, content: [{ type: "text", text: `Turn ${i}: ${paragraph}` }],
		});
	}
	// Direct tool.execute writes no branch entries, so upstream replay
	// (last `todo` toolResult wins, EMPTY_STATE fallback) cannot see the
	// created task. Persist the envelope the way a real model turn would, so
	// the compaction path below exercises genuine replay input.
	entry.session.sessionManager.appendMessage({
		role: "toolResult", toolCallId: "compact-todo-turn", toolName: "todo",
		content: [{ type: "text", text: "Created #1" }],
		details: created.details, isError: false, timestamp: Date.now(),
	});
	const before = await sessions.call("session.load", { sessionId: id }) as any;
	const messagesBefore = (before.messages as unknown[]).length;
	expect(messagesBefore).toBeGreaterThan(6);
	const result = await entry.session.compact();
	expect(result.summary.length).toBeGreaterThan(0);
	expect(result.tokensBefore).toBeGreaterThan(0);
	const after = await sessions.call("session.load", { sessionId: id }) as any;
	// Recent turns stay by design; the compaction summary entry is appended.
	expect((after.messages as unknown[]).length).toBeGreaterThanOrEqual(messagesBefore);
	expect(after.messages).toEqual(expect.arrayContaining([
		expect.objectContaining({ summaryKind: "compaction" }),
	]));
	// Extension context survives: tools still registered, todo state intact.
	// Re-resolve the tool handle post-compaction: native compaction may
	// rebuild tool instances, so a pre-compaction handle can observe
	// discarded state.
	expect(entry.session.getActiveToolNames()).toContain("todo");
	const freshTodo = entry.session.agent.state.tools.find((tool) => tool.name === "todo")!;
	const listed = await freshTodo.execute("compact-todo-list", { action: "list" }, signal);
	expect(listed.details.tasks).toMatchObject([{ id: 1, subject: "Surviving task" }]);
	// Reopening the same native file keeps the compaction entry and the
	// session answers again; compacting twice is a native error, not corruption.
	await sessions.release(id);
	const reopened = await sessions.get(id);
	const reloaded = await sessions.call("session.load", { sessionId: id }) as any;
	expect(JSON.stringify(reloaded.messages)).toContain("summary");
	await sessions.call("session.prompt", { sessionId: id, content: [{ type: "text", text: "Still here" }] });
	expect(reopened.session.getActiveToolNames()).toContain("todo");
	// The single new turn after reopen is too small to compact again, so the
	// native gate refuses instead of corrupting state.
	await expect(reopened.session.compact()).rejects.toThrow(/compact/i);
}, 120000);

test("native in-session branches do not corrupt Pixie snapshot, load or fork", async () => {
	const { dir, sessions, entry, id, prompt } = await fixture((pi) => {
		pi.registerTool({ name: "branch_probe", description: "Branch fixture",
			parameters: { type: "object", properties: {} },
			execute: async () => ({ content: [{ type: "text", text: "probe" }] }) });
	});
	await prompt("First turn");
	const manager = entry.session.sessionManager;
	const before = manager.getBranch();
	expect(before.length).toBeGreaterThan(0);
	// Branch natively from the first user message, the way Pi TUI does, then
	// continue on the new branch. Pixie has no branch-navigation UI; it must
	// simply keep working on the current branch.
	const forkPoint = before.find((e: any) => e.message?.role === "user") ?? before[0];
	manager.branch((forkPoint as any).id);
	await prompt("Branched turn");
	// getTree returns root nodes; count the whole structure instead.
	const countNodes = (nodes: any[]): number =>
		nodes.reduce((sum, node) => sum + 1 + countNodes(node.children ?? []), 0);
	expect(countNodes(manager.getTree() as any[])).toBeGreaterThan(before.length);
	// Snapshot, load and fork all follow the current branch without errors.
	const snapshot = sessions.snapshot(entry, true);
	expect(snapshot).toBeTruthy();
	const loaded = await sessions.call("session.load", { sessionId: id }) as any;
	expect((loaded.messages as unknown[]).length).toBeGreaterThan(0);
	const forked = await sessions.call("session.fork", { sessionId: id, cwd: dir }) as any;
	expect(forked.sessionId).not.toBe(id);
	const forkLoaded = await sessions.call("session.load", { sessionId: forked.sessionId }) as any;
	expect((forkLoaded.messages as unknown[]).length).toBeGreaterThan(0);
});
