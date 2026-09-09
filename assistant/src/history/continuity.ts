/**
 * Pure continuity helpers for native clone/fork operations.
 *
 * Native clone/fork is a history operation, not a browser-side copy.  The
 * caller supplies both identities explicitly and this module never derives a
 * replacement ID or treats cancellation as proof that a native mutation was
 * undone.
 */

export type CloneForkKind = "clone" | "fork";

export type CloneForkStatus =
	| "prepared"
	| "dispatching"
	| "accepted"
	| "completed"
	| "rejected"
	| "cancelled"
	| "uncertain";

export type CloneForkCancellation =
	| "not-requested"
	| "requested"
	| "accepted"
	| "rejected"
	| "timed-out"
	| "interrupted"
	| "uncertain";

/** The fields which identify a Pixie/native session association. */
export interface ExplicitSessionIdentity {
	/** Additional native identity fields (branch/leaf/cwd) stay opaque and are preserved. */
	readonly [key: string]: unknown;
	readonly sessionKey: string;
	readonly sessionId: string;
	readonly nativeSessionId: string;
	readonly bootId: string;
	readonly childGeneration: number;
}

export interface CloneForkRequest {
	readonly operationId: string;
	readonly kind: CloneForkKind;
	readonly source: ExplicitSessionIdentity;
	/** The native result supplies this identity; it is never inferred here. */
	readonly target: ExplicitSessionIdentity;
}

export interface CloneForkOperation {
	readonly operationId: string;
	readonly kind: CloneForkKind;
	readonly source: ExplicitSessionIdentity;
	readonly target: ExplicitSessionIdentity;
	readonly status: CloneForkStatus;
	readonly cancellation: CloneForkCancellation;
	/** Ownership transfers only after a successful native completion. */
	readonly owner: "source" | "target";
}

export type CloneForkEvent =
	| { readonly type: "dispatch" }
	| { readonly type: "accept" }
	| { readonly type: "complete" }
	| { readonly type: "reject" }
	| { readonly type: "cancel.request" }
	| {
			readonly type: "cancel.result";
			readonly result: "accepted" | "already-idle" | "timed-out" | "rejected" | "interrupted";
	  };

export interface CloneForkMutation {
	readonly mutationId: string;
	readonly operationId: string;
	readonly kind: CloneForkKind;
	readonly status: CloneForkStatus;
	readonly sourceSessionKey: string;
	readonly targetSessionKey: string;
}

function copyIdentity(identity: ExplicitSessionIdentity): ExplicitSessionIdentity {
	return { ...identity };
}

/**
 * Keep the caller/native identities verbatim and establish the initial
 * ownership.  In particular, a fork does not rewrite the source identity and
 * a clone does not synthesize a child ID from the source ID.
 */
export function beginCloneFork(request: CloneForkRequest): CloneForkOperation {
	return {
		operationId: request.operationId,
		kind: request.kind,
		source: copyIdentity(request.source),
		target: copyIdentity(request.target),
		status: "prepared",
		cancellation: "not-requested",
		owner: "source",
	};
}

/** Advance a clone/fork operation without ever replaying a native mutation. */
export function transitionCloneFork(
	state: CloneForkOperation,
	event: CloneForkEvent,
): CloneForkOperation {
	if (event.type === "cancel.request") {
		if (state.status === "completed" || state.status === "rejected" || state.status === "cancelled") return state;
		return { ...state, cancellation: "requested" };
	}

	if (event.type === "cancel.result") {
		if (state.status === "completed" || state.status === "rejected" || state.status === "cancelled") return state;
		if (event.result === "accepted" || event.result === "interrupted" || event.result === "already-idle")
			return {
				...state,
				status: state.status === "accepted" ? "uncertain" : "cancelled",
				cancellation: event.result === "accepted" ? "accepted" : event.result === "interrupted" ? "interrupted" : "accepted",
			};
		if (event.result === "timed-out")
			return { ...state, status: state.status === "prepared" ? "cancelled" : "uncertain", cancellation: "timed-out" };
		return { ...state, status: state.status === "prepared" ? "rejected" : "uncertain", cancellation: "rejected" };
	}

	if (state.status === "completed" || state.status === "rejected" || state.status === "cancelled") return state;
	if (event.type === "dispatch" && state.status === "prepared") return { ...state, status: "dispatching" };
	if (event.type === "accept" && state.status === "dispatching") return { ...state, status: "accepted" };
	if (event.type === "complete" && (state.status === "accepted" || state.status === "dispatching"))
		return { ...state, status: "completed", cancellation: "not-requested", owner: "target" };
	if (event.type === "reject" && (state.status === "prepared" || state.status === "dispatching"))
		return { ...state, status: "rejected" };
	return state;
}

/** A completed native operation is the only point at which target ownership is valid. */
export function completeCloneFork(state: CloneForkOperation): CloneForkOperation {
	return transitionCloneFork(state, { type: "complete" });
}

/**
 * A known mutation ID is a status lookup, never permission to send the native
 * clone/fork again.  Callers can retain this bounded ledger alongside their
 * durable session association.
 */
export function findCloneForkMutation(
	mutations: readonly CloneForkMutation[],
	mutationId: string,
): CloneForkMutation | undefined {
	return mutations.find((mutation) => mutation.mutationId === mutationId);
}

export function isCloneForkMutationKnown(
	mutations: readonly CloneForkMutation[],
	mutationId: string,
): boolean {
	return findCloneForkMutation(mutations, mutationId) !== undefined;
}

export function rememberCloneForkMutation(
	mutations: readonly CloneForkMutation[],
	mutation: CloneForkMutation,
	maxEntries = 128,
): readonly CloneForkMutation[] {
	if (findCloneForkMutation(mutations, mutation.mutationId)) return mutations;
	const limit = Number.isSafeInteger(maxEntries) && maxEntries > 0 ? maxEntries : 1;
	return [...mutations, { ...mutation }].slice(-limit);
}

export const cloneForkIdentity = beginCloneFork;
export const preserveCloneForkIdentity = beginCloneFork;
export const advanceCloneFork = transitionCloneFork;
