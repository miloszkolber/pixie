/** Read-only, bounded native JSONL catalog/history projections. */

export const DEFAULT_HISTORY_MAX_RECORDS = 256;
export const DEFAULT_HISTORY_MAX_BYTES = 8 * 1024 * 1024;
export const DEFAULT_HISTORY_MAX_RECORD_BYTES = 32 * 1024 * 1024;
export const DEFAULT_CATALOG_MAX_ENTRIES = 512;
export const DEFAULT_CATALOG_MAX_BYTES = 2 * 1024 * 1024;
/** Common Pi transcript entry types; future types remain source-visible. */
export const DEFAULT_NATIVE_HISTORY_TYPES = [
	"session",
	"message",
	"model_change",
	"thinking_level_change",
	"compaction",
	"branch_summary",
	"custom",
	"custom_message",
	"label",
	"turn_start",
	"turn_end",
] as const;

export interface NativeFileFingerprint {
	readonly path?: string;
	readonly device?: number | string;
	readonly inode?: number | string;
	readonly size?: number;
	readonly mtimeMs?: number;
	readonly revision?: string;
}

export type SourceChange = "same" | "modified" | "replaced" | "missing" | "unknown";

export function compareNativeFile(
	previous: NativeFileFingerprint | null | undefined,
	current: NativeFileFingerprint | null | undefined,
): SourceChange {
	if (!current) return "missing";
	if (!previous) return "unknown";
	if (previous.revision !== undefined && current.revision !== undefined && previous.revision !== current.revision)
		return "replaced";
	if (
		previous.device !== undefined &&
		current.device !== undefined &&
		previous.inode !== undefined &&
		current.inode !== undefined &&
		(previous.device !== current.device || previous.inode !== current.inode)
	)
		return "replaced";
	if (previous.path !== undefined && current.path !== undefined && previous.path !== current.path) return "replaced";
	if (previous.size !== current.size || previous.mtimeMs !== current.mtimeMs) return "modified";
	return "same";
}

export function isExternalReplacement(
	previous: NativeFileFingerprint | null | undefined,
	current: NativeFileFingerprint | null | undefined,
): boolean {
	return compareNativeFile(previous, current) === "replaced";
}

export interface HistoryRecordBase {
	readonly line: number;
	readonly offset: number;
	readonly bytes: number;
	readonly raw: string;
	readonly value: unknown;
	readonly id?: string;
	readonly type?: string;
}

export interface KnownHistoryRecord extends HistoryRecordBase {
	readonly kind: "known";
	readonly type: string;
	readonly value: Record<string, unknown>;
}

export interface UnknownHistoryRecord extends HistoryRecordBase {
	readonly kind: "unknown";
	readonly reason: "unknown-type" | "malformed" | "non-object" | "oversized";
}

export type IndexedHistoryRecord = KnownHistoryRecord | UnknownHistoryRecord;

export interface HistoryScanOptions {
	readonly maxRecords?: number;
	readonly maxBytes?: number;
	readonly maxRecordBytes?: number;
	readonly sourceRevision?: string;
	/** When supplied, only these native record types are considered known. */
	readonly knownTypes?: readonly string[];
}

export interface IncompleteTrailingRecord {
	readonly offset: number;
	readonly bytes: number;
	readonly raw: string;
}

export interface HistoryScanResult {
	readonly records: readonly IndexedHistoryRecord[];
	readonly unknownRecords: readonly UnknownHistoryRecord[];
	readonly sourceRevision?: string;
	readonly bytes: number;
	readonly truncated: boolean;
	readonly incompleteTrailing?: IncompleteTrailingRecord;
}

function bounded(value: number | undefined, fallback: number): number {
	return Number.isSafeInteger(value) && value !== undefined && value > 0 ? value : fallback;
}

function utf8Bytes(value: string): number {
	return new TextEncoder().encode(value).byteLength;
}

function recordIdentity(value: Record<string, unknown>): { id?: string; type?: string } {
	return {
		...(typeof value.id === "string" ? { id: value.id } : {}),
		...(typeof value.type === "string" ? { type: value.type } : {}),
	};
}

