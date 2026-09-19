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

/** Default retained-entry capacity for the logger and stderr buffer. */
export const DEFAULT_HOST_LOG_CAPACITY = 200;
/**
 * Upper bound for a caller-supplied capacity. A finite but enormous value would
 * otherwise let a single logger retain an unbounded window.
 */
export const MAX_HOST_LOG_CAPACITY = 10_000;
const MIN_SECRET_LENGTH = 8;

/**
 * Normalize a caller-supplied capacity to a finite positive integer. A
 * non-finite, missing or negative value falls back to the default (or 1 for a
 * zero/negative value), and a huge finite value is capped, so `NaN` and
 * `Infinity` can never disable the retention bound.
 */
export function normalizeHostLogCapacity(value: number | undefined): number {
	if (typeof value !== "number" || !Number.isFinite(value)) return DEFAULT_HOST_LOG_CAPACITY;
	const truncated = Math.trunc(value);
	if (truncated < 1) return 1;
	if (truncated > MAX_HOST_LOG_CAPACITY) return MAX_HOST_LOG_CAPACITY;
	return truncated;
}

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

/**
 * Maximum input length that reaches redaction patterns. Every pattern is a
 * global regexp, and an unanchored prefix such as `[A-Za-z0-9_-]{40,256}\/`
 * makes the engine retry at each offset, so a long single-token string (with
 * no `/`) costs super-linear time. The redactor runs synchronously on the host
 * event loop and is reachable from an oversized client frame or native error
 * text, so the input is cut before any pattern runs. Pattern work is therefore
 * bounded for any input size; placeholder substitution can still grow the
 * result, but only by a small constant factor of this budget.
 */
export const MAX_REDACTED_TEXT_LENGTH = 16 * 1024;
const TEXT_TRUNCATION_MARKER = FIELD_BOUND_PLACEHOLDER;

