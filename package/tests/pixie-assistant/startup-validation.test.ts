import { expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	parseAssistantPort,
	validateAssistantHost,
	validateAssistantSecret,
} from "../../../assistant/src/startup.ts";

test("assistant startup accepts only literal loopback hosts", () => {
	for (const value of [undefined, "localhost", "LOCALHOST", "127.0.0.1", "::1"])
		expect(validateAssistantHost(value)).toBe(
			value === undefined ? "127.0.0.1" : value.toLowerCase(),
		);
	for (const value of [
		"",
		" ",
		"0.0.0.0",
		"::",
		"127.0.0.2",
		"192.0.2.1",
		"example.test",
		"127.0.0.1:3284",
		"[::1]",
	])
		expect(() => validateAssistantHost(value)).toThrow("literal loopback host");
});

test("assistant startup rejects ambiguous ports and short secrets", () => {
	expect(parseAssistantPort(undefined)).toBe(3284);
	for (const value of ["1", "3284", "65535", "00001"])
		expect(parseAssistantPort(value)).toBeGreaterThan(0);
	for (const value of ["", " ", "0", "-1", "65536", "1.5", "0x10", "Infinity", "NaN"])
		expect(() => parseAssistantPort(value)).toThrow("integer from 1 to 65535");
	expect(validateAssistantSecret("sixteen-character")).toBe("sixteen-character");
	expect(() => validateAssistantSecret("short")).toThrow("at least 16 characters");
});

test("invalid production startup input fails before creating assistant state", async () => {
	for (const [option, value, message] of [
		["--host", "0.0.0.0", "literal loopback host"],
		["--port", "65536", "integer from 1 to 65535"],
	] as const) {
		const root = await mkdtemp(`${tmpdir()}/pixie-startup-validation-`);
		const agentDir = join(root, "agent");
		try {
			const child = Bun.spawn(
				[
					process.execPath,
					join(import.meta.dir, "../../../assistant/src/main.ts"),
					"--agent-dir",
					agentDir,
					option,
					value,
				],
				{
					cwd: join(import.meta.dir, "../../.."),
					env: { ...process.env, PIXIE_PI_SECRET_KEY: "startup-validation-secret" },
					stdout: "pipe",
					stderr: "pipe",
				},
			);
			const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text()]);
			expect(code).not.toBe(0);
			expect(stderr).toContain(message);
			expect(existsSync(join(agentDir, "pixie"))).toBe(false);
		} finally {
			await rm(root, { recursive: true, force: true });
		}
	}
});
