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

/** Redact one structured field value without changing its JSON-safe shape. */
export function redactHostLogField(value: unknown, secrets: readonly string[] = []): unknown {
	return redactFieldValue(value, secrets, 0, new WeakSet<object>());
}

function redactFieldValue(
	value: unknown,
	secrets: readonly string[],
	depth: number,
	seen: WeakSet<object>,
): unknown {
	if (typeof value === "string") return redactHostLogText(value, secrets);
	if (typeof value === "number" || typeof value === "boolean" || value === null) return value;
	if (typeof value !== "object") return undefined;
	if (depth >= MAX_FIELD_DEPTH) return FIELD_BOUND_PLACEHOLDER;
	if (seen.has(value)) return FIELD_BOUND_PLACEHOLDER;
	// `seen` tracks the current path only, so a shared (non-cyclic) reference is
	// still serialized at each position while a true cycle stops immediately.
	seen.add(value);
	try {
		if (Array.isArray(value)) {
			const result: unknown[] = [];
			let truncated = false;
			for (let index = 0; index < value.length; index += 1) {
				if (result.length >= MAX_COLLECTION_ENTRIES) {
					truncated = true;
					break;
				}
				result.push(redactFieldValue(value[index], secrets, depth + 1, seen));
			}
			if (truncated) result.push(FIELD_BOUND_PLACEHOLDER);
			return result;
		}
		const source = value as Record<string, unknown>;
		const result: Record<string, unknown> = {};
		let count = 0;
		for (const key in source) {
			if (!Object.hasOwn(source, key)) continue;
			if (count >= MAX_COLLECTION_ENTRIES) {
				result[FIELD_BOUND_PLACEHOLDER] = true;
				break;
			}
			result[key] = redactFieldValue(source[key], secrets, depth + 1, seen);
			count += 1;
		}
		return result;
	} finally {
		seen.delete(value);
	}
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