// Absolute POSIX paths (at least two segments) become a placeholder. A bare
// "/" or a short relative fragment is left alone so ordinary prose survives.
const ABSOLUTE_PATH = /(?<![\w.])\/(?:[A-Za-z0-9._-]+\/)+[A-Za-z0-9._-]*/g;
// An absolute path glued directly to a long opaque token, e.g.
// `${"A".repeat(60)}/home/operator/secret.json`: the delimiter lookbehind in
// ABSOLUTE_PATH excludes a preceding word character, so the opaque run is
// consumed together with the path. Bounded at the same 40-character minimum
// used by OPAQUE_TOKEN; replacing the whole match also hides the token. The
// upper repetition bound keeps the greedy prefix from scanning the whole input
// at every offset when the required `/` never arrives.
const GLUED_ABSOLUTE_PATH = /[A-Za-z0-9_-]{40,256}\/(?:[A-Za-z0-9._-]+\/)+[A-Za-z0-9._-]*/g;
// A quoted absolute path is delimited, so a space inside it is part of the
// value rather than a word boundary. The closing quote is the same character as
// the opening one, so this cannot run past the enclosing string.
const QUOTED_ABSOLUTE_PATH = /(["'])\/(?:[^"'\\\r\n]|\\.)*\1/g;
// An unquoted absolute path whose directory segments contain spaces. Spaces are
// allowed only between slash-terminated segments; the final segment may not
// contain one, so a trailing ordinary word ("... and then stopped") is not
// consumed. The inner repetition bound keeps backtracking fixed for any input.
const SPACED_ABSOLUTE_PATH =
	/(?<![\w.])\/(?:[A-Za-z0-9._-]+(?:[ \t]+[A-Za-z0-9._-]+){0,8}\/)+[A-Za-z0-9._-]+/g;
const BEARER_TOKEN = /\bBearer\s+[A-Za-z0-9._~+/=-]+/gi;
const HTTP_URL = /\b(?:https?|wss?):\/\/[^\s"'<>]+/gi;
const CREDENTIAL_PREFIX = /\b(?:sk|pk|ghp|gho|ghs|glpat|xox[baprs])-[A-Za-z0-9._-]{6,}\b/g;
// AWS-style access key IDs have no delimiter, so CREDENTIAL_PREFIX cannot see
// them. The prefix is a fixed uppercase tag plus sixteen characters.
const AWS_ACCESS_KEY = /\b(?:AKIA|ASIA)[A-Z0-9]{16}\b/g;
const OPAQUE_TOKEN = /\b[A-Za-z0-9_-]{40,}\b/g;
// A credential label followed by whitespace or an assignment ("Authorization:
// token abcdef", "token=abcdef", "api_key: abcdef") and a quoted or bare
// value, and a UNC share. The separator is optional whitespace, so both
// `token=value` and `token value` are covered. Prefer a false positive over
// retaining the value.
const CREDENTIAL_LABEL =
	/\b(?:token|api[-_ ]?key|access[-_ ]?token|bearer[-_ ]?token|secret|password)\s*(?:[:=]\s*|\s+)(?:"[^"]*"|'[^']*'|[A-Za-z0-9._~+/=-]{8,})/gi;
const UNC_PATH = /\\\\[^\s"'<>]+/g;

/** Replace configured secrets with the placeholder. Split/join is linear. */
function redactConfiguredSecrets(text: string, secrets: readonly string[]): string {
	let out = text;
	for (const secret of secrets) {
		if (secret.length >= MIN_SECRET_LENGTH) out = out.split(secret).join("[redacted]");
	}
	return out;
}

/**
 * Cut one text value to the redaction budget and remove configured secrets
 * before any pattern runs. The retained prefix plus one budget of lookahead is
 * scanned for secrets, so a secret that straddles the cut is replaced instead
 * of being kept as a partial prefix; the lookahead is capped at one budget, so
 * the work stays fixed for any input size. The truncation marker replaces the
 * tail so the bounded result still shows that text was dropped.
 */
function boundRedactionInput(text: string, secrets: readonly string[]): string {
	if (text.length <= MAX_REDACTED_TEXT_LENGTH) return redactConfiguredSecrets(text, secrets);
	const keep = MAX_REDACTED_TEXT_LENGTH - TEXT_TRUNCATION_MARKER.length;
	const scanLength = Math.min(text.length, MAX_REDACTED_TEXT_LENGTH * 2);
	const head = redactConfiguredSecrets(text.slice(0, scanLength), secrets);
	return `${head.slice(0, keep)}${TEXT_TRUNCATION_MARKER}`;
}

/** Replace secrets, credentials, URLs and absolute paths with placeholders. */
export function redactHostLogText(text: string, secrets: readonly string[] = []): string {
	let out = boundRedactionInput(text, secrets);
	out = out.replace(BEARER_TOKEN, "Bearer [redacted]");
	out = out.replace(HTTP_URL, "[url]");
	out = out.replace(CREDENTIAL_LABEL, "[redacted]");
	out = out.replace(CREDENTIAL_PREFIX, "[redacted]");
	out = out.replace(AWS_ACCESS_KEY, "[redacted]");
	out = out.replace(UNC_PATH, "[path]");
	out = out.replace(GLUED_ABSOLUTE_PATH, "[redacted]");
	out = out.replace(QUOTED_ABSOLUTE_PATH, "$1[path]$1");
	out = out.replace(SPACED_ABSOLUTE_PATH, "[path]");
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
	const record: Record<string, unknown> = Object.create(null) as Record<string, unknown>;
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
	const capacity = normalizeHostLogCapacity(options.capacity);
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
 * Retain a bounded window of child stderr text. Complete lines are redacted
 * before they are stored, and an incomplete line is buffered across appends so a
 * secret or path split between two writes is redacted as one value. A line that
 * grows past the per-line bound is discarded rather than retained as fragments,
 * which keeps the buffer bounded and prevents a partial secret from surviving.
 */
export class BoundedStderrBuffer {
	readonly #lines: string[] = [];
	readonly #capacity: number;
	readonly #secrets: readonly string[];
	#partial = "";
	#discarding = false;

	constructor(capacity = DEFAULT_HOST_LOG_CAPACITY, secrets: readonly string[] = []) {
		this.#capacity = normalizeHostLogCapacity(capacity);
		this.#secrets = secrets;
	}

	append(chunk: string): void {
		let start = 0;
		while (start < chunk.length) {
			const newline = chunk.indexOf("\n", start);
			if (newline < 0) {
				this.#appendPartial(chunk.slice(start));
				return;
			}
			this.#appendPartial(chunk.slice(start, newline));
			this.#flushPartial();
			start = newline + 1;
		}
	}

	#appendPartial(fragment: string): void {
		if (this.#discarding) return;
		const text = fragment.endsWith("\r") ? fragment.slice(0, -1) : fragment;
		if (this.#partial.length + text.length > MAX_STDERR_LINE_LENGTH) {
			// Drop the whole line: a retained fragment could contain the prefix
			// of a secret that was split across appends.
			this.#partial = "";
			this.#discarding = true;
			return;
		}
		this.#partial += text;
	}

	#flushPartial(): void {
		if (this.#discarding) {
			this.#discarding = false;
			this.#partial = "";
			return;
		}
		const line = this.#partial;
		this.#partial = "";
		if (line === "") return;
		this.#lines.push(this.#boundLine(redactHostLogText(line, this.#secrets)));
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
		this.#partial = "";
		this.#discarding = false;
	}
}
