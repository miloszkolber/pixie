import type {
	AbortOutcome,
	DeliveryState,
	FlowError,
	FlowResult,
	NativeModel,
	NativeResource,
	NativeSessionIdentity,
	NativeThinkingLevel,
	PromptRequest,
	ReopenState,
	SessionCreateRequest,
	SessionFlowState,
	SessionReplay,
} from "./types.ts";
import {
	normalizeNativeModels,
	normalizeThinkingLevels,
	redactSecrets,
	resolveNativeTrust,
	validateCreateRequest,
	validateModelSelection,
	validatePromptRequest,
	validateThinkingSelection,
} from "./validation.ts";

function failure<T>(code: FlowError["code"], message: string): FlowResult<T> {
	return { ok: false, error: { code, message } };
}

function success<T>(value: T): FlowResult<T> {
	return { ok: true, value };
}

function sameSession(
	state: SessionFlowState,
	sessionKey: string,
	generation: number,
): FlowError | undefined {
	if (state.identity.sessionKey !== sessionKey)
		return { code: "wrong-session", message: "Session association does not match" };
	if (state.identity.childGeneration !== generation)
		return { code: "stale-generation", message: "Native session generation is no longer current" };
	return undefined;
}

function sameDelivery(
	state: SessionFlowState,
	deliveryId: string,
	runId: string,
): FlowError | undefined {
	if (!state.delivery || state.delivery.deliveryId !== deliveryId || state.delivery.runId !== runId)
		return { code: "unknown-delivery", message: "Prompt delivery is no longer active" };
	return undefined;
}

function deliveryIsActive(state: SessionFlowState): boolean {
	const status = state.delivery?.status;
	return (
		status === "prepared" ||
		status === "dispatching" ||
		status === "accepted" ||
		status === "uncertain"
	);
}

function terminalDelivery(
	state: SessionFlowState,
	delivery: DeliveryState,
	status: DeliveryState["status"],
	fields: { readonly reason?: string; readonly stopReason?: string } = {},
): SessionFlowState {
	return {
		...state,
		phase: "ready",
		delivery: { ...delivery, status, ...fields },
		abort: undefined,
	};
}

function validateIdentity(identity: NativeSessionIdentity): FlowResult<NativeSessionIdentity> {
	const request: SessionCreateRequest = { identity };
	const result = validateCreateRequest(request);
	return result.ok ? success(result.value.identity) : result;
}

export interface ReopenBegin {
	readonly sessionKey: string;
	readonly expectedGeneration: number;
	readonly requestId: string;
	readonly nextGeneration: number;
}

export interface ReopenCommit {
	readonly requestId: string;
	readonly sessionKey: string;
	readonly generation: number;
	readonly bootId: string;
	readonly nativeSessionId: string;
	readonly resources: readonly NativeResource[];
	readonly projectTrust?: boolean | null;
	readonly defaultProjectTrust?: "ask" | "always" | "never";
	readonly availableModels?: readonly NativeModel[];
	readonly availableThinkingLevels?: readonly NativeThinkingLevel[];
	readonly model?: NativeModel;
	readonly thinkingLevel?: NativeThinkingLevel;
}

export type NativeAbortResult =
	| { readonly kind: "aborted" }
	| { readonly kind: "already-idle" }
	| { readonly kind: "timed-out"; readonly reason?: string }
	| { readonly kind: "rejected"; readonly reason?: string }
	| { readonly kind: "interrupted"; readonly reason?: string };

