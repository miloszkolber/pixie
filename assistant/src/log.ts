/**
 * Secret-safe structured host logging.
 *
 * Host diagnostics must never export raw bearer tokens, credential values,
 * endpoint URLs or absolute filesystem paths. Every value crosses one redaction
 * function, and retained entries are bounded so a noisy or hostile child cannot
 * grow host memory without limit.
 */

export type HostLogLevel = "info" | "warn" | "error";

export interface HostLogEntry {
	readonly level: HostLogLevel;
	readonly event: string;
	readonly fields: Readonly<Record<string, unknown>>;
}

export interface HostLogger {
	info(event: string, fields?: Record<string, unknown>): void;
	warn(event: string, fields?: Record<string, unknown>): void;
	error(event: string, error: unknown, fields?: Record<string, unknown>): void;
	/** Bounded retained entries, oldest first. */
	entries(): readonly HostLogEntry[];
}

export interface HostLoggerOptions {
	/** Maximum retained entries. Defaults to 200. */
	readonly capacity?: number;
	/** Secret values that must never appear verbatim. */
	readonly secrets?: readonly string[];
	/** Structured sink for each emitted entry. Defaults to a no-op. */
	readonly sink?: (entry: HostLogEntry) => void;
}

const DEFAULT_CAPACITY = 200;
const MIN_SECRET_LENGTH = 8;

/**
 * Bounds for structured field redaction. A hostile or accidental field graph
 * must not grow retained host memory or overflow the stack, so a value that
 * crosses a depth, entry-count or cycle bound becomes a fixed placeholder
 * instead of being traversed further.
 */
export const MAX_FIELD_DEPTH = 8;
export const MAX_COLLECTION_ENTRIES = 100;
export const FIELD_BOUND_PLACEHOLDER = "[truncated]";

/**
 * Approximate serialized-byte budget for one converted field graph. Conversion
 * memoizes one result per source object, but the converted graph is shared:
 * the emit sink serializes it with `JSON.stringify`, which re-expands a shared
 * subtree once per reference. Every materialized occurrence — including a
 * repeated reference to an already-converted object — is charged against this
 * budget, so the serializable output is bounded regardless of sharing. A value
 * that would exceed the remaining budget becomes `FIELD_BOUND_PLACEHOLDER`.
 */
export const MAX_MATERIALIZED_BYTES = 64 * 1024;

/** Serialized length of the placeholder, used by the materialization budget. */
const PLACEHOLDER_COST = FIELD_BOUND_PLACEHOLDER.length + 2;

/** Conservative upper bound of `JSON.stringify(text).length`. */
function jsonStringCost(text: string): number {
	let cost = 2;
	for (let index = 0; index < text.length; index += 1) {
		const code = text.charCodeAt(index);
		if (code === 0x22 || code === 0x5c) cost += 2;
		else if (code < 0x20 || (code >= 0xd800 && code <= 0xdfff)) cost += 6;
		else cost += 1;
	}
	return cost;
}

function jsonNumberCost(value: number): number {
	if (!Number.isFinite(value)) return 4;
	return Math.max(1, String(value).length);
}

/**
 * Maximum retained length of one redacted stderr line. The line is redacted
 * first so a truncated tail can never expose raw secret text, then shortened.
 */
export const MAX_STDERR_LINE_LENGTH = 4096;
const STDERR_TRUNCATION_MARKER = FIELD_BOUND_PLACEHOLDER;

