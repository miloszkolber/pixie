// Bounded native JSONL framing for GO-02.
//
// One reader and one serialized writer per managed native child exchange
// LF-delimited JSON records. An optional carriage return immediately before
// LF is accepted; no other Unicode separator delimits a record. Whole
// records and whole writes are bounded; framing never truncates JSON and
// never treats a log line as an event.

export const NATIVE_JSONL_MAX_RECORD_BYTES = 32 * 1024 * 1024;
export const NATIVE_JSONL_MAX_WRITE_BYTES = 32 * 1024 * 1024;
export const NATIVE_JSONL_AGGREGATE_MAX_BYTES = 64 * 1024 * 1024;
export const NATIVE_JSONL_CONTROL_RESERVE_BYTES = 1024 * 1024;
export const NATIVE_JSONL_CONTROL_MAX_OPS = 8;
export const NATIVE_JSONL_CONTROL_MAX_BYTES_EACH = 64 * 1024;
export const NATIVE_JSONL_MAX_PENDING_PER_CHILD = 128;

export const NATIVE_JSONL_HELLO_TIMEOUT_MS = 10_000;
export const NATIVE_JSONL_WRITE_TIMEOUT_MS = 10_000;
export const NATIVE_JSONL_READ_STALL_TIMEOUT_MS = 30_000;
export const NATIVE_JSONL_ABORT_GRACE_MS = 10_000;
export const NATIVE_JSONL_DRAIN_TIMEOUT_MS = 25_000;

export type NativeTransportErrorKind =
	| "too_big"
	| "incomplete"
	| "invalid_utf8"
	| "invalid_json"
	| "log_line"
	| "duplicate"
	| "backpressure"
	| "stalled"
	| "child_exit";

export class NativeTransportError extends Error {
	readonly kind: NativeTransportErrorKind;
	readonly timeoutMs?: number;

	constructor(kind: NativeTransportErrorKind, message: string, timeoutMs?: number) {
		super(message);
		this.name = "NativeTransportError";
		this.kind = kind;
		this.timeoutMs = timeoutMs;
	}
}

export function nativeTransportErrorKindOf(error: unknown): NativeTransportErrorKind | undefined {
	if (error instanceof NativeTransportError) return error.kind;
	return undefined;
}

function byteLength(value: string): number {
	return Buffer.byteLength(value, "utf8");
}

/** Split buffered text on LF only. Unicode separators never delimit. */
export function splitNativeJsonlBuffer(buffer: string): { records: string[]; remainder: string } {
	const records: string[] = [];
	let start = 0;
	for (;;) {
		const index = buffer.indexOf("\n", start);
		if (index < 0) break;
		let line = buffer.slice(start, index);
		if (line.endsWith("\r")) line = line.slice(0, -1);
		if (byteLength(line) > NATIVE_JSONL_MAX_RECORD_BYTES) {
			throw new NativeTransportError(
				"too_big",
				`Native record exceeds the ${NATIVE_JSONL_MAX_RECORD_BYTES}-byte limit`,
			);
		}
		if (line.includes("\u0000")) {
			throw new NativeTransportError("invalid_utf8", "Native record is not valid UTF-8");
		}
		records.push(line);
		start = index + 1;
	}
	return { records, remainder: buffer.slice(start) };
}

/** Serialize one native record plus its LF delimiter without truncation. */
export function encodeNativeJsonlRecord(value: unknown): string {
	let raw: string;
	try {
		raw = JSON.stringify(value);
	} catch {
		throw new NativeTransportError("invalid_json", "Native record is not serializable");
	}
	if (raw === undefined) {
		throw new NativeTransportError("invalid_json", "Native record is not serializable");
	}
	if (byteLength(raw) > NATIVE_JSONL_MAX_WRITE_BYTES) {
		throw new NativeTransportError(
			"too_big",
			`Native write exceeds the ${NATIVE_JSONL_MAX_WRITE_BYTES}-byte limit`,
		);
	}
	return `${raw}\n`;
}

/**
 * Validate one framed native record. Non-object JSON and malformed JSON are
 * log lines, never events.
 */
export function parseNativeJsonlRecord(line: string): unknown {
	let trimmed = line;
	if (trimmed.endsWith("\n")) trimmed = trimmed.slice(0, -1);
	if (trimmed.endsWith("\r")) trimmed = trimmed.slice(0, -1);
	if (byteLength(trimmed) > NATIVE_JSONL_MAX_RECORD_BYTES) {
		throw new NativeTransportError(
			"too_big",
			`Native record exceeds the ${NATIVE_JSONL_MAX_RECORD_BYTES}-byte limit`,
		);
	}
	if (trimmed.includes("\u0000")) {
		throw new NativeTransportError("invalid_utf8", "Native record is not valid UTF-8");
	}
	if (trimmed.trim().length === 0 || trimmed.trimStart()[0] !== "{") {
		throw new NativeTransportError("log_line", "Native log line is not an event");
	}
	try {
		const parsed: unknown = JSON.parse(trimmed);
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
			throw new NativeTransportError("log_line", "Native log line is not an event");
		}
		return parsed;
	} catch (error) {
		if (error instanceof NativeTransportError) throw error;
		throw new NativeTransportError("invalid_json", "Invalid native JSON");
	}
}
