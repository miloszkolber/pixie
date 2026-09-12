import { expect, test } from "bun:test";
import {
	createRollbackHooks,
	decideLegacyCompatibility,
	isOldReaderCompatible,
	planSchemaRollback,
} from "../../../assistant/src/discovery-compat.ts";

test("staged compatibility keeps fresh, matching, migrate and blocked distinct", () => {
	const fresh = decideLegacyCompatibility({ legacy: null });
	expect(fresh.outcome).toBe("compatible");
	expect(fresh.stage).toBe("read");
	expect(fresh.dispatchAllowed).toBe(true);
	expect(fresh.rollbackAvailable).toBe(false);

	const matching = decideLegacyCompatibility({
		legacy: { protocolVersion: 1, stateSchemaVersion: 1 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
	});
	expect(matching.outcome).toBe("compatible");
	expect(matching.dispatchAllowed).toBe(true);

	const migrate = decideLegacyCompatibility({
		legacy: { protocolVersion: 1, stateSchemaVersion: 0 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
		supportedLegacyStateSchemas: [1],
	});
	expect(migrate.outcome).toBe("migrate");
	expect(migrate.stage).toBe("migrate");
	expect(migrate.rollbackAvailable).toBe(true);
	expect(migrate.dispatchAllowed).toBe(false);
	expect(migrate.requiredActions.join(",")).toMatch(/backup-with-hashes/);
	expect(migrate.requiredActions.join(",")).toMatch(/pause-schedule-outbox-admission/);

	const newerProtocol = decideLegacyCompatibility({
		legacy: { protocolVersion: 2, stateSchemaVersion: 1 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
	});
	expect(newerProtocol.outcome).toBe("blocked");
	expect(newerProtocol.dispatchAllowed).toBe(false);
	expect(newerProtocol.requiredActions.join(",")).toMatch(/reconcile/);

	const newerSchema = decideLegacyCompatibility({
		legacy: { protocolVersion: 1, stateSchemaVersion: 2 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
	});
	expect(newerSchema.outcome).toBe("blocked");

	const unknownNewer = decideLegacyCompatibility({
		legacy: { hasUnknownNewerSchema: true },
	});
	expect(unknownNewer.outcome).toBe("blocked");
	expect(unknownNewer.dispatchAllowed).toBe(false);

	const migrationNotAllowed = decideLegacyCompatibility({
		legacy: { protocolVersion: 1, stateSchemaVersion: 0 },
		currentProtocolVersion: 1,
		currentStateSchemaVersion: 1,
		supportedLegacyStateSchemas: [1],
		allowMigration: false,
	});
	expect(migrationNotAllowed.outcome).toBe("blocked");
});

test("rollback never restores older snapshots as runnable authority after effects", () => {
	const clean = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: false },
		restoredFiles: ["agentDir/pixie/sessions.json"],
	});
	expect(clean.canRestoreAsRunnable).toBe(true);
	expect(clean.dispatchEnabled).toBe(true);
	expect(clean.requiresReconciliation).toBe(false);
	expect(clean.receipt.restoredFiles).toEqual(["agentDir/pixie/sessions.json"]);
	expect(clean.receipt.retainedPostBackupEffects).toEqual([]);

	const dispatched = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: true, confirmedDeletions: false, monotonicClaims: false },
		restoredFiles: ["agentDir/pixie/sessions.json"],
	});
	expect(dispatched.canRestoreAsRunnable).toBe(false);
	expect(dispatched.dispatchEnabled).toBe(false);
	expect(dispatched.requiresReconciliation).toBe(true);
	expect(dispatched.retainedEffects.join(",")).toMatch(/dispatched-work-retained/);
	expect(dispatched.unresolvedWork.join(",")).toMatch(/reconcile-dispatched-work/);
	expect(dispatched.receipt.retainedPostBackupEffects.length).toBeGreaterThan(0);

	const deletions = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: false, confirmedDeletions: true, monotonicClaims: false },
		restoredFiles: ["agentDir/pi-session-deletions.json"],
	});
	expect(deletions.canRestoreAsRunnable).toBe(false);
	expect(deletions.dispatchEnabled).toBe(false);
	expect(deletions.unresolvedWork.join(",")).toMatch(/preserve-confirmed-tombstones/);

	const monotonic = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 0,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: true },
		restoredFiles: ["agentDir/pi-session-queues.json"],
	});
	expect(monotonic.dispatchEnabled).toBe(false);
	expect(monotonic.requiresReconciliation).toBe(true);
	expect(monotonic.unresolvedWork.join(",")).toMatch(/monotonic-claims/);

	const noop = planSchemaRollback({
		backupAvailable: false,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: false },
	});
	expect(noop.receipt.noop).toBe(true);
	expect(noop.canRestoreAsRunnable).toBe(false);
	expect(noop.dispatchEnabled).toBe(false);
});

test("rollback hooks stay ordered, explicit and safe when interrupted", async () => {
	const blocked = planSchemaRollback({
		backupAvailable: true,
		backupProtocolVersion: 1,
		backupStateSchemaVersion: 1,
		effects: { dispatchedWork: true, confirmedDeletions: false, monotonicClaims: false },
		restoredFiles: ["agentDir/pixie/sessions.json"],
	});
	const calls: string[] = [];
	const hooks = createRollbackHooks(blocked, {
		"verify-pairing": async () => {
			calls.push("verify-pairing");
			return { ok: true, detail: "pairing verified" };
		},
		"pause-admission": async () => {
			calls.push("pause-admission");
			return { ok: true, detail: "admission paused" };
		},
	});
	expect(hooks.map((hook) => hook.id)).toEqual([
		"verify-pairing",
		"pause-admission",
		"restore-backup",
		"reconcile-effects",
		"publish-receipt",
	]);
	// Read-only phases sort before mutating restore/reconcile/receipt.
	expect(hooks[0]?.mutating).toBe(false);
	expect(hooks[2]?.mutating).toBe(true);
	expect(hooks[4]?.id).toBe("publish-receipt");

	for (const hook of hooks) {
		const result = await hook.run();
		expect(typeof result.ok).toBe("boolean");
		expect(result.detail.length).toBeGreaterThan(0);
	}
	expect(calls).toEqual(["verify-pairing", "pause-admission"]);
	// Blocked restore reports explicitly instead of pretending to restore.
	const restore = hooks.find((hook) => hook.id === "restore-backup");
	expect((await restore?.run())?.ok).toBe(false);

	const noopPlan = planSchemaRollback({
		backupAvailable: false,
		effects: { dispatchedWork: false, confirmedDeletions: false, monotonicClaims: false },
	});
	const noopHooks = createRollbackHooks(noopPlan);
	expect(noopHooks.length).toBe(1);
	expect(noopHooks[0]?.id).toBe("noop-rollback");
	expect(noopHooks[0]?.mutating).toBe(false);
	expect((await noopHooks[0]?.run())?.ok).toBe(true);
});

test("old-reader compatibility requires exact schema readers", () => {
	expect(isOldReaderCompatible({ oldProtocol: 1, oldStateSchema: 1 })).toBe(true);
	expect(isOldReaderCompatible({ oldProtocol: 1, oldStateSchema: 0, currentStateSchema: 1 })).toBe(
		false,
	);
	expect(isOldReaderCompatible({ oldProtocol: 0, oldStateSchema: 1, currentProtocol: 1 })).toBe(
		false,
	);
});