export type SessionFlowEvent =
	| { readonly type: "prompt.prepare"; readonly request: PromptRequest }
	| {
			readonly type: "prompt.dispatch";
			readonly sessionKey: string;
			readonly generation: number;
			readonly deliveryId: string;
			readonly runId: string;
	  }
	| {
			readonly type: "prompt.accept";
			readonly sessionKey: string;
			readonly generation: number;
			readonly deliveryId: string;
			readonly runId: string;
	  }
	| {
			readonly type: "prompt.settle";
			readonly sessionKey: string;
			readonly generation: number;
			readonly deliveryId: string;
			readonly runId: string;
			readonly stopReason: string;
	  }
	| {
			readonly type: "prompt.reject";
			readonly sessionKey: string;
			readonly generation: number;
			readonly deliveryId: string;
			readonly runId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "prompt.uncertain";
			readonly sessionKey: string;
			readonly generation: number;
			readonly deliveryId: string;
			readonly runId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "abort.request";
			readonly sessionKey: string;
			readonly generation: number;
			readonly requestId: string;
			readonly runId: string;
	  }
	| {
			readonly type: "abort.result";
			readonly sessionKey: string;
			readonly generation: number;
			readonly requestId: string;
			readonly runId: string;
			readonly result: NativeAbortResult;
	  }
	| { readonly type: "reopen.begin"; readonly request: ReopenBegin }
	| { readonly type: "reopen.commit"; readonly commit: ReopenCommit }
	| {
			readonly type: "reopen.fail";
			readonly sessionKey: string;
			readonly requestId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "model.set";
			readonly sessionKey: string;
			readonly generation: number;
			readonly model: NativeModel;
	  }
	| {
			readonly type: "thinking.set";
			readonly sessionKey: string;
			readonly generation: number;
			readonly level: NativeThinkingLevel;
	  }
	| { readonly type: "close" };

function updateModel(
	state: SessionFlowState,
	sessionKey: string,
	generation: number,
	model: NativeModel,
): FlowResult<SessionFlowState> {
	const identityError = sameSession(state, sessionKey, generation);
	if (identityError) return { ok: false, error: identityError };
	if (state.phase !== "ready")
		return failure("invalid-phase", "Model changes require an idle native session");
	const selected = validateModelSelection(model, state.availableModels);
	if (!selected.ok) return selected;
	return success({ ...state, model: selected.value });
}

function updateThinking(
	state: SessionFlowState,
	sessionKey: string,
	generation: number,
	level: NativeThinkingLevel,
): FlowResult<SessionFlowState> {
	const identityError = sameSession(state, sessionKey, generation);
	if (identityError) return { ok: false, error: identityError };
	if (state.phase !== "ready")
		return failure("invalid-phase", "Thinking changes require an idle native session");
	const selected = validateThinkingSelection(level, state.availableThinkingLevels);
	if (!selected.ok) return selected;
	return success({ ...state, thinkingLevel: selected.value });
}

function beginPrompt(
	state: SessionFlowState,
	request: PromptRequest,
): FlowResult<SessionFlowState> {
	const identityError = sameSession(state, request.sessionKey, request.generation);
	if (identityError) return { ok: false, error: identityError };
	if (state.phase !== "ready")
		return failure("invalid-phase", "Native session is not ready for a prompt");
	if (deliveryIsActive(state))
		return failure("duplicate-delivery", "Prompt delivery is already active");
	const prompt = validatePromptRequest(request);
	if (!prompt.ok) return prompt;
	return success({
		...state,
		phase: "prompting",
		delivery: {
			deliveryId: request.deliveryId,
			runId: request.runId,
			generation: request.generation,
			status: "prepared",
			prompt: prompt.value,
		},
		abort: undefined,
		lastAbort: undefined,
	});
}

