import { expect, test } from "bun:test";
import {
	createSessionFlow,
	isCurrentGeneration,
	replaySessionState,
	SessionFlowCoordinator,
	transitionSessionFlow,
} from "../../../assistant/src/session/flow.ts";
import type { SessionCreateRequest } from "../../../assistant/src/session/types.ts";

const request: SessionCreateRequest = {
	identity: {
		sessionKey: "session-key",
		sessionId: "native-session",
		nativeSessionId: "native-session",
		bootId: "boot-1",
		childGeneration: 0,
		cwd: "/tmp/project",
		agentDir: "/tmp/agent",
	},
	resources: [
		{ path: "/tmp/agent/extensions/user.js", scope: "user" },
		{ path: "/tmp/project/.pi/extensions/project.js", scope: "project" },
	],
	projectTrust: true,
	availableModels: [
		{ provider: "fixture", id: "echo", name: "Echo" },
		{ provider: "fixture", id: "other", name: "Other" },
	],
	availableThinkingLevels: ["minimal", "low", "high", "max"],
	model: { provider: "fixture", id: "echo" },
	thinkingLevel: "low",
};

const prompt = (generation = 0, deliveryId = "delivery-1", runId = "run-1") => ({
	type: "prompt.prepare" as const,
	request: {
		sessionKey: "session-key",
		generation,
		deliveryId,
		runId,
		content: [
			{ type: "text" as const, text: "hello" },
			{ type: "image" as const, mimeType: "image/png", data: "aGVsbG8=" },
		],
	},
});

function coordinator() {
	return new SessionFlowCoordinator(request);
}

test("create/prompt keeps native acceptance distinct from settlement", () => {
	const flow = coordinator();
	expect(flow.state.phase).toBe("ready");
	expect(flow.state.loadedResources).toHaveLength(2);
	expect(flow.apply(prompt())).toMatchObject({
		ok: true,
		value: { phase: "prompting", delivery: { status: "prepared" } },
	});
	expect(
		flow.apply({
			type: "prompt.dispatch",
			sessionKey: "session-key",
			generation: 0,
			deliveryId: "delivery-1",
			runId: "run-1",
		}),
	).toMatchObject({
		ok: true,
		value: { delivery: { status: "dispatching" } },
	});
	expect(
		flow.apply({
			type: "prompt.accept",
			sessionKey: "session-key",
			generation: 0,
			deliveryId: "delivery-1",
			runId: "run-1",
		}),
	).toMatchObject({
		ok: true,
		value: { delivery: { status: "accepted" } },
	});
	expect(
		flow.apply({
			type: "prompt.settle",
			sessionKey: "session-key",
			generation: 0,
			deliveryId: "delivery-1",
			runId: "run-1",
			stopReason: "stop",
		}),
	).toMatchObject({
		ok: true,
		value: { phase: "ready", delivery: { status: "settled", stopReason: "stop" } },
	});
	// A settled delivery is history, not an active duplicate reservation.
	expect(flow.apply(prompt(0, "delivery-2", "run-2"))).toMatchObject({
		ok: true,
		value: { delivery: { status: "prepared" } },
	});
});

test("model and supported max thinking changes are generation guarded", () => {
	const flow = coordinator();
	expect(
		flow.apply({
			type: "model.set",
			sessionKey: "session-key",
			generation: 0,
			model: { provider: "fixture", id: "other" },
		}),
	).toMatchObject({
		ok: true,
		value: { model: { id: "other" } },
	});
	expect(
		flow.apply({ type: "thinking.set", sessionKey: "session-key", generation: 0, level: "max" }),
	).toMatchObject({
		ok: true,
		value: { thinkingLevel: "max" },
	});
	expect(
		flow.apply({ type: "thinking.set", sessionKey: "session-key", generation: 0, level: "xhigh" }),
	).toMatchObject({
		ok: false,
		error: { code: "unsupported-thinking" },
	});
	expect(
		flow.apply({
			type: "model.set",
			sessionKey: "session-key",
			generation: 1,
			model: { provider: "fixture", id: "echo" },
		}),
	).toMatchObject({
		ok: false,
		error: { code: "stale-generation" },
	});
});

test("abort outcomes are explicit and timeout never authorizes prompt replay", () => {
	const flow = coordinator();
	flow.apply(prompt());
	flow.apply({
		type: "prompt.dispatch",
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-1",
		runId: "run-1",
	});
	flow.apply({
		type: "prompt.accept",
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-1",
		runId: "run-1",
	});
	expect(
		flow.apply({
			type: "abort.request",
			sessionKey: "session-key",
			generation: 0,
			requestId: "abort-1",
			runId: "run-1",
		}),
	).toMatchObject({
		ok: true,
		value: { phase: "aborting" },
	});
	expect(
		flow.apply({
			type: "abort.result",
			sessionKey: "session-key",
			generation: 0,
			requestId: "abort-1",
			runId: "run-1",
			result: { kind: "timed-out", reason: "provider did not stop" },
		}),
	).toMatchObject({
		ok: true,
		value: {
			phase: "prompting",
			delivery: { status: "accepted" },
			lastAbort: { kind: "timed-out" },
		},
	});
	expect(flow.apply(prompt(0, "delivery-2", "run-2"))).toMatchObject({
		ok: false,
		error: { code: "invalid-phase" },
	});

	expect(
		flow.apply({
			type: "abort.request",
			sessionKey: "session-key",
			generation: 99,
			requestId: "abort-stale",
			runId: "run-1",
		}),
	).toMatchObject({
		ok: true,
		value: { kind: "stale-generation" },
	});
});

