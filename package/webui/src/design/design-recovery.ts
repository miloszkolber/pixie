import type { DesignState } from "./design-model";

export type DesignRecoveryAction =
	| "refresh-document"
	| "refresh-selection"
	| "reauthorize"
	| "show-removed"
	| "show-unavailable"
	| "none";

export interface DesignRecovery {
	action: DesignRecoveryAction;
	code: string | null;
	expectedGeneration: number | null;
	expectedRevision: number | null;
	message: string;
}

function record(value: unknown): Record<string, unknown> | null {
	return value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

export function designErrorCode(value: unknown): string | null {
	const direct = record(value);
	const nested = direct ? record(direct.error) : null;
	const code = direct?.code ?? nested?.code;
	return typeof code === "string" ? code : null;
}

export function isDesignStaleDocument(value: unknown): boolean {
	return ["stale_document", "document_conflict", "conflict"].includes(designErrorCode(value) ?? "");
}

export function isDesignStaleSelection(value: unknown): boolean {
	return ["stale_selection", "selection_conflict"].includes(designErrorCode(value) ?? "");
}

export function designRevisionRecovery(
	value: unknown,
	options: { expectedGeneration?: number | null; expectedRevision?: number | null } = {},
): DesignRecovery {
	const code = designErrorCode(value);
	const expectedGeneration = options.expectedGeneration ?? null;
	const expectedRevision = options.expectedRevision ?? null;
	if (isDesignStaleDocument(value)) {
		return {
			action: "refresh-document",
			code,
			expectedGeneration,
			expectedRevision,
			message: "The Design document changed. Refresh document status before retrying.",
		};
	}
	if (isDesignStaleSelection(value)) {
		return {
			action: "refresh-selection",
			code,
			expectedGeneration,
			expectedRevision,
			message: "Shared Design focus changed elsewhere. Refresh focus before retrying.",
		};
	}
	if (code === "disabled" || code === "unavailable" || code === "busy") {
		return {
			action: "show-unavailable",
			code,
			expectedGeneration,
			expectedRevision,
			message: "Design inspection is currently unavailable.",
		};
	}
	if (code === "removed" || code === "not_found") {
		return {
			action: "show-removed",
			code,
			expectedGeneration,
			expectedRevision,
			message: "The Design document was removed. It cannot be restored by a late response.",
		};
	}
	if (code === "unauthorized" || code === "forbidden") {
		return {
			action: "reauthorize",
			code,
			expectedGeneration,
			expectedRevision,
			message: "Design access is no longer authorized. Reconnect before retrying.",
		};
	}
	return {
		action: "none",
		code,
		expectedGeneration,
		expectedRevision,
		message: "Design request failed.",
	};
}

/** Keep instance-wide scope explicit and prevent stale structure from winning. */
export function recoverDesignRevision(state: DesignState, value: unknown): DesignState {
	const recovery = designRevisionRecovery(value, {
		expectedGeneration: state.document?.generation ?? null,
		expectedRevision: state.selectionRevision,
	});
	if (recovery.action === "refresh-document") {
		return {
			...state,
			availability: "stale",
			pages: [],
			nodes: [],
			focus: { ...state.focus, privateFocus: null, sharedFocus: null, scope: "instance" },
			preview: {
				...state.preview,
				status: "stale",
				reason: recovery.message,
			},
			error: { code: recovery.code ?? "stale_document", message: recovery.message },
		};
	}
	if (recovery.action === "refresh-selection") {
		return {
			...state,
			focus: { ...state.focus, sharedFocus: null, scope: "instance" },
			error: { code: recovery.code ?? "stale_selection", message: recovery.message },
		};
	}
	if (recovery.action === "show-removed") {
		return {
			...state,
			availability: "removed",
			preview: { ...state.preview, status: "unavailable", url: null, reason: recovery.message },
			error: { code: recovery.code ?? "removed", message: recovery.message },
		};
	}
	if (recovery.action === "show-unavailable") {
		return {
			...state,
			availability: recovery.code === "disabled" ? "disabled" : "unavailable",
			preview: { ...state.preview, status: "unavailable", url: null, reason: recovery.message },
			error: { code: recovery.code ?? "unavailable", message: recovery.message },
		};
	}
	if (recovery.action === "reauthorize") {
		return {
			...state,
			availability: "unavailable",
			error: { code: recovery.code ?? "unauthorized", message: recovery.message },
		};
	}
	return { ...state, error: { code: recovery.code ?? "internal", message: recovery.message } };
}

export const recoverDesignStaleRevision = recoverDesignRevision;
export const designStaleRevisionRecovery = designRevisionRecovery;
