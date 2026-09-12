import type { CanvasState } from "./canvas-model";
import { staleCanvasPreview } from "./canvas-model";

export type CanvasRecoveryAction =
	| "refresh-status"
	| "refresh-preview"
	| "reauthorize"
	| "show-removed"
	| "show-unavailable"
	| "none";

export interface CanvasRecovery {
	action: CanvasRecoveryAction;
	code: string | null;
	expectedVersion: number | null;
	currentVersion: number | null;
	message: string;
}

function record(value: unknown): Record<string, unknown> | null {
	return value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

/** Read a stable controller error code without rendering its raw payload. */
export function canvasErrorCode(value: unknown): string | null {
	const direct = record(value);
	const nested = direct ? record(direct.error) : null;
	const code = direct?.code ?? nested?.code;
	return typeof code === "string" ? code : null;
}

export function isCanvasStaleVersion(value: unknown): boolean {
	return ["conflict", "stale_version", "version_conflict", "generation_revoked"].includes(
		canvasErrorCode(value) ?? "",
	);
}

export function isCanvasRemoved(value: unknown): boolean {
	return ["removed", "not_found"].includes(canvasErrorCode(value) ?? "");
}

export function canvasVersionRecovery(
	value: unknown,
	options: { expectedVersion?: number | null; currentVersion?: number | null } = {},
): CanvasRecovery {
	const code = canvasErrorCode(value);
	const expectedVersion = options.expectedVersion ?? null;
	const currentVersion = options.currentVersion ?? null;
	if (isCanvasStaleVersion(value)) {
		return {
			action: "refresh-status",
			code,
			expectedVersion,
			currentVersion,
			message: "Canvas changed elsewhere. Refresh the current version before retrying.",
		};
	}
	if (code === "unauthorized" || code === "authority_expired" || code === "authority_revoked") {
		return {
			action: "reauthorize",
			code,
			expectedVersion,
			currentVersion,
			message: "Canvas access expired. Reconnect this session before retrying.",
		};
	}
	if (isCanvasRemoved(value)) {
		return {
			action: "show-removed",
			code,
			expectedVersion,
			currentVersion,
			message: "This Canvas was removed and cannot be restored by a late update.",
		};
	}
	if (code === "disabled" || code === "unavailable" || code === "busy") {
		return {
			action: "show-unavailable",
			code,
			expectedVersion,
			currentVersion,
			message: "Canvas preview is currently unavailable.",
		};
	}
	return {
		action: "none",
		code,
		expectedVersion,
		currentVersion,
		message: "Canvas request failed.",
	};
}

/** Apply a stale/error outcome without allowing old content to look current. */
export function recoverCanvasVersion(state: CanvasState, value: unknown): CanvasState {
	const recovery = canvasVersionRecovery(value, {
		expectedVersion: state.viewedVersion,
		currentVersion: state.currentVersion,
	});
	if (recovery.action === "refresh-status") {
		return {
			...state,
			status: "stale",
			preview: staleCanvasPreview(state.preview),
			error: { code: recovery.code ?? "conflict", message: recovery.message },
		};
	}
	if (recovery.action === "show-removed") {
		return {
			...state,
			status: "removed",
			preview: { ...state.preview, status: "unavailable", url: null, reason: recovery.message },
			error: { code: recovery.code ?? "removed", message: recovery.message },
		};
	}
	if (recovery.action === "show-unavailable") {
		return {
			...state,
			status: recovery.code === "disabled" ? "disabled" : "unavailable",
			preview: { ...state.preview, status: "unavailable", url: null, reason: recovery.message },
			error: { code: recovery.code ?? "unavailable", message: recovery.message },
		};
	}
	if (recovery.action === "reauthorize") {
		return {
			...state,
			status: "unavailable",
			error: { code: recovery.code ?? "unauthorized", message: recovery.message },
		};
	}
	return { ...state, error: { code: recovery.code ?? "internal", message: recovery.message } };
}

export const recoverCanvasStaleVersion = recoverCanvasVersion;
export const canvasStaleVersionRecovery = canvasVersionRecovery;
