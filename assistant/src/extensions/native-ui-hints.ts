// BRIDGE-03 exact native cancellation/working hints.
//
// Pi 0.85.1 plain RPC cannot observe precise working hints or
// request-specific dialog cancellation: `setWorkingMessage` is a no-op and
// dialog signal/timeout cleanup removes the pending request locally without
// emitting a cancellation frame. This module exposes that boundary honestly:
// working hints are Pixie-local projections (never native acceptance) and
// cancellation forwards require the exact request identity while reporting the
// native outcome as unknown. Nothing here fabricates an acknowledgment.

import { NATIVE_UI_BOUNDS } from "./native-ui-mapping.ts";

export const NATIVE_BASELINE = {
	distribution: "Pi 0.85.1",
	rpcImplementation: "packages/coding-agent/src/modes/rpc/rpc-mode.ts",
	inspectedExports: "packages/coding-agent/src/index.ts",
	extensionTypes: "packages/coding-agent/src/core/extensions/types.ts",
} as const;

export interface UpstreamBlocker {
	readonly fcId: "FC15";
	readonly distribution: string;
	readonly missingPublicSymbol: string;
	readonly reproduction: string;
	readonly attemptedAlternatives: readonly string[];
	readonly userVisibleLimitation: string;
	readonly owner: string;
	readonly releaseConsequence: string;
}

export const FC15_WORKING_BLOCKER: UpstreamBlocker = {
	fcId: "FC15",
	distribution: "npm and standalone Pi 0.85.1",
	missingPublicSymbol:
		"Observable working-message hook with exact request identity (RPC setWorkingMessage)",
	reproduction:
		"Call ctx.ui.setWorkingMessage('working') in an extension bound with mode rpc on Pi 0.85.1; observe rpc-mode.ts setWorkingMessage is an empty body and no extension_ui_request frame is emitted.",
	attemptedAlternatives: [
		"Forwarding setWorkingMessage as pixie:ui:working without claiming native acceptance",
		"Correlating public ui_prompt spans by timing/title (rejected: identities differ, timing guesses are unsound)",
		"Monkey-patching private runner/UI internals (rejected: not a supported public API)",
	],
	userVisibleLimitation:
		"Working hints shown in Pixie are Pixie-local projections. They do not prove the native tool accepted or is still running the same step.",
	owner: "E/B",
	releaseConsequence:
		"FC15 working-hint fidelity stays open until a supported observable hook or narrowly scoped upstream RPC addition with exact IDs ships and is pinned to a released Pi.",
};

export const FC15_CANCELLATION_BLOCKER: UpstreamBlocker = {
	fcId: "FC15",
	distribution: "npm and standalone Pi 0.85.1",
	missingPublicSymbol:
		"Request-specific dialog cancellation frame for aborted/timed-out extension_ui_request",
	reproduction:
		"Open ctx.ui.select/input/confirm/editor with an AbortSignal or timeout in rpc mode, abort it, and observe the pending map entry is deleted locally with no request-specific cancellation output; the host only resolves the default value.",
	attemptedAlternatives: [
		"Matching cancellation to dialogs by title or elapsed time (rejected: ambiguous under concurrent dialogs)",
		"Treating dialog-timeout cleanup as native cancellation evidence (rejected: local cleanup only)",
		"Reusing ui_prompt lifecycle spans as dialog IDs (rejected: public spans are not RPC dialog IDs)",
	],
	userVisibleLimitation:
		"A forwarded Pixie cancellation dismisses the Web UI dialog and settles the awaiting bridge promise, but the native side-effect outcome stays unknown: the native tool may still run.",
	owner: "E/B",
	releaseConsequence:
		"FC15 precise-cancellation fidelity stays open until a supported request-ID cancellation event or narrowly scoped upstream RPC addition ships and is pinned to a released Pi.",
};

export function isNativeWorkingMessageObservable(): false {
	return false;
}

export function isNativeRequestCancellationObservable(): false {
	return false;
}

export interface WorkingHint {
	readonly kind: "pixie-local-working-hint";
	readonly sessionId: string;
	readonly message?: string;
	/** Always false: a Pixie projection is never native acceptance. */
	readonly nativeAccepted: false;
	readonly blocker: UpstreamBlocker;
}

/** Create an honest working hint. Always marks native acceptance as false. */
export function createWorkingHint(input: { sessionId: string; message?: string }): WorkingHint {
	if (!input.sessionId) throw new Error("Working hint requires a session identity");
	const trimmed = input.message?.slice(0, NATIVE_UI_BOUNDS.maxTextChars);
	return {
		kind: "pixie-local-working-hint",
		sessionId: input.sessionId,
		...(trimmed !== undefined ? { message: trimmed } : {}),
		nativeAccepted: false,
		blocker: FC15_WORKING_BLOCKER,
	};
}

export interface CancellationForward {
	readonly kind: "pixie-cancellation-forward";
	readonly sessionId: string;
	readonly requestId: string;
	readonly reason: string;
	/** A forwarded cancellation is not proof the native side cancelled. */
	readonly forwarded: true;
	readonly nativeCancelled: "unknown";
	readonly blocker: UpstreamBlocker;
}

/**
 * Forward a cancellation for the exact pending request. The request ID is
 * required and matched exactly; timing/title heuristics are never used. The
 * native outcome is always reported as unknown, never as cancelled.
 */
export function createCancellationForward(input: {
	sessionId: string;
	requestId: string;
	reason?: string;
}): CancellationForward {
	if (!input.sessionId) throw new Error("Cancellation requires a session identity");
	if (!input.requestId) throw new Error("Cancellation requires the exact request ID");
	return {
		kind: "pixie-cancellation-forward",
		sessionId: input.sessionId,
		requestId: input.requestId,
		reason: input.reason || "cancelled",
		forwarded: true,
		nativeCancelled: "unknown",
		blocker: FC15_CANCELLATION_BLOCKER,
	};
}

/** A forwarded answer is delivery to the bridge, not proof the native tool accepted it. */
export function describeForwardedVsAccepted(): string {
	return (
		"Forwarded means Pixie delivered the answer to the awaiting bridge promise. " +
		"Accepted would mean the native tool confirmed the side effect, which plain RPC does not report. " +
		"Callers must keep the native outcome as unknown/uncertain and never present forwarding as acceptance."
	);
}
