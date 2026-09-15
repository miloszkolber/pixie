import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
	deriveDoctorScenarioConfig,
	fatalServeMessage,
	normalizeAssistantHostValue,
	pairedAssistantPortNote,
	piVersionError,
	type ResolvedConfig,
	rejectPublicPiPackageEnvironment,
	usage,
	validateAssistantConfig,
} from "../src/serve.ts";

describe("assistant serve port/host parity", () => {
	test("accepts localhost as an alias for 127.0.0.1", () => {
		expect(normalizeAssistantHostValue("localhost")).toBe("127.0.0.1");
		expect(normalizeAssistantHostValue("LOCALHOST")).toBe("127.0.0.1");
		expect(normalizeAssistantHostValue("127.0.0.1")).toBe("127.0.0.1");
		expect(normalizeAssistantHostValue("::1")).toBe("::1");
	});

	test("directs operators to the config port and controller port", () => {
		const help = usage();
		expect(help).toStartWith("usage: pixie_assistant ");
		expect(help).toContain("config port");
		expect(help).toContain("PIXIE_PI_PORT");
		expect(help).not.toContain("PIXIE_ASSISTANT_PORT");
		const note = pairedAssistantPortNote(3284);
		expect(note).toContain("config port");
		expect(note).toContain("PIXIE_PI_PORT");
	});

	test("documents config port and PIXIE_PI_PORT matching in deployment docs", () => {
		const deployment = readFileSync(join(import.meta.dir, "../../docs/deployment.md"), "utf8");
		expect(deployment).toContain("config `port`");
		expect(deployment).toContain("PIXIE_PI_PORT");
		expect(deployment).toContain("must match");
		// The retired variable may be named, but every mention must be in a
		// rejection context so the doc never presents it as a usable setting.
		const retiredMentions = deployment
			.split("\n")
			.filter((line) => line.includes("PIXIE_ASSISTANT_PORT"));
		expect(retiredMentions.length).toBeGreaterThan(0);
		for (const line of retiredMentions) {
			expect(line).toMatch(/reject|retired|ignored|not supported|unsupported/i);
			// A rejection sentence must never also assign or recommend the
			// variable, e.g. "PIXIE_ASSISTANT_PORT=3284 still works" or
			// "set PIXIE_ASSISTANT_PORT to ...".
			expect(line).not.toMatch(/PIXIE_ASSISTANT_PORT\s*=/);
			expect(line).not.toMatch(/\bset(?:ting)?\b[^.\n]*\bPIXIE_ASSISTANT_PORT\b/i);
			expect(line).not.toMatch(/\bPIXIE_ASSISTANT_PORT\b[^.\n]*\bset(?:ting)?\b[^.\n]*\bto\b/i);
		}
	});

	test("rejects the deprecated assistant port environment in favor of config port", () => {
		const source = join(import.meta.dir, "../src/serve.ts");
		const result = Bun.spawnSync({
			cmd: [process.execPath, source, "serve", "--config", "/tmp/assistant.json"],
			env: { ...process.env, PIXIE_ASSISTANT_PORT: "3285" },
			stdout: "pipe",
			stderr: "pipe",
		});
		const stderr = new TextDecoder().decode(result.stderr);
		expect(result.exitCode).not.toBe(0);
		expect(stderr).toContain("PIXIE_ASSISTANT_PORT is not supported");
		expect(stderr).toContain("config port");
		expect(stderr).toContain("PIXIE_PI_PORT");
		expect(stderr).not.toContain("3285");
	});

	test("redacts a top-level serve error before it reaches stderr", () => {
		const error = new Error(
			"could not start https://pi.example.test/pi with Bearer abcdef0123456789 at /home/operator/.pi/agent",
		);
		const message = fatalServeMessage(error);
		expect(message).not.toContain("abcdef0123456789");
		expect(message).not.toContain("https://pi.example.test");
		expect(message).not.toContain("/home/operator");
		expect(message).toContain("[redacted]");
		expect(message).toContain("[url]");
		expect(message).toContain("[path]");
		// The executable's uncaught path must use the redacting formatter.
		const source = readFileSync(join(import.meta.dir, "../src/serve.ts"), "utf8");
		expect(source).toContain("fatalServeMessage(error)");
		expect(source).not.toMatch(/pixie_assistant: \$\{errorMessage\(error\)\}/);
	});
});

