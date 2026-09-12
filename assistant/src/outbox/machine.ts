import type {
	DispatchDecision,
	NativeQueueDraftInput,
	NativeQueueDraftProposal,
	OutboxDeliveryIdentity,
	OutboxEntry,
	OutboxError,
	OutboxEvent,
	OutboxPayload,
	OutboxPrepareInput,
	OutboxResult,
	OutboxState,
	OutboxStopRecord,
	OutboxStopVerification,
} from "./types.ts";
import { OUTBOX_VERSION } from "./types.ts";

function success<T>(value: T): OutboxResult<T> {
	return { ok: true, value };
}

function failure<T>(code: OutboxError["code"], message: string): OutboxResult<T> {
	return { ok: false, error: { code, message } };
}

function validId(value: unknown): value is string {
	return (
		typeof value === "string" && value.length > 0 && value.length <= 512 && !value.includes("\0")
	);
}

function validGeneration(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function nonEmptyText(value: unknown): value is string {
	return typeof value === "string" && value.trim().length > 0;
}

/**
 * Deterministically represent a payload for mutation identity checks.
 * Object key order is ignored, array order is retained, and unsupported
 * values/cycles are rejected rather than silently producing a weak identity.
 */
export function stablePayloadFingerprint(payload: OutboxPayload): string {
	const active = new WeakSet<object>();
	const encode = (value: unknown): string => {
		if (value === null) return "null";
		switch (typeof value) {
			case "undefined":
				return "undefined";
			case "string":
				return `string:${JSON.stringify(value)}`;
			case "boolean":
				return `boolean:${value}`;
			case "number":
				if (Number.isNaN(value)) return "number:NaN";
				if (value === Infinity) return "number:+Infinity";
				if (value === -Infinity) return "number:-Infinity";
				return `number:${Object.is(value, -0) ? "-0" : String(value)}`;
			case "bigint":
				return `bigint:${value.toString()}`;
			case "function":
			case "symbol":
				throw new TypeError("Outbox payload contains an unsupported value");
		}

		if (active.has(value)) throw new TypeError("Outbox payload contains a cycle");
		active.add(value);
		try {
			if (Array.isArray(value)) return `array:[${value.map(encode).join(",")}]`;
			const keys = Object.keys(value).sort();
			return `object:{${keys.map((key) => `${JSON.stringify(key)}:${encode((value as Record<string, unknown>)[key])}`).join(",")}}`;
		} finally {
			active.delete(value);
		}
	};
	return encode(payload);
}

export function createOutboxState(sessionKey: string, generation = 0): OutboxState {
	if (!validId(sessionKey)) throw new TypeError("Outbox session key is invalid");
	if (!validGeneration(generation)) throw new TypeError("Outbox generation is invalid");
	return { version: OUTBOX_VERSION, sessionKey, generation, phase: "open", entries: [] };
}

export function getOutboxEntry(
	state: OutboxState,
	mutationId: string,
	deliveryId?: string,
): OutboxEntry | undefined {
	return state.entries.find(
		(entry) =>
			entry.mutationId === mutationId &&
			(deliveryId === undefined || entry.deliveryId === deliveryId),
	);
}

function replaceEntry(state: OutboxState, replacement: OutboxEntry): OutboxState {
	const entries = state.entries.map((entry) =>
		entry.deliveryId === replacement.deliveryId ? replacement : entry,
	);
	if (state.stop?.phase !== "stopped") return { ...state, entries };
	const effects = stopEffect(entries);
	return {
		...state,
		entries,
		stop: {
			...state.stop,
			retainedDeliveryIds: entries
				.filter((entry) => entry.status === "prepared")
				.map((entry) => entry.deliveryId),
			...effects,
		},
	};
}

function validPrepare(request: OutboxPrepareInput): OutboxError | undefined {
	if (!validId(request.mutationId) || !validId(request.deliveryId) || !validId(request.sessionKey))
		return {
			code: "invalid-request",
			message: "Mutation, delivery and session identities are required",
		};
	if (!validGeneration(request.generation))
		return { code: "invalid-request", message: "Generation is invalid" };
	if (
		request.kind !== undefined &&
		!["prompt", "continuation", "compaction"].includes(request.kind)
	)
		return { code: "invalid-request", message: "Outbox operation kind is invalid" };
	if (request.runId !== undefined && !validId(request.runId))
		return { code: "invalid-request", message: "Run identity is invalid" };
	if (request.parentDeliveryId !== undefined && !validId(request.parentDeliveryId))
		return { code: "invalid-request", message: "Parent delivery identity is invalid" };
	if (request.automatic !== undefined && typeof request.automatic !== "boolean")
		return { code: "invalid-request", message: "Automatic continuation flag is invalid" };
	if (
		request.queuedAt !== undefined &&
		(!Number.isFinite(request.queuedAt) || request.queuedAt < 0)
	)
		return { code: "invalid-request", message: "Queue timestamp is invalid" };
	return undefined;
}

function preparedEntry(request: OutboxPrepareInput, fingerprint: string): OutboxEntry {
	return {
		mutationId: request.mutationId,
		deliveryId: request.deliveryId,
		sessionKey: request.sessionKey,
		generation: request.generation,
		kind: request.kind ?? "prompt",
		runId: request.runId,
		parentDeliveryId: request.parentDeliveryId,
		automatic: request.automatic ?? false,
		payload: request.payload,
		fingerprint,
		status: "prepared",
		attempt: 0,
		queuedAt: request.queuedAt,
		paused: false,
	};
}

function samePreparation(
	entry: OutboxEntry,
	request: OutboxPrepareInput,
	fingerprint: string,
): boolean {
	return (
		entry.deliveryId === request.deliveryId &&
		entry.sessionKey === request.sessionKey &&
		entry.generation === request.generation &&
		entry.kind === (request.kind ?? "prompt") &&
		entry.runId === request.runId &&
		entry.parentDeliveryId === request.parentDeliveryId &&
		entry.automatic === (request.automatic ?? false) &&
		entry.fingerprint === fingerprint
	);
}

function identityError(
	state: OutboxState,
	event: { readonly mutationId: string; readonly deliveryId: string },
): OutboxResult<OutboxEntry> {
	const byMutation = state.entries.find((entry) => entry.mutationId === event.mutationId);
	if (!byMutation) return failure("unknown-delivery", "Outbox delivery is not known");
	if (byMutation.deliveryId !== event.deliveryId)
		return failure("mutation-conflict", "Mutation identity is already bound to another delivery");
	return success(byMutation);
}

function eventScopeError(
	state: OutboxState,
	event: { readonly sessionKey: string; readonly generation: number },
): OutboxError | undefined {
	if (event.sessionKey !== state.sessionKey)
		return { code: "invalid-request", message: "Session association does not match" };
	if (event.generation !== state.generation)
		return { code: "stale-generation", message: "Outbox generation is no longer current" };
}

function entryGenerationError(entry: OutboxEntry, generation: number): OutboxError | undefined {
	if (entry.generation !== generation)
		return {
			code: "stale-generation",
			message: "Delivery belongs to a previous native generation",
		};
}

function active(entry: OutboxEntry): boolean {
	return entry.status === "dispatching" || entry.status === "accepted";
}

function unsettled(entry: OutboxEntry): boolean {
	return (
		entry.status === "prepared" ||
		entry.status === "dispatching" ||
		entry.status === "accepted" ||
		entry.status === "uncertain"
	);
}

function dispatchBlock(
	state: OutboxState,
): "ready" | "stopping" | "stopped" | "uncertain-delivery" | "active-delivery" {
	if (state.phase === "stopping") return "stopping";
	if (state.phase === "stopped") return "stopped";
	if (state.entries.some((entry) => entry.status === "uncertain")) return "uncertain-delivery";
	if (state.entries.some(active)) return "active-delivery";
	return "ready";
}

/** Return the one item whose persisted claim may be dispatched next. */
export function nextOutboxDispatch(state: OutboxState): DispatchDecision {
	const block = dispatchBlock(state);
	if (block !== "ready") return { kind: "blocked", reason: block };
	for (const entry of state.entries) {
		if (entry.status !== "prepared" || entry.paused) continue;
		if (
			entry.kind === "compaction" &&
			state.entries.some((candidate) => candidate !== entry && unsettled(candidate))
		)
			return { kind: "blocked", reason: "compaction-blocked" };
		if (entry.automatic && entry.parentDeliveryId) {
			const parent = state.entries.find(
				(candidate) => candidate.deliveryId === entry.parentDeliveryId,
			);
			if (!parent || parent.status !== "settled")
				return { kind: "blocked", reason: "parent-unsettled" };
		}
		return { kind: "ready", entry };
	}
	return { kind: "idle" };
}

function transitionPrepare(
	state: OutboxState,
	request: OutboxPrepareInput,
): OutboxResult<OutboxState> {
	const error = validPrepare(request);
	if (error) return { ok: false, error };
	if (request.sessionKey !== state.sessionKey)
		return failure("invalid-request", "Outbox session association does not match");
	let fingerprint: string;
	try {
		fingerprint = stablePayloadFingerprint(request.payload);
	} catch (error) {
		return failure(
			"invalid-request",
			error instanceof Error ? error.message : "Outbox payload cannot be fingerprinted",
		);
	}
	const byMutation = state.entries.find((entry) => entry.mutationId === request.mutationId);
	if (byMutation) {
		if (!samePreparation(byMutation, request, fingerprint))
			return failure(
				"mutation-conflict",
				"Mutation identity is already bound to a different payload or delivery",
			);
		// A duplicate prepare is an idempotent read of the known delivery.  It
		// never unpauses or resets a delivery that Stop retained.
		return success(state);
	}
	const byDelivery = state.entries.find((entry) => entry.deliveryId === request.deliveryId);
	if (byDelivery)
		return failure("duplicate-delivery", "Delivery identity is already in the outbox");
	if (request.generation !== state.generation)
		return failure("stale-generation", "New work must target the current outbox generation");
	if (state.phase === "stopping") return failure("stopping", "Outbox admission is frozen by Stop");
	if (state.phase === "stopped")
		return failure("stopped", "Resume the outbox before preparing new work");
	return success({ ...state, entries: [...state.entries, preparedEntry(request, fingerprint)] });
}

function transitionDispatch(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "dispatch" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	const entry = known.value;
	const generationError = entryGenerationError(entry, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (state.phase === "stopping") return failure("stopping", "Stop froze outbox dispatch");
	if (state.phase === "stopped") return failure("stopped", "Outbox is stopped");
	if (entry.status !== "prepared") {
		// Repeating a dispatch request is a status read, never another native send.
		if (
			entry.status === "dispatching" ||
			entry.status === "accepted" ||
			entry.status === "settled" ||
			entry.status === "uncertain"
		)
			return success(state);
		return failure("invalid-state", "Only prepared work can be dispatched");
	}
	if (entry.paused) return failure("invalid-state", "Prepared delivery is paused");
	const next = nextOutboxDispatch(state);
	if (next.kind !== "ready")
		return failure(
			"dispatch-blocked",
			next.kind === "blocked"
				? `Outbox dispatch is blocked: ${next.reason}`
				: "Outbox has no prepared work",
		);
	if (next.entry.deliveryId !== entry.deliveryId)
		return failure("dispatch-blocked", "Earlier outbox work must be dispatched first");
	return success(
		replaceEntry(state, { ...entry, status: "dispatching", attempt: entry.attempt + 1 }),
	);
}

function transitionAccept(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "accept" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	const entry = known.value;
	const generationError = entryGenerationError(entry, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (state.phase === "stopped")
		return failure("stopped", "Late native acceptance cannot revive a stopped outbox");
	if (
		entry.status === "accepted" ||
		entry.status === "settled" ||
		entry.status === "rejected" ||
		entry.status === "interrupted"
	)
		return success(state);
	if (entry.status === "uncertain") return success(state);
	if (entry.status !== "dispatching")
		return failure("invalid-state", "Native acceptance requires a dispatching delivery");
	return success(replaceEntry(state, { ...entry, status: "accepted" }));
}

function transitionSettle(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "settle" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	if (!nonEmptyText(event.stopReason))
		return failure("invalid-request", "Settlement requires a stop reason");
	const entry = known.value;
	const generationError = entryGenerationError(entry, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (state.phase === "stopped")
		return failure("stopped", "Late native settlement cannot revive a stopped outbox");
	if (entry.status === "settled") return success(state);
	if (entry.status !== "accepted" && entry.status !== "uncertain")
		return failure("invalid-state", "Native settlement requires accepted or uncertain delivery");
	return success(
		replaceEntry(state, {
			...entry,
			status: "settled",
			stopReason: event.stopReason,
			reason: undefined,
			paused: false,
		}),
	);
}

function transitionReject(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "reject" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	const entry = known.value;
	const generationError = entryGenerationError(entry, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (!nonEmptyText(event.reason)) return failure("invalid-request", "Rejection requires a reason");
	if (entry.status === "rejected" || entry.status === "settled" || entry.status === "interrupted")
		return success(state);
	if (entry.status !== "prepared" && entry.status !== "dispatching")
		return failure("invalid-state", "An accepted delivery cannot be rewritten as a rejection");
	return success(
		replaceEntry(state, { ...entry, status: "rejected", reason: event.reason, paused: false }),
	);
}

function transitionUncertain(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "uncertain" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	const generationError = entryGenerationError(known.value, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (!nonEmptyText(event.reason))
		return failure("invalid-request", "Uncertain delivery requires a reason");
	const entry = known.value;
	if (entry.status === "uncertain")
		return success(replaceEntry(state, { ...entry, reason: event.reason }));
	if (entry.status === "settled" || entry.status === "rejected" || entry.status === "interrupted")
		return success(state);
	if (entry.status !== "dispatching" && entry.status !== "accepted")
		return failure("invalid-state", "Only dispatched or accepted work can become uncertain");
	return success(replaceEntry(state, { ...entry, status: "uncertain", reason: event.reason }));
}

function transitionInterrupt(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "interrupt" }>,
): OutboxResult<OutboxState> {
	const scopeError = eventScopeError(state, event);
	if (scopeError) return { ok: false, error: scopeError };
	const known = identityError(state, event);
	if (!known.ok) return known;
	const generationError = entryGenerationError(known.value, event.generation);
	if (generationError) return { ok: false, error: generationError };
	if (!nonEmptyText(event.reason))
		return failure("invalid-request", "Interrupted delivery requires a reason");
	const entry = known.value;
	if (entry.status === "interrupted" || entry.status === "settled" || entry.status === "rejected")
		return success(state);
	if (entry.status !== "dispatching" && entry.status !== "accepted" && entry.status !== "uncertain")
		return failure("invalid-state", "Only handed-off work can be interrupted");
	return success(
		replaceEntry(state, { ...entry, status: "interrupted", reason: event.reason, paused: false }),
	);
}

function transitionRetry(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "retry" }>,
): OutboxResult<OutboxState> {
	const entry = getOutboxEntry(state, event.mutationId, event.deliveryId);
	if (!entry) {
		const byMutation = state.entries.find((candidate) => candidate.mutationId === event.mutationId);
		return byMutation
			? failure("mutation-conflict", "Mutation identity is already bound to another delivery")
			: failure("unknown-delivery", "Outbox delivery is not known");
	}
	if (event.fingerprint !== undefined && event.fingerprint !== entry.fingerprint)
		return failure(
			"mutation-conflict",
			"Retry payload fingerprint does not match the original mutation",
		);
	// An explicit retry may reopen only a delivery that native explicitly
	// rejected before acceptance.  The identity and payload remain unchanged;
	// dispatch will persist the next claim before another send.  Accepted,
	// settled, interrupted and uncertain work is a status read, not a replay.
	if (entry.status === "rejected" && state.phase === "open")
		return success(
			replaceEntry(state, { ...entry, status: "prepared", reason: undefined, paused: false }),
		);
	// Retry is deliberately a status lookup.  In particular, uncertain work
	// cannot be resent because its first native effect may already exist.
	return success(state);
}

function transitionReconcile(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "reconcile" }>,
): OutboxResult<OutboxState> {
	const entry = getOutboxEntry(state, event.mutationId, event.deliveryId);
	if (!entry) return failure("unknown-delivery", "Outbox delivery is not known");
	if (entry.status !== "uncertain")
		return failure("uncertain-delivery", "Only uncertain deliveries require reconciliation");
	if (event.outcome === "uncertain") {
		return success(
			nonEmptyText(event.reason) ? replaceEntry(state, { ...entry, reason: event.reason }) : state,
		);
	}
	if (event.outcome === "settled") {
		if (!nonEmptyText(event.stopReason))
			return failure("invalid-request", "Reconciled settlement requires a stop reason");
		return success(
			replaceEntry(state, {
				...entry,
				status: "settled",
				stopReason: event.stopReason,
				reason: undefined,
				paused: false,
			}),
		);
	}
	if (!nonEmptyText(event.reason))
		return failure("invalid-request", "Reconciled outcome requires a reason");
	return success(
		replaceEntry(state, {
			...entry,
			status: event.outcome,
			reason: event.reason,
			paused: false,
		}),
	);
}

function beginStop(
	state: OutboxState,
	event: Extract<OutboxEvent, { type: "stop.begin" }>,
): OutboxResult<OutboxState> {
	if (!validId(event.requestId)) return failure("invalid-stop", "Stop request identity is invalid");
	if (event.sessionKey !== state.sessionKey || event.generation !== state.generation)
		return failure("stale-generation", "Stop request is not for the current session generation");
	if (state.phase === "stopped") {
		if (state.stop?.requestId === event.requestId) return success(state);
		return failure("invalid-stop", "Outbox already has a completed Stop");
	}
	if (state.phase === "stopping") {
		if (state.stop?.requestId === event.requestId) return success(state);
		return failure("invalid-stop", "A different Stop request is already active");
	}
	const entries = state.entries.map((entry) =>
		entry.status === "prepared" ? { ...entry, paused: true } : entry,
	);
	const retainedDeliveryIds = entries
		.filter((entry) => entry.status === "prepared")
		.map((entry) => entry.deliveryId);
	const stop: OutboxStopRecord = {
		requestId: event.requestId,
		generation: state.generation,
		phase: "stopping",
		retainedDeliveryIds,
		uncertainDeliveryIds: entries
			.filter((entry) => entry.status === "uncertain")
			.map((entry) => entry.deliveryId),
		interruptedDeliveryIds: [],
	};
	return success({ ...state, phase: "stopping", entries, stop });
}

function stopEffect(
	entries: readonly OutboxEntry[],
): Pick<OutboxStopRecord, "uncertainDeliveryIds" | "interruptedDeliveryIds" | "effectDisposition"> {
	const uncertainDeliveryIds: string[] = [];
	const interruptedDeliveryIds: string[] = [];
	for (const entry of entries) {
		if (entry.status === "uncertain") uncertainDeliveryIds.push(entry.deliveryId);
		if (entry.status === "interrupted") interruptedDeliveryIds.push(entry.deliveryId);
	}
	return {
		uncertainDeliveryIds,
		interruptedDeliveryIds,
		effectDisposition: uncertainDeliveryIds.length
			? "uncertain"
			: interruptedDeliveryIds.length
				? "interrupted"
				: "none",
	};
}

function verifyStop(
	state: OutboxState,
	verification: OutboxStopVerification,
): OutboxResult<OutboxState> {
	if (state.phase !== "stopping" || !state.stop)
		return failure("invalid-stop", "No Stop is awaiting verification");
	if (
		verification.requestId !== state.stop.requestId ||
		verification.generation !== state.generation
	)
		return failure("stale-generation", "Stop verification does not match the active generation");
	if (!verification.generationQuiescent)
		return failure("invalid-stop", "Stop requires verified native generation quiescence");
	const abortAcknowledged =
		verification.abortOutcome === "aborted" || verification.abortOutcome === "already-idle";
	if (
		!verification.forcedTermination &&
		(!verification.continuationCleared || !verification.pendingUiCancelled || !abortAcknowledged)
	)
		return failure(
			"invalid-stop",
			"Stop has not verified continuation, UI and native abort disposition",
		);
	const entries = state.entries.map((entry) => {
		if (entry.status === "dispatching")
			return {
				...entry,
				status: "uncertain" as const,
				reason: verification.reason ?? "Stop interrupted an unacknowledged delivery",
			};
		if (entry.status === "accepted")
			return {
				...entry,
				status: "interrupted" as const,
				reason: verification.reason ?? "Stop interrupted accepted native work",
			};
		return entry;
	});
	const effects = stopEffect(entries);
	const forcedUncertainty =
		verification.forcedTermination &&
		(!verification.continuationCleared ||
			!verification.pendingUiCancelled ||
			(verification.abortOutcome !== "aborted" && verification.abortOutcome !== "already-idle"));
	const reportedEffects =
		forcedUncertainty && effects.effectDisposition === "none"
			? { ...effects, effectDisposition: "uncertain" as const }
			: effects;
	const retainedDeliveryIds = entries
		.filter((entry) => entry.status === "prepared")
		.map((entry) => entry.deliveryId);
	const stop: OutboxStopRecord = {
		...state.stop,
		phase: "stopped",
		verified: true,
		disposition: verification.forcedTermination ? "forced" : "graceful",
		verification,
		retainedDeliveryIds,
		...reportedEffects,
	};
	return success({ ...state, phase: "stopped", entries, stop });
}

function resume(state: OutboxState): OutboxResult<OutboxState> {
	if (state.phase !== "stopped")
		return failure("invalid-stop", "Only a completed Stop can be resumed");
	return success({
		...state,
		phase: "open",
		entries: state.entries.map((entry) =>
			entry.status === "prepared" ? { ...entry, paused: false } : entry,
		),
	});
}

function discard(state: OutboxState, mutationIds: readonly string[]): OutboxResult<OutboxState> {
	if (state.phase === "stopping")
		return failure("stopping", "Wait for Stop verification before discarding queued work");
	if (!mutationIds.length || mutationIds.some((id) => !validId(id)))
		return failure("invalid-request", "Discard requires mutation identities");
	const ids = new Set(mutationIds);
	for (const id of ids) {
		const entry = state.entries.find((candidate) => candidate.mutationId === id);
		if (!entry) return failure("unknown-delivery", `Outbox mutation ${id} is not known`);
		if (entry.status !== "prepared")
			return failure("invalid-state", "Only unsent prepared work can be discarded");
	}
	const entries = state.entries.filter((entry) => !ids.has(entry.mutationId));
	return success({
		...state,
		entries,
		...(state.stop?.phase === "stopped"
			? {
					stop: {
						...state.stop,
						retainedDeliveryIds: entries
							.filter((entry) => entry.status === "prepared")
							.map((entry) => entry.deliveryId),
					},
				}
			: {}),
	});
}

function reconnect(state: OutboxState, generation: number): OutboxResult<OutboxState> {
	if (state.phase !== "open")
		return failure("invalid-state", "Reconnect cannot reopen a stopping or stopped outbox");
	if (!validGeneration(generation) || generation <= state.generation)
		return failure("stale-generation", "Reconnect generation must advance");
	const entries = state.entries.map((entry) => {
		if (entry.status === "dispatching" || entry.status === "accepted")
			return {
				...entry,
				status: "uncertain" as const,
				reason: "Native generation changed before delivery settlement",
			};
		if (entry.status === "prepared") return { ...entry, generation };
		return entry;
	});
	return success({ ...state, generation, entries });
}

export function transitionOutbox(
	state: OutboxState,
	event: OutboxEvent,
): OutboxResult<OutboxState> {
	switch (event.type) {
		case "prepare":
			return transitionPrepare(state, event.request);
		case "dispatch":
			return transitionDispatch(state, event);
		case "accept":
			return transitionAccept(state, event);
		case "settle":
			return transitionSettle(state, event);
		case "reject":
			return transitionReject(state, event);
		case "uncertain":
			return transitionUncertain(state, event);
		case "interrupt":
			return transitionInterrupt(state, event);
		case "retry":
			return transitionRetry(state, event);
		case "reconcile":
			return transitionReconcile(state, event);
		case "stop.begin":
			return beginStop(state, event);
		case "stop.verify":
			return verifyStop(state, event.verification);
		case "resume":
			return resume(state);
		case "discard":
			return discard(state, event.mutationIds);
		case "reconnect":
			return reconnect(state, event.generation);
	}
}

function requireTransition<T>(result: OutboxResult<T>): T {
	if (!result.ok) throw new Error(`${result.error.code}: ${result.error.message}`);
	return result.value;
}

export function prepareOutboxDelivery(
	state: OutboxState,
	request: OutboxPrepareInput,
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "prepare", request });
}

export function enqueueContinuation(
	state: OutboxState,
	request: Omit<OutboxPrepareInput, "kind">,
): OutboxResult<OutboxState> {
	return prepareOutboxDelivery(state, {
		...request,
		kind: "continuation",
		automatic: request.automatic ?? true,
	});
}

export function prepareCompaction(
	state: OutboxState,
	request: Omit<OutboxPrepareInput, "kind">,
): OutboxResult<OutboxState> {
	return prepareOutboxDelivery(state, { ...request, kind: "compaction", automatic: false });
}

export function dispatchOutboxDelivery(
	state: OutboxState,
	identity: OutboxDeliveryIdentity,
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "dispatch", ...identity });
}

