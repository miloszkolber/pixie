/**
 * Sidecar frame, verification and FC17 mapping tests.
 *
 * The selected installation is a stub module written into a temporary
 * directory. This keeps the test independent of an installed Pi while still
 * exercising the real dynamic import, package verification and response
 * shaping paths.
 */

import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "bun:test";
import {
	BRIDGE_MAX_FRAME_BYTES,
	createBridge,
	createBridgeServer,
	encodeResponse,
	loadPublicApi,
	parseRequest,
	resolveAgentDir,
	resolveInstallation,
	resolvePackagePath,
} from "./serve.ts";

const STUB_PACKAGE = "@earendil-works/pi-coding-agent";

async function stubInstallation(version = "0.85.1"): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "pixie-bridge-"));
	await writeFile(
		join(dir, "package.json"),
		JSON.stringify({
			name: STUB_PACKAGE,
			version,
			type: "module",
			exports: { ".": { import: "./index.js" } },
		}),
	);
	await writeFile(
		join(dir, "index.js"),
		`export class ModelRuntime {
	static async create() {
		return {
			async getAvailable() { return [{ provider: "alpha" }]; },
			getProviders() {
				return [
					{ id: "alpha", name: "Alpha", auth: { apiKey: { login: true }, oauth: false } },
					{ id: "beta", name: "Beta", auth: { apiKey: null, oauth: true } },
				];
			},
			async checkAuth(id) { return id === "alpha" ? { type: "api_key" } : undefined; },
			getModels(id) {
				if (id !== "alpha") return [];
				return [{ id: id + "-1", name: "Model " + id, contextWindow: 1000, maxTokens: 100, reasoning: true, input: ["text", "image"] }];
			},
			getModel(provider, model) {
				return { id: model, contextWindow: 2000, maxTokens: 200, reasoning: false, cost: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4 } };
			},
			async refresh() { return { errors: new Map([["alpha", "boom"]]) }; },
		};
	}
}
`,
	);
	return dir;
}

async function stubBridge(version = "0.85.1") {
	const dir = await stubInstallation(version);
	const installation = await resolveInstallation(dir);
	const module = await loadPublicApi(installation);
	const agentDir = await mkdtemp(join(tmpdir(), "pixie-agent-"));
	return { dir, bridge: createBridge({ installation, module, agentDir }) };
}

describe("bridge frame parsing", () => {
	test("parses a bounded request frame", () => {
		expect(parseRequest('{"id":7,"method":"bridge.hello","params":{}}')).toEqual({
			id: 7,
			method: "bridge.hello",
			params: {},
		});
		expect(parseRequest('{"id":"login-1","method":"pi.providers.list"}')).toEqual({
			id: "login-1",
			method: "pi.providers.list",
			params: {},
		});
	});

	test("rejects malformed frames", () => {
		expect(() => parseRequest("")).toThrow();
		expect(() => parseRequest("not json")).toThrow();
		expect(() => parseRequest("[]")).toThrow();
		expect(() => parseRequest('{"id":0,"method":"bridge.hello"}')).toThrow();
		expect(() => parseRequest('{"id":1,"method":""}')).toThrow();
		expect(() => parseRequest('{"id":1,"method":"bridge.hello","params":[]}')).toThrow();
	});

	test("bounds encoded responses", () => {
		expect(encodeResponse({ id: 1, ok: true, result: {} })).toBe('{"id":1,"ok":true,"result":{}}\n');
		expect(() =>
			encodeResponse({ id: 1, ok: true, result: "x".repeat(BRIDGE_MAX_FRAME_BYTES) }),
		).toThrow();
	});
});

describe("selected installation verification", () => {
	test("accepts the expected package and rejects anything else", async () => {
		const dir = await stubInstallation();
		const installation = await resolveInstallation(dir);
		expect(installation.packageName).toBe(STUB_PACKAGE);
		expect(installation.packageVersion).toBe("0.85.1");
		expect(installation.entryPath.startsWith(dir)).toBe(true);

		const wrong = await mkdtemp(join(tmpdir(), "pixie-bridge-wrong-"));
		await writeFile(
			join(wrong, "package.json"),
			JSON.stringify({ name: "some-other-package", version: "1.0.0", main: "./index.js" }),
		);
		await writeFile(join(wrong, "index.js"), "export {};\n");
		await expect(resolveInstallation(wrong)).rejects.toThrow();
		await expect(resolveInstallation("relative/path")).rejects.toThrow();
	});
});

