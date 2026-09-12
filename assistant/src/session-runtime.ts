import { randomUUID } from "node:crypto";
import {
	applyDraftMutation,
	createDraft,
	createDraftState,
	type DraftMutation,
	type DraftMutationOutcome,
	type DraftState,
	replaceDraftState,
} from "./drafts/continuity.ts";
import {
	applyOutbox,
	getOutboxEntry,
	type OutboxEntry,
	type OutboxEvent,
	type OutboxPrepareInput,
	type OutboxState,
	prepareOutboxDelivery,
	reconcileUncertainDelivery,
	stablePayloadFingerprint,
	transitionOutbox,
} from "./outbox/index.ts";
import {
	createSessionFlow,
	type NativeAbortResult,
	type SessionFlowEvent,
	transitionSessionFlow,
} from "./session/flow.ts";
import type {
	FlowError,
	NativeModel,
	NativeSessionIdentity,
	NativeThinkingLevel,
	PromptBlock,
	PromptRequest,
	SessionCreateRequest,
	SessionFlowState,
	SessionReplay,
} from "./session/types.ts";
import { redactSecrets, validatePromptRequest } from "./session/validation.ts";
import {
	applyPassiveEvent,
	applyPassiveReplay,
	createPassiveReplay,
	createPassiveState,
	type PassiveReplay,
	type PassiveState,
	type PassiveUiEvent,
} from "./ui-state/index.ts";

/** A small result type used by the mutable integration shell. */
export type RuntimeResult<T> =
	| { readonly ok: true; readonly value: T }
	| { readonly ok: false; readonly error: { readonly code: string; readonly message: string } };

export interface SessionRuntimeOptions extends Omit<SessionCreateRequest, "identity"> {
	readonly identity: NativeSessionIdentity;
	readonly outbox?: OutboxState;
	readonly drafts?: readonly DraftState<string>[];
	readonly passive?: PassiveReplay;
}

export interface PromptRuntimeRequest {
	readonly content: readonly PromptBlock[];
	readonly mutationId?: string;
	readonly deliveryId?: string;
	readonly runId?: string;
	readonly queuedAt?: number;
}

export interface PreparedPrompt {
	readonly mutationId: string;
	readonly deliveryId: string;
	readonly runId: string;
	readonly request: PromptRequest;
	readonly payload: { readonly content: readonly PromptBlock[] };
	readonly replayed: boolean;
	readonly entry: OutboxEntry;
}

export interface PromptRuntimeResult {
	readonly stopReason?: string;
	readonly mutationId: string;
	readonly deliveryId: string;
	readonly runId: string;
	readonly replayed?: true;
}

export interface RuntimeAbortResult {
	readonly outcome: NativeAbortResult["kind"];
	readonly generation: number;
	readonly runId?: string;
	readonly reason?: string;
	readonly native?: unknown;
}

export interface SessionRuntimeReplay {
	readonly sessionKey: string;
	readonly sessionId: string;
	readonly nativeSessionId: string;
	readonly childGeneration: number;
	readonly flow: SessionReplay;
	readonly outbox: {
		readonly version: number;
		readonly sessionKey: string;
		readonly generation: number;
		readonly phase: OutboxState["phase"];
		readonly entries: readonly Omit<OutboxEntry, "payload" | "fingerprint">[];
		readonly stop?: OutboxState["stop"];
	};
	readonly passive: PassiveReplay;
	readonly draft?: DraftState<string>;
	readonly residency?: {
		readonly phase: string;
		readonly releaseable: boolean;
		readonly reason: string;
	};
}

function success<T>(value: T): RuntimeResult<T> {
	return { ok: true, value };
}

function failure<T>(code: string, message: string): RuntimeResult<T> {
	return { ok: false, error: { code, message } };
}

function flowFailure<T>(error: FlowError): RuntimeResult<T> {
	return failure(error.code, error.message);
}

function validId(value: unknown): value is string {
	return (
		typeof value === "string" && value.length > 0 && value.length <= 512 && !value.includes("\0")
	);
}

