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
