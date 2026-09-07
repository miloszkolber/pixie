import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { SettingsManager } from "@earendil-works/pi-coding-agent";
import { type ManagedSession, Sessions } from "../../../assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const fn of cleanup.splice(0).reverse()) await fn();
});

async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-lifecycle-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"),
		cwd = join(root, "project");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(cwd);
	await writeFile(
		join(agentDir, "settings.json"),
		JSON.stringify({
			extensions: [
				join(import.meta.dir, "lifecycle-provider.ts"),
				import.meta.resolve("@juicesharp/rpiv-todo").replace("file://", ""),
			],
			defaultProvider: "lifecycle-fixture",
			defaultModel: "echo",
			compaction: { enabled: false, keepRecentTokens: 256, reserveTokens: 512 },
			retry: { enabled: true, maxRetries: 2, baseDelayMs: 10, maxDelayMs: 20 },
		}),
	);
	await writeFile(
		join(agentDir, "extensions", "unknown.js"),
		`export default pi => {
		let token;
		pi.registerTool({ name: "unknown_native_probe", label: "Probe", description: "Probe", parameters: {type: "object", properties: {}}, execute: async () => ({content: [], details: {}}) });
		pi.on("session_start", (_event, ctx) => {
			const saved = ctx.sessionManager.getBranch().findLast(entry => entry.type === "custom" && entry.customType === "unknown-native-state");
			token = saved?.data.token ?? crypto.randomUUID();
			if (!saved) pi.appendEntry("unknown-native-state", { token });
			ctx.ui.setStatus("unknown-native", "ready");
		});
		pi.on("before_agent_start", (event, ctx) => ({ systemPrompt: event.systemPrompt + "\\nUNKNOWN_NATIVE_CONTEXT:" + ctx.sessionManager.getSessionId() + "\\nUNKNOWN_NATIVE_STATE:" + token }));
	};`,
	);
	const events: Record<string, unknown>[] = [];
	const sessions = new Sessions(agentDir, [], (id, event) =>
		events.push({ ...(event as object), sessionId: id }),
	);
	cleanup.push(() => sessions.close());
	const prompt = (entry: ManagedSession, text: string) =>
		sessions.call("session.prompt", {
			sessionId: entry.session.sessionId,
			content: [{ type: "text", text }],
		});
	const audit = async () =>
		(await readFile(join(cwd, "lifecycle-audit.jsonl"), "utf8"))
			.trim()
			.split("\n")
			.map((line) => JSON.parse(line));
	return { root, agentDir, cwd, sessions, events, prompt, audit };
}
async function until(check: () => boolean) {
	for (let i = 0; i < 500 && !check(); i++) await Bun.sleep(10);
	expect(check()).toBe(true);
}
async function todos(entry: ManagedSession) {
	const tool = entry.session.agent.state.tools.find((tool) => tool.name === "todo");
	if (!tool) throw new Error("Native todo tool missing");
	return (await tool.execute(crypto.randomUUID(), { action: "list" }, new AbortController().signal))
		.details;
}