function promptTransition(
	state: SessionFlowState,
	event: Extract<SessionFlowEvent, { type: `prompt.${string}` }>,
): FlowResult<SessionFlowState> {
	if (event.type === "prompt.prepare") return beginPrompt(state, event.request);
	const identityError = sameSession(state, event.sessionKey, event.generation);
	if (identityError) return { ok: false, error: identityError };
	const deliveryError = sameDelivery(state, event.deliveryId, event.runId);
	if (deliveryError) return { ok: false, error: deliveryError };
	const delivery = state.delivery;
	if (!delivery) return failure("unknown-delivery", "Prompt delivery is no longer active");
	if (event.type === "prompt.dispatch") {
		if (delivery.status !== "prepared")
			return failure("invalid-delivery-state", "Prompt is not prepared for dispatch");
		return success({ ...state, delivery: { ...delivery, status: "dispatching" } });
	}
	if (event.type === "prompt.accept") {
		if (delivery.status !== "dispatching")
			return failure("invalid-delivery-state", "Native prompt was not dispatching");
		return success({ ...state, phase: "prompting", delivery: { ...delivery, status: "accepted" } });
	}
	if (event.type === "prompt.settle") {
		if (delivery.status !== "accepted" && delivery.status !== "uncertain")
			return failure("invalid-delivery-state", "Native settlement arrived before acceptance");
		if (!event.stopReason)
			return failure("invalid-request", "Native settlement requires a stop reason");
		return success(terminalDelivery(state, delivery, "settled", { stopReason: event.stopReason }));
	}
	if (event.type === "prompt.reject") {
		if (delivery.status !== "prepared" && delivery.status !== "dispatching")
			return failure("invalid-delivery-state", "Prompt cannot be rejected after native acceptance");
		return success(
			terminalDelivery(state, delivery, "rejected", { reason: event.reason || "native rejection" }),
		);
	}
	if (
		delivery.status !== "dispatching" &&
		delivery.status !== "accepted" &&
		delivery.status !== "uncertain"
	)
		return failure("invalid-delivery-state", "Prompt is not eligible for an uncertain outcome");
	return success({
		...state,
		phase: "prompting",
		delivery: {
			...delivery,
			status: "uncertain",
			reason: event.reason || "delivery outcome is uncertain",
		},
	});
}

function abortRequest(
	state: SessionFlowState,
	event: Extract<SessionFlowEvent, { type: "abort.request" }>,
): FlowResult<SessionFlowState | AbortOutcome> {
	const identityError = sameSession(state, event.sessionKey, event.generation);
	if (identityError)
		return success({
			kind: "stale-generation",
			generation: state.identity.childGeneration,
			runId: event.runId,
			reason: identityError.message,
		});
	if (!deliveryIsActive(state) || !state.delivery || state.delivery.runId !== event.runId) {
		return success({ kind: "already-idle", generation: event.generation, runId: event.runId });
	}
	if (state.phase !== "prompting" && state.phase !== "aborting")
		return failure("invalid-phase", "Native session cannot be aborted in its current phase");
	if (
		!state.delivery ||
		(state.delivery.status !== "prepared" &&
			state.delivery.status !== "dispatching" &&
			state.delivery.status !== "accepted" &&
			state.delivery.status !== "uncertain")
	)
		return failure("invalid-delivery-state", "Prompt is not active in the native session");
	return success({
		...state,
		phase: "aborting",
		abort: { requestId: event.requestId, generation: event.generation, runId: event.runId },
	});
}

function abortResult(
	state: SessionFlowState,
	event: Extract<SessionFlowEvent, { type: "abort.result" }>,
): FlowResult<SessionFlowState> {
	const identityError = sameSession(state, event.sessionKey, event.generation);
	if (identityError) return { ok: false, error: identityError };
	if (
		!state.abort ||
		state.abort.requestId !== event.requestId ||
		state.abort.runId !== event.runId
	)
		return failure("unknown-delivery", "Abort request is no longer active");
	const result: AbortOutcome = {
		kind: event.result.kind,
		generation: event.generation,
		runId: event.runId,
		...(typeof event.result === "object" && "reason" in event.result && event.result.reason
			? { reason: event.result.reason }
			: {}),
	};
	if (event.result.kind === "aborted" || event.result.kind === "interrupted") {
		return success({
			...terminalDelivery(state, state.delivery!, "interrupted", {
				reason:
					(typeof event.result === "object" && "reason" in event.result
						? event.result.reason
						: undefined) ??
					(event.result.kind === "aborted" ? "native abort accepted" : "native run interrupted"),
			}),
			lastAbort: result,
		});
	}
	if (event.result.kind === "already-idle") {
		return success({
			...terminalDelivery(state, state.delivery!, "interrupted", {
				reason: "native session was already idle",
			}),
			lastAbort: result,
		});
	}
	// A timeout or rejection is not evidence that native execution stopped.
	// Keep the delivery and mark the outcome, so callers cannot immediately
	// replay the prompt as if it had never been accepted.
	return success({ ...state, phase: "prompting", abort: undefined, lastAbort: result });
}

