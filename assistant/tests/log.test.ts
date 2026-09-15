import { describe, expect, test } from "bun:test";
import {
	BoundedStderrBuffer,
	createHostLogger,
	describeHostLogError,
	FIELD_BOUND_PLACEHOLDER,
	MAX_COLLECTION_ENTRIES,
	MAX_FIELD_DEPTH,
	MAX_MATERIALIZED_BYTES,
	MAX_STDERR_LINE_LENGTH,
	redactHostLogField,
	redactHostLogText,
} from "../src/log.ts";

/** Count distinct objects reachable from `root`, aborting once `limit` is passed. */
function countConvertedNodes(root: unknown, limit: number): number {
	const seen = new WeakSet<object>();
	const pending: unknown[] = [root];
	let count = 0;
	while (pending.length > 0) {
		const current = pending.pop();
		if (current === null || typeof current !== "object") continue;
		if (seen.has(current)) continue;
		seen.add(current);
		count += 1;
		if (count > limit) return count;
		for (const key of Object.keys(current)) {
			pending.push((current as Record<string, unknown>)[key]);
		}
	}
	return count;
}

describe("secret-safe host logging", () => {
	test("redacts bearer tokens, credential values, URLs and absolute paths", () => {
		const text =
			"request failed Authorization: Bearer abcdef0123456789 at https://pi.example.test/pi " +
			"using sk-live-abcdef123456 and /home/operator/.pi/agents/secret.md";
		const redacted = redactHostLogText(text, ["another-secret-value"]);
		expect(redacted).not.toContain("abcdef0123456789");
		expect(redacted).not.toContain("https://pi.example.test");
		expect(redacted).not.toContain("sk-live-abcdef123456");
		expect(redacted).not.toContain("/home/operator");
		expect(redacted).toContain("Bearer [redacted]");
		expect(redacted).toContain("[url]");
		expect(redacted).toContain("[path]");
	});

	test("redacts an absolute path glued to a long opaque token", () => {
		const opaque = "A".repeat(60);
		const gluedPath = `${opaque}/home/operator/.pi/agent/secret.json`;

		const value = redactHostLogText(`cannot read ${gluedPath}`);
		expect(value).not.toContain("/home/operator");
		expect(value).not.toContain(opaque);
		expect(value).toContain("[redacted]");

		// The same glued form must be redacted when it is used as a record key.
		const keyed = redactHostLogField({ [gluedPath]: "benign" }) as Record<string, unknown>;
		const keys = Object.keys(keyed).join(" ");
		expect(keys).not.toContain("/home/operator");
		expect(keys).not.toContain(opaque);
		expect(JSON.stringify(keyed)).not.toContain("/home/operator");
		expect(Object.values(keyed)).toEqual(["benign"]);
	});

	test("redacts an explicit secret wherever it appears", () => {
		const secret = "s".repeat(48);
		expect(redactHostLogText(`token=${secret}`, [secret])).not.toContain(secret);
		expect(redactHostLogText(`token=${secret}`, [secret])).toContain("[redacted]");
	});

	test("redacts nested structured fields", () => {
		const value = redactHostLogField(
			{ endpoint: "ws://127.0.0.1:3284/pi", nested: ["/var/lib/pi-agent/auth.json"] },
			[],
		);
		expect(JSON.stringify(value)).not.toContain("127.0.0.1");
		expect(JSON.stringify(value)).not.toContain("/var/lib");
	});

	test("redacts record keys as well as record values", () => {
		const secret = "k".repeat(48);
		const value = redactHostLogField(
			{
				"Bearer abcdef0123456789": "benign",
				[`prefix-${secret}`]: "benign",
				plain: "benign",
			},
			[secret],
		) as Record<string, unknown>;
		const keys = Object.keys(value).join(" ");
		expect(keys).not.toContain("abcdef0123456789");
		expect(keys).not.toContain(secret);
		expect(keys).toContain("Bearer [redacted]");
		expect(keys).toContain("plain");
		expect(JSON.stringify(value)).not.toContain("abcdef0123456789");
		expect(JSON.stringify(value)).not.toContain(secret);
	});

	test("bounds a self-referential field graph instead of recursing without limit", () => {
		const value: Record<string, unknown> = { name: "root" };
		value.self = value;
		const redacted = redactHostLogField(value) as Record<string, unknown>;
		expect(redacted.name).toBe("root");
		expect(redacted.self).toBe(FIELD_BOUND_PLACEHOLDER);
		expect(JSON.stringify(redacted)).toContain(FIELD_BOUND_PLACEHOLDER);

		const array: unknown[] = [];
		array.push(array);
		expect(redactHostLogField(array)).toEqual([FIELD_BOUND_PLACEHOLDER]);
	});

	test("bounds deeply nested field objects at the depth limit", () => {
		let nested: Record<string, unknown> = { leaf: "/var/lib/pi-agent/secret.json" };
		for (let level = 0; level < MAX_FIELD_DEPTH * 3; level += 1) nested = { nested };
		const redacted = redactHostLogField(nested) as Record<string, unknown>;
		let cursor: unknown = redacted;
		let depth = 0;
		while (
			cursor &&
			typeof cursor === "object" &&
			"nested" in (cursor as Record<string, unknown>)
		) {
			cursor = (cursor as Record<string, unknown>).nested;
			depth += 1;
		}
		expect(depth).toBeLessThanOrEqual(MAX_FIELD_DEPTH);
		expect(cursor).toBe(FIELD_BOUND_PLACEHOLDER);
		expect(JSON.stringify(redacted)).not.toContain("/var/lib");
	});

	test("converts a shared DAG once instead of re-walking it per path", () => {
		// Only DEPTH + 1 objects exist, but each level points all of its children
		// at one shared next level, so a path-only visited set expands 8^7 paths.
		const branching = 8;
		const depth = 7;
		let node: Record<string, unknown> = { leaf: "/var/lib/pi-agent/secret.json" };
		for (let level = 0; level < depth; level += 1) {
			const parent: Record<string, unknown> = {};
			for (let child = 0; child < branching; child += 1) parent[`child${child}`] = node;
			node = parent;
		}

		const started = performance.now();
		const redacted = redactHostLogField(node) as Record<string, unknown>;
		const elapsed = performance.now() - started;
		expect(elapsed).toBeLessThan(1000);

		// Bounded output: converted nodes stay proportional to the distinct source
		// objects rather than to the number of root-to-leaf paths.
		expect(countConvertedNodes(redacted, 1000)).toBeLessThanOrEqual(depth + 1);

		let cursor: unknown = redacted;
		for (let level = 0; level < depth; level += 1) {
			cursor = (cursor as Record<string, unknown>).child0;
		}
		expect(cursor).toEqual({ leaf: "[path]" });
	});

	test("bounds the serialized expansion of a shared DAG within the per-level caps", () => {
		// The same branch-8 depth-7 DAG as above. Memoization converts it to a
		// small shared graph, but the emit sink's JSON.stringify re-expands the
		// shared subtree once per path, so without a materialization budget the
		// serialized form is millions of nodes (~tens of MB).
		const branching = 8;
		const depth = 7;
		let node: Record<string, unknown> = { leaf: "/var/lib/pi-agent/secret.json" };
		for (let level = 0; level < depth; level += 1) {
			const parent: Record<string, unknown> = {};
			for (let child = 0; child < branching; child += 1) parent[`child${child}`] = node;
			node = parent;
		}

		const redacted = redactHostLogField(node);
		const serialized = JSON.stringify(redacted);
		expect(serialized.length).toBeLessThanOrEqual(MAX_MATERIALIZED_BYTES + 64 * 1024);
		expect(serialized).not.toContain("/var/lib");

		// The first path is still fully materialized: truncation starts only once
		// the budget is spent, so redaction still applies before truncation.
		let cursor: unknown = redacted;
		for (let level = 0; level < depth; level += 1) {
			cursor = (cursor as Record<string, unknown>).child0;
		}
		expect(cursor).toEqual({ leaf: "[path]" });
	});

	test("caps per-collection entries in a redacted field", () => {
		const wide = Array.from(
			{ length: MAX_COLLECTION_ENTRIES + 5 },
			(_, index) => `/var/lib/${index}`,
		);
		const redacted = redactHostLogField(wide) as unknown[];
		expect(redacted.length).toBe(MAX_COLLECTION_ENTRIES + 1);
		expect(redacted.at(-1)).toBe(FIELD_BOUND_PLACEHOLDER);
		expect(JSON.stringify(redacted)).not.toContain("/var/lib");
	});

	test("describes errors without retaining stacks", () => {
		const error = new Error("cannot read /home/operator/secret.json");
		error.stack = "Error: cannot read /home/operator/secret.json\n    at /home/operator/app.ts:1:1";
		const described = describeHostLogError(error);
		expect(described.errorName).toBe("Error");
		expect(String(described.errorMessage)).not.toContain("/home/operator");
		expect(JSON.stringify(described)).not.toContain("at /");
	});

	test("retains only a bounded window of redacted entries", () => {
		const logger = createHostLogger({ capacity: 3, secrets: ["s".repeat(32)] });
		for (let index = 0; index < 6; index += 1) {
			logger.info(`event-${index}`, { path: `/var/lib/pi-agent/${index}.json` });
		}
		const entries = logger.entries();
		expect(entries.length).toBe(3);
		expect(entries.map((entry) => entry.event)).toEqual(["event-3", "event-4", "event-5"]);
		for (const entry of entries) expect(JSON.stringify(entry)).not.toContain("/var/lib");
	});

	test("bounds and redacts retained child stderr", () => {
		const buffer = new BoundedStderrBuffer(2, ["s".repeat(32)]);
		buffer.append("line-1 /home/operator/a\nline-2\nline-3");
		expect(buffer.lines()).toEqual(["line-2", "line-3"]);
		buffer.append(`token=${"s".repeat(32)}`);
		expect(buffer.lines().at(-1)).toBe("token=[redacted]");
		buffer.clear();
		expect(buffer.lines()).toEqual([]);
	});

	test("bounds the length of one oversized stderr line", () => {
		const buffer = new BoundedStderrBuffer(1);
		buffer.append("warning ".repeat(MAX_STDERR_LINE_LENGTH * 4));
		const [line] = buffer.lines();
		expect(line.length).toBeLessThanOrEqual(MAX_STDERR_LINE_LENGTH);
		expect(line.endsWith(FIELD_BOUND_PLACEHOLDER)).toBe(true);
	});

	test("redacts a secret inside an oversized stderr line before truncating", () => {
		const secret = "s".repeat(32);
		const buffer = new BoundedStderrBuffer(1, [secret]);
		buffer.append(`token=${secret} ${"warning ".repeat(MAX_STDERR_LINE_LENGTH * 4)}`);
		const [line] = buffer.lines();
		expect(line.length).toBeLessThanOrEqual(MAX_STDERR_LINE_LENGTH);
		expect(line).not.toContain(secret);
		expect(line).toContain("[redacted]");
	});

	test("redacts labelled credentials, bracketed paths and UNC shares", () => {
		const text =
			"Authorization: token abcdef0123456789 at [/home/operator/.pi] and \\\\server\\share\\secret";
		const redacted = redactHostLogText(text);
		expect(redacted).not.toContain("abcdef0123456789");
		expect(redacted).not.toContain("/home/operator");
		expect(redacted).not.toContain("server\\share");
		expect(redacted).toContain("[redacted]");
		expect(redacted).toContain("[path]");
	});
});