test("actual native compaction preserves todo and unknown extension context across reopen and session switching", async () => {
	const { agentDir, cwd, sessions, prompt, audit } = await fixture();
	const first = await sessions.create(cwd);
	expect(sessions.inventory(first).errors).toEqual([]);
	expect(first.session.model).toMatchObject({ provider: "lifecycle-fixture", id: "echo" });
	await prompt(first, "make-todo");
	const expected = await todos(first);
	const nativeState = first.session.sessionManager
		.getBranch()
		.find((entry) => entry.type === "custom" && entry.customType === "unknown-native-state");
	if (nativeState?.type !== "custom") throw new Error("Native extension state missing");
	const token = (nativeState.data as { token: string }).token;
	expect(expected).toMatchObject({ nextId: 2, tasks: [{ subject: "Survive real compaction" }] });
	// Real agent turns produce a native transcript, not hand-appended compaction entries.
	for (let i = 0; i < 8; i++)
		await prompt(first, `history-${i} ${"adequate native transcript ".repeat(250)}`);
	const before = first.session.sessionManager.getBranch();
	await prompt(first, "/compact");
	const compact = first.session.sessionManager
		.getBranch()
		.find((entry) => entry.type === "compaction");
	if (compact?.type !== "compaction") throw new Error("Actual compaction did not persist");
	expect(compact.summary).toContain("Native compaction fixture summary");
	expect(compact.tokensBefore).toBeGreaterThan(256);
	expect(before.some((entry) => entry.id === compact.firstKeptEntryId)).toBe(true);
	expect((await audit()).some((event) => event.summary && event.textLength > 10000)).toBe(true);
	expect(await todos(first)).toEqual(expected);
	const second = await sessions.create(cwd);
	expect(await todos(second)).toMatchObject({ tasks: [], nextId: 1 });
	await prompt(second, "second session");
	const id = first.session.sessionId;
	await sessions.release(id);
	expect(sessions.entries.has(id)).toBe(false);
	const reopened = await sessions.get(id);
	expect(await todos(reopened)).toEqual(expected);
	await prompt(reopened, "after compaction reopen");
	const last = (await audit()).filter((event) => event.event === "stream").at(-1);
	expect(last.system).toContain(`UNKNOWN_NATIVE_CONTEXT:${id}`);
	expect(last.system).toContain(`UNKNOWN_NATIVE_STATE:${token}`);
	expect(last.system).not.toContain(`UNKNOWN_NATIVE_CONTEXT:${second.session.sessionId}`);
	expect(sessions.snapshot(reopened, true).messages).toContainEqual(
		expect.objectContaining({ summaryKind: "compaction", messageId: compact.id }),
	);
	if (!reopened.session.sessionFile) throw new Error("Native session file missing");
	const native = (await readFile(reopened.session.sessionFile, "utf8"))
		.trim()
		.split("\n")
		.map((line) => JSON.parse(line));
	expect(native.filter((entry) => entry.type === "compaction")).toHaveLength(1);
	expect(
		native.some(
			(entry) => entry.message?.role === "toolResult" && entry.message.toolName === "todo",
		),
	).toBe(true);
	await sessions.close();
	const restarted = new Sessions(agentDir, [], () => {});
	cleanup.push(() => restarted.close());
	const restored = await restarted.get(id);
	expect(
		restored.session.sessionManager
			.getBranch()
			.filter((entry) => entry.type === "custom" && entry.customType === "unknown-native-state"),
	).toEqual([nativeState]);
	expect(await todos(restored)).toEqual(expected);
	expect(restored.session.model?.id).toBe("echo");
	expect(restarted.snapshot(restored).runId).toBe("");
	expect(await todos(await restarted.get(second.session.sessionId))).toMatchObject({ tasks: [] });
}, 30000);

for (const stop of [false, true])
	test(`native retry and continuation settle authoritatively after reconnect (${stop ? "Stop" : "answer"})`, async () => {
		const { cwd, sessions, prompt, events } = await fixture();
		const entry = await sessions.create(cwd);
		expect(sessions.inventory(entry).errors).toEqual([]);
		await prompt(entry, "retry-fixture");
		expect(events.filter((event) => event.type === "auto_retry_start")).toHaveLength(1);
		expect(events.filter((event) => event.type === "run_end")).toHaveLength(1);
		expect(events.at(-1)).toMatchObject({ type: "run_end", stopReason: "stop" });
		events.length = 0;
		const run = prompt(entry, "continue-fixture");
		void run.catch(() => {});
		try {
			await until(() => events.some((event) => event.type === "pixie:ui:request"));
			expect(events.filter((event) => event.type === "agent_end").length).toBeGreaterThan(0);
			expect(events.filter((event) => event.type === "run_end")).toHaveLength(0);
			expect(entry.runId).not.toBe("");
			const snapshot = (await sessions.call("session.load", {
				sessionId: entry.session.sessionId,
			})) as { runId: string; pendingDialogs: { requestId: string }[] };
			expect(snapshot.pendingDialogs).toHaveLength(1);
			expect(snapshot.runId).toBe(entry.runId);
			if (stop) {
				expect(
					await sessions.call("session.cancel", { sessionId: entry.session.sessionId }),
				).toEqual({ ok: true, aborted: true });
				// Pi can report the pre-stream abort as an assistant error. Preserve
				// that outcome rather than declaring a successful model completion.
				await run.catch((error) => expect(error.message).toContain("could not complete"));
			} else {
				await sessions.resolveUiResponse({
					sessionId: entry.session.sessionId,
					requestId: snapshot.pendingDialogs[0].requestId,
					value: "answered",
				});
				expect(await run).toEqual({ stopReason: "stop" });
			}
			expect(sessions.snapshot(entry)).toMatchObject({
				pendingDialogs: [],
				streaming: false,
				runId: "",
			});
			expect(entry.partialTools.size).toBe(0);
			expect(events.filter((event) => event.type === "run_end")).toHaveLength(1);
			expect(events.findIndex((event) => event.type === "agent_settled")).toBeLessThan(
				events.findIndex((event) => event.type === "run_end"),
			);
			if (stop) {
				const last = entry.session.messages
					.filter((message) => message.role === "assistant")
					.at(-1);
				if (!last) throw new Error("Native abort outcome missing");
				expect(["error", "aborted"]).toContain(last.stopReason);
				expect(last.errorMessage).toMatch(/aborted/i);
			}
			await expect(
				sessions.resolveUiResponse({
					sessionId: entry.session.sessionId,
					requestId: snapshot.pendingDialogs[0].requestId,
					value: "late",
				}),
			).rejects.toThrow();
		} finally {
			await sessions.call("session.cancel", { sessionId: entry.session.sessionId });
			await run.catch(() => {});
		}
	}, 15000);

