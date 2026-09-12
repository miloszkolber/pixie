// Pure per-client native editor draft proposal handling.
//
// Native editor text is a proposal, not a submission. Only an unchanged empty
// originating draft is applied automatically. Every other case is explicit so
// another client cannot overwrite text the user is currently editing.

export const DRAFT_CONFLICT_ACTIONS = ["insert", "replace", "dismiss"] as const;
export type DraftConflictAction = (typeof DRAFT_CONFLICT_ACTIONS)[number];
export type DraftProposalOperation = "setEditorText" | "pasteToEditor";

export interface DraftState {
	readonly sessionId: string;
	readonly clientId: string;
	readonly generation: number;
	readonly childGeneration: number;
	readonly revision: number;
	readonly text: string;
}

export interface DraftProposal {
	readonly sessionId: string;
	readonly clientId: string;
	readonly generation: number;
	readonly childGeneration: number;
	readonly operation: DraftProposalOperation;
	readonly baseRevision: number;
	readonly originatingText: string;
	readonly text: string;
	/** Optional insertion point for an explicit insert; default is append. */
	readonly insertAt?: number;
}

export interface DraftConflict {
	readonly kind: "draft-conflict";
	readonly reason: "revision-mismatch" | "non-empty-origin" | "non-empty-draft";
	readonly expectedRevision: number;
	readonly currentRevision: number;
	readonly currentText: string;
	readonly proposal: DraftProposal;
	readonly choices: typeof DRAFT_CONFLICT_ACTIONS;
	/** Conflict resolution never submits the prompt. */
	readonly autoSubmitted: false;
}

export type DraftProposalResult =
	| {
			readonly accepted: true;
			readonly outcome: "auto-applied";
			readonly state: DraftState;
			readonly autoSubmitted: false;
	  }
	| {
			readonly accepted: false;
			readonly outcome: "conflict";
			readonly conflict: DraftConflict;
			readonly autoSubmitted: false;
	  }
	| {
			readonly accepted: false;
			readonly outcome: "rejected";
			readonly reason:
				| "foreign-session"
				| "foreign-client"
				| "stale-generation"
				| "invalid-proposal";
			readonly autoSubmitted: false;
	  };

export type DraftResolutionResult =
	| {
			readonly accepted: true;
			readonly outcome: "inserted" | "replaced" | "dismissed";
			readonly state: DraftState;
			readonly autoSubmitted: false;
	  }
	| {
			readonly accepted: false;
			readonly outcome: "rejected";
			readonly reason: "stale-conflict" | "foreign-session" | "foreign-client" | "stale-generation";
			readonly autoSubmitted: false;
	  };

interface DraftIdentityInput {
	readonly sessionId: string;
	readonly clientId: string;
	readonly generation?: number;
	readonly childGeneration?: number;
}

function readGeneration(input: {
	readonly generation?: number;
	readonly childGeneration?: number;
}): number | undefined {
	if (
		input.generation !== undefined &&
		input.childGeneration !== undefined &&
		input.generation !== input.childGeneration
	)
		return undefined;
	return input.generation ?? input.childGeneration;
}

function validGeneration(value: number | undefined): value is number {
	return value !== undefined && Number.isSafeInteger(value) && value >= 0;
}

function validIdentity(input: DraftIdentityInput): number | undefined {
	if (!input.sessionId || !input.clientId) return undefined;
	const generation = readGeneration(input);
	return validGeneration(generation) ? generation : undefined;
}

function nextRevision(revision: number): number {
	if (!Number.isSafeInteger(revision) || revision < 0 || revision === Number.MAX_SAFE_INTEGER)
		throw new Error("Draft revision cannot advance");
	return revision + 1;
}

export function createDraftState(
	input: DraftIdentityInput & { readonly revision?: number; readonly text?: string },
): DraftState {
	const generation = validIdentity(input);
	if (generation === undefined)
		throw new Error("Draft state requires session, client, and generation");
	const revision = input.revision ?? 0;
	if (!Number.isSafeInteger(revision) || revision < 0)
		throw new Error("Draft state requires a valid revision");
	if (input.text !== undefined && typeof input.text !== "string")
		throw new Error("Draft text must be a string");
	return {
		sessionId: input.sessionId,
		clientId: input.clientId,
		generation,
		childGeneration: generation,
		revision,
		text: input.text ?? "",
	};
}

