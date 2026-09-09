export const CURRENT_PROTOCOL_VERSION = 1;
export const CURRENT_STATE_SCHEMA_VERSION = 1;

export type CompatibilityOutcome = "compatible" | "migrate" | "blocked";
export type CompatibilityStage = "read" | "migrate" | "rollback";

export interface LegacyStateRef {
	readonly protocolVersion?: unknown;
	readonly stateSchemaVersion?: unknown;
	readonly hasMonotonicClaims?: boolean;
	readonly hasConfirmedDeletions?: boolean;
	readonly hasUnknownNewerSchema?: boolean;
	readonly state?: Readonly<Record<string, unknown>>;
	readonly unknownFields?: readonly string[];
}

export interface CompatibilityInput {
	readonly legacy?: LegacyStateRef | null;
	readonly currentProtocolVersion?: number;
	readonly currentStateSchemaVersion?: number;
	readonly supportedLegacyStateSchemas?: readonly number[];
	readonly supportedLegacyProtocolVersions?: readonly number[];
	readonly allowMigration?: boolean;
	readonly knownFields?: readonly string[];
}

export interface CompatibilityDecision {
	readonly outcome: CompatibilityOutcome;
	readonly stage: CompatibilityStage;
	readonly reason: string;
	readonly requiredActions: readonly string[];
	readonly rollbackAvailable: boolean;
	readonly dispatchAllowed: boolean;
	readonly unknownFields: readonly string[];
	readonly unknownFieldsPreserved: true;
}

export interface LedgerClaim {
	readonly id: string;
	readonly sequence: number;
	readonly status: string;
	readonly [key: string]: unknown;
}

export interface Tombstone {
	readonly id: string;
	readonly sequence: number;
	readonly confirmed?: boolean;
	readonly [key: string]: unknown;
}

export interface MonotonicState {
	readonly ledger: readonly LedgerClaim[];
	readonly tombstones: readonly Tombstone[];
}

export interface RollbackEffects {
	readonly dispatchedWork: boolean;
	readonly confirmedDeletions: boolean;
	readonly monotonicClaims: boolean;
}

export interface RollbackInput {
	readonly currentProtocolVersion?: number;
	readonly currentStateSchemaVersion?: number;
	readonly backupProtocolVersion?: number | null;
	readonly backupStateSchemaVersion?: number | null;
	readonly backupAvailable: boolean;
	readonly effects: RollbackEffects;
	readonly restoredFiles?: readonly string[];
	readonly backupState?: Partial<MonotonicState>;
	readonly currentState?: Partial<MonotonicState>;
	readonly olderSchemaCanRepresentMonotonic?: boolean;
}

export interface RollbackReceipt {
	readonly currentProtocol: number;
	readonly currentStateSchema: number;
	readonly backupProtocol: number | null;
	readonly backupStateSchema: number | null;
	readonly restoredFiles: readonly string[];
	readonly retainedPostBackupEffects: readonly string[];
	readonly unresolvedWork: readonly string[];
	readonly preservedLedger: readonly LedgerClaim[];
	readonly preservedTombstones: readonly Tombstone[];
	readonly dispatchEnabled: boolean;
	readonly noop: boolean;
}

export interface RollbackPlan {
	readonly canRestoreAsRunnable: boolean;
	readonly dispatchEnabled: boolean;
	readonly requiresReconciliation: boolean;
	readonly restoredFiles: readonly string[];
	readonly retainedEffects: readonly string[];
	readonly unresolvedWork: readonly string[];
	readonly preservedState: MonotonicState;
	readonly receipt: RollbackReceipt;
}

export interface RollbackHookResult {
	readonly ok: boolean;
	readonly detail: string;
}

export interface RollbackHook {
	readonly id: string;
	readonly phase: CompatibilityStage;
	readonly mutating: boolean;
	readonly description: string;
	readonly run: () => Promise<RollbackHookResult>;
}

export interface RollbackRun {
	readonly outcome: "completed" | "interrupted" | "noop";
	readonly dispatchEnabled: boolean;
	readonly executed: readonly string[];
	readonly failure: string | null;
}
