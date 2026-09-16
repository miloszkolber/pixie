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
