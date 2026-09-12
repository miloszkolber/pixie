/**
 * Pure residency and capacity contracts for managed native runtimes.
 *
 * This module describes admission and ownership only.  It does not launch,
 * stop, signal or otherwise touch a child process.  A runtime remains
 * resident until its release/handoff completion transition is recorded.
 */

export const RESIDENCY_VERSION = 1 as const;

/** Initial limits from the shared assistant lifecycle contract. */
export const DEFAULT_RESIDENCY_LIMITS = {
	maxResidents: 16,
	maxLaunching: 4,
	maxActiveWork: 8,
	maxPendingUiPerResident: 16,
	maxLivenessPinsPerResident: 16,
	/** Terminal records are metadata, not resident children. */
	maxTerminalRecords: 16,
} as const;

export interface ResidencyLimits {
	readonly maxResidents: number;
	readonly maxLaunching: number;
	readonly maxActiveWork: number;
	readonly maxPendingUiPerResident: number;
	readonly maxLivenessPinsPerResident: number;
	readonly maxTerminalRecords: number;
}

export interface RuntimeIdentity {
	/** Opaque application/session association, not a native or transport ID. */
	readonly sessionKey: string;
	/** Native child generation guarded by every transition. */
	readonly generation: number;
}

export type DetachedWorkStatus = "none" | "active" | "unknown" | "uncertain";

/** Work facts used to decide whether a managed runtime can be released. */
export interface RuntimeWork {
	readonly activeWorkIds: readonly string[];
	readonly pendingUiIds: readonly string[];
	readonly livenessPinKeys: readonly string[];
	/** Unknown/uncertain work is never treated as idle. */
	readonly detached: DetachedWorkStatus;
}

export type WorkDispositionKind = "idle" | "active" | "pinned" | "unknown" | "uncertain";

export interface WorkDisposition {
	readonly kind: WorkDispositionKind;
	readonly releaseable: boolean;
	readonly reason:
		| "settled"
		| "active-work"
		| "detached-active"
		| "pending-ui"
		| "liveness-pin"
		| "detached-unknown"
		| "detached-uncertain";
}

export type ResidentPhase = "launching" | "resident" | "releasing" | "handoff-pending";

export interface ResidentRuntime extends RuntimeIdentity {
	readonly phase: ResidentPhase;
	readonly work: RuntimeWork;
	/** Present only while an explicit TUI handoff is pending. */
	readonly handoffInstruction?: string;
}

export type TerminalRuntimePhase = "released" | "released-to-tui";

export interface TerminalRuntimeState extends RuntimeIdentity {
	readonly phase: TerminalRuntimePhase;
	readonly instruction?: string;
}

export interface ResidencyState {
	readonly version: typeof RESIDENCY_VERSION;
	readonly limits: ResidencyLimits;
	/** Only these entries hold a managed child or are completing its release. */
	readonly residents: readonly ResidentRuntime[];
	/** Bounded metadata describing the latest completed release/handoff states. */
	readonly terminal: readonly TerminalRuntimeState[];
}

export interface CapacityUsage {
	readonly residents: number;
	readonly launching: number;
	readonly activeWork: number;
}

export type ResidencyErrorCode =
	| "invalid-request"
	| "resident-capacity"
	| "launch-capacity"
	| "active-capacity"
	| "duplicate-resident"
	| "unknown-resident"
	| "stale-generation"
	| "duplicate-work"
	| "unknown-work"
	| "duplicate-pin"
	| "unknown-pin"
	| "invalid-state"
	| "not-idle"
	| "invalid-handoff";

export interface ResidencyError {
	readonly code: ResidencyErrorCode;
	readonly message: string;
	readonly disposition?: WorkDisposition;
}

export type ResidencyResult<T> =
	| { readonly ok: true; readonly value: T }
	| { readonly ok: false; readonly error: ResidencyError };

export type ResidencyEvent =
	| { readonly type: "launch.begin"; readonly identity: RuntimeIdentity }
	| { readonly type: "launch.complete"; readonly identity: RuntimeIdentity }
	| { readonly type: "launch.fail"; readonly identity: RuntimeIdentity }
	| { readonly type: "work.admit"; readonly identity: RuntimeIdentity; readonly workId: string }
	| { readonly type: "work.settle"; readonly identity: RuntimeIdentity; readonly workId: string }
	| {
			readonly type: "work.detached";
			readonly identity: RuntimeIdentity;
			readonly disposition: DetachedWorkStatus;
	  }
	| { readonly type: "ui.pin"; readonly identity: RuntimeIdentity; readonly requestId: string }
	| { readonly type: "ui.unpin"; readonly identity: RuntimeIdentity; readonly requestId: string }
	| { readonly type: "liveness.pin"; readonly identity: RuntimeIdentity; readonly key: string }
	| { readonly type: "liveness.unpin"; readonly identity: RuntimeIdentity; readonly key: string }
	| { readonly type: "runtime.release.begin"; readonly identity: RuntimeIdentity }
	| { readonly type: "runtime.release.complete"; readonly identity: RuntimeIdentity }
	| {
			readonly type: "tui.handoff.begin";
			readonly identity: RuntimeIdentity;
			readonly instruction: string;
	  }
	| { readonly type: "tui.handoff.complete"; readonly identity: RuntimeIdentity };