function nativeAbortKind(value: unknown): NativeAbortResult["kind"] {
	if (!value || typeof value !== "object") return "rejected";
	const kind = (value as { kind?: unknown }).kind;
	if (
		kind === "aborted" ||
		kind === "already-idle" ||
		kind === "timed-out" ||
		kind === "rejected" ||
		kind === "interrupted"
	)
		return kind;
	if ((value as { aborted?: unknown }).aborted === true) return "aborted";
	return "timed-out";
}

/**
 * A native prompt crossed the SDK boundary but did not produce a settlement.
 * The caller must not retry it as a new native operation.
 */
export class UncertainPromptError extends Error {
	readonly outcome = "uncertain" as const;
	readonly code = "uncertain-delivery" as const;

	constructor() {
		super("Prompt delivery outcome is uncertain; reconcile the native session before retrying");
		this.name = "UncertainPromptError";
	}
}

/**
 * One mutable owner for the pure session, outbox, draft and passive-state
 * contracts.  It never creates an AgentSession or runs a Pi loop; callers
 * provide the one native operation to invoke at the handoff boundary.
 */
export class SessionRuntime {
	private current: SessionFlowState;
	private outboxState: OutboxState;
	private passiveState: PassiveState;
	private readonly drafts = new Map<string, DraftState<string>>();
	private uiSequence = 0;

	constructor(readonly options: SessionRuntimeOptions) {
		const created = createSessionFlow(options);
		if (!created.ok) throw new Error(`${created.error.code}: ${created.error.message}`);
		this.current = created.value;
		this.outboxState = options.outbox ?? {
			version: 1,
			sessionKey: options.identity.sessionKey,
			generation: options.identity.childGeneration,
			phase: "open",
			entries: [],
		};
		if (this.outboxState.sessionKey !== options.identity.sessionKey)
			throw new Error("Outbox session association does not match native session identity");
		for (const draft of options.drafts ?? []) {
			if (draft.draft.sessionKey === options.identity.sessionKey && draft.draft.clientId) {
				this.drafts.set(
					draft.draft.clientId,
					replaceDraftState(draft, {
						sessionKey: options.identity.sessionKey,
						identity: { ...options.identity },
					}),
				);
			}
		}
		this.passiveState = createPassiveState({
			sessionId: options.identity.sessionId,
			generation: options.identity.childGeneration,
		});
		if (options.passive && options.passive.sequence > 0) {
			const restored = applyPassiveReplay(this.passiveState, options.passive);
			if (restored.accepted) {
				this.passiveState = restored.state;
				this.uiSequence = restored.state.sequence;
			}
		}
	}

	get state(): SessionFlowState {
		return this.current;
	}

	get outbox(): OutboxState {
		return this.outboxState;
	}

	get passive(): PassiveState {
		return this.passiveState;
	}

	get identity(): NativeSessionIdentity {
		return this.current.identity;
	}

	get draftStates(): readonly DraftState<string>[] {
		return [...this.drafts.values()];
	}

	/** Work which must pin a resident, including unsent work retained by Stop. */
	get activeWorkIds(): readonly string[] {
		return this.outboxState.entries
			.filter(
				(entry) =>
					entry.status === "prepared" ||
					entry.status === "dispatching" ||
					entry.status === "accepted" ||
					entry.status === "uncertain",
			)
			.map((entry) => entry.deliveryId);
	}

	private applyFlow(event: SessionFlowEvent): RuntimeResult<SessionFlowState> {
		const result = transitionSessionFlow(this.current, event);
		if (!result.ok) return flowFailure(result.error);
		if (!("phase" in result.value))
			return failure("invalid-state", "Session flow produced a non-state result");
		this.current = result.value;
		return success(this.current);
	}

	private applyOutbox(event: OutboxEvent): RuntimeResult<OutboxState> {
		const result = transitionOutbox(this.outboxState, event);
		if (!result.ok) return failure(result.error.code, result.error.message);
		this.outboxState = result.value;
		return success(this.outboxState);
	}

