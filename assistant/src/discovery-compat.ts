/**
 * Staged legacy compatibility with schema-aware rollback hooks.
 *
 * GO-09 before host/npm retirement: decide whether a legacy assistant state
 * can be read as-is, needs staged migration, or is blocked pending explicit
 * reconciliation; then plan a rollback that never rewinds external effects.
 *
 * Rules (from migration.md, implemented here as pure decisions):
 * - A newer unsupported schema blocks affected mutations; diagnostics stay
 *   available.
 * - Never restore an older queue/schedule/deletion snapshot as runnable
 *   authority after the new version may have dispatched work or deleted
 *   content. Restoring JSON does not undo native tool effects.
 * - Preserve monotonic claims and tombstones. When the older schema cannot
 *   represent them, keep dispatch disabled and require reconciliation.
 * - A rollback receipt names schemas, restored files, retained post-backup
 *   effects and unresolved work. Interrupted and no-op rollbacks are explicit.
 *
 * No filesystem, network or Pi execution happens here. Callers supply refs;
 * hooks wrap caller-supplied async handlers so tests prove ordering without
 * live state.
 */

import { ASSISTANT_PROTOCOL_VERSION } from "./facade.ts";

export const CURRENT_STATE_SCHEMA_VERSION = 1;

export type CompatibilityOutcome = "compatible" | "migrate" | "blocked";
export type CompatibilityStage = "read" | "migrate" | "rollback";

export interface LegacyStateRef {
	protocolVersion?: unknown;
	stateSchemaVersion?: unknown;
	hasMonotonicClaims?: boolean;
	hasConfirmedDeletions?: boolean;
	hasUnknownNewerSchema?: boolean;
}

export interface CompatibilityInput {
	legacy?: LegacyStateRef | null;
	currentProtocolVersion?: number;
	currentStateSchemaVersion?: number;
	supportedLegacyStateSchemas?: readonly number[];
	allowMigration?: boolean;
}

export interface CompatibilityDecision {
	outcome: CompatibilityOutcome;
	stage: CompatibilityStage;
	reason: string;
	requiredActions: readonly string[];
	rollbackAvailable: boolean;
	dispatchAllowed: boolean;
}

export interface RollbackEffects {
	dispatchedWork: boolean;
	confirmedDeletions: boolean;
	monotonicClaims: boolean;
}

export interface RollbackInput {
	currentProtocolVersion?: number;
	currentStateSchemaVersion?: number;
	backupProtocolVersion?: number | null;
	backupStateSchemaVersion?: number | null;
	backupAvailable: boolean;
	effects: RollbackEffects;
	restoredFiles?: readonly string[];
}

export interface RollbackReceipt {
	currentProtocol: number;
	currentStateSchema: number;
	backupProtocol: number | null;
	backupStateSchema: number | null;
	restoredFiles: readonly string[];
	retainedPostBackupEffects: readonly string[];
	unresolvedWork: readonly string[];
	dispatchEnabled: boolean;
	noop: boolean;
}

export interface RollbackPlan {
	canRestoreAsRunnable: boolean;
	dispatchEnabled: boolean;
	requiresReconciliation: boolean;
	restoredFiles: readonly string[];
	retainedEffects: readonly string[];
	unresolvedWork: readonly string[];
	receipt: RollbackReceipt;
}

export interface RollbackHook {
	id: string;
	phase: string;
	mutating: boolean;
	description: string;
	run: () => Promise<{ ok: boolean; detail: string }>;
}

