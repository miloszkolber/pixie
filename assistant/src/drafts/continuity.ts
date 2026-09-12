/** Pure, per-client draft continuity and mutation-id helpers. */

export interface DraftIdentity {
	readonly [key: string]: unknown;
	readonly sessionKey: string;
	readonly sessionId?: string;
	readonly nativeSessionId?: string;
	readonly bootId?: string;
	readonly childGeneration?: number;
}

export interface Draft<T> {
	readonly sessionKey: string;
	readonly clientId?: string;
	readonly content: T;
	/** Content-edit revision. External replacement does not discard or reset it. */
	readonly revision: number;
	/** Alias useful to wire adapters that call this value a version. */
	readonly version: number;
	/** Continuity metadata revision, incremented when the native identity changes. */
	readonly continuityRevision: number;
	readonly identity?: DraftIdentity;
	readonly dirty: boolean;
	readonly origin: "local" | "external-replacement";
}

export interface DraftInput<T> {
	readonly sessionKey: string;
	readonly clientId?: string;
	readonly content: T;
	readonly identity?: DraftIdentity;
	readonly revision?: number;
	readonly continuityRevision?: number;
	readonly dirty?: boolean;
}

export type DraftUpdate<T> =
	| { readonly ok: true; readonly draft: Draft<T> }
	| { readonly ok: false; readonly reason: "revision-conflict"; readonly draft: Draft<T> };

export interface ExternalDraftReplacement<T> {
	readonly sessionKey?: string;
	readonly identity?: DraftIdentity;
	readonly content?: T;
}

export interface DraftReplacement<T> {
	readonly draft: Draft<T>;
	readonly preserved: true;
	readonly previousRevision: number;
	readonly reason: "external-replacement";
}

function safeRevision(value: number | undefined): number {
	return Number.isSafeInteger(value) && value !== undefined && value >= 0 ? value : 0;
}

export function createDraft<T>(input: DraftInput<T>): Draft<T>;
export function createDraft<T>(sessionKey: string, content: T, identity?: DraftIdentity): Draft<T>;
export function createDraft<T>(
	inputOrSessionKey: DraftInput<T> | string,
	content?: T,
	identity?: DraftIdentity,
): Draft<T> {
	const input: DraftInput<T> =
		typeof inputOrSessionKey === "string"
			? { sessionKey: inputOrSessionKey, content: content as T, identity }
			: inputOrSessionKey;
	const revision = safeRevision(input.revision);
	return {
		sessionKey: input.sessionKey,
		...(input.clientId !== undefined ? { clientId: input.clientId } : {}),
		content: input.content,
		revision,
		version: revision,
		continuityRevision: safeRevision(input.continuityRevision),
		...(input.identity ? { identity: { ...input.identity } } : {}),
		dirty: input.dirty ?? true,
		origin: "local",
	};
}

export function updateDraft<T>(
	draft: Draft<T>,
	expectedRevision: number,
	content: T,
): DraftUpdate<T> {
	if (expectedRevision !== draft.revision) return { ok: false, reason: "revision-conflict", draft };
	const revision = draft.revision + 1;
	return {
		ok: true,
		draft: {
			...draft,
			content,
			revision,
			version: revision,
			dirty: true,
			origin: "local",
		},
	};
}

/**
 * Rebind a draft to a newly discovered native identity without replacing its
 * text.  This is intentionally not a save and therefore does not increment
 * the content revision.
 */
export function preserveDraftOnExternalReplacement<T>(
	draft: Draft<T>,
	replacement: ExternalDraftReplacement<T>,
): DraftReplacement<T> {
	const nextIdentity = replacement.identity ?? draft.identity;
	const nextSessionKey = replacement.sessionKey ?? draft.sessionKey;
	const next: Draft<T> = {
		...draft,
		sessionKey: nextSessionKey,
		continuityRevision: draft.continuityRevision + 1,
		...(nextIdentity ? { identity: { ...nextIdentity } } : {}),
		origin: "external-replacement",
	};
	return {
		draft: next,
		preserved: true,
		previousRevision: draft.revision,
		reason: "external-replacement",
	};
}

export const rebindDraft = preserveDraftOnExternalReplacement;
export const preserveDraft = preserveDraftOnExternalReplacement;
export const setDraft = updateDraft;

export interface DraftMutation<T> {
	readonly mutationId: string;
	readonly expectedRevision: number;
	readonly content: T;
}

export interface DraftMutationReceipt<T> {
	readonly mutationId: string;
	readonly expectedRevision: number;
	readonly revision: number;
	readonly content: T;
}

export interface DraftState<T> {
	readonly draft: Draft<T>;
	readonly receipts: readonly DraftMutationReceipt<T>[];
	readonly maxReceipts: number;
}

export type DraftMutationOutcome<T> =
	| { readonly kind: "applied"; readonly state: DraftState<T> }
	| { readonly kind: "replayed"; readonly state: DraftState<T> }
	| {
			readonly kind: "conflict";
			readonly state: DraftState<T>;
			readonly reason: "mutation-reuse" | "revision-conflict";
	  };

export function createDraftState<T>(draft: Draft<T>, maxReceipts = 128): DraftState<T> {
	return {
		draft,
		receipts: [],
		maxReceipts: Number.isSafeInteger(maxReceipts) && maxReceipts > 0 ? maxReceipts : 1,
	};
}

function sameMutation<T>(receipt: DraftMutationReceipt<T>, mutation: DraftMutation<T>): boolean {
	if (receipt.expectedRevision !== mutation.expectedRevision) return false;
	if (Object.is(receipt.content, mutation.content)) return true;
	try {
		return JSON.stringify(receipt.content) === JSON.stringify(mutation.content);
	} catch {
		return false;
	}
}

/**
 * Apply an edit once. A known mutation ID returns its original state and is
 * never submitted again, including after an external native replacement.
 */
export function applyDraftMutation<T>(
	state: DraftState<T>,
	mutation: DraftMutation<T>,
): DraftMutationOutcome<T> {
	const prior = state.receipts.find((receipt) => receipt.mutationId === mutation.mutationId);
	if (prior) {
		return sameMutation(prior, mutation)
			? { kind: "replayed", state }
			: { kind: "conflict", state, reason: "mutation-reuse" };
	}
	if (mutation.expectedRevision !== state.draft.revision)
		return { kind: "conflict", state, reason: "revision-conflict" };
	const updated = updateDraft(state.draft, mutation.expectedRevision, mutation.content);
	if (!updated.ok) return { kind: "conflict", state, reason: "revision-conflict" };
	const receipt: DraftMutationReceipt<T> = {
		mutationId: mutation.mutationId,
		expectedRevision: mutation.expectedRevision,
		revision: updated.draft.revision,
		content: mutation.content,
	};
	return {
		kind: "applied",
		state: {
			...state,
			draft: updated.draft,
			receipts: [...state.receipts, receipt].slice(-state.maxReceipts),
		},
	};
}

export function replaceDraftState<T>(
	state: DraftState<T>,
	replacement: ExternalDraftReplacement<T>,
): DraftState<T> {
	return { ...state, draft: preserveDraftOnExternalReplacement(state.draft, replacement).draft };
}