	/** Prepare a prompt in both state machines before invoking native Pi. */
	preparePrompt(input: PromptRuntimeRequest): RuntimeResult<PreparedPrompt> {
		const mutationId =
			input.mutationId && validId(input.mutationId) ? input.mutationId : randomUUID();
		const prior = getOutboxEntry(this.outboxState, mutationId);
		const deliveryId =
			input.deliveryId && validId(input.deliveryId)
				? input.deliveryId
				: (prior?.deliveryId ?? randomUUID());
		const runId =
			input.runId && validId(input.runId) ? input.runId : (prior?.runId ?? randomUUID());
		const request: PromptRequest = {
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			deliveryId,
			runId,
			content: input.content,
		};
		const validated = validatePromptRequest(request);
		if (!validated.ok) return flowFailure(validated.error);
		const payload = { content: input.content } as const;
		if (prior) {
			let fingerprint: string;
			try {
				fingerprint = stablePayloadFingerprint(payload);
			} catch (error) {
				return failure(
					"invalid-request",
					error instanceof Error ? error.message : "Outbox payload cannot be fingerprinted",
				);
			}
			if (prior.deliveryId !== deliveryId || prior.fingerprint !== fingerprint)
				return failure(
					"mutation-conflict",
					"Mutation identity is already bound to a different payload or delivery",
				);
			return success({
				mutationId,
				deliveryId: prior.deliveryId,
				runId: prior.runId ?? runId,
				request,
				payload,
				replayed: true,
				entry: prior,
			});
		}
		const outboxInput: OutboxPrepareInput = {
			mutationId,
			deliveryId,
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			runId,
			kind: "prompt",
			payload,
			...(input.queuedAt !== undefined ? { queuedAt: input.queuedAt } : {}),
		};
		const prepared = prepareOutboxDelivery(this.outboxState, outboxInput);
		if (!prepared.ok) return failure(prepared.error.code, prepared.error.message);
		const existing = getOutboxEntry(prepared.value, mutationId);
		if (!existing)
			return failure("invalid-state", "Outbox preparation did not retain the delivery");
		this.outboxState = prepared.value;
		if (existing.mutationId === mutationId && existing.deliveryId !== deliveryId)
			return failure("mutation-conflict", "Mutation identity is already bound to another delivery");
		if (existing.status !== "prepared")
			return success({
				mutationId,
				deliveryId: existing.deliveryId,
				runId: existing.runId ?? runId,
				request,
				payload,
				replayed: true,
				entry: existing,
			});
		const flow = this.applyFlow({ type: "prompt.prepare", request });
		if (!flow.ok) {
			// A failed flow admission must never leave a runnable outbox entry.
			this.outboxState = applyOutbox(this.outboxState, {
				type: "discard",
				mutationIds: [mutationId],
			});
			return flow;
		}
		return success({
			mutationId,
			deliveryId,
			runId,
			request,
			payload,
			replayed: false,
			entry: existing,
		});
	}

	private deliveryIdentity(prepared: PreparedPrompt) {
		return {
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			mutationId: prepared.mutationId,
			deliveryId: prepared.deliveryId,
		};
	}

	private rejectPrompt(prepared: PreparedPrompt, reason: string): void {
		const identity = this.deliveryIdentity(prepared);
		this.applyFlow({ type: "prompt.reject", ...identity, runId: prepared.runId, reason });
		this.applyOutbox({ type: "reject", ...identity, reason });
	}

	private uncertainPrompt(prepared: PreparedPrompt, reason: string): void {
		const identity = this.deliveryIdentity(prepared);
		this.applyFlow({ type: "prompt.uncertain", ...identity, runId: prepared.runId, reason });
		this.applyOutbox({ type: "uncertain", ...identity, reason });
	}

