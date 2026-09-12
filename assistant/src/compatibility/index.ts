import { createRollbackHooks, planSchemaRollback, runRollbackStages } from "./rollback.ts";
import type { RollbackInput, RollbackPlan, RollbackRun } from "./types.ts";
import {
	type CompatibilityDecision,
	type CompatibilityInput,
	CURRENT_PROTOCOL_VERSION,
	CURRENT_STATE_SCHEMA_VERSION,
} from "./types.ts";

const DEFAULT_KNOWN_FIELDS = [
	"protocolVersion",
	"stateSchemaVersion",
	"ledger",
	"tombstones",
	"monotonicClaims",
	"hasMonotonicClaims",
	"hasConfirmedDeletions",
	"hasUnknownNewerSchema",
] as const;

function version(value: unknown): number | null {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function unknownFields(input: CompatibilityInput): readonly string[] {
	const legacy = input.legacy;
	if (!legacy) return [];
	const known = new Set(input.knownFields ?? DEFAULT_KNOWN_FIELDS);
	const fields = Object.keys(legacy.state ?? {}).filter((field) => !known.has(field));
	return [...new Set([...(legacy.unknownFields ?? []), ...fields])].sort();
}

/** Copy unknown source fields into a converted state without overwriting a newer value. */
export function preserveUnknownFields<T extends Record<string, unknown>>(
	source: Readonly<Record<string, unknown>>,
	target: T,
	known: readonly string[] = DEFAULT_KNOWN_FIELDS,
): T {
	const result = { ...target } as T;
	const knownFields = new Set(known);
	for (const [key, value] of Object.entries(source)) {
		if (!knownFields.has(key) && !Object.hasOwn(target, key))
			result[key as keyof T] = value as T[keyof T];
	}
	return result;
}

export const mergeUnknownFields = preserveUnknownFields;

/**
 * Decide read/migrate/block without loading native resources or mutating state.
 * Unknown source fields are explicitly carried forward rather than discarded.
 */
export function decideCompatibility(input: CompatibilityInput = {}): CompatibilityDecision {
	const currentProtocol = input.currentProtocolVersion ?? CURRENT_PROTOCOL_VERSION;
	const currentSchema = input.currentStateSchemaVersion ?? CURRENT_STATE_SCHEMA_VERSION;
	const allowMigration = input.allowMigration ?? true;
	const legacy = input.legacy ?? null;
	const fields = unknownFields(input);
	const base = { unknownFields: fields, unknownFieldsPreserved: true as const };
	if (legacy === null)
		return {
			...base,
			outcome: "compatible",
			stage: "read",
			reason: "No legacy assistant state; current schemas can be created.",
			requiredActions: [],
			rollbackAvailable: false,
			dispatchAllowed: true,
		};
	if (legacy.hasUnknownNewerSchema === true)
		return {
			...base,
			outcome: "blocked",
			stage: "read",
			reason:
				"Legacy state uses an unknown newer schema; diagnostics remain readable but mutations are blocked.",
			requiredActions: [
				"reconcile-newer-schema-explicitly",
				"preserve-unknown-fields",
				"keep-dispatch-disabled-until-reconciled",
			],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	const legacyProtocol = version(legacy.protocolVersion);
	const legacySchema = version(legacy.stateSchemaVersion);
	if (
		(legacy.protocolVersion !== undefined && legacyProtocol === null) ||
		(legacy.stateSchemaVersion !== undefined && legacySchema === null)
	)
		return {
			...base,
			outcome: "blocked",
			stage: "read",
			reason: "Legacy protocol or state schema is malformed; no mutation is admitted.",
			requiredActions: ["repair-schema-metadata", "keep-dispatch-disabled-until-reconciled"],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	if (legacyProtocol !== null && legacyProtocol > currentProtocol)
		return {
			...base,
			outcome: "blocked",
			stage: "read",
			reason: `Legacy protocol ${legacyProtocol} is newer than current ${currentProtocol}; mutations are blocked.`,
			requiredActions: [
				"reconcile-newer-protocol-explicitly",
				"preserve-unknown-fields",
				"keep-dispatch-disabled-until-reconciled",
			],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	if (legacySchema !== null && legacySchema > currentSchema)
		return {
			...base,
			outcome: "blocked",
			stage: "read",
			reason: `Legacy state schema ${legacySchema} is newer than current ${currentSchema}; mutations are blocked.`,
			requiredActions: [
				"reconcile-newer-state-schema-explicitly",
				"preserve-unknown-fields",
				"keep-dispatch-disabled-until-reconciled",
			],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	const protocolMatches = legacyProtocol === null || legacyProtocol === currentProtocol;
	const schemaMatches = legacySchema === null || legacySchema === currentSchema;
	const supportedSchema =
		legacySchema === null ||
		schemaMatches ||
		(input.supportedLegacyStateSchemas ?? []).includes(legacySchema);
	const supportedProtocol =
		legacyProtocol === null ||
		protocolMatches ||
		(input.supportedLegacyProtocolVersions ?? []).includes(legacyProtocol);
	if (supportedProtocol && supportedSchema)
		return {
			...base,
			outcome: "compatible",
			stage: "read",
			reason: fields.length
				? "Legacy schemas match current readers; unknown fields will be preserved."
				: "Legacy schemas match current readers; no migration is required.",
			requiredActions: fields.length ? ["preserve-unknown-fields"] : [],
			rollbackAvailable: false,
			dispatchAllowed: true,
		};
	if (!allowMigration)
		return {
			...base,
			outcome: "blocked",
			stage: "read",
			reason:
				"Legacy schemas differ or are not in the supported conversion set; staged migration is required.",
			requiredActions: [
				"enable-staged-migration-explicitly",
				"preserve-unknown-fields",
				"keep-dispatch-disabled-until-reconciled",
			],
			rollbackAvailable: false,
			dispatchAllowed: false,
		};
	const requiredActions = [
		"pause-schedule-outbox-admission",
		"backup-with-hashes-before-staging",
		"stage-convert-validate-then-publish-checkpoints",
		"preserve-unknown-fields",
		"publish-migration-receipt",
	];
	if (legacy.hasMonotonicClaims || legacy.hasConfirmedDeletions)
		requiredActions.push("preserve-monotonic-ledger-and-tombstones");
	return {
		...base,
		outcome: "migrate",
		stage: "migrate",
		reason: `Legacy state (protocol ${legacyProtocol ?? "unknown"}, schema ${legacySchema ?? "unknown"}) needs staged conversion to protocol ${currentProtocol}, schema ${currentSchema}.`,
		requiredActions,
		rollbackAvailable: true,
		dispatchAllowed: false,
	};
}

export const decideLegacyCompatibility = decideCompatibility;
export const checkCompatibility = decideCompatibility;
export const assessCompatibility = decideCompatibility;

export function isOldReaderCompatible(input: {
	readonly currentProtocol?: number;
	readonly currentStateSchema?: number;
	readonly oldProtocol: number;
	readonly oldStateSchema: number;
	readonly unknownFields?: readonly string[];
	readonly preservesUnknownFields?: boolean;
}): boolean {
	return (
		input.oldProtocol === (input.currentProtocol ?? CURRENT_PROTOCOL_VERSION) &&
		input.oldStateSchema === (input.currentStateSchema ?? CURRENT_STATE_SCHEMA_VERSION) &&
		(input.unknownFields === undefined ||
			input.unknownFields.length === 0 ||
			input.preservesUnknownFields === true)
	);
}

export { mergeMonotonicLedger, mergeMonotonicState, mergeTombstones } from "./ledger.ts";
export type {
	CompatibilityDecision,
	CompatibilityInput,
	CompatibilityOutcome,
	CompatibilityStage,
	LedgerClaim,
	MonotonicState,
	RollbackEffects,
	RollbackHook,
	RollbackHookResult,
	RollbackInput,
	RollbackPlan,
	RollbackReceipt,
	RollbackRun,
	Tombstone,
} from "./types.ts";
export { CURRENT_PROTOCOL_VERSION, CURRENT_STATE_SCHEMA_VERSION } from "./types.ts";
export { createRollbackHooks, planSchemaRollback, runRollbackStages };
export const planRollback = planSchemaRollback;

// Keep the public API explicit while giving callers a convenient staged helper.
export async function stagedRollback(
	input: RollbackInput,
	handlers: Readonly<Record<string, () => Promise<{ ok: boolean; detail: string }>>> = {},
): Promise<{ plan: RollbackPlan; run: RollbackRun }> {
	const plan = planSchemaRollback(input);
	const hooks = createRollbackHooks(plan, handlers);
	return { plan, run: await runRollbackStages(plan, hooks) };
}