export function acceptOutboxDelivery(
	state: OutboxState,
	identity: OutboxDeliveryIdentity,
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "accept", ...identity });
}

export function settleOutboxDelivery(
	state: OutboxState,
	identity: OutboxDeliveryIdentity & { readonly stopReason: string },
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "settle", ...identity });
}

export function retryOutboxDelivery(
	state: OutboxState,
	identity: Pick<OutboxEntry, "mutationId"> & {
		readonly deliveryId?: string;
		readonly fingerprint?: string;
	},
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "retry", ...identity });
}

export function reconcileUncertainDelivery(
	state: OutboxState,
	identity: Pick<OutboxEntry, "mutationId" | "deliveryId"> & {
		readonly outcome: Extract<OutboxEvent, { type: "reconcile" }>["outcome"];
		readonly reason?: string;
		readonly stopReason?: string;
	},
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "reconcile", ...identity });
}

export function beginOutboxStop(
	state: OutboxState,
	request: Omit<Extract<OutboxEvent, { type: "stop.begin" }>, "type">,
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "stop.begin", ...request });
}

export function verifyOutboxStop(
	state: OutboxState,
	verification: OutboxStopVerification,
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "stop.verify", verification });
}

export function resumeOutbox(state: OutboxState): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "resume" });
}

export function discardPreparedOutbox(
	state: OutboxState,
	mutationIds: readonly string[],
): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "discard", mutationIds });
}