// Absolute POSIX paths (at least two segments) become a placeholder. A bare
// "/" or a short relative fragment is left alone so ordinary prose survives.
const ABSOLUTE_PATH = /(?<![\w.])\/(?:[A-Za-z0-9._-]+\/)+[A-Za-z0-9._-]*/g;
const BEARER_TOKEN = /\bBearer\s+[A-Za-z0-9._~+/=-]+/gi;
const HTTP_URL = /\b(?:https?|wss?):\/\/[^\s"'<>]+/gi;
const CREDENTIAL_PREFIX = /\b(?:sk|pk|ghp|gho|ghs|glpat|xox[baprs])-[A-Za-z0-9._-]{6,}\b/g;
const OPAQUE_TOKEN = /\b[A-Za-z0-9_-]{40,}\b/g;
// A credential label followed by whitespace or an assignment ("Authorization:
// token abcdef", "api key=..."), and a UNC share. Prefer a false positive over
// retaining the value.
const CREDENTIAL_LABEL =
	/\b(?:token|api[-_ ]?key|access[-_ ]?token|bearer[-_ ]?token|secret|password)\s*[:=]?\s+[A-Za-z0-9._~+/=-]{8,}/gi;
const UNC_PATH = /\\\\[^\s"'<>]+/g;

/** Replace secrets, credentials, URLs and absolute paths with placeholders. */
export function redactHostLogText(text: string, secrets: readonly string[] = []): string {
	let out = text;
	for (const secret of secrets) {
		if (secret.length >= MIN_SECRET_LENGTH) out = out.split(secret).join("[redacted]");
	}
	out = out.replace(BEARER_TOKEN, "Bearer [redacted]");
	out = out.replace(HTTP_URL, "[url]");
	out = out.replace(CREDENTIAL_LABEL, "[redacted]");
	out = out.replace(CREDENTIAL_PREFIX, "[redacted]");
	out = out.replace(UNC_PATH, "[path]");
	out = out.replace(ABSOLUTE_PATH, "[path]");
	out = out.replace(OPAQUE_TOKEN, "[redacted]");
	return out;
}

/**
 * Per-conversion state. `cache` maps each source object to its converted
 * result plus the serialized cost of one materialization, so a shared
 * (non-cyclic) reference is converted once instead of being re-walked per
 * path. `active` tracks the current path only, so a true cycle still becomes
 * a placeholder. `remaining` is the approximate serialized-byte budget left
 * for materializations: a repeated reference is charged the full cost of its
 * subtree again because the emit sink's `JSON.stringify` expands it again.
 */
interface RedactedField {
	readonly value: unknown;
	readonly cost: number;
}

interface RedactionState {
	readonly cache: WeakMap<object, RedactedField>;
	readonly active: WeakSet<object>;
	remaining: number;
}

/** Redact one structured field value without changing its JSON-safe shape. */
export function redactHostLogField(value: unknown, secrets: readonly string[] = []): unknown {
	return redactFieldValue(value, secrets, 0, {
		cache: new WeakMap<object, RedactedField>(),
		active: new WeakSet<object>(),
		remaining: MAX_MATERIALIZED_BYTES,
	}).value;
}

/**
 * Substitute the truncation placeholder once the materialization budget is
 * spent. The placeholder is still charged so the accounting stays conservative.
 */
function placeholderField(state: RedactionState): RedactedField {
	state.remaining -= PLACEHOLDER_COST;
	return { value: FIELD_BOUND_PLACEHOLDER, cost: PLACEHOLDER_COST };
}

function redactFieldValue(
	value: unknown,
	secrets: readonly string[],
	depth: number,
	state: RedactionState,
): RedactedField {
	if (state.remaining <= 0) return placeholderField(state);
	if (typeof value === "string") {
		const text = redactHostLogText(value, secrets);
		const cost = jsonStringCost(text);
		if (cost > state.remaining) return placeholderField(state);
		state.remaining -= cost;
		return { value: text, cost };
	}
	if (typeof value === "number") {
		const cost = jsonNumberCost(value);
		state.remaining -= cost;
		return { value, cost };
	}
	if (typeof value === "boolean") {
		const cost = value ? 4 : 5;
		state.remaining -= cost;
		return { value, cost };
	}
	if (value === null) {
		state.remaining -= 4;
		return { value: null, cost: 4 };
	}
	if (typeof value !== "object") return { value: undefined, cost: 0 };
	if (depth >= MAX_FIELD_DEPTH) return placeholderField(state);
	if (state.active.has(value)) return placeholderField(state);
	// The cache keeps conversion work linear in the number of distinct source
	// objects. A completed result is never re-entered, so returning it cannot
	// recurse further. A repeated reference is charged its full materialized
	// cost, because `JSON.stringify` expands the shared subtree again here.
	const cached = state.cache.get(value);
	if (cached !== undefined) {
		if (cached.cost > state.remaining) return placeholderField(state);
		state.remaining -= cached.cost;
		return cached;
	}
	state.active.add(value);
	try {
		const result = Array.isArray(value)
			? redactFieldArray(value, secrets, depth, state)
			: redactFieldRecord(value as Record<string, unknown>, secrets, depth, state);
		state.cache.set(value, result);
		return result;
	} finally {
		state.active.delete(value);
	}
}

function redactFieldArray(
	value: readonly unknown[],
	secrets: readonly string[],
	depth: number,
	state: RedactionState,
): RedactedField {
	const array: unknown[] = [];
	let cost = 2;
	let childCost = 0;
	let truncated = false;
	for (let index = 0; index < value.length; index += 1) {
		if (array.length >= MAX_COLLECTION_ENTRIES) {
			truncated = true;
			break;
		}
		const child = redactFieldValue(value[index], secrets, depth + 1, state);
		array.push(child.value);
		cost += child.cost + 1;
		childCost += child.cost;
	}
	if (truncated) {
		array.push(FIELD_BOUND_PLACEHOLDER);
		cost += PLACEHOLDER_COST + 1;
	}
	state.remaining -= cost - childCost;
	return { value: array, cost };
}

function redactFieldRecord(
	source: Record<string, unknown>,
	secrets: readonly string[],
	depth: number,
	state: RedactionState,
): RedactedField {
	const record: Record<string, unknown> = {};
	let cost = 2;
	let childCost = 0;
	let count = 0;
	let truncated = false;
	for (const key in source) {
		if (!Object.hasOwn(source, key)) continue;
		if (count >= MAX_COLLECTION_ENTRIES) {
			truncated = true;
			break;
		}
		// Keys cross the same redaction as values so a credential label or
		// bearer token cannot survive as a record key.
		const redactedKey = redactHostLogText(key, secrets);
		const child = redactFieldValue(source[key], secrets, depth + 1, state);
		record[redactedKey] = child.value;
		cost += jsonStringCost(redactedKey) + 2 + child.cost;
		childCost += child.cost;
		count += 1;
	}
	if (truncated) {
		record[FIELD_BOUND_PLACEHOLDER] = true;
		cost += jsonStringCost(FIELD_BOUND_PLACEHOLDER) + 2 + 4;
	}
	state.remaining -= cost - childCost;
	return { value: record, cost };
}

/**
 * A failed operation is logged by error name plus a redacted message. Stack
 * traces and arbitrary error fields are never retained.
 */
export function describeHostLogError(
	error: unknown,
	secrets: readonly string[] = [],
): Record<string, unknown> {
	if (error instanceof Error) {
		return { errorName: error.name, errorMessage: redactHostLogText(error.message, secrets) };
	}
	return { errorName: "Error", errorMessage: redactHostLogText(String(error), secrets) };
}

export function createHostLogger(options: HostLoggerOptions = {}): HostLogger {
	const capacity = Math.max(1, Math.trunc(options.capacity ?? DEFAULT_CAPACITY));
	const secrets = options.secrets ?? [];
	const sink = options.sink;
	const retained: HostLogEntry[] = [];
	const record = (level: HostLogLevel, event: string, fields: Record<string, unknown>): void => {
		const entry: HostLogEntry = {
			level,
			event: redactHostLogText(event, secrets),
			fields: (redactHostLogField(fields, secrets) as Record<string, unknown>) ?? {},
		};
		retained.push(entry);
		while (retained.length > capacity) retained.shift();
		sink?.(entry);
	};
	return {
		info: (event, fields = {}) => record("info", event, fields),
		warn: (event, fields = {}) => record("warn", event, fields),
		error: (event, error, fields = {}) =>
			record("error", event, { ...fields, ...describeHostLogError(error, secrets) }),
		entries: () => retained.slice(),
	};
}

/**
 * Retain a bounded window of child stderr text. Lines are redacted before they
 * are stored, so even an exported snapshot can never contain raw secret text.
 */
export class BoundedStderrBuffer {
	readonly #lines: string[] = [];
	readonly #capacity: number;
	readonly #secrets: readonly string[];

	constructor(capacity = DEFAULT_CAPACITY, secrets: readonly string[] = []) {
		this.#capacity = Math.max(1, Math.trunc(capacity));
		this.#secrets = secrets;
	}

	append(chunk: string): void {
		for (const line of chunk.split(/\r?\n/)) {
			if (line === "") continue;
			this.#lines.push(this.#boundLine(redactHostLogText(line, this.#secrets)));
		}
		while (this.#lines.length > this.#capacity) this.#lines.shift();
	}

	#boundLine(line: string): string {
		if (line.length <= MAX_STDERR_LINE_LENGTH) return line;
		const keep = MAX_STDERR_LINE_LENGTH - STDERR_TRUNCATION_MARKER.length;
		return `${line.slice(0, keep)}${STDERR_TRUNCATION_MARKER}`;
	}

	lines(): readonly string[] {
		return this.#lines.slice();
	}

	clear(): void {
		this.#lines.length = 0;
	}
}