function beginReopen(state: SessionFlowState, request: ReopenBegin): FlowResult<SessionFlowState> {
	const identityError = sameSession(state, request.sessionKey, request.expectedGeneration);
	if (identityError) return { ok: false, error: identityError };
	if (!request.requestId || request.requestId.includes("\0"))
		return failure("invalid-reopen", "Reopen request identity is invalid");
	if (request.nextGeneration !== state.identity.childGeneration + 1)
		return failure("invalid-reopen", "Reopen generation must advance exactly once");
	if (state.phase !== "ready" || deliveryIsActive(state))
		return failure("invalid-phase", "Only an idle native session can be reopened");
	const reopen: ReopenState = {
		requestId: request.requestId,
		fromGeneration: state.identity.childGeneration,
		generation: request.nextGeneration,
	};
	return success({
		...state,
		phase: "reopening",
		identity: { ...state.identity, childGeneration: request.nextGeneration },
		reopen,
		abort: undefined,
		lastAbort: undefined,
	});
}

function commitReopen(state: SessionFlowState, commit: ReopenCommit): FlowResult<SessionFlowState> {
	if (state.phase !== "reopening" || !state.reopen)
		return failure("invalid-reopen", "No reopen is awaiting completion");
	if (
		state.reopen.requestId !== commit.requestId ||
		state.identity.sessionKey !== commit.sessionKey
	)
		return failure("invalid-reopen", "Reopen completion does not match the active request");
	if (state.reopen.generation !== commit.generation)
		return failure("stale-generation", "Reopen completion is from an old generation");
	if (
		!commit.bootId ||
		commit.bootId.includes("\0") ||
		commit.nativeSessionId !== state.identity.nativeSessionId
	)
		return failure("invalid-reopen", "Reopen changed the native session identity");
	const identityResult = validateIdentity({
		...state.identity,
		bootId: commit.bootId,
		childGeneration: commit.generation,
	});
	if (!identityResult.ok) return identityResult;
	const resources = resolveNativeTrust({
		resources: commit.resources,
		projectTrust: commit.projectTrust,
		defaultProjectTrust: commit.defaultProjectTrust,
	});
	if (!resources.ok) return resources;
	const models = validateModelsForReopen(commit.availableModels, state.availableModels);
	if (!models.ok) return models;
	const levels = validateLevelsForReopen(
		commit.availableThinkingLevels,
		state.availableThinkingLevels,
	);
	if (!levels.ok) return levels;
	const model = commit.model ?? state.model;
	if (model) {
		const selected = validateModelSelection(model, models.value);
		if (!selected.ok) return selected;
	}
	const thinkingLevel = commit.thinkingLevel ?? state.thinkingLevel;
	if (thinkingLevel !== undefined) {
		const selected = validateThinkingSelection(thinkingLevel, levels.value);
		if (!selected.ok) return selected;
	}
	return success({
		...state,
		phase: "ready",
		identity: identityResult.value,
		resources: resources.value.resources,
		loadedResources: resources.value.loadedResources,
		trust: resources.value.trust,
		availableModels: models.value,
		availableThinkingLevels: levels.value,
		...(model ? { model } : { model: undefined }),
		...(thinkingLevel !== undefined ? { thinkingLevel } : { thinkingLevel: undefined }),
		delivery: undefined,
		abort: undefined,
		reopen: undefined,
	});
}

function validateModelsForReopen(
	models: readonly NativeModel[] | undefined,
	previous: readonly NativeModel[],
): FlowResult<readonly NativeModel[]> {
	if (models === undefined) return success(previous);
	return normalizeNativeModels(models);
}

function validateLevelsForReopen(
	levels: readonly NativeThinkingLevel[] | undefined,
	previous: readonly NativeThinkingLevel[],
): FlowResult<readonly NativeThinkingLevel[]> {
	if (levels === undefined) return success(previous);
	return normalizeThinkingLevels(levels);
}

function closeFlow(state: SessionFlowState): FlowResult<SessionFlowState> {
	if (state.phase === "closed") return success(state);
	return success({
		...state,
		phase: "closed",
		delivery: undefined,
		abort: undefined,
		reopen: undefined,
	});
}

