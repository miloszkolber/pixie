import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import agents from "../../pi-host/src/extensions/agents.ts";
import plans from "../../pi-host/src/extensions/plans.ts";
import { Sessions } from "../../pi-host/src/sessions.ts";
import { JsonStore } from "../../pi-host/src/storage.ts";

const cleanups: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});
async function fixture(factories: ExtensionFactory[] = []) {
	const dir = await mkdtemp(tmpdir() + "/pixie-pi-test-");
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const events: unknown[] = [];
	const sessions = new Sessions(dir, factories, (_id, event) => events.push(event));
	cleanups.push(() => sessions.close());
	return { dir, sessions, events };
}
function makeProvider(pause?: Promise<void>, finalOnly = false): ExtensionFactory {
	return (pi) => {
		pi.registerProvider("fixture", {
			baseUrl: "http://localhost/unused",
			apiKey: "fixture-only-key",
			api: "fixture-api",
			models: [
				{
					id: "echo",
					name: "Echo",
					reasoning: false,
					input: ["text", "image"],
					cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
					contextWindow: 64000,
					maxTokens: 1024,
				},
			],
			streamSimple: (_model, context) => {
				const stream = createAssistantMessageEventStream();
				const message: AssistantMessage = {
					role: "assistant",
					api: "fixture-api",
					provider: "fixture",
					model: "echo",
					content: [{ type: "text", text: "Hello from Pi" }],
					stopReason: "stop",
					timestamp: Date.now(),
					usage: {
						input: 5,
						output: 4,
						cacheRead: 0,
						cacheWrite: 0,
						totalTokens: 9,
						cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
					},
				};
				queueMicrotask(async () => {
					if (!finalOnly) {
						stream.push({ type: "start", partial: { ...message, content: [] } });
						stream.push({
							type: "text_start",
							contentIndex: 0,
							partial: { ...message, content: [{ type: "text", text: "" }] },
						});
						stream.push({
							type: "text_delta",
							contentIndex: 0,
							delta: "Hello from Pi",
							partial: message,
						});
						stream.push({
							type: "text_end",
							contentIndex: 0,
							content: "Hello from Pi",
							partial: message,
						});
					}
					await pause;
					stream.push({ type: "done", reason: "stop", message });
					stream.end(message);
				});
				return stream;
			},
		});
	};
}
const provider = makeProvider();

test("vanilla SDK creates, discovers, renames, archives and deletes sessions without extensions", async () => {
	const { dir, sessions } = await fixture();
	const entry = await sessions.create(dir),
		id = entry.session.sessionId;
	expect(entry.capabilities.snapshot()).toEqual({});
	expect(entry.session.getActiveToolNames()).toContain("bash");
	expect((await sessions.list("")).sessions).toHaveLength(1);
	await sessions.call("pi.session.rename", { sessionId: id, title: "A Pi session" });
	await sessions.call("pi.session.archive", { sessionId: id });
	expect(
		((await sessions.call("pi.session.info", { sessionId: id })) as any).session.archived,
	).toBe(true);
	await sessions.call("pi.session.unarchive", { sessionId: id });
	await expect(sessions.call("pi.sources.list", { sessionId: id })).rejects.toThrow(
		"Unsupported capability",
	);
	await sessions.call("session.delete", { sessionId: id });
	expect((await sessions.list("")).sessions).toHaveLength(0);
});

test("native SDK streams and replays text attachments without losing display metadata", async () => {
	const { dir, sessions, events } = await fixture([provider]);
	const entry = await sessions.create(dir);
	await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	const content = [
		{ type: "text", text: "Review this" },
		{
			type: "resource",
			resource: {
				uri: "pixie://attachment/a.txt",
				mimeType: "text/plain",
				text: "attached content",
			},
		},
	];
	const result = await sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content,
	});
	expect(result).toEqual({ stopReason: "stop" });
	expect(
		events.some(
			(e: any) => e.type === "message_update" && e.assistantMessageEvent.type === "text_delta",
		),
	).toBe(true);
	expect(
		events.find((e: any) => e.type === "message_start" && e.message.role === "user"),
	).toMatchObject({ message: { displayContent: content } });
	const snapshot = sessions.snapshot(entry, true);
	expect((snapshot.messages as any[]).find((m) => m.role === "user").displayContent).toEqual(
		content,
	);
	const reloaded = new Sessions(dir, [provider], () => {});
	cleanups.push(() => reloaded.close());
	const loaded = await reloaded.get(entry.session.sessionId);
	expect(reloaded.snapshot(loaded, true).messages).toEqual(snapshot.messages);
});

test("agent definitions and plans add tools without replacing Pi core tools", async () => {
	const { dir, sessions } = await fixture([(pi) => agents(pi, dir), plans]);
	const entry = await sessions.create(dir);
	expect(entry.capabilities.snapshot()).toEqual({ agents: 1, plans: 1 });
	expect(entry.session.getActiveToolNames()).toEqual(
		expect.arrayContaining(["read", "bash", "edit", "write", "delegate", "update_plan"]),
	);
	const saved = await entry.capabilities.call(
		"pi.sources.create",
		{
			name: "Reviewer",
			description: "Review changes",
			content: "Review the changes carefully",
			properties: { model: "fixture/echo" },
		},
		sessions.context(entry),
	);
	expect(saved).toMatchObject({
		source: { name: "Reviewer", properties: { model: "fixture/echo" } },
	});
	const catalog = await entry.capabilities.call("pi.sources.list", {}, sessions.context(entry));
	expect((catalog as any).sources).toHaveLength(1);
});

