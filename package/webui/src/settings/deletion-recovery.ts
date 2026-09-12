import type { DeletionRecovery } from "@pixie/contracts";

/** Stable identity for one retained deletion tombstone. */
export function deletionRecoveryKey(
	record: Pick<DeletionRecovery, "projectId" | "sessionId">,
): string {
	return `${record.projectId}\0${record.sessionId}`;
}

/**
 * Validate the optional welcome/readiness `deletionRecovery` payload without
 * trusting untyped transport data. Invalid rows are dropped rather than shown
 * as an operator record.
 */
export function normalizeDeletionRecovery(value: unknown): DeletionRecovery[] {
	if (!Array.isArray(value)) return [];
	const records: DeletionRecovery[] = [];
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
		records.push({ projectId, sessionId, phase, reason });
	}
	return records;
}

/** Remove exactly one confirmed tombstone; no bulk clear exists. */
export function dropDeletionRecovery(
	records: readonly DeletionRecovery[],
	projectId: string,
	sessionId: string,
): DeletionRecovery[] {
	return records.filter(
		(record) => record.projectId !== projectId || record.sessionId !== sessionId,
	);
}