export function createSessionFlow(request: SessionCreateRequest): FlowResult<SessionFlowState> {
	const valid = validateCreateRequest(request);
	if (!valid.ok) return valid;
	const resources = resolveNativeTrust(valid.value);
	if (!resources.ok) return resources;
	return success({
		version: 1,
		identity: valid.value.identity,
		phase: "ready",
		resources: resources.value.resources,
		loadedResources: resources.value.loadedResources,
		trust: resources.value.trust,
		availableModels: valid.value.availableModels ?? [],
		availableThinkingLevels: valid.value.availableThinkingLevels ?? [],
		...(valid.value.model ? { model: valid.value.model } : {}),
		...(valid.value.thinkingLevel ? { thinkingLevel: valid.value.thinkingLevel } : {}),
	});
}

export function transitionSessionFlow(
	state: SessionFlowState,
	event: SessionFlowEvent,
): FlowResult<SessionFlowState | AbortOutcome> {
	if (event.type === "close") return closeFlow(state);
	if (state.phase === "closed") return failure("session-closed", "Native session is closed");
	if (event.type.startsWith("prompt."))
		return promptTransition(
			state,
			event as Extract<SessionFlowEvent, { type: `prompt.${string}` }>,
		);
	if (event.type === "abort.request") return abortRequest(state, event);
	if (event.type === "abort.result") return abortResult(state, event);
	if (event.type === "reopen.begin") return beginReopen(state, event.request);
	if (event.type === "reopen.commit") return commitReopen(state, event.commit);
	if (event.type === "reopen.fail") {
		if (
			state.phase !== "reopening" ||
			state.identity.sessionKey !== event.sessionKey ||
			state.reopen?.requestId !== event.requestId
		)
			return failure("invalid-reopen", "Reopen failure does not match the active request");
		return success({
			...state,
			phase: "closed",
			delivery: undefined,
			abort: undefined,
			reopen: undefined,
		});
	}
	if (event.type === "model.set")
		return updateModel(state, event.sessionKey, event.generation, event.model);
	if (event.type === "thinking.set")
		return updateThinking(state, event.sessionKey, event.generation, event.level);
	return failure("invalid-request", "Unsupported session flow event");
}

export class SessionFlowCoordinator {
	private current: SessionFlowState;

	constructor(request: SessionCreateRequest) {
		const created = createSessionFlow(request);
		if (!created.ok) throw new Error(`${created.error.code}: ${created.error.message}`);
		this.current = created.value;
	}

	get state(): SessionFlowState {
		return this.current;
	}

	apply(event: SessionFlowEvent): FlowResult<SessionFlowState | AbortOutcome> {
		const result = transitionSessionFlow(this.current, event);
		if (result.ok && "phase" in result.value) this.current = result.value;
		return result;
	}

	replay(): SessionReplay {
		return replaySessionState(this.current);
	}
}

export function isCurrentGeneration(state: SessionFlowState, generation: number): boolean {
	return (
		state.phase !== "reopening" &&
		state.phase !== "closed" &&
		state.identity.childGeneration === generation
	);
}

export function replaySessionState(state: SessionFlowState): SessionReplay {
	const replay = {
		version: state.version,
		sessionKey: state.identity.sessionKey,
		sessionId: state.identity.sessionId,
		nativeSessionId: state.identity.nativeSessionId,
		bootId: state.identity.bootId,
		childGeneration: state.identity.childGeneration,
		phase: state.phase,
		trust: { state: state.trust.state, allowed: state.trust.allowed, reason: state.trust.reason },
		resources: state.resources,
		loadedResources: state.loadedResources,
		...(state.model ? { model: state.model } : {}),
		...(state.thinkingLevel !== undefined ? { thinkingLevel: state.thinkingLevel } : {}),
		...(state.delivery
			? {
					delivery: {
						deliveryId: state.delivery.deliveryId,
						runId: state.delivery.runId,
						generation: state.delivery.generation,
						status: state.delivery.status,
					},
				}
			: {}),
		...(state.lastAbort
			? {
					lastAbort: {
						kind: state.lastAbort.kind,
						generation: state.lastAbort.generation,
						...(state.lastAbort.runId ? { runId: state.lastAbort.runId } : {}),
					},
				}
			: {}),
	};
	return redactSecrets(replay) as SessionReplay;
}

export type { AbortOutcome, SessionFlowState, SessionReplay } from "./types.ts";
