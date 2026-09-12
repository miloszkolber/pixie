import { expect, test } from "bun:test";
import {
	decideCompatibility,
	mergeMonotonicState,
	planSchemaRollback,
	runRollbackStages,
	stagedRollback,
} from "../../../assistant/src/compatibility/index.ts";
import { createRollbackHooks } from "../../../assistant/src/compatibility/rollback.ts";

test("old, current and newer state schemas have distinct mutation behavior", () => {
	expect(decideCompatibility({ legacy: null }).dispatchAllowed).toBe(true);
	expect(
		decideCompatibility({ legacy: { protocolVersion: 1, stateSchemaVersion: 1 } }).outcome,
	).toBe("compatible");
	const older = decideCompatibility({
		legacy: { protocolVersion: 1, stateSchemaVersion: 0 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
		supportedLegacyStateSchemas: [1],
	});
	expect(older.outcome).toBe("migrate");
	expect(older.dispatchAllowed).toBe(false);
	const newer = decideCompatibility({
		legacy: { protocolVersion: 2, stateSchemaVersion: 2 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
	});
	expect(newer.outcome).toBe("blocked");
	expect(newer.dispatchAllowed).toBe(false);
});

test("unknown state fields are reported and preserved rather than silently dropped", () => {
	const result = decideCompatibility({
		legacy: {
			protocolVersion: 1,
			stateSchemaVersion: 1,
			state: { futureLedgerMode: "append-only", unrelated: { keep: true } },
		},
	});
	expect(result.outcome).toBe("compatible");
	expect(result.unknownFields).toEqual(["futureLedgerMode", "unrelated"]);
	expect(result.unknownFieldsPreserved).toBe(true);
});

test("ledger claims and tombstones are monotonic across a rollback merge", () => {
	const state = mergeMonotonicState(
		{
			ledger: [{ id: "delivery-1", sequence: 3, status: "accepted", future: "keep" }],
			tombstones: [{ id: "session-1", sequence: 3, confirmed: true }],
		},
		{
			ledger: [
				{ id: "delivery-1", sequence: 2, status: "prepared" },
				{ id: "delivery-2", sequence: 1, status: "settled" },
			],
			tombstones: [
				{ id: "session-1", sequence: 1, confirmed: false },
				{ id: "session-2", sequence: 1 },
			],
		},
	);
	expect(state.ledger).toEqual([
		{ id: "delivery-1", sequence: 3, status: "accepted", future: "keep" },
		{ id: "delivery-2", sequence: 1, status: "settled" },
	]);
	expect(state.tombstones).toEqual([
		{ id: "session-1", sequence: 3, confirmed: true },
		{ id: "session-2", sequence: 1 },
	]);
});

test("rollback refuses runnable rewind after accepted work or deletion", async () => {
	const clean = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: false },
		restoredFiles: ["pixie/sessions.json"],
	});
	expect(clean.dispatchEnabled).toBe(true);

	const unsafe = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: true, confirmedDeletions: true, monotonicClaims: true },
		restoredFiles: ["pixie/sessions.json", "pi-session-deletions.json"],
		backupState: { tombstones: [{ id: "deleted", sequence: 2, confirmed: true }] },
		currentState: { ledger: [{ id: "delivery", sequence: 4, status: "accepted" }] },
	});
	expect(unsafe.canRestoreAsRunnable).toBe(false);
	expect(unsafe.dispatchEnabled).toBe(false);
	expect(unsafe.preservedState.ledger[0]?.status).toBe("accepted");
	expect(unsafe.preservedState.tombstones[0]?.confirmed).toBe(true);
	expect(unsafe.unresolvedWork.join(",")).toContain("preserve-confirmed-tombstones");
	const staged = await stagedRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: true, confirmedDeletions: true, monotonicClaims: true },
		restoredFiles: ["pixie/sessions.json"],
	});
	expect(staged.run.outcome).toBe("completed");
	expect(staged.run.dispatchEnabled).toBe(false);
	expect(staged.run.executed).toEqual([
		"verify-pairing",
		"pause-admission",
		"restore-backup",
		"reconcile-effects",
		"publish-receipt",
	]);
});

test("staged rollback phases are ordered and an interrupted phase cannot enable dispatch", async () => {
	const plan = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: true, confirmedDeletions: false, monotonicClaims: false },
		restoredFiles: ["pixie/sessions.json"],
	});
	const calls: string[] = [];
	const hooks = createRollbackHooks(plan, {
		"verify-pairing": async () => {
			calls.push("verify-pairing");
			return { ok: true, detail: "paired" };
		},
		"pause-admission": async () => {
			calls.push("pause-admission");
			return { ok: false, detail: "admission did not pause" };
		},
	});
	const run = await runRollbackStages(plan, hooks);
	expect(run.outcome).toBe("interrupted");
	expect(run.dispatchEnabled).toBe(false);
	expect(calls).toEqual(["verify-pairing", "pause-admission"]);

	const completed = await stagedRollback({
		backupAvailable: false,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: false },
	});
	expect(completed.run.outcome).toBe("noop");
	expect(completed.run.dispatchEnabled).toBe(false);
});
