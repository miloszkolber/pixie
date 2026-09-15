import type { DeletionRecovery } from "@pixie/shared";

const deletionPhases = new Set<DeletionRecovery["phase"]>(["requested", "confirmed"]);

/** Stable identity for one retained deletion tombstone. */
export function deletionReconciliationKey(
	record: Pick<DeletionRecovery, "projectId" | "sessionId">,
): string {
	return `${record.projectId}\0${record.sessionId}`;
}

/** Display the explicit empty durable project key without fabricating a project. */
export function deletionProjectLabel(record: Pick<DeletionRecovery, "projectId">): string {
	return record.projectId === "" ? "Ungrouped" : record.projectId;
}

/**
 * Validate an untyped `session.deletionRecovery` payload without trusting
 * transport data. Invalid rows are dropped rather than shown as an operator
 * record, so a malformed entry can never clear a real tombstone.
 */
export function normalizeDeletionReconciliation(value: unknown): DeletionRecovery[] {
	if (!Array.isArray(value)) return [];
	const records: DeletionRecovery[] = [];
	const seen = new Set<string>();
	for (const item of value) {
		if (typeof item !== "object" || item === null) continue;
		const { projectId, sessionId, phase, reason } = item as Record<string, unknown>;
		if (
			typeof projectId !== "string" ||
			typeof sessionId !== "string" ||
			typeof phase !== "string" ||
			typeof reason !== "string"
		) {
			continue;
		}
		if (
			sessionId === "" ||
			projectId.includes("\0") ||
			sessionId.includes("\0") ||
			reason.trim() === "" ||
			reason.includes("\0") ||
			!deletionPhases.has(phase)
		) {
			continue;
		}
		const key = deletionReconciliationKey({ projectId, sessionId });
		if (seen.has(key)) continue;
		seen.add(key);
		records.push({ projectId, sessionId, phase, reason });
	}
	return records;
}

/** True when the native outcome is unknown and needs restart reconciliation. */
export function isUncertainDeletion(record: Pick<DeletionRecovery, "phase" | "reason">): boolean {
	if (record.phase === "requested") return true;
	return /uncertain|reconcile/i.test(record.reason);
}

/**
 * Actionable confirm/retain guidance for one tombstone. Confirm asserts the
 * native session is already gone; retain is an explicit no-op that leaves the
 * tombstone in place. Nothing here clears records automatically.
 */
export function remediationForDeletion(record: Pick<DeletionRecovery, "phase" | "reason">): string {
	const reason = record.reason.toLowerCase();
	if (
		reason.includes("binding changed") ||
		reason.includes("recovery-blocked") ||
		reason.includes("host identity mismatch")
	) {
		return "Native identity changed: confirm only after verifying the native session is gone outside Pixie, or retain to keep the tombstone for a later host.";
	}
	if (reason.includes("uncertain") || reason.includes("reconcile")) {
		return "Outcome is uncertain: confirm only after verifying the native session is gone, or retain to keep the tombstone and retry after restart.";
	}
	if (reason.includes("cleanup") || reason.includes("finish deletion")) {
		return "Native deletion is done but local cleanup is pending: confirm again to retry cleanup, or retain to keep the tombstone.";
	}
	if (reason.includes("capability") || reason.includes("unsupported")) {
		return "Connected agent cannot delete: retain the record and retry after restoring a host with session.delete support; confirm only if the native session is already gone.";
	}
	return "Confirm only after verifying the native session is gone, or retain to keep the tombstone in place.";
}