export function reconnectOutbox(state: OutboxState, generation: number): OutboxResult<OutboxState> {
	return transitionOutbox(state, { type: "reconnect", generation });
}

/**
 * Recovery of text found in a native queue is intentionally a draft-only
 * operation.  Callers must explicitly prepare a new delivery after review.
 */
export function restoreNativeQueueAsDraft(
	input: NativeQueueDraftInput,
): OutboxResult<NativeQueueDraftProposal> {
	if (!validId(input.draftId) || !validId(input.sessionKey) || !validGeneration(input.generation))
		return failure("invalid-request", "Native queue draft identity is invalid");
	if (input.lane !== "steering" && input.lane !== "followUp")
		return failure("invalid-request", "Native queue draft lane is invalid");
	if (typeof input.text !== "string" || !input.text.trim())
		return failure("invalid-request", "Native queue draft cannot be empty");
	if (input.sourceDeliveryId !== undefined && !validId(input.sourceDeliveryId))
		return failure("invalid-request", "Native queue source delivery identity is invalid");
	return success({
		draftId: input.draftId,
		sessionKey: input.sessionKey,
		generation: input.generation,
		lane: input.lane,
		text: input.text,
		sourceDeliveryId: input.sourceDeliveryId,
		source: "native-queue-recovery",
		requiresExplicitSubmission: true,
	});
}

/** Convenience for tests/callers that want exception-style composition. */
export function applyOutbox(state: OutboxState, event: OutboxEvent): OutboxState {
	return requireTransition(transitionOutbox(state, event));
}

/** Small mutable shell for an owner; transitionOutbox itself remains pure. */
export class OutboxCoordinator {
	private current: OutboxState;

	constructor(sessionKeyOrState: string | OutboxState, generation = 0) {
		this.current =
			typeof sessionKeyOrState === "string"
				? createOutboxState(sessionKeyOrState, generation)
				: sessionKeyOrState;
	}

	get state(): OutboxState {
		return this.current;
	}

	apply(event: OutboxEvent): OutboxResult<OutboxState> {
		const result = transitionOutbox(this.current, event);
		if (result.ok) this.current = result.value;
		return result;
	}
}

export type { OutboxKind, OutboxStatus } from "./types.ts";
