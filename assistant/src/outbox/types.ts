/**
 * Pure contracts for controller-owned work awaiting native handoff.
 *
 * The outbox deliberately keeps the delivery and mutation identities beside
 * the complete payload.  A retry can therefore inspect the original work
 * without manufacturing a new identity or reducing an attachment to text.
 */

export const OUTBOX_VERSION = 1 as const;

/** States required by the handoff contract. */
export const OUTBOX_DELIVERY_STATES = [
	"prepared",
	"dispatching",
	"accepted",
	"settled",
	"uncertain",
] as const;

export type OutboxDeliveryState = (typeof OUTBOX_DELIVERY_STATES)[number];
export type OutboxStatus = OutboxDeliveryState | "rejected" | "interrupted";
export type OutboxKind = "prompt" | "continuation" | "compaction";
export type OutboxPhase = "open" | "stopping" | "stopped";

/** Payload is intentionally opaque to the state machine and retained verbatim. */
export type OutboxPayload = unknown;

export interface OutboxPrepareInput {
	readonly mutationId: string;
	readonly deliveryId: string;
	readonly sessionKey: string;
	readonly generation: number;
	readonly kind?: OutboxKind;
	readonly runId?: string;
	readonly parentDeliveryId?: string;
	/** Automatic follow-ups require a settled parent before dispatch. */
	readonly automatic?: boolean;
	readonly payload: OutboxPayload;
	readonly queuedAt?: number;
}

export interface OutboxEntry {
	readonly mutationId: string;
	readonly deliveryId: string;
	readonly sessionKey: string;
	readonly generation: number;
	readonly kind: OutboxKind;
	readonly runId?: string;
	readonly parentDeliveryId?: string;
	readonly automatic: boolean;
	readonly payload: OutboxPayload;
	readonly fingerprint: string;
	readonly status: OutboxStatus;
	readonly attempt: number;
	readonly queuedAt?: number;
	/** Prepared work retained by Stop is paused and never auto-submitted. */
	readonly paused: boolean;
	readonly reason?: string;
	readonly stopReason?: string;
}

export interface OutboxDeliveryIdentity {
	readonly sessionKey: string;
	readonly generation: number;
	readonly mutationId: string;
	readonly deliveryId: string;
}

export type OutboxErrorCode =
	| "invalid-request"
	| "duplicate-delivery"
	| "mutation-conflict"
	| "unknown-delivery"
	| "invalid-state"
	| "stale-generation"
	| "dispatch-blocked"
	| "stopping"
	| "stopped"
	| "invalid-stop"
	| "uncertain-delivery";

export interface OutboxError {
	readonly code: OutboxErrorCode;
	readonly message: string;
}

export type OutboxResult<T> =
	| { readonly ok: true; readonly value: T }
	| { readonly ok: false; readonly error: OutboxError };

export type StopDisposition = "graceful" | "forced";
export type StopEffectDisposition = "none" | "interrupted" | "uncertain";

/** Evidence required before a Stop can become a terminal state. */
export interface OutboxStopVerification {
	readonly requestId: string;
	readonly generation: number;
	readonly continuationCleared: boolean;
	readonly pendingUiCancelled: boolean;
	readonly generationQuiescent: boolean;
	readonly forcedTermination: boolean;
	readonly abortOutcome: "aborted" | "already-idle" | "timed-out" | "rejected" | "interrupted";
	readonly reason?: string;
}

export interface OutboxStopRecord {
	readonly requestId: string;
	readonly generation: number;
	readonly phase: "stopping" | "stopped";
	readonly verified?: boolean;
	readonly disposition?: StopDisposition;
	readonly effectDisposition?: StopEffectDisposition;
	readonly retainedDeliveryIds: readonly string[];
	readonly uncertainDeliveryIds: readonly string[];
	readonly interruptedDeliveryIds: readonly string[];
	readonly verification?: OutboxStopVerification;
}

export interface OutboxState {
	readonly version: typeof OUTBOX_VERSION;
	readonly sessionKey: string;
	readonly generation: number;
	readonly phase: OutboxPhase;
	readonly entries: readonly OutboxEntry[];
	/** Last Stop record is retained as an audit/disposition result. */
	readonly stop?: OutboxStopRecord;
}

export type OutboxEvent =
	| { readonly type: "prepare"; readonly request: OutboxPrepareInput }
	| {
			readonly type: "dispatch";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
	  }
	| {
			readonly type: "accept";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
	  }
	| {
			readonly type: "settle";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
			readonly stopReason: string;
	  }
	| {
			readonly type: "reject";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "uncertain";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "interrupt";
			readonly sessionKey: string;
			readonly generation: number;
			readonly mutationId: string;
			readonly deliveryId: string;
			readonly reason: string;
	  }
	| {
			readonly type: "retry";
			readonly mutationId: string;
			readonly deliveryId?: string;
			readonly fingerprint?: string;
	  }
	| {
			readonly type: "reconcile";
			readonly mutationId: string;
			readonly deliveryId: string;
			readonly outcome: "settled" | "rejected" | "interrupted" | "uncertain";
			readonly reason?: string;
			readonly stopReason?: string;
	  }
	| {
			readonly type: "stop.begin";
			readonly requestId: string;
			readonly sessionKey: string;
			readonly generation: number;
	  }
	| { readonly type: "stop.verify"; readonly verification: OutboxStopVerification }
	| { readonly type: "resume" }
	| { readonly type: "discard"; readonly mutationIds: readonly string[] }
	| { readonly type: "reconnect"; readonly generation: number };

export type DispatchDecision =
	| { readonly kind: "ready"; readonly entry: OutboxEntry }
	| { readonly kind: "idle" }
	| {
			readonly kind: "blocked";
			readonly reason:
				| "stopping"
				| "stopped"
				| "uncertain-delivery"
				| "active-delivery"
				| "parent-unsettled"
				| "compaction-blocked";
	  };

export interface NativeQueueDraftInput {
	readonly draftId: string;
	readonly sessionKey: string;
	readonly generation: number;
	readonly lane: "steering" | "followUp";
	readonly text: string;
	readonly sourceDeliveryId?: string;
}

/** A recovery proposal is not an outbox entry and cannot be dispatched. */
export interface NativeQueueDraftProposal {
	readonly draftId: string;
	readonly sessionKey: string;
	readonly generation: number;
	readonly lane: "steering" | "followUp";
	readonly text: string;
	readonly sourceDeliveryId?: string;
	readonly source: "native-queue-recovery";
	readonly requiresExplicitSubmission: true;
}