function unknownRecord(
	line: number,
	offset: number,
	raw: string,
	reason: UnknownHistoryRecord["reason"],
	value: unknown = raw,
): UnknownHistoryRecord {
	const details = value && typeof value === "object" && !Array.isArray(value) ? recordIdentity(value as Record<string, unknown>) : {};
	return {
		kind: "unknown",
		line,
		offset,
		bytes: utf8Bytes(raw) + 1,
		raw,
		value,
		reason,
		...details,
	};
}

/**
 * Index complete JSONL records without writing, reopening, or repairing the
 * source. Unknown and malformed records remain visible in the result.
 */
export function scanNativeHistory(source: string, options: HistoryScanOptions = {}): HistoryScanResult {
	const maxRecords = bounded(options.maxRecords, DEFAULT_HISTORY_MAX_RECORDS);
	const maxBytes = bounded(options.maxBytes, DEFAULT_HISTORY_MAX_BYTES);
	const maxRecordBytes = bounded(options.maxRecordBytes, DEFAULT_HISTORY_MAX_RECORD_BYTES);
	const knownTypes = new Set(options.knownTypes ?? DEFAULT_NATIVE_HISTORY_TYPES);
	const records: IndexedHistoryRecord[] = [];
	let bytes = 0;
	let offset = 0;
	let line = 0;
	let truncated = false;
	let incompleteTrailing: IncompleteTrailingRecord | undefined;

	for (;;) {
		const end = source.indexOf("\n", offset);
		if (end < 0) {
			if (offset < source.length) {
				const trailing = source.slice(offset);
				incompleteTrailing = { offset, bytes: utf8Bytes(trailing), raw: trailing };
			}
			break;
		}
		const rawWithCr = source.slice(offset, end);
		const raw = rawWithCr.endsWith("\r") ? rawWithCr.slice(0, -1) : rawWithCr;
		const recordBytes = utf8Bytes(rawWithCr) + 1;
		if (records.length >= maxRecords || bytes + recordBytes > maxBytes) {
			truncated = true;
			break;
		}
		let record: IndexedHistoryRecord;
		if (utf8Bytes(raw) > maxRecordBytes) record = unknownRecord(line, offset, raw, "oversized");
		else {
			try {
				const value: unknown = JSON.parse(raw);
				if (!value || typeof value !== "object" || Array.isArray(value))
					record = unknownRecord(line, offset, raw, "non-object", value);
				else {
					const object = value as Record<string, unknown>;
					const type = typeof object.type === "string" ? object.type : undefined;
					const known = type !== undefined && knownTypes.has(type);
					record = known
						? { kind: "known", line, offset, bytes: recordBytes, raw, value: object, ...recordIdentity(object), type }
						: unknownRecord(line, offset, raw, "unknown-type", object);
				}
			} catch {
				record = unknownRecord(line, offset, raw, "malformed");
			}
		}
		records.push(record);
		bytes += recordBytes;
		offset = end + 1;
		line++;
	}

	return {
		records,
		unknownRecords: records.filter((record): record is UnknownHistoryRecord => record.kind === "unknown"),
		...(options.sourceRevision !== undefined ? { sourceRevision: options.sourceRevision } : {}),
		bytes,
		truncated,
		...(incompleteTrailing ? { incompleteTrailing } : {}),
	};
}

export interface HistoryCursor {
	readonly sourceRevision: string;
	readonly index: number;
}

export interface HistoryPage {
	readonly records: readonly IndexedHistoryRecord[];
	readonly nextCursor?: HistoryCursor;
	readonly invalidCursor: boolean;
}

export function encodeHistoryCursor(cursor: HistoryCursor): string {
	return encodeURIComponent(JSON.stringify(cursor));
}