	/**
	 * Dispatch exactly once. Native errors after the handoff are uncertain by
	 * default because the SDK does not expose a separate prompt acceptance reply.
	 */
	async executePrompt(
		input: PromptRuntimeRequest,
		invoke: (request: PromptRequest) => Promise<unknown>,
	): Promise<PromptRuntimeResult> {
		const preparedResult = this.preparePrompt(input);
		if (!preparedResult.ok)
			throw new Error(`${preparedResult.error.code}: ${preparedResult.error.message}`);
		const prepared = preparedResult.value;
		if (prepared.replayed)
			return {
				mutationId: prepared.mutationId,
				deliveryId: prepared.deliveryId,
				runId: prepared.runId,
				replayed: true,
				...(prepared.entry.stopReason ? { stopReason: prepared.entry.stopReason } : {}),
			};

		const identity = this.deliveryIdentity(prepared);
		const dispatched = this.applyFlow({
			type: "prompt.dispatch",
			...identity,
			runId: prepared.runId,
		});
		if (!dispatched.ok) {
			this.rejectPrompt(prepared, dispatched.error.message);
			throw new Error(`${dispatched.error.code}: ${dispatched.error.message}`);
		}
		const outboxDispatched = this.applyOutbox({ type: "dispatch", ...identity });
		if (!outboxDispatched.ok) {
			this.rejectPrompt(prepared, outboxDispatched.error.message);
			throw new Error(`${outboxDispatched.error.code}: ${outboxDispatched.error.message}`);
		}
		// The call itself is the only native handoff available from AgentSession.
		const accepted = this.applyFlow({ type: "prompt.accept", ...identity, runId: prepared.runId });
		if (!accepted.ok) {
			this.uncertainPrompt(prepared, "Native acceptance state could not be recorded");
			throw new UncertainPromptError();
		}
		const outboxAccepted = this.applyOutbox({ type: "accept", ...identity });
		if (!outboxAccepted.ok) {
			this.uncertainPrompt(prepared, "Outbox acceptance state could not be recorded");
			throw new UncertainPromptError();
		}

		try {
			const native = await invoke(prepared.request);
			const stopReason =
				native &&
				typeof native === "object" &&
				typeof (native as { stopReason?: unknown }).stopReason === "string"
					? (native as { stopReason: string }).stopReason
					: "end_turn";
			const settled = this.applyFlow({
				type: "prompt.settle",
				...identity,
				runId: prepared.runId,
				stopReason,
			});
			if (!settled.ok) throw new Error(`${settled.error.code}: ${settled.error.message}`);
			const outboxSettled = this.applyOutbox({ type: "settle", ...identity, stopReason });
			if (!outboxSettled.ok)
				throw new Error(`${outboxSettled.error.code}: ${outboxSettled.error.message}`);
			return {
				stopReason,
				mutationId: prepared.mutationId,
				deliveryId: prepared.deliveryId,
				runId: prepared.runId,
			};
		} catch (error) {
			if (!(error instanceof UncertainPromptError))
				this.uncertainPrompt(prepared, "Native prompt did not settle");
			throw error instanceof UncertainPromptError ? error : new UncertainPromptError();
		}
	}

	/** Apply the exact native abort result; a timeout/rejection remains uncertain. */
	async executeAbort(
		requestId: string,
		invoke: () => Promise<unknown>,
	): Promise<RuntimeAbortResult> {
		const delivery = this.current.delivery;
		const runId = delivery?.runId ?? "idle";
		const identity = {
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			requestId,
			runId,
		};
		const started = this.applyFlow({ type: "abort.request", ...identity });
		if (!started.ok) throw new Error(`${started.error.code}: ${started.error.message}`);
		if (!("phase" in started.value)) return started.value;
		let native: unknown;
		let result: NativeAbortResult;
		try {
			native = await invoke();
			const kind = nativeAbortKind(native);
			result = {
				kind,
				...(native &&
				typeof native === "object" &&
				typeof (native as { reason?: unknown }).reason === "string"
					? { reason: (native as { reason: string }).reason }
					: {}),
			};
		} catch {
			result = { kind: "rejected", reason: "Native abort was rejected" };
		}
		const applied = this.applyFlow({ type: "abort.result", ...identity, result });
		if (!applied.ok) throw new Error(`${applied.error.code}: ${applied.error.message}`);
		const successful =
			result.kind === "aborted" || result.kind === "already-idle" || result.kind === "interrupted";
		const reason =
			"reason" in result && result.reason ? result.reason : `Native abort ${result.kind}`;
		if (delivery) {
			const outboxIdentity = {
				sessionKey: this.identity.sessionKey,
				generation: this.identity.childGeneration,
				mutationId:
					this.outboxState.entries.find((entry) => entry.runId === runId)?.mutationId ?? "",
				deliveryId: delivery.deliveryId,
			};
			if (outboxIdentity.mutationId) {
				this.applyOutbox(
					successful
						? { type: "interrupt", ...outboxIdentity, reason }
						: { type: "uncertain", ...outboxIdentity, reason },
				);
			}
		}
		return {
			outcome: result.kind,
			generation: this.identity.childGeneration,
			runId,
			...(reason ? { reason } : {}),
			native,
		};
	}