test("successful abort and idle abort are distinguishable", () => {
	const flow = coordinator();
	expect(
		flow.apply({
			type: "abort.request",
			sessionKey: "session-key",
			generation: 0,
			requestId: "idle",
			runId: "missing",
		}),
	).toEqual({
		ok: true,
		value: { kind: "already-idle", generation: 0, runId: "missing" },
	});
	flow.apply(prompt());
	flow.apply({
		type: "prompt.dispatch",
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-1",
		runId: "run-1",
	});
	flow.apply({
		type: "prompt.accept",
		sessionKey: "session-key",
		generation: 0,
		deliveryId: "delivery-1",
		runId: "run-1",
	});
	flow.apply({
		type: "abort.request",
		sessionKey: "session-key",
		generation: 0,
		requestId: "abort-2",
		runId: "run-1",
	});
	const result = flow.apply({
		type: "abort.result",
		sessionKey: "session-key",
		generation: 0,
		requestId: "abort-2",
		runId: "run-1",
		result: { kind: "aborted" },
	});
	expect(result).toMatchObject({
		ok: true,
		value: { phase: "ready", delivery: { status: "interrupted" }, lastAbort: { kind: "aborted" } },
	});

	const beforeAcceptance = coordinator();
	beforeAcceptance.apply(prompt());
	expect(
		beforeAcceptance.apply({
			type: "abort.request",
			sessionKey: "session-key",
			generation: 0,
			requestId: "abort-before-accept",
			runId: "run-1",
		}),
	).toMatchObject({
		ok: true,
		value: { phase: "aborting" },
	});
});

test("reopen advances generation, preserves native identity and rejects old callbacks", () => {
	const flow = coordinator();
	const begin = flow.apply({
		type: "reopen.begin",
		request: {
			sessionKey: "session-key",
			expectedGeneration: 0,
			requestId: "reopen-1",
			nextGeneration: 1,
		},
	});
	expect(begin).toMatchObject({
		ok: true,
		value: { phase: "reopening", identity: { childGeneration: 1 } },
	});
	expect(isCurrentGeneration(flow.state, 0)).toBe(false);
	expect(
		flow.apply({
			type: "model.set",
			sessionKey: "session-key",
			generation: 0,
			model: { provider: "fixture", id: "echo" },
		}),
	).toMatchObject({
		ok: false,
		error: { code: "stale-generation" },
	});

	const commit = flow.apply({
		type: "reopen.commit",
		commit: {
			requestId: "reopen-1",
			sessionKey: "session-key",
			generation: 1,
			bootId: "boot-2",
			nativeSessionId: "native-session",
			resources: request.resources!,
			projectTrust: true,
			availableModels: request.availableModels,
			availableThinkingLevels: ["minimal", "max"],
			model: { provider: "fixture", id: "echo" },
			thinkingLevel: "max",
		},
	});
	expect(commit).toMatchObject({
		ok: true,
		value: {
			phase: "ready",
			identity: { bootId: "boot-2", childGeneration: 1 },
			thinkingLevel: "max",
		},
	});
	expect(isCurrentGeneration(flow.state, 1)).toBe(true);
	expect(
		flow.apply({ type: "thinking.set", sessionKey: "session-key", generation: 0, level: "max" }),
	).toMatchObject({
		ok: false,
		error: { code: "stale-generation" },
	});
});

test("reopen failure closes the flow instead of resurrecting the old generation", () => {
	const created = createSessionFlow(request);
	if (!created.ok) throw new Error(created.error.message);
	const reopened = transitionSessionFlow(created.value, {
		type: "reopen.begin",
		request: {
			sessionKey: "session-key",
			expectedGeneration: 0,
			requestId: "reopen-2",
			nextGeneration: 1,
		},
	});
	if (!reopened.ok || !("phase" in reopened.value)) throw new Error("reopen did not start");
	const failed = transitionSessionFlow(reopened.value, {
		type: "reopen.fail",
		sessionKey: "session-key",
		requestId: "reopen-2",
		reason: "native session missing",
	});
	if (!failed.ok || !("phase" in failed.value)) throw new Error("reopen failure did not apply");
	expect(failed.value.phase).toBe("closed");
	expect(isCurrentGeneration(failed.value, 1)).toBe(false);
});

test("replay contains state and outcomes but never prompt image bytes", () => {
	const flow = coordinator();
	flow.apply(prompt());
	const replay = replaySessionState(flow.state);
	expect(replay).toMatchObject({
		sessionKey: "session-key",
		childGeneration: 0,
		delivery: { status: "prepared" },
	});
	expect(JSON.stringify(replay)).not.toContain("aGVsbG8=");
	expect(JSON.stringify(replay)).not.toContain("authorization");
	expect(flow.replay()).toEqual(replay);
});
