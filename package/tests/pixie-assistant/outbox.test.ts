import { expect, test } from "bun:test";
import {
	acceptOutboxDelivery,
	applyOutbox,
	beginOutboxStop,
	createOutboxState,
	discardPreparedOutbox,
	dispatchOutboxDelivery,
	enqueueContinuation,
	getOutboxEntry,
	nextOutboxDispatch,
	prepareCompaction,
	prepareOutboxDelivery,
	reconcileUncertainDelivery,
	reconnectOutbox,
	restoreNativeQueueAsDraft,
	resumeOutbox,
	retryOutboxDelivery,
	verifyOutboxStop,
} from "../../../assistant/src/outbox/index.ts";
import type { OutboxState } from "../../../assistant/src/outbox/types.ts";

const identity = {
	sessionKey: "session-1",
	generation: 0,
	mutationId: "mutation-1",
	deliveryId: "delivery-1",
};

function prepared(state: OutboxState, overrides: Partial<typeof identity> = {}) {
	const result = prepareOutboxDelivery(state, {
		...identity,
		...overrides,
		kind: "prompt",
		payload: {
			text: "hello",
			attachments: [{ name: "diagram.png", mimeType: "image/png", data: "bytes" }],
		},
	});
	if (!result.ok) throw new Error(result.error.message);
	return result.value;
}

function accepted(state: OutboxState, id = identity) {
	let current = dispatchOutboxDelivery(state, id);
	if (!current.ok) throw new Error(current.error.message);
	current = acceptOutboxDelivery(current.value, id);
	if (!current.ok) throw new Error(current.error.message);
	return current.value;
}

test("outbox persists identity and advances prepared/dispatching/accepted/settled exactly once", () => {
	const payload = {
		text: "hello",
		attachments: [{ name: "diagram.png", mimeType: "image/png", data: "bytes" }],
	};
	let state = createOutboxState("session-1");
	const first = prepareOutboxDelivery(state, { ...identity, payload });
	expect(first.ok).toBe(true);
	if (!first.ok) return;
	state = first.value;
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "prepared",
		attempt: 0,
		payload,
	});

	state = applyOutbox(state, { type: "dispatch", ...identity });
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "dispatching",
		attempt: 1,
	});
	// A repeated dispatch is only a status read; it cannot increment the claim.
	const repeatedDispatch = dispatchOutboxDelivery(state, identity);
	expect(repeatedDispatch).toEqual({ ok: true, value: state });
	state = repeatedDispatch.ok ? repeatedDispatch.value : state;
	state = applyOutbox(state, { type: "accept", ...identity });
	state = applyOutbox(state, { type: "settle", ...identity, stopReason: "stop" });
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "settled",
		attempt: 1,
	});
	// Re-preparing or retrying a known mutation never creates a second delivery.
	const retry = retryOutboxDelivery(state, identity);
	expect(retry).toEqual({ ok: true, value: state });
	const duplicate = prepareOutboxDelivery(state, { ...identity, payload });
	expect(duplicate).toEqual({ ok: true, value: state });
	expect(state.entries).toHaveLength(1);
});

test("uncertain delivery blocks retry and automatic continuation until authoritative reconciliation", () => {
	let state = prepared(createOutboxState("session-1"));
	state = accepted(state);
	state = applyOutbox(state, {
		type: "uncertain",
		...identity,
		reason: "dispatch response was lost",
	});
	const uncertain = getOutboxEntry(state, identity.mutationId);
	expect(uncertain).toMatchObject({ status: "uncertain", attempt: 1 });

	const continuation = enqueueContinuation(state, {
		mutationId: "mutation-2",
		deliveryId: "delivery-2",
		sessionKey: "session-1",
		generation: 0,
		parentDeliveryId: identity.deliveryId,
		payload: { text: "continue" },
	});
	expect(continuation.ok).toBe(true);
	if (!continuation.ok) return;
	state = continuation.value;
	expect(nextOutboxDispatch(state)).toEqual({ kind: "blocked", reason: "uncertain-delivery" });
	const retry = retryOutboxDelivery(state, identity);
	expect(retry).toEqual({ ok: true, value: state });
	expect(getOutboxEntry(retry.ok ? retry.value : state, identity.mutationId)).toMatchObject({
		status: "uncertain",
		attempt: 1,
	});

	const resolved = reconcileUncertainDelivery(state, {
		...identity,
		outcome: "settled",
		stopReason: "stop",
	});
	expect(resolved.ok).toBe(true);
	if (!resolved.ok) return;
	state = resolved.value;
	expect(nextOutboxDispatch(state)).toMatchObject({
		kind: "ready",
		entry: { deliveryId: "delivery-2" },
	});
});

test("explicit retry reuses a rejected delivery identity but never reopens uncertain work", () => {
	let state = prepared(createOutboxState("session-1"));
	state = applyOutbox(state, { type: "dispatch", ...identity });
	state = applyOutbox(state, {
		type: "reject",
		...identity,
		reason: "native rejected before acceptance",
	});
	const retried = retryOutboxDelivery(state, identity);
	expect(retried.ok).toBe(true);
	if (!retried.ok) return;
	state = retried.value;
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "prepared",
		attempt: 1,
	});
	state = applyOutbox(state, { type: "dispatch", ...identity });
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "dispatching",
		attempt: 2,
	});
});