describe("assistant doctor scenario configuration", () => {
	test("derives an isolated ephemeral host configuration", () => {
		const configured: ResolvedConfig = {
			host: "::1",
			port: 49152,
			secret: "configured-".repeat(4),
			agentDir: "/operator/pi-agent",
			piPackage: "/operator/pi-package",
			allowSelfRestart: true,
		};
		const secret = "d".repeat(64);
		const scenario = deriveDoctorScenarioConfig(
			configured,
			"/tmp/pixie_assistant-doctor-test",
			secret,
		);

		expect(scenario).toEqual({
			host: "127.0.0.1",
			port: 0,
			secret,
			agentDir: "/tmp/pixie_assistant-doctor-test",
			piPackage: configured.piPackage,
			allowSelfRestart: false,
		});
		expect(scenario.agentDir).not.toBe(configured.agentDir);
		expect(scenario.port).not.toBe(configured.port);
		expect(scenario.secret).not.toBe(configured.secret);
	});
});

describe("assistant v2 archive configuration", () => {
	const valid = {
		schemaVersion: 2,
		host: "127.0.0.1",
		port: 3284,
		agentDir: "/var/lib/pi-agent",
		allowSelfRestart: false,
	};

	test("accepts the v2 host/port/agentDir/restart contract", () => {
		expect(() => validateAssistantConfig(valid)).not.toThrow();
	});

	test("rejects old package selection and incomplete or wrong-version config", () => {
		expect(() => validateAssistantConfig({ ...valid, piPackage: "/outside/pi" })).toThrow(
			"piPackage is not accepted",
		);
		expect(() => validateAssistantConfig({ ...valid, schemaVersion: 1 })).toThrow(
			"schemaVersion must be 2",
		);
		expect(() => {
			const withoutRestart = { ...valid } as Record<string, unknown>;
			delete withoutRestart.allowSelfRestart;
			validateAssistantConfig(withoutRestart);
		}).toThrow("allowSelfRestart must be a boolean");
		expect(() => {
			const withoutPort = { ...valid } as Record<string, unknown>;
			delete withoutPort.port;
			validateAssistantConfig(withoutPort);
		}).toThrow("config port");
		expect(() => validateAssistantConfig({ ...valid, agentDir: "relative" })).toThrow(
			"agentDir must be an absolute path",
		);
		expect(() => validateAssistantConfig({ ...valid, port: 0 })).toThrow("config port");
		expect(() => rejectPublicPiPackageEnvironment({ PIXIE_PI_PACKAGE: "" })).toThrow(
			"PIXIE_PI_PACKAGE is not accepted",
		);
	});

	test("rejects --pi-package before it can select a public package", () => {
		const source = join(import.meta.dir, "../src/serve.ts");
		const result = Bun.spawnSync({
			cmd: [process.execPath, source, "serve", "--pi-package", "/outside/pi"],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).not.toBe(0);
		expect(new TextDecoder().decode(result.stderr)).toContain("--pi-package is not accepted");
	});

	test("requires the archive-private package to be exactly Pi 0.85.1", () => {
		const verified = {
			packageName: "@earendil-works/pi-coding-agent" as const,
			packageVersion: "0.85.1",
			packageDir: "/archive/runtime/node_modules/@earendil-works/pi-coding-agent",
			entryPath: "/archive/runtime/node_modules/@earendil-works/pi-coding-agent/dist/index.js",
			manifestDigest: "a".repeat(64),
			entryDigest: "b".repeat(64),
		};
		expect(piVersionError(verified)).toBeUndefined();
		expect(piVersionError({ ...verified, packageVersion: "0.85.2" })).toContain("@0.85.1");
	});
});
