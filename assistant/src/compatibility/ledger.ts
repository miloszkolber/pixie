import type { LedgerClaim, MonotonicState, Tombstone } from "./types.ts";

const statusRank: Record<string, number> = {
	prepared: 1,
	dispatching: 2,
	accepted: 3,
	settled: 4,
	uncertain: 5,
	interrupted: 5,
	rejected: 5,
};

function rank(status: string): number {
	return statusRank[status] ?? 0;
}

function sequence(value: unknown): number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : 0;
}

function mergeRecord<T extends Record<string, unknown>>(older: T, newer: T): T {
	return { ...older, ...newer };
}

function chooseClaim(left: LedgerClaim, right: LedgerClaim): LedgerClaim {
	const preferred =
		right.sequence >= left.sequence ? mergeRecord(left, right) : mergeRecord(right, left);
	return {
		...preferred,
		sequence: Math.max(left.sequence, right.sequence),
		status: rank(right.status) >= rank(left.status) ? right.status : left.status,
	};
}

/**
 * Merge ledger claims without allowing an older snapshot to make a newer
 * delivery runnable again.  Unknown claim fields are copied from both sides.
 */
export function mergeMonotonicLedger(
	older: readonly LedgerClaim[] = [],
	newer: readonly LedgerClaim[] = [],
): readonly LedgerClaim[] {
	const merged = new Map<string, LedgerClaim>();
	for (const claim of [...older, ...newer]) {
		if (!claim || typeof claim.id !== "string" || claim.id.length === 0) continue;
		const normalized = { ...claim, sequence: sequence(claim.sequence) };
		const previous = merged.get(normalized.id);
		merged.set(normalized.id, previous ? chooseClaim(previous, normalized) : normalized);
	}
	return [...merged.values()].sort((left, right) => left.id.localeCompare(right.id));
}

function chooseTombstone(left: Tombstone, right: Tombstone): Tombstone {
	const preferred =
		right.sequence >= left.sequence ? mergeRecord(left, right) : mergeRecord(right, left);
	return {
		...preferred,
		sequence: Math.max(left.sequence, right.sequence),
		confirmed: left.confirmed === true || right.confirmed === true,
	};
}

/** Tombstones are append-only. A rollback cannot remove a confirmed deletion. */
export function mergeTombstones(
	older: readonly Tombstone[] = [],
	newer: readonly Tombstone[] = [],
): readonly Tombstone[] {
	const merged = new Map<string, Tombstone>();
	for (const tombstone of [...older, ...newer]) {
		if (!tombstone || typeof tombstone.id !== "string" || tombstone.id.length === 0) continue;
		const normalized = { ...tombstone, sequence: sequence(tombstone.sequence) };
		const previous = merged.get(normalized.id);
		merged.set(normalized.id, previous ? chooseTombstone(previous, normalized) : normalized);
	}
	return [...merged.values()].sort((left, right) => left.id.localeCompare(right.id));
}

export function mergeMonotonicState(
	older: Partial<MonotonicState> = {},
	newer: Partial<MonotonicState> = {},
): MonotonicState {
	return {
		ledger: mergeMonotonicLedger(older.ledger ?? [], newer.ledger ?? []),
		tombstones: mergeTombstones(older.tombstones ?? [], newer.tombstones ?? []),
	};
}
