/**
 * Sidecar frame, verification and FC17 mapping tests.
 *
 * The selected installation is a stub module written into a temporary
 * directory. This keeps the test independent of an installed Pi while still
 * exercising the real dynamic import, package verification and response
 * shaping paths.
 */

import { describe, expect, test } from "bun:test";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as realPi from "@earendil-works/pi-coding-agent";
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
		`import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const readSettings = (agentDir) => {
  try { return JSON.parse(readFileSync(join(agentDir, "settings.json"), "utf8")); } catch { return {}; }
};
export class SettingsManager {
  static create(cwd, agentDir) {
    const manager = new SettingsManager(readSettings(agentDir));
    manager.agentDir = agentDir;
    return manager;
  }
  static fromStorage(storage) { const manager = new SettingsManager({}); manager.storage = storage; return manager; }
  constructor(state = {}, storage) { this.state = state; this.storage = storage; }
  async reload() {}
  async flush() {
    if (this.storage) {
      const state = this.state;
      this.storage.withLock("global", (current) => {
        const base = current ? JSON.parse(current) : {};
        const merged = { ...base, ...state };
        for (const key of Object.keys(state)) if (state[key] === undefined) delete merged[key];
        return JSON.stringify(merged, null, 2);
      });
      return;
    }
    if (!this.agentDir) return;
    const path = join(this.agentDir, "settings.json");
    let base = {};
    try { base = JSON.parse(readFileSync(path, "utf8")); } catch {}
    const merged = { ...base, ...this.state };
    for (const key of Object.keys(this.state)) if (this.state[key] === undefined) delete merged[key];
    writeFileSync(path, JSON.stringify(merged, null, 2));
  }
  drainErrors() { return []; }
  getGlobalSettings() { return JSON.parse(JSON.stringify(this.state)); }
  getProjectSettings() { return {}; }
  getDefaultProvider() { return this.state.defaultProvider; }
  getDefaultModel() { return this.state.defaultModel; }
  setDefaultProvider(value) { this.state.defaultProvider = value; }
  setDefaultModel(value) { this.state.defaultModel = value; }
  getDefaultThinkingLevel() { return this.state.defaultThinkingLevel; }
  setDefaultThinkingLevel(value) { this.state.defaultThinkingLevel = value; }
  getCompactionReserveTokens() { return this.state.compaction?.reserveTokens ?? 16384; }
  getLastChangelogVersion() { return this.state.lastChangelogVersion; }
  setLastChangelogVersion(value) { this.state.lastChangelogVersion = value; }
}
export class DefaultPackageManager {
  constructor() {}
  listConfiguredPackages() {
    return [{ source: "npm:demo", scope: "user", filtered: false }];
  }
  async resolve() {
    return {
      extensions: [{ path: "/tmp/demo/ext.js", enabled: true, metadata: { source: "npm:demo", scope: "user", origin: "package" } }],
      skills: [{ path: "/tmp/demo/skills/demo/SKILL.md", enabled: true, metadata: { source: "npm:demo", scope: "user", origin: "package" } }],
      prompts: [{ path: "/tmp/demo/prompts/review.md", enabled: true, metadata: { source: "npm:demo", scope: "user", origin: "package" } }],
      themes: [],
    };
  }
}
export class ProjectTrustStore {
  constructor() {}
  get() { return null; }
}
export function hasTrustRequiringProjectResources() { return false; }
export class ModelRuntime {
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
	return { dir, agentDir, bridge: createBridge({ installation, module, agentDir }) };
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
		expect(encodeResponse({ id: 1, ok: true, result: {} })).toBe(
			'{"id":1,"ok":true,"result":{}}\n',
		);
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
		expect(hello.publicSymbols).toEqual([
			"ModelRuntime",
			"SettingsManager",
			"DefaultPackageManager",
			"ProjectTrustStore",
			"hasTrustRequiringProjectResources",
		]);
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
		expect(alpha.configKeys).toEqual([
			{ name: "api_key", secret: true, required: true, primary: true },
		]);
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
			await bridge.dispatch("pi.providers.canonical-model-info", {
				provider: "alpha",
				model: "alpha-1",
			}),
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

describe("bridge FC19 defaults and preferences", () => {
	test("pi.defaults read, save and clear round-trip", async () => {
		const { bridge } = await stubBridge();
		expect(await bridge.dispatch("pi.defaults.read", {})).toEqual({
			providerId: null,
			modelId: null,
		});
		expect(
			await bridge.dispatch("pi.defaults.save", { providerId: "alpha", modelId: "alpha-1" }),
		).toEqual({ providerId: "alpha", modelId: "alpha-1" });
		expect(await bridge.dispatch("pi.defaults.read", {})).toEqual({
			providerId: "alpha",
			modelId: "alpha-1",
		});
		expect(await bridge.dispatch("pi.defaults.clear", {})).toEqual({
			providerId: null,
			modelId: null,
		});
	});

	test("pi.preferences save and reset persist the thinking effort", async () => {
		const { bridge, agentDir } = await stubBridge();
		expect(
			await bridge.dispatch("pi.preferences.save", {
				values: [{ key: "piThinkingEffort", value: "max" }],
			}),
		).toEqual({
			values: [
				{ key: "piThinkingEffort", value: "max" },
				{ key: "compactionReserveTokens", value: null },
			],
		});
		expect(await bridge.dispatch("pi.preferences.reset", { keys: ["piThinkingEffort"] })).toEqual({
			values: [
				{ key: "piThinkingEffort", value: null },
				{ key: "compactionReserveTokens", value: null },
			],
		});
		expect(JSON.parse(await readFile(join(agentDir, "settings.json"), "utf8"))).toEqual({});
	});

	test("preference validation fails closed", async () => {
		const { bridge } = await stubBridge();
		await expect(
			bridge.dispatch("pi.preferences.save", {
				values: [{ key: "piThinkingEffort", value: "bogus" }],
			}),
		).rejects.toThrow("Unsupported thinking effort");
		await expect(
			bridge.dispatch("pi.preferences.save", {
				values: [{ key: "compactionReserveTokens", value: 10 }],
			}),
		).rejects.toThrow("no public setter");
		await expect(
			bridge.dispatch("pi.preferences.save", { values: [{ key: "unknown", value: 1 }] }),
		).rejects.toThrow("Unknown preference");
	});
});

describe("bridge FC20 inventory and MCP configuration", () => {
	test("pi.extensions.list reports configured resource inventory", async () => {
		const { bridge } = await stubBridge();
		const result = (await bridge.dispatch("pi.extensions.list", {})) as Record<string, any>;
		expect(result.version).toBe(1);
		expect(result.context.reader).toBe("service");
		expect(result.packages[0]).toMatchObject({
			source: "npm:demo",
			scope: "user",
			state: "missing",
		});
		expect(result.resources[0]).toMatchObject({
			path: "/tmp/demo/ext.js",
			state: "not-observed",
			enabled: true,
		});
		expect(result.extensions).toEqual([]);
		expect(result.errors).toEqual([]);
	});

	test("pi.extensions.list reports a session reader without live evidence", async () => {
		const { bridge } = await stubBridge();
		const result = (await bridge.dispatch("pi.extensions.list", {
			cwd: "/tmp/project",
			sessionId: "session-1",
		})) as Record<string, any>;
		expect(result.context.reader).toBe("not-resident");
		expect(result.context.sessionId).toBe("session-1");
		expect(result.extensions).toEqual([]);
	});

	test("pi.config.extensions.list mirrors persisted MCP configuration", async () => {
		const { bridge, agentDir } = await stubBridge();
		await writeFile(
			join(agentDir, "mcp.json"),
			JSON.stringify({
				demo: { command: "node", args: ["server.js"], env: {} },
				bad: { url: "not a url" },
			}),
		);
		const result = (await bridge.dispatch("pi.config.extensions.list", {})) as Record<string, any>;
		const demo = result.extensions.find((entry: any) => entry.configKey === "demo");
		expect(demo.enabled).toBe(true);
		expect(demo.extension.type).toBe("mcp");
		expect(demo.extension.server).toMatchObject({ name: "demo", command: "node" });
		const bad = result.extensions.find((entry: any) => entry.configKey === "bad");
		expect(bad.invalid).toBe(true);
		expect(result.warnings).toEqual(["Invalid MCP configuration: bad"]);
	});

	test("pi.config.extensions.list rejects native pi-mcp-adapter configuration", async () => {
		const { bridge, agentDir } = await stubBridge();
		await writeFile(join(agentDir, "mcp.json"), JSON.stringify({ mcpServers: {} }));
		await expect(bridge.dispatch("pi.config.extensions.list", {})).rejects.toThrow(
			"pi-mcp-adapter",
		);
	});

	test("pi.session.extensions.list merges persisted memberships", async () => {
		const { bridge, agentDir } = await stubBridge();
		await writeFile(join(agentDir, "mcp.json"), JSON.stringify({ base: { command: "node" } }));
		await writeFile(
			join(agentDir, "mcp-sessions.json"),
			JSON.stringify({ "session-1": { add: { active: { command: "node" } }, remove: ["base"] } }),
		);
		const result = (await bridge.dispatch("pi.session.extensions.list", {
			sessionId: "session-1",
		})) as Record<string, any>;
		expect(result.extensions.map((entry: any) => entry.extensionKey)).toEqual(["active"]);
		expect(result.extensions[0].extension.type).toBe("mcp");
	});

	test("pi.slash-commands.list reports builtin and resolved commands", async () => {
		const { bridge } = await stubBridge();
		const result = (await bridge.dispatch("pi.slash-commands.list", {})) as Record<string, any>;
		expect(result.availableCommands.map((command: any) => command.name)).toEqual([
			"compact",
			"review",
			"skill:demo",
		]);
	});

	test("unsupported FC18/FC21 methods still fail closed", async () => {
		const { bridge } = await stubBridge();
		await expect(bridge.dispatch("pi.extensions.configure", {})).rejects.toThrow(
			"Unsupported bridge method",
		);
		await expect(bridge.dispatch("pi.providers.config.delete", {})).rejects.toThrow(
			"Unsupported bridge method",
		);
	});
});

describe.skipIf(typeof realPi.SettingsManager !== "function")("real Pi 0.85.1 SDK smoke", () => {
	const installation = {
		packageName: STUB_PACKAGE,
		packageVersion: "0.85.1",
		packageDir: "/tmp/pixie-real-pi",
		entryPath: "/tmp/pixie-real-pi/dist/index.js",
		moduleOrigin: "/tmp/pixie-real-pi/dist/index.js",
	};

	test("native settings serve defaults and preferences", async () => {
		const agentDir = await mkdtemp(join(tmpdir(), "pixie-real-agent-"));
		const bridge = createBridge({
			installation,
			module: realPi as unknown as Record<string, unknown>,
			agentDir,
		});
		expect(await bridge.dispatch("pi.defaults.read", {})).toEqual({
			providerId: null,
			modelId: null,
		});
		expect(
			await bridge.dispatch("pi.defaults.save", { providerId: "alpha", modelId: "alpha-1" }),
		).toEqual({ providerId: "alpha", modelId: "alpha-1" });
		expect(await bridge.dispatch("pi.defaults.clear", {})).toEqual({
			providerId: null,
			modelId: null,
		});
		expect(
			await bridge.dispatch("pi.preferences.save", {
				values: [{ key: "piThinkingEffort", value: "high" }],
			}),
		).toEqual({
			values: [
				{ key: "piThinkingEffort", value: "high" },
				{ key: "compactionReserveTokens", value: null },
			],
		});
		await expect(
			bridge.dispatch("pi.preferences.save", {
				values: [{ key: "compactionReserveTokens", value: 20000 }],
			}),
		).rejects.toThrow("no public setter");
		expect(await bridge.dispatch("pi.preferences.reset", { keys: ["piThinkingEffort"] })).toEqual({
			values: [
				{ key: "piThinkingEffort", value: null },
				{ key: "compactionReserveTokens", value: null },
			],
		});
	});

	test("native package inventory resolves configured resources", async () => {
		const agentDir = await mkdtemp(join(tmpdir(), "pixie-real-agent-"));
		const bridge = createBridge({
			installation,
			module: realPi as unknown as Record<string, unknown>,
			agentDir,
		});
		const result = (await bridge.dispatch("pi.extensions.list", {})) as Record<string, any>;
		expect(result.version).toBe(1);
		expect(result.context.reader).toBe("service");
		expect(Array.isArray(result.packages)).toBe(true);
		expect(Array.isArray(result.resources)).toBe(true);
	});
});

describe("argument resolution", () => {
	test("prefers explicit arguments and never silently defaults", () => {
		expect(resolvePackagePath(["--package", "/opt/pi", "--agent-dir", "/tmp/agent"])).toBe(
			"/opt/pi",
		);
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
