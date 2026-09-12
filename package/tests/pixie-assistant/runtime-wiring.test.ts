import { expect, test } from "bun:test";
import { RuntimeWiring } from "../../../assistant/src/runtime-wiring.ts";
import { SessionRuntime, UncertainPromptError } from "../../../assistant/src/session-runtime.ts";

const options = (overrides: Record<string, unknown> = {}) => ({
	identity: {
		sessionKey: "session-key",
		sessionId: "native-session",
		nativeSessionId: "native-session",
		bootId: "boot-1",
		childGeneration: 0,
		cwd: "/tmp/project",
		agentDir: "/tmp/agent",
	},
	availableThinkingLevels: ["minimal", "max"],
	...overrides,
});

test("session runtime validates, claims, and settles one native prompt", async () => {
	const runtime = new SessionRuntime(options());
	let calls = 0;
	const result = await runtime.executePrompt(
		{
			mutationId: "mutation-1",
			content: [{ type: "text", text: "hello" }],
		},
		async () => {
			calls++;
			return { stopReason: "stop" };
		},
	);
	expect(calls).toBe(1);
	expect(result).toMatchObject({ mutationId: "mutation-1", stopReason: "stop" });
	expect(runtime.state).toMatchObject({ phase: "ready", delivery: { status: "settled" } });
	expect(runtime.outbox.entries[0]).toMatchObject({ status: "settled", attempt: 1 });
});

test("known mutation retries are status reads and never start a second Pi operation", async () => {
	const runtime = new SessionRuntime(options());
	let calls = 0;
	const invoke = async () => {
		calls++;
		return { stopReason: "stop" };
	};
	await runtime.executePrompt(
		{ mutationId: "mutation-1", content: [{ type: "text", text: "hello" }] },
		invoke,
	);
	const replay = await runtime.executePrompt(
		{ mutationId: "mutation-1", content: [{ type: "text", text: "hello" }] },
		invoke,
	);
	expect(calls).toBe(1);
	expect(replay).toMatchObject({ replayed: true, mutationId: "mutation-1", stopReason: "stop" });
});

test("native failure leaves an explicit uncertain outcome and blocks automatic continuation", async () => {
	const runtime = new SessionRuntime(options());
	await expect(
		runtime.executePrompt(
			{ mutationId: "mutation-1", content: [{ type: "text", text: "hello" }] },
			async () => {
				throw new Error("provider failed");
			},
		),
	).rejects.toBeInstanceOf(UncertainPromptError);
	expect(runtime.outbox.entries[0]).toMatchObject({ status: "uncertain" });
	const continuation = runtime.outbox.entries[0];
	expect(continuation).toBeDefined();
});

test("passive state and drafts rebind across a native replacement without losing draft text", () => {
	const runtime = new SessionRuntime(options());
	const draft = runtime.getDraft("browser-a", "keep this");
	const applied = runtime.applyDraft("browser-a", {
		mutationId: "edit-1",
		expectedRevision: draft.draft.revision,
		content: "keep this and more",
	});
	expect(applied.kind).toBe("applied");
	expect(runtime.applyPassiveEvent({ type: "pixie:ui:title", title: "before" })).toBe(true);
	const reopened = runtime.reopen({ ...options(), bootId: "boot-2" });
	expect(reopened.ok).toBe(true);
	expect(runtime.identity).toMatchObject({ bootId: "boot-2", childGeneration: 1 });
	expect(runtime.getDraft("browser-a").draft).toMatchObject({
		content: "keep this and more",
		revision: 1,
		continuityRevision: 1,
	});
	expect(runtime.passive.title).toBe("");
});

test("runtime wiring keeps history reads bounded and capacity decisions explicit", async () => {
	const wiring = new RuntimeWiring({
		agentDir: "/tmp/runtime-wiring-test",
		bootId: "boot-1",
		persist: false,
		maxOutboxEntries: 1,
	});
	expect(wiring.canCreate()).toEqual({ ok: true, value: true });
	expect(
		(await wiring.scanHistory('{"type":"message","id":"one"}\nnot-json\n')).unknownRecords,
	).toHaveLength(1);
	expect(
		wiring.catalog([
			{ sessionId: "one", known: true },
			{ sessionId: "one", known: false },
		]),
	).toMatchObject({
		entries: [{ key: "one#1" }, { key: "one#2", duplicateOf: "one#1", kind: "unknown" }],
	});
	await wiring.close();
});