test("compaction is queued through the same identity-safe handoff and waits for unsettled work", () => {
	let state = prepared(createOutboxState("session-1"));
	const compact = prepareCompaction(state, {
		mutationId: "compact-1",
		deliveryId: "compact-delivery-1",
		sessionKey: "session-1",
		generation: 0,
		payload: { reason: "threshold" },
	});
	expect(compact.ok).toBe(true);
	if (!compact.ok) return;
	state = compact.value;
	expect(nextOutboxDispatch(state)).toMatchObject({ kind: "ready", entry: { kind: "prompt" } });
	state = accepted(state);
	expect(nextOutboxDispatch(state)).toEqual({ kind: "blocked", reason: "active-delivery" });
	state = applyOutbox(state, { type: "settle", ...identity, stopReason: "stop" });
	expect(nextOutboxDispatch(state)).toMatchObject({ kind: "ready", entry: { kind: "compaction" } });
});

test("reconnect marks handed-off work uncertain without replaying it", () => {
	let state = prepared(createOutboxState("session-1"));
	state = accepted(state);
	const queued = prepared(state, { mutationId: "mutation-2", deliveryId: "delivery-2" });
	state = queued;
	const reconnected = reconnectOutbox(state, 1);
	expect(reconnected.ok).toBe(true);
	if (!reconnected.ok) return;
	state = reconnected.value;
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({
		status: "uncertain",
		attempt: 1,
		generation: 0,
	});
	expect(getOutboxEntry(state, "mutation-2")).toMatchObject({ status: "prepared", generation: 1 });
	expect(nextOutboxDispatch(state)).toEqual({ kind: "blocked", reason: "uncertain-delivery" });
});

test("Stop freezes and retains unsent work, then exposes verified graceful or forced disposition", () => {
	let state = prepared(createOutboxState("session-1"));
	state = accepted(state);
	state = prepared(state, { mutationId: "mutation-2", deliveryId: "delivery-2" });
	const started = beginOutboxStop(state, {
		requestId: "stop-1",
		sessionKey: "session-1",
		generation: 0,
	});
	expect(started.ok).toBe(true);
	if (!started.ok) return;
	state = started.value;
	expect(state.phase).toBe("stopping");
	expect(getOutboxEntry(state, "mutation-2")).toMatchObject({ status: "prepared", paused: true });
	expect(nextOutboxDispatch(state)).toEqual({ kind: "blocked", reason: "stopping" });
	const incomplete = verifyOutboxStop(state, {
		requestId: "stop-1",
		generation: 0,
		continuationCleared: false,
		pendingUiCancelled: true,
		generationQuiescent: false,
		forcedTermination: false,
		abortOutcome: "timed-out",
	});
	expect(incomplete).toMatchObject({ ok: false, error: { code: "invalid-stop" } });
	const stopped = verifyOutboxStop(state, {
		requestId: "stop-1",
		generation: 0,
		continuationCleared: true,
		pendingUiCancelled: true,
		generationQuiescent: true,
		forcedTermination: false,
		abortOutcome: "aborted",
	});
	expect(stopped.ok).toBe(true);
	if (!stopped.ok) return;
	state = stopped.value;
	expect(state).toMatchObject({
		phase: "stopped",
		stop: {
			verified: true,
			disposition: "graceful",
			effectDisposition: "interrupted",
			retainedDeliveryIds: ["delivery-2"],
		},
	});
	expect(getOutboxEntry(state, identity.mutationId)).toMatchObject({ status: "interrupted" });

	const resumed = resumeOutbox(state);
	expect(resumed.ok).toBe(true);
	if (!resumed.ok) return;
	state = resumed.value;
	expect(getOutboxEntry(state, "mutation-2")).toMatchObject({ status: "prepared", paused: false });
	const discarded = discardPreparedOutbox(state, ["mutation-2"]);
	expect(discarded.ok).toBe(true);
	if (discarded.ok) expect(discarded.value.entries).toHaveLength(1);
});

test("forced Stop requires quiescence and reports the accepted effect as interrupted", () => {
	let state = accepted(prepared(createOutboxState("session-1")));
	state = applyOutbox(state, {
		type: "stop.begin",
		requestId: "stop-forced",
		sessionKey: "session-1",
		generation: 0,
	});
	const stopped = verifyOutboxStop(state, {
		requestId: "stop-forced",
		generation: 0,
		continuationCleared: false,
		pendingUiCancelled: false,
		generationQuiescent: true,
		forcedTermination: true,
		abortOutcome: "timed-out",
		reason: "managed generation terminated after abort deadline",
	});
	expect(stopped).toMatchObject({
		ok: true,
		value: { phase: "stopped", stop: { disposition: "forced", effectDisposition: "interrupted" } },
	});
});

test("native queue recovery is a draft proposal and never auto-submits", () => {
	const state = createOutboxState("session-1");
	const draft = restoreNativeQueueAsDraft({
		draftId: "draft-1",
		sessionKey: "session-1",
		generation: 0,
		lane: "followUp",
		text: "recover this queued text",
		sourceDeliveryId: "native-delivery",
	});
	expect(draft).toMatchObject({
		ok: true,
		value: {
			source: "native-queue-recovery",
			requiresExplicitSubmission: true,
			text: "recover this queued text",
		},
	});
	expect(state.entries).toHaveLength(0);
});