test("repeated native idle reopen applies settings and preserves selected model while another session waits in a tool", async () => {
	const { agentDir, cwd, sessions, prompt, events, audit } = await fixture();
	let idle = await sessions.create(cwd);
	const idleId = idle.session.sessionId;
	const alternate = idle.modelRuntime.getModel("lifecycle-fixture", "alternate");
	if (!alternate) throw new Error("Native alternate model missing");
	await idle.session.setModel(alternate);
	await prompt(idle, "persist alternate model");
	const active = await sessions.create(cwd);
	const run = prompt(active, "stop-dialog");
	void run.catch(() => {});
	try {
		await until(() => events.some((event) => event.type === "pixie:ui:request"));
		const activeRun = active.runId;
		const activeModel = active.session.model;
		const settings = SettingsManager.create(cwd, agentDir);
		const paths = settings.getExtensionPaths();
		const unknown = join(agentDir, "extensions", "unknown.js");
		for (const enabled of [false, true, false, true]) {
			settings.setExtensionPaths(enabled ? paths : [...paths, `-${unknown}`]);
			await settings.flush();
			await sessions.release(idleId);
			expect(sessions.entries.has(idleId)).toBe(false);
			idle = await sessions.get(idleId);
			expect(idle.session.model?.id).toBe("alternate");
			expect(
				idle.session.getActiveToolNames().filter((name) => name === "unknown_native_probe"),
			).toHaveLength(enabled ? 1 : 0);
			await prompt(idle, "reopened model still works");
			expect(idle.session.messages.at(-1)).toMatchObject({
				role: "assistant",
				model: "alternate",
				stopReason: "stop",
			});
			expect(sessions.entries.get(active.session.sessionId)).toBe(active);
			expect(active.session.model).toBe(activeModel);
			expect(active.runId).toBe(activeRun);
			expect(sessions.snapshot(active).pendingDialogs).toHaveLength(1);
		}
		const [request] = sessions.snapshot(active).pendingDialogs as { requestId: string }[];
		await sessions.resolveUiResponse({
			sessionId: active.session.sessionId,
			requestId: request.requestId,
			value: "continue",
		});
		expect(await run).toEqual({ stopReason: "stop" });
		const log = await audit();
		expect(log.filter((event) => event.event === "start")).toHaveLength(6);
		expect(log.filter((event) => event.event === "shutdown")).toHaveLength(4);
		expect(
			events.filter(
				(event) =>
					event.type === "pixie:ui:status" && event.sessionId === idleId && event.text === "ready",
			),
		).toHaveLength(3);
		expect(sessions.snapshot(idle).pendingDialogs).toEqual([]);
	} finally {
		await sessions.call("session.cancel", { sessionId: active.session.sessionId });
		await run.catch(() => {});
	}
}, 15000);

test("repeated forks retain source history and all three native files resume independently", async () => {
	const { cwd, sessions, prompt } = await fixture();
	const parent = await sessions.create(cwd);
	await prompt(parent, "source-first");
	const first = await sessions.create(cwd, parent.session.sessionId);
	await prompt(first, "first-child-only");
	await prompt(parent, "source-second");
	const second = await sessions.create(cwd, parent.session.sessionId);
	await prompt(second, "second-child-only");
	const userTexts = (entry: ManagedSession) =>
		entry.session.messages
			.filter((message) => message.role === "user")
			.map((message) =>
				typeof message.content === "string"
					? message.content
					: message.content
							.filter((part) => part.type === "text")
							.map((part) => part.text)
							.join(""),
			);
	const expected = [
		["source-first", "source-second"],
		["source-first", "first-child-only"],
		["source-first", "source-second", "second-child-only"],
	];
	expect(new Set([parent, first, second].map((entry) => entry.session.sessionFile)).size).toBe(3);
	for (const [index, entry] of [parent, first, second].entries()) {
		const id = entry.session.sessionId;
		const path = entry.session.sessionFile;
		expect(userTexts(entry)).toEqual(expected[index]);
		await sessions.release(id);
		const reopened = await sessions.get(id);
		expect(reopened.session.sessionFile).toBe(path);
		expect(reopened.session.sessionId).toBe(id);
		expect(reopened.session.model?.id).toBe("echo");
		expect(userTexts(reopened)).toEqual(expected[index]);
		await prompt(reopened, `resumed-${index}`);
		expect(userTexts(reopened)).toEqual([...(expected[index] ?? []), `resumed-${index}`]);
	}
});