	/** Keep the current client draft and mutation receipts across native replacement. */
	getDraft(clientId: string, initialText = ""): DraftState<string> {
		const existing = this.drafts.get(clientId);
		if (existing) return existing;
		const created = createDraftState(
			createDraft({
				sessionKey: this.identity.sessionKey,
				clientId,
				content: initialText,
				identity: {
					sessionKey: this.identity.sessionKey,
					sessionId: this.identity.sessionId,
					nativeSessionId: this.identity.nativeSessionId,
					bootId: this.identity.bootId,
					childGeneration: this.identity.childGeneration,
				},
			}),
		);
		this.drafts.set(clientId, created);
		return created;
	}

	applyDraft(clientId: string, mutation: DraftMutation<string>): DraftMutationOutcome<string> {
		const outcome = applyDraftMutation(this.getDraft(clientId), mutation);
		this.drafts.set(clientId, outcome.state);
		return outcome;
	}

	setModel(model: NativeModel): RuntimeResult<SessionFlowState> {
		return this.applyFlow({
			type: "model.set",
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			model,
		});
	}

	setThinkingLevel(level: NativeThinkingLevel): RuntimeResult<SessionFlowState> {
		return this.applyFlow({
			type: "thinking.set",
			sessionKey: this.identity.sessionKey,
			generation: this.identity.childGeneration,
			level,
		});
	}

	/** Mark persisted in-flight work uncertain on a new native generation. */
	reconnectGeneration(generation: number): RuntimeResult<OutboxState> {
		const result = transitionOutbox(this.outboxState, { type: "reconnect", generation });
		if (!result.ok) return failure(result.error.code, result.error.message);
		this.outboxState = result.value;
		return success(this.outboxState);
	}

	/** Rebind native passive/draft identities without deleting user text. */
	reopen(
		next: Omit<SessionCreateRequest, "identity"> & { readonly bootId: string },
	): RuntimeResult<SessionRuntime> {
		const nextGeneration = this.identity.childGeneration + 1;
		const begin = this.applyFlow({
			type: "reopen.begin",
			request: {
				sessionKey: this.identity.sessionKey,
				expectedGeneration: this.identity.childGeneration,
				requestId: randomUUID(),
				nextGeneration,
			},
		});
		if (!begin.ok) return failure(begin.error.code, begin.error.message);
		const requestId = this.current.reopen?.requestId;
		if (!requestId) return failure("invalid-reopen", "Reopen request was not retained");
		const commit = this.applyFlow({
			type: "reopen.commit",
			commit: {
				requestId,
				sessionKey: this.identity.sessionKey,
				generation: nextGeneration,
				bootId: next.bootId,
				nativeSessionId: this.identity.nativeSessionId,
				resources: next.resources ?? [],
				projectTrust: next.projectTrust,
				defaultProjectTrust: next.defaultProjectTrust,
				availableModels: next.availableModels,
				availableThinkingLevels: next.availableThinkingLevels,
				model: next.model,
				thinkingLevel: next.thinkingLevel,
			},
		});
		if (!commit.ok) return failure(commit.error.code, commit.error.message);
		const reconnected = transitionOutbox(this.outboxState, {
			type: "reconnect",
			generation: nextGeneration,
		});
		if (!reconnected.ok) return failure(reconnected.error.code, reconnected.error.message);
		this.outboxState = reconnected.value;
		this.passiveState = createPassiveState({
			sessionId: this.identity.sessionId,
			generation: nextGeneration,
		});
		this.uiSequence = 0;
		for (const [clientId, state] of this.drafts.entries()) {
			const rebound = replaceDraftState(state, {
				sessionKey: this.identity.sessionKey,
				identity: {
					sessionKey: this.identity.sessionKey,
					sessionId: this.identity.sessionId,
					nativeSessionId: this.identity.nativeSessionId,
					bootId: next.bootId,
					childGeneration: nextGeneration,
				},
			});
			this.drafts.set(clientId, rebound);
		}
		return success(this);
	}