function asVersionNumber(value: unknown): number | null {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

/**
 * Stage the legacy compatibility decision.
 * Read-only: inspects refs, never migrates, never rolls back.
 */
export function decideLegacyCompatibility(input: CompatibilityInput = {}): CompatibilityDecision {
	const currentProtocol = input.currentProtocolVersion ?? ASSISTANT_PROTOCOL_VERSION;
	const currentSchema = input.currentStateSchemaVersion ?? CURRENT_STATE_SCHEMA_VERSION;
	const supported = input.supportedLegacyStateSchemas ?? [currentSchema];
	const allowMigration = input.allowMigration ?? true;
	const legacy = input.legacy ?? null;

	if (legacy === null) {
		return {
			outcome: "compatible",
			stage: "read",
			reason: "No legacy assistant state; fresh install reads current schemas.",
			requiredActions: [],
			rollbackAvailable: false,
			dispatchAllowed: true,
		};
	}

	if (legacy.hasUnknownNewerSchema === true) {
		return {
			outcome: "blocked",
			stage: "read",
			reason: "Legacy state uses an unknown newer schema; mutations blocked, diagnostics available.",
			requiredActions: ["reconcile-newer-schema-explicitly", "keep-dispatch-disabled-until-reconciled"],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	}

	const legacyProtocol = asVersionNumber(legacy.protocolVersion);
	const legacySchema = asVersionNumber(legacy.stateSchemaVersion);
	if (legacyProtocol !== null && legacyProtocol > currentProtocol) {
		return {
			outcome: "blocked",
			stage: "read",
			reason: `Legacy protocol ${legacyProtocol} is newer than current ${currentProtocol}; block mutations, keep diagnostics.`,
			requiredActions: ["reconcile-newer-protocol-explicitly", "keep-dispatch-disabled-until-reconciled"],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	}
	if (legacySchema !== null && legacySchema > currentSchema) {
		return {
			outcome: "blocked",
			stage: "read",
			reason: `Legacy state schema ${legacySchema} is newer than current ${currentSchema}; block mutations, keep diagnostics.`,
			requiredActions: ["reconcile-newer-state-schema-explicitly", "keep-dispatch-disabled-until-reconciled"],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	}

	const schemaKnown = legacySchema === null || legacySchema === currentSchema || supported.includes(legacySchema);
	const protocolKnown = legacyProtocol === null || legacyProtocol === currentProtocol;
	if (schemaKnown && protocolKnown) {
		return {
			outcome: "compatible",
			stage: "read",
			reason: "Legacy schemas match current readers; no migration required.",
			requiredActions: [],
			rollbackAvailable: false,
			dispatchAllowed: true,
		};
	}

	if (!schemaKnown || !protocolKnown) {
		if (!allowMigration) {
			return {
				outcome: "blocked",
				stage: "read",
				reason: "Legacy schemas differ and migration is not allowed in this stage.",
				requiredActions: ["enable-staged-migration-explicitly"],
				rollbackAvailable: false,
				dispatchAllowed: false,
			};
		}
		return {
			outcome: "migrate",
			stage: "migrate",
			reason: `Legacy state (protocol ${legacyProtocol ?? "unknown"}, schema ${legacySchema ?? "unknown"}) needs staged conversion to protocol ${currentProtocol}, schema ${currentSchema}.`,
			requiredActions: [
				"pause-schedule-outbox-admission",
				"backup-with-hashes-before-staging",
				"stage-convert-validate-then-publish-checkpoints",
				"publish-migration-receipt",
			],
			rollbackAvailable: true,
			dispatchAllowed: false,
		};
	}

	return {
		outcome: "blocked",
		stage: "read",
		reason: "Unrecognized legacy state; block mutations pending explicit reconciliation.",
		requiredActions: ["reconcile-legacy-state-explicitly"],
		rollbackAvailable: false,
		dispatchAllowed: false,
	};
}

/**
 * Plan a schema-aware rollback.
 * Never authorizes restoring an older snapshot as runnable authority after
 * post-backup dispatch or confirmed deletion. When monotonic claims exist the
 * older schema cannot represent, dispatch stays disabled until reconciled.
 */
export function planSchemaRollback(input: RollbackInput): RollbackPlan {
	const currentProtocol = input.currentProtocolVersion ?? ASSISTANT_PROTOCOL_VERSION;
	const currentSchema = input.currentStateSchemaVersion ?? CURRENT_STATE_SCHEMA_VERSION;
	const backupProtocol = input.backupProtocolVersion ?? null;
	const backupSchema = input.backupStateSchemaVersion ?? null;
	const restoredFiles = [...(input.restoredFiles ?? [])];

	const retainedEffects: string[] = [];
	if (input.effects.dispatchedWork) retainedEffects.push("post-backup-dispatched-work-retained");
	if (input.effects.confirmedDeletions) retainedEffects.push("post-backup-confirmed-deletions-retained");
	if (input.effects.monotonicClaims) retainedEffects.push("post-backup-monotonic-claims-retained");

	const unresolvedWork: string[] = [];
	if (input.effects.dispatchedWork) unresolvedWork.push("reconcile-dispatched-work-before-resume");
	if (input.effects.confirmedDeletions) unresolvedWork.push("preserve-confirmed-tombstones-do-not-replay-deletions");
	if (input.effects.monotonicClaims) unresolvedWork.push("reconcile-monotonic-claims-older-schema-cannot-represent");

	const noop = !input.backupAvailable && restoredFiles.length === 0;
	const backupReadable =
		input.backupAvailable && backupProtocol !== null && backupSchema !== null;

	// Restoring JSON never rewinds native effects. Runnable restore is allowed
	// only with a readable backup and no post-backup dispatch/deletion that
	// the older snapshot would incorrectly resurrect as runnable authority.
	const canRestoreAsRunnable =
		backupReadable &&
		!input.effects.dispatchedWork &&
		!input.effects.confirmedDeletions &&
		!noop;

	const requiresReconciliation = unresolvedWork.length > 0 || !backupReadable;

	const dispatchEnabled = canRestoreAsRunnable && !requiresReconciliation;

	const receipt: RollbackReceipt = {
		currentProtocol,
		currentStateSchema: currentSchema,
		backupProtocol,
		backupStateSchema: backupSchema,
		restoredFiles: [...restoredFiles],
		retainedPostBackupEffects: [...retainedEffects],
		unresolvedWork: [...unresolvedWork],
		dispatchEnabled,
		noop,
	};

	return {
		canRestoreAsRunnable,
		dispatchEnabled,
		requiresReconciliation,
		restoredFiles: [...restoredFiles],
		retainedEffects,
		unresolvedWork,
		receipt,
	};
}

/**
 * Wrap ordered rollback phases around caller-supplied handlers.
 * Read-only phases (`verify-pairing`, `receipt-preview`) sort before mutating
 * ones; every hook reports its own outcome so interrupted rollbacks stay
 * explicit. Handlers are injected; this function performs no I/O itself.
 */
export function createRollbackHooks(
	plan: RollbackPlan,
	handlers: Readonly<Record<string, () => Promise<{ ok: boolean; detail: string }>>> = {},
): RollbackHook[] {
	const phases: Array<{ id: string; mutating: boolean; description: string }> = [
		{
			id: "verify-pairing",
			mutating: false,
			description: "Verify host/native pairing before touching state; never stop an unrelated TUI.",
		},
		{
			id: "pause-admission",
			mutating: false,
			description: "Pause schedule/outbox admission; never dispatch missed occurrences on rollback.",
		},
		{
			id: "restore-backup",
			mutating: true,
			description: "Restore only the declared backup files; no wildcard cleanup.",
		},
		{
			id: "reconcile-effects",
			mutating: true,
			description: "Reconcile post-backup effects; preserve tombstones and monotonic claims.",
		},
		{
			id: "publish-receipt",
			mutating: true,
			description: "Publish rollback receipt naming schemas, files, retained effects and unresolved work.",
		},
	];

	if (plan.receipt.noop) {
		return [
			{
				id: "noop-rollback",
				phase: "read",
				mutating: false,
				description: "No backup and no staged files; rollback is an explicit no-op.",
				run: handlers["noop-rollback"] ?? (async () => ({ ok: true, detail: "noop rollback; state untouched" })),
			},
		];
	}

	return phases.map((phase) => ({
		id: phase.id,
		phase: plan.canRestoreAsRunnable ? "rollback" : "read",
		mutating: phase.mutating,
		description: phase.description,
		run:
			handlers[phase.id] ??
			(async () => ({
				ok: phase.id === "restore-backup" ? plan.canRestoreAsRunnable : true,
				detail:
					phase.id === "restore-backup" && !plan.canRestoreAsRunnable
						? "restore blocked: post-backup effects require reconciliation first"
						: `${phase.id} recorded`,
			})),
	}));
}

/**
 * Old-reader compatibility: an older binary is sufficient only when it can
 * safely read current state (same protocol and same state schema).
 */
export function isOldReaderCompatible(input: {
	currentProtocol?: number;
	currentStateSchema?: number;
	oldProtocol: number;
	oldStateSchema: number;
}): boolean {
	const currentProtocol = input.currentProtocol ?? ASSISTANT_PROTOCOL_VERSION;
	const currentSchema = input.currentStateSchema ?? CURRENT_STATE_SCHEMA_VERSION;
	return input.oldProtocol === currentProtocol && input.oldStateSchema === currentSchema;
}
