import { mergeMonotonicState } from "./ledger.ts";
import type {
	RollbackHook,
	RollbackHookResult,
	RollbackInput,
	RollbackPlan,
	RollbackReceipt,
	RollbackRun,
} from "./types.ts";
import { CURRENT_PROTOCOL_VERSION, CURRENT_STATE_SCHEMA_VERSION } from "./types.ts";

function version(value: number | null | undefined, fallback: number | null): number | null {
	return value === null
		? null
		: typeof value === "number" && Number.isSafeInteger(value) && value >= 0
			? value
			: fallback;
}

/**
 * Build a rollback plan without touching files. Restored files are metadata;
 * the caller must perform any staged publication and reconciliation.
 */
export function planSchemaRollback(input: RollbackInput): RollbackPlan {
	const currentProtocol = input.currentProtocolVersion ?? CURRENT_PROTOCOL_VERSION;
	const currentSchema = input.currentStateSchemaVersion ?? CURRENT_STATE_SCHEMA_VERSION;
	const backupProtocol = version(input.backupProtocolVersion, null);
	const backupSchema = version(input.backupStateSchemaVersion, null);
	const restoredFiles = [...(input.restoredFiles ?? [])];
	const preservedState = mergeMonotonicState(input.backupState, input.currentState);
	const retainedEffects: string[] = [];
	if (input.effects.dispatchedWork) retainedEffects.push("post-backup-dispatched-work-retained");
	if (input.effects.confirmedDeletions)
		retainedEffects.push("post-backup-confirmed-deletions-retained");
	if (input.effects.monotonicClaims) retainedEffects.push("post-backup-monotonic-claims-retained");

	const unresolvedWork: string[] = [];
	if (input.effects.dispatchedWork) unresolvedWork.push("reconcile-dispatched-work-before-resume");
	if (input.effects.confirmedDeletions)
		unresolvedWork.push("preserve-confirmed-tombstones-do-not-replay-deletions");
	if (input.effects.monotonicClaims)
		unresolvedWork.push("reconcile-monotonic-claims-older-schema-cannot-represent");
	const backupReadable =
		input.backupAvailable &&
		backupProtocol !== null &&
		backupSchema !== null &&
		backupProtocol <= currentProtocol &&
		backupSchema <= currentSchema;
	const schemaRunnable =
		backupReadable && backupProtocol === currentProtocol && backupSchema === currentSchema;
	const monotonicRepresentable = input.olderSchemaCanRepresentMonotonic ?? true;
	if (!monotonicRepresentable)
		unresolvedWork.push("keep-dispatch-disabled-until-monotonic-state-is-reconciled");
	const noop = !input.backupAvailable && restoredFiles.length === 0;
	const requiresReconciliation =
		unresolvedWork.length > 0 || !backupReadable || !monotonicRepresentable || !schemaRunnable;
	const canRestoreAsRunnable =
		schemaRunnable &&
		!input.effects.dispatchedWork &&
		!input.effects.confirmedDeletions &&
		!input.effects.monotonicClaims &&
		monotonicRepresentable &&
		!noop;
	const dispatchEnabled = canRestoreAsRunnable && !requiresReconciliation;
	const receipt: RollbackReceipt = {
		currentProtocol,
		currentStateSchema: currentSchema,
		backupProtocol,
		backupStateSchema: backupSchema,
		restoredFiles,
		retainedPostBackupEffects: [...retainedEffects],
		unresolvedWork: [...unresolvedWork],
		preservedLedger: [...preservedState.ledger],
		preservedTombstones: [...preservedState.tombstones],
		dispatchEnabled,
		noop,
	};
	return {
		canRestoreAsRunnable,
		dispatchEnabled,
		requiresReconciliation,
		restoredFiles,
		retainedEffects,
		unresolvedWork,
		preservedState,
		receipt,
	};
}

/** Create ordered, caller-owned phases for a staged rollback. */
export function createRollbackHooks(
	plan: RollbackPlan,
	handlers: Readonly<Record<string, () => Promise<RollbackHookResult>>> = {},
): readonly RollbackHook[] {
	if (plan.receipt.noop)
		return [
			{
				id: "noop-rollback",
				phase: "read",
				mutating: false,
				description: "No backup and no staged files; state remains untouched.",
				run: handlers["noop-rollback"] ?? (async () => ({ ok: true, detail: "rollback no-op" })),
			},
		];
	const phases: ReadonlyArray<{
		id: string;
		phase: "read" | "rollback";
		mutating: boolean;
		description: string;
	}> = [
		{
			id: "verify-pairing",
			phase: "read",
			mutating: false,
			description: "Verify the paired native storage before touching staged state.",
		},
		{
			id: "pause-admission",
			phase: "read",
			mutating: false,
			description: "Pause schedule and outbox admission; never dispatch missed work.",
		},
		{
			id: "restore-backup",
			phase: plan.canRestoreAsRunnable ? "rollback" : "read",
			mutating: true,
			description: plan.canRestoreAsRunnable
				? "Publish only the declared compatible backup files."
				: "Do not restore an older snapshot as runnable authority.",
		},
		{
			id: "reconcile-effects",
			phase: "read",
			mutating: false,
			description: "Retain post-backup claims and tombstones; reconcile external effects.",
		},
		{
			id: "publish-receipt",
			phase: "read",
			mutating: false,
			description: "Publish schemas, files, retained effects and unresolved work.",
		},
	];
	return phases.map((phase) => ({
		...phase,
		run:
			handlers[phase.id] ??
			(async () => ({
				ok: phase.id !== "restore-backup" || plan.canRestoreAsRunnable,
				detail:
					phase.id === "restore-backup" && !plan.canRestoreAsRunnable
						? "restore blocked; post-backup effects require reconciliation"
						: `${phase.id} recorded`,
			})),
	}));
}

/** Run staged hooks in order and never enable dispatch after an interruption. */
export async function runRollbackStages(
	plan: RollbackPlan,
	hooks: readonly RollbackHook[],
): Promise<RollbackRun> {
	if (plan.receipt.noop) {
		const hook = hooks[0];
		const result = hook ? await hook.run() : { ok: true, detail: "rollback no-op" };
		return {
			outcome: result.ok ? "noop" : "interrupted",
			dispatchEnabled: false,
			executed: hook ? [hook.id] : [],
			failure: result.ok ? null : result.detail,
		};
	}
	const executed: string[] = [];
	for (const hook of hooks) {
		const result = await hook.run();
		executed.push(hook.id);
		if (!result.ok && !(hook.id === "restore-backup" && !plan.canRestoreAsRunnable))
			return {
				outcome: "interrupted",
				dispatchEnabled: false,
				executed,
				failure: result.detail,
			};
	}
	return {
		outcome: "completed",
		dispatchEnabled: plan.dispatchEnabled,
		executed,
		failure: null,
	};
}