export function decodeHistoryCursor(value: string): HistoryCursor | undefined {
	try {
		const parsed: unknown = JSON.parse(decodeURIComponent(value));
		if (!parsed || typeof parsed !== "object") return undefined;
		const cursor = parsed as { sourceRevision?: unknown; index?: unknown };
		const index = cursor.index;
		return typeof cursor.sourceRevision === "string" && typeof index === "number" && Number.isSafeInteger(index) && index >= 0
			? { sourceRevision: cursor.sourceRevision, index }
			: undefined;
	} catch {
		return undefined;
	}
}

export function pageNativeHistory(
	records: readonly IndexedHistoryRecord[],
	options: { readonly sourceRevision: string; readonly cursor?: HistoryCursor; readonly limit?: number },
): HistoryPage {
	const limit = bounded(options.limit, 100);
	const cursor = options.cursor;
	if (cursor && (cursor.sourceRevision !== options.sourceRevision || !Number.isSafeInteger(cursor.index) || cursor.index < 0))
		return { records: [], invalidCursor: true };
	const start = cursor?.index ?? 0;
	const page = records.slice(start, start + limit);
	return {
		records: page,
		invalidCursor: false,
		...(start + page.length < records.length
			? { nextCursor: { sourceRevision: options.sourceRevision, index: start + page.length } }
			: {}),
	};
}

export interface CatalogCandidate {
	readonly sessionId?: string;
	readonly nativeSessionId?: string;
	readonly path?: string;
	readonly updatedAt?: string | number;
	readonly known?: boolean;
	readonly record?: unknown;
	readonly metadata?: Readonly<Record<string, unknown>>;
}

export interface CatalogEntry extends CatalogCandidate {
	readonly key: string;
	readonly kind: "known" | "unknown";
	readonly duplicateOf?: string;
}

export interface CatalogResult {
	readonly entries: readonly CatalogEntry[];
	readonly truncated: boolean;
	readonly bytes: number;
}

/** Keep duplicate IDs and unknown source rows instead of silently deduplicating them. */
export function buildBoundedCatalog(
	candidates: readonly CatalogCandidate[],
	options: { readonly maxEntries?: number; readonly maxBytes?: number } = {},
): CatalogResult {
	const maxEntries = bounded(options.maxEntries, DEFAULT_CATALOG_MAX_ENTRIES);
	const maxBytes = bounded(options.maxBytes, DEFAULT_CATALOG_MAX_BYTES);
	const seen = new Map<string, string>();
	const occurrences = new Map<string, number>();
	const entries: CatalogEntry[] = [];
	let bytes = 0;
	let truncated = false;
	for (const candidate of candidates) {
		if (entries.length >= maxEntries) {
			truncated = true;
			break;
		}
		const base = candidate.sessionId ?? candidate.nativeSessionId ?? candidate.path ?? "unknown";
		const occurrence = (occurrences.get(base) ?? 0) + 1;
		occurrences.set(base, occurrence);
		const key = `${base}#${occurrence}`;
		const encodedBytes = utf8Bytes(JSON.stringify(candidate));
		if (bytes + encodedBytes > maxBytes) {
			truncated = true;
			break;
		}
		const first = seen.get(base);
		seen.set(base, first ?? key);
		entries.push({
			...candidate,
			key,
			kind: candidate.known === false ? "unknown" : "known",
			...(first ? { duplicateOf: first } : {}),
		});
		bytes += encodedBytes;
	}
	return { entries, truncated, bytes };
}

export interface HistoryContinuity {
	readonly change: SourceChange;
	readonly restartRequired: boolean;
	readonly preserveUnknownRecords: true;
}

export function reconcileHistorySource(
	previous: NativeFileFingerprint | null | undefined,
	current: NativeFileFingerprint | null | undefined,
): HistoryContinuity {
	const change = compareNativeFile(previous, current);
	return {
		change,
		restartRequired: change === "replaced" || change === "missing" || change === "unknown",
		preserveUnknownRecords: true,
	};
}

export const scanHistory = scanNativeHistory;
export const paginateHistory = pageNativeHistory;
export const catalogSessions = buildBoundedCatalog;
export const serializeHistoryCursor = encodeHistoryCursor;
export const parseHistoryCursor = decodeHistoryCursor;