describe("bridge hello and FC17 mapping", () => {
	test("hello reports the verified identity", async () => {
		const { bridge } = await stubBridge();
		const hello = (await bridge.dispatch("bridge.hello", {})) as Record<string, unknown>;
		expect(hello.packageName).toBe(STUB_PACKAGE);
		expect(hello.packageVersion).toBe("0.85.1");
		expect(hello.moduleOrigin).toBe(bridge.installation.entryPath);
		expect(hello.publicSymbols).toEqual(["ModelRuntime"]);
	});

	test("pi.providers.list mirrors the legacy inventory shape", async () => {
		const { bridge } = await stubBridge();
		const result = (await bridge.dispatch("pi.providers.list", {})) as {
			entries: Array<Record<string, any>>;
		};
		expect(result.entries).toHaveLength(2);
		const alpha = result.entries.find((entry) => entry.providerId === "alpha")!;
		expect(alpha).toMatchObject({
			providerName: "Alpha",
			configured: true,
			available: true,
			canApiKey: true,
			canOAuth: false,
			readinessCheck: true,
		});
		expect(alpha.configKeys).toEqual([{ name: "api_key", secret: true, required: true, primary: true }]);
		expect(alpha.models[0]).toEqual({
			id: "alpha-1",
			name: "Model alpha",
			contextLimit: 1000,
			maxOutputTokens: 100,
			reasoning: true,
			modalities: ["text", "image"],
		});
		const beta = result.entries.find((entry) => entry.providerId === "beta")!;
		expect(beta.configured).toBe(false);
		expect(beta.available).toBe(false);
		expect(beta.configKeys).toEqual([{ name: "oauth", oauthFlow: true }]);
		expect(beta.models).toEqual([]);
	});

	test("pi.providers.readiness.check reports configured readiness", async () => {
		const { bridge } = await stubBridge();
		expect(await bridge.dispatch("pi.providers.readiness.check", { providerId: "alpha" })).toEqual({
			providerId: "alpha",
			ready: true,
			hasIssue: false,
			verified: false,
		});
		expect(await bridge.dispatch("pi.providers.readiness.check", { providerId: "beta" })).toEqual({
			providerId: "beta",
			ready: false,
			hasIssue: true,
			verified: false,
		});
	});

	test("pi.providers.canonical-model-info returns canonical metadata", async () => {
		const { bridge } = await stubBridge();
		expect(
			await bridge.dispatch("pi.providers.canonical-model-info", { provider: "alpha", model: "alpha-1" }),
		).toEqual({
			modelInfo: {
				provider: "alpha",
				model: "alpha-1",
				contextLimit: 2000,
				maxOutputTokens: 200,
				reasoning: false,
				currency: "USD",
				inputTokenCost: 1,
				outputTokenCost: 2,
				cacheReadTokenCost: 3,
				cacheWriteTokenCost: 4,
			},
		});
	});

	test("pi.providers.inventory.refresh reports upstream errors", async () => {
		const { bridge } = await stubBridge();
		expect(await bridge.dispatch("pi.providers.inventory.refresh", {})).toEqual({
			started: [],
			skipped: [],
			errors: ["alpha"],
		});
	});

	test("unknown methods fail closed", async () => {
		const { bridge } = await stubBridge();
		await expect(bridge.dispatch("provider.loginStart", {})).rejects.toThrow(
			"Unsupported bridge method",
		);
	});

	test("server frames success and method errors", async () => {
		const { bridge } = await stubBridge();
		const server = createBridgeServer(bridge);
		expect(await server.handle('{"id":1,"method":"bridge.hello","params":{}}')).toMatchObject({
			id: 1,
			ok: true,
		});
		expect(await server.handle('{"id":2,"method":"provider.loginStart","params":{}}')).toEqual({
			id: 2,
			ok: false,
			error: "Unsupported bridge method: provider.loginStart",
		});
		await expect(server.handle("bogus")).rejects.toThrow();
	});
});

describe("argument resolution", () => {
	test("prefers explicit arguments and never silently defaults", () => {
		expect(resolvePackagePath(["--package", "/opt/pi", "--agent-dir", "/tmp/agent"])).toBe("/opt/pi");
		expect(resolvePackagePath(["--package=/opt/pi"])).toBe("/opt/pi");
		expect(resolveAgentDir(["--agent-dir", "/tmp/agent"])).toBe("/tmp/agent");
		const previous = process.env.PIXIE_PI_PACKAGE;
		process.env.PIXIE_PI_PACKAGE = "/env/pi";
		try {
			expect(resolvePackagePath([])).toBe("/env/pi");
		} finally {
			if (previous === undefined) delete process.env.PIXIE_PI_PACKAGE;
			else process.env.PIXIE_PI_PACKAGE = previous;
		}
	});

	test("the process exits non-zero when the installation cannot be verified", async () => {
		const agentDir = await mkdtemp(join(tmpdir(), "pixie-agent-"));
		const result = Bun.spawnSync({
			cmd: [
				process.execPath,
				join(import.meta.dir, "serve.ts"),
				"--package",
				join(tmpdir(), "pixie-bridge-missing-installation"),
				"--agent-dir",
				agentDir,
			],
			stdout: "pipe",
			stderr: "pipe",
		});
		expect(result.exitCode).not.toBe(0);
		expect(new TextDecoder().decode(result.stderr)).toContain("pixie-admin-bridge:");
	});
});