export function createDraftProposal(
	input: DraftIdentityInput & {
		readonly operation: DraftProposalOperation;
		readonly baseRevision: number;
		readonly originatingText: string;
		readonly text: string;
		readonly insertAt?: number;
	},
): DraftProposal {
	const generation = validIdentity(input);
	if (generation === undefined)
		throw new Error("Draft proposal requires session, client, and generation");
	if (!Number.isSafeInteger(input.baseRevision) || input.baseRevision < 0)
		throw new Error("Draft proposal requires a valid base revision");
	if (typeof input.originatingText !== "string" || typeof input.text !== "string")
		throw new Error("Draft proposal text must be a string");
	if (input.insertAt !== undefined && (!Number.isSafeInteger(input.insertAt) || input.insertAt < 0))
		throw new Error("Draft insertion point must be non-negative");
	return {
		sessionId: input.sessionId,
		clientId: input.clientId,
		generation,
		childGeneration: generation,
		operation: input.operation,
		baseRevision: input.baseRevision,
		originatingText: input.originatingText,
		text: input.text,
		...(input.insertAt !== undefined ? { insertAt: input.insertAt } : {}),
	};
}

function rejected(
	reason: "foreign-session" | "foreign-client" | "stale-generation" | "invalid-proposal",
): DraftProposalResult {
	return { accepted: false, outcome: "rejected", reason, autoSubmitted: false };
}

/** Evaluate a native editor proposal against the exact client revision. */
export function evaluateDraftProposal(
	state: DraftState,
	proposal: DraftProposal,
): DraftProposalResult {
	const generation = readGeneration(proposal);
	if (proposal.sessionId !== state.sessionId) return rejected("foreign-session");
	if (proposal.clientId !== state.clientId) return rejected("foreign-client");
	if (!validGeneration(generation) || generation !== state.generation)
		return rejected("stale-generation");
	if (
		!Number.isSafeInteger(proposal.baseRevision) ||
		proposal.baseRevision < 0 ||
		(proposal.operation !== "setEditorText" && proposal.operation !== "pasteToEditor") ||
		typeof proposal.originatingText !== "string" ||
		typeof proposal.text !== "string"
	)
		return rejected("invalid-proposal");

	const conflictReason =
		proposal.baseRevision !== state.revision
			? "revision-mismatch"
			: proposal.originatingText !== ""
				? "non-empty-origin"
				: state.text !== ""
					? "non-empty-draft"
					: undefined;
	if (conflictReason) {
		const conflict: DraftConflict = {
			kind: "draft-conflict",
			reason: conflictReason,
			expectedRevision: proposal.baseRevision,
			currentRevision: state.revision,
			currentText: state.text,
			proposal,
			choices: DRAFT_CONFLICT_ACTIONS,
			autoSubmitted: false,
		};
		return { accepted: false, outcome: "conflict", conflict, autoSubmitted: false };
	}
	return {
		accepted: true,
		outcome: "auto-applied",
		state: { ...state, revision: nextRevision(state.revision), text: proposal.text },
		autoSubmitted: false,
	};
}

/** Resolve an explicit conflict with a compare-and-set revision guard. */
export function resolveDraftConflict(
	state: DraftState,
	conflict: DraftConflict,
	action: DraftConflictAction,
): DraftResolutionResult {
	const proposal = conflict.proposal;
	const generation = readGeneration(proposal);
	if (proposal.sessionId !== state.sessionId)
		return {
			accepted: false,
			outcome: "rejected",
			reason: "foreign-session",
			autoSubmitted: false,
		};
	if (proposal.clientId !== state.clientId)
		return { accepted: false, outcome: "rejected", reason: "foreign-client", autoSubmitted: false };
	if (!validGeneration(generation) || generation !== state.generation)
		return {
			accepted: false,
			outcome: "rejected",
			reason: "stale-generation",
			autoSubmitted: false,
		};
	if (state.revision !== conflict.currentRevision)
		return { accepted: false, outcome: "rejected", reason: "stale-conflict", autoSubmitted: false };
	if (action === "dismiss")
		return { accepted: true, outcome: "dismissed", state, autoSubmitted: false };
	if (!DRAFT_CONFLICT_ACTIONS.includes(action))
		return { accepted: false, outcome: "rejected", reason: "stale-conflict", autoSubmitted: false };

	if (action === "replace") {
		return {
			accepted: true,
			outcome: "replaced",
			state: { ...state, revision: nextRevision(state.revision), text: proposal.text },
			autoSubmitted: false,
		};
	}
	const insertAt = Math.min(Math.max(proposal.insertAt ?? state.text.length, 0), state.text.length);
	const text = `${state.text.slice(0, insertAt)}${proposal.text}${state.text.slice(insertAt)}`;
	return {
		accepted: true,
		outcome: "inserted",
		state: { ...state, revision: nextRevision(state.revision), text },
		autoSubmitted: false,
	};
}

/** Keep the proposal terminology available to callers that call this a draft decision. */
export const applyDraftProposal = evaluateDraftProposal;
