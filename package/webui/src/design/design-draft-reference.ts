import type { DesignDocument, DesignNode, DesignPage, DesignState } from "./design-model";

/**
 * Explicit Design chat draft-reference action (FIG-04).
 *
 * Reference insertion is always an explicit user action. These helpers only
 * build a compact document/page/node text fragment and splice it into an
 * existing draft string. They never submit the draft, change the model
 * prompt, or attach the whole .fig file.
 */

export interface DesignDraftTarget {
	documentId: string;
	documentName?: string | null;
	pageId?: string | null;
	pageName?: string | null;
	nodeId?: string | null;
	nodeName?: string | null;
}

export interface DesignDraftInsertion {
	value: string;
	caret: number;
	inserted: string;
}

export interface DesignDraftReferenceAction {
	label: string;
	hint: string;
	enabled: boolean;
	reason: string | null;
	reference: string | null;
	/** Always false: this action never auto-submits. */
	autoSubmit: false;
}

const MAX_NAME_LENGTH = 48;

function compactName(value: string | null | undefined, fallback: string): string {
	if (!value || !value.trim()) return fallback;
	const trimmed = value.trim().replace(/\s+/g, " ");
	return trimmed.length > MAX_NAME_LENGTH
		? `${trimmed.slice(0, MAX_NAME_LENGTH - 1).trimEnd()}…`
		: trimmed;
}

function shortId(id: string): string {
	return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

/** Build a compact, human-readable reference fragment. Returns null when invalid. */
export function buildDesignDraftReference(target: DesignDraftTarget | null): string | null {
	if (!target || !target.documentId.trim()) return null;
	const documentLabel = compactName(target.documentName, `Design ${shortId(target.documentId)}`);
	let reference = `[${documentLabel} ${shortId(target.documentId)}]`;
	if (target.pageId || target.pageName) {
		const pageLabel = compactName(target.pageName, shortId(target.pageId ?? "page"));
		reference += ` · page "${pageLabel}"`;
	}
	if (target.nodeId || target.nodeName) {
		const nodeLabel = compactName(target.nodeName, shortId(target.nodeId ?? "node"));
		reference += ` · node "${nodeLabel}"`;
	}
	return reference;
}

export function designDraftTargetFromState(
	state: DesignState,
	options: { pageId?: string | null; nodeId?: string | null } = {},
): DesignDraftTarget | null {
	if (!state.document) return null;
	const pageId = options.pageId ?? state.focus.privateFocus?.pageId ?? null;
	const nodeId = options.nodeId ?? state.focus.privateFocus?.nodeId ?? null;
	const page: DesignPage | undefined = pageId
		? state.pages.find((candidate) => candidate.id === pageId)
		: undefined;
	const node: DesignNode | undefined = nodeId
		? state.nodes.find((candidate) => candidate.id === nodeId)
		: undefined;
	return {
		documentId: state.document.id,
		documentName: state.document.name,
		...(pageId ? { pageId } : {}),
		...(page ? { pageName: page.name } : {}),
		...(nodeId ? { nodeId } : {}),
		...(node ? { nodeName: node.name } : {}),
	};
}

/**
 * Splice a reference fragment into a draft. The caller owns submission;
 * this function only returns the next draft value and caret.
 */
export function applyDesignDraftReference(
	draft: string,
	reference: string | null,
	caret?: number | null,
): DesignDraftInsertion | null {
	if (!reference || !reference.trim()) return null;
	const safeCaret =
		typeof caret === "number" && Number.isInteger(caret)
			? Math.min(Math.max(caret, 0), draft.length)
			: draft.length;
	const before = draft.slice(0, safeCaret);
	const after = draft.slice(safeCaret);
	const needsLeadingSpace = before.length > 0 && !/\s$/.test(before);
	const needsTrailingSpace = after.length > 0 && !/^\s/.test(after);
	const inserted = `${needsLeadingSpace ? " " : ""}${reference.trim()}${needsTrailingSpace ? " " : ""}`;
	return {
		value: `${before}${inserted}${after}`,
		caret: before.length + inserted.length,
		inserted: inserted.trim(),
	};
}

export function canInsertDesignDraftReference(
	state: DesignState,
	target: DesignDraftTarget | null,
): { enabled: boolean; reason: string | null } {
	if (!state.document) return { enabled: false, reason: "No Design document is loaded." };
	if (state.availability !== "ready" && state.availability !== "stale") {
		return { enabled: false, reason: "Design inspection is currently unavailable." };
	}
	if (!target || target.documentId !== state.document.id) {
		return { enabled: false, reason: "Reference must name the current Design document." };
	}
	if (!buildDesignDraftReference(target)) {
		return { enabled: false, reason: "Reference is invalid." };
	}
	return { enabled: true, reason: null };
}

/** Describe the explicit user action. Never auto-submits or attaches the file. */
export function designDraftReferenceAction(
	state: DesignState,
	target: DesignDraftTarget | null,
): DesignDraftReferenceAction {
	const gate = canInsertDesignDraftReference(state, target);
	const reference = gate.enabled && target ? buildDesignDraftReference(target) : null;
	return {
		label: "Insert reference into draft",
		hint: "Inserts a compact document/page/node reference into the chat draft only when you choose it. Never submits, changes the model prompt, or attaches the whole file.",
		enabled: gate.enabled && reference !== null,
		reason: gate.reason,
		reference,
		autoSubmit: false,
	};
}

/** Convenience for document-level references without page/node scope. */
export function designDocumentDraftTarget(document: DesignDocument): DesignDraftTarget {
	return { documentId: document.id, documentName: document.name };
}