test("separate state-store instances serialize concurrent updates to one file", async () => {
	const { dir } = await fixture();
	const path = join(dir, "state.json");
	const a = new JsonStore(path, () => ({ count: 0 })),
		b = new JsonStore(path, () => ({ count: 0 }));
	await Promise.all(
		Array.from({ length: 30 }, (_, i) =>
			(i % 2 ? a : b).update(async (state) => {
				await Promise.resolve();
				state.count++;
			}),
		),
	);
	expect(JSON.parse(await readFile(path, "utf8"))).toEqual({ count: 30 });
});

test("session attachment checks each concurrent caller and accepts the same directory through a symlink", async () => {
	const { dir, sessions } = await fixture();
	const entry = await sessions.create(dir);
	const alias = join(dir, "alias");
	await symlink(dir, alias);
	const other = join(dir, "other");
	await mkdir(other);
	const [valid, invalid] = await Promise.allSettled([
		sessions.get(entry.session.sessionId, alias),
		sessions.get(entry.session.sessionId, other),
	]);
	expect(valid.status).toBe("fulfilled");
	expect(invalid.status).toBe("rejected");
});

test("defined agents execute through ordinary SDK child sessions", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-pi-jobs-");
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const runner = async (cwd: string) => {
		const entry = await sessions.create(cwd);
		await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
		return {
			session: entry.session,
			prompt: (text: string) =>
				sessions.call("session.prompt", {
					sessionId: entry.session.sessionId,
					content: [{ type: "text", text }],
				}),
			close: async () => {},
		};
	};
	const sessions = new Sessions(dir, [provider, (pi) => agents(pi, dir, runner)], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(dir),
		ctx = sessions.context(entry);
	await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	await entry.capabilities.call(
		"pi.sources.create",
		{
			name: "Reviewer",
			description: "Review",
			content: "Review the task",
			properties: { model: "fixture/echo" },
		},
		ctx,
	);
	const tool = entry.session.agent.state.tools.find((t) => t.name === "delegate")!;
	const result = await tool.execute(
		"delegate-test",
		{ agent: "Reviewer", task: "Check this" },
		new AbortController().signal,
	);
	expect(result).toMatchObject({
		content: [{ type: "text", text: "Hello from Pi" }],
		details: { status: "completed" },
	});
	expect((await sessions.list("")).sessions).toHaveLength(2);
});

test("incompatible or incomplete optional registrations leave vanilla Pi usable", async () => {
	const { dir, sessions } = await fixture([
		(pi) => {
			pi.events.emit("pixie:capability:v1", { id: "agents", version: 2, operations: {} });
			pi.events.emit("pixie:capability:v1", { id: "mcp", version: 1, operations: {} });
		},
	]);
	const entry = await sessions.create(dir);
	expect(entry.capabilities.snapshot()).toEqual({});
	expect(entry.session.getActiveToolNames()).toEqual(
		expect.arrayContaining(["read", "bash", "write", "edit"]),
	);
});

test("reattachment during a native stream retains the complete partial message", async () => {
	let finish!: () => void;
	const pause = new Promise<void>((resolve) => {
		finish = resolve;
	});
	const { dir, sessions, events } = await fixture([makeProvider(pause)]);
	const entry = await sessions.create(dir);
	await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	const run = sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content: [{ type: "text", text: "Pause" }],
	});
	try {
		for (let i = 0; i < 100 && !events.some((e: any) => e.type === "message_update"); i++)
			await Bun.sleep(5);
		const snapshot = sessions.snapshot(entry, true);
		expect(snapshot.runId).not.toBe("");
		expect((snapshot.messages as any[]).filter((m) => m.role === "assistant")).toMatchObject([
			{ content: [{ type: "text", text: "Hello from Pi" }] },
		]);
	} finally {
		finish();
		await run;
	}
	expect(
		(sessions.snapshot(entry, true).messages as any[]).filter((m) => m.role === "assistant"),
	).toHaveLength(1);
});

for (const finalOnly of [false, true])
	test(`live text is complete exactly once with finalOnly=${finalOnly}`, async () => {
		const { dir, sessions, events } = await fixture([makeProvider(undefined, finalOnly)]);
		const entry = await sessions.create(dir);
		const model = entry.modelRuntime.getModel("fixture", "echo");
		if (!model) throw new Error("fixture model missing");
		await entry.session.setModel(model);
		await sessions.call("session.prompt", {
			sessionId: entry.session.sessionId,
			content: [{ type: "text", text: "Hello" }],
		});
		const deltas = events.flatMap((event) => {
			const e = event as { type: string; assistantMessageEvent?: { type: string; delta: string } };
			return e.type === "message_update" && e.assistantMessageEvent?.type === "text_delta"
				? [e.assistantMessageEvent.delta]
				: [];
		});
		expect(deltas.join("")).toBe("Hello from Pi");
	});

test("native commands do not inherit an earlier turn's failure status", async () => {
	const { dir, sessions } = await fixture([
		provider,
		(pi) => {
			pi.registerCommand("noop", { description: "No-op fixture", handler: async () => {} });
		},
	]);
	const entry = await sessions.create(dir);
	const model = entry.modelRuntime.getModel("fixture", "echo");
	if (!model) throw new Error("Missing fixture model");
	await entry.session.setModel(model);
	await sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content: [{ type: "text", text: "Hello" }],
	});
	const previous = entry.session.messages.filter((m) => m.role === "assistant").at(-1);
	if (!previous) throw new Error("Missing previous answer");
	previous.stopReason = "aborted";
	expect(
		await sessions.call("session.prompt", {
			sessionId: entry.session.sessionId,
			content: [{ type: "text", text: "/noop" }],
		}),
	).toMatchObject({ stopReason: "end_turn" });
});
