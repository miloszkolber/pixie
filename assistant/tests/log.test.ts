import { describe, expect, test } from "bun:test";
import {
	BoundedStderrBuffer,
	createHostLogger,
	describeHostLogError,
	redactHostLogField,
	redactHostLogText,
} from "../src/log.ts";

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