	/** Apply a projected extension UI event only for this session/generation. */
	applyPassiveEvent(event: Record<string, unknown>): boolean {
		const type = event.type;
		if (typeof type !== "string" || !type.startsWith("pixie:ui:")) return false;
		const projected = {
			...event,
			sessionId: this.identity.sessionId,
			generation: this.identity.childGeneration,
			sequence: ++this.uiSequence,
		} as PassiveUiEvent;
		const result = applyPassiveEvent(this.passiveState, projected);
		if (!result.accepted) return false;
		this.passiveState = result.state;
		return true;
	}

	reconcile(
		mutationId: string,
		deliveryId: string,
		outcome: "settled" | "rejected" | "interrupted" | "uncertain",
		reason?: string,
		stopReason?: string,
	): RuntimeResult<OutboxState> {
		const result = reconcileUncertainDelivery(this.outboxState, {
			mutationId,
			deliveryId,
			outcome,
			reason,
			stopReason,
		});
		if (!result.ok) return failure(result.error.code, result.error.message);
		this.outboxState = result.value;
		return success(this.outboxState);
	}

	snapshot(clientId?: string, residency?: SessionRuntimeReplay["residency"]): SessionRuntimeReplay {
		const entries = this.outboxState.entries.map(
			({ payload: _payload, fingerprint: _fingerprint, ...entry }) => entry,
		);
		return redactSecrets({
			sessionKey: this.identity.sessionKey,
			sessionId: this.identity.sessionId,
			nativeSessionId: this.identity.nativeSessionId,
			childGeneration: this.identity.childGeneration,
			flow: {
				version: this.current.version,
				sessionKey: this.identity.sessionKey,
				sessionId: this.identity.sessionId,
				nativeSessionId: this.identity.nativeSessionId,
				bootId: this.identity.bootId,
				childGeneration: this.identity.childGeneration,
				phase: this.current.phase,
				trust: {
					state: this.current.trust.state,
					allowed: this.current.trust.allowed,
					reason: this.current.trust.reason,
				},
				resources: this.current.resources,
				loadedResources: this.current.loadedResources,
				...(this.current.model ? { model: this.current.model } : {}),
				...(this.current.thinkingLevel !== undefined
					? { thinkingLevel: this.current.thinkingLevel }
					: {}),
				...(this.current.delivery
					? {
							delivery: {
								deliveryId: this.current.delivery.deliveryId,
								runId: this.current.delivery.runId,
								generation: this.current.delivery.generation,
								status: this.current.delivery.status,
							},
						}
					: {}),
				...(this.current.lastAbort
					? {
							lastAbort: {
								kind: this.current.lastAbort.kind,
								generation: this.current.lastAbort.generation,
								...(this.current.lastAbort.runId ? { runId: this.current.lastAbort.runId } : {}),
							},
						}
					: {}),
			} as SessionReplay,
			outbox: {
				version: this.outboxState.version,
				sessionKey: this.outboxState.sessionKey,
				generation: this.outboxState.generation,
				phase: this.outboxState.phase,
				entries,
				...(this.outboxState.stop ? { stop: this.outboxState.stop } : {}),
			},
			passive: createPassiveReplay(this.passiveState),
			...(clientId && this.drafts.has(clientId) ? { draft: this.drafts.get(clientId) } : {}),
			...(residency ? { residency } : {}),
		}) as SessionRuntimeReplay;
	}
}

export const createSessionRuntime = (options: SessionRuntimeOptions): SessionRuntime =>
	new SessionRuntime(options);
