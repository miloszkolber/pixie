import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { startBunHost } from "../src/host.ts";
import { createHostLogger } from "../src/log.ts";
import {
	credential,
	expectSecretFree,
	FakeSession,
	helloV2,
	hostWith,
	operatorPath,
	operatorUrl,
	rawFrames,
	rawHost,
	registerHostCleanup,
	request,
	secret,
	socket,
	tempAgentDir,
	trackHost,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

describe("Bun host admin parity", () => {
	function adminSdk() {
		let provider: string | undefined = "test";
		let model: string | undefined = "m1";
		let thinking: string | undefined;
		return {
			SettingsManager: {
				create: () => ({
					reload: async () => {},
					flush: async () => {},
					drainErrors: () => [],
					getGlobalSettings: () => ({
						defaultThinkingLevel: thinking,
						compaction: {},
						packages: [],
						extensions: [],
					}),
					getProjectSettings: () => ({ packages: [], extensions: [] }),
					getDefaultProvider: () => provider,
					getDefaultModel: () => model,
					setDefaultProvider: (value: string | undefined) => {
						provider = value;
					},
					setDefaultModel: (value: string | undefined) => {
						model = value;
					},
					getDefaultThinkingLevel: () => thinking,
					setDefaultThinkingLevel: (value: string | undefined) => {
						thinking = value;
					},
					getCompactionReserveTokens: () => 2048,
					setPackages: () => {},
					setProjectPackages: () => {},
					setExtensionPaths: () => {},
					setProjectExtensionPaths: () => {},
				}),
			},
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getModels: (id: string) =>
						id === "test" ? [{ id: "m1", name: "M1", contextWindow: 8000, maxTokens: 1000 }] : [],
					getModel: (providerId: string, modelId: string) =>
						providerId === "test" && modelId === "m1"
							? { contextWindow: 8000, maxTokens: 1000, cost: { input: 1 } }
							: undefined,
					getAvailable: async () => [{ provider: "test", id: "m1" }],
					checkAuth: async () => true,
					refresh: async () => ({ errors: new Map() }),
					login: async () => ({}),
					logout: async () => {},
				}),
			},
			DefaultPackageManager: class {
				listConfiguredPackages(): unknown[] {
					return [];
				}
				async resolve(): Promise<unknown> {
					return { extensions: [], skills: [], prompts: [], themes: [] };
				}
			},
		};
	}

	test("serves providers, defaults, preferences, and slash commands in-process", async () => {
		const raw = rawHost(new FakeSession("admin"), { sdk: adminSdk() });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.list", params: { providerIds: [] } });
		const providers = (await waitForId(raw.socket, 2)).result as unknown as {
			entries: Array<{ providerId: string }>;
		};
		expect(providers.entries.map((entry) => entry.providerId)).toContain("test");
		await raw.send({
			id: 3,
			method: "pi.providers.canonical-model-info",
			params: { provider: "test", model: "m1" },
		});
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({
			modelInfo: expect.objectContaining({ provider: "test", currency: "USD" }),
		});
		await raw.send({ id: 4, method: "pi.defaults.read", params: {} });
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({
			providerId: "test",
			modelId: "m1",
		});
		await raw.send({
			id: 5,
			method: "pi.preferences.save",
			params: { values: [{ key: "piThinkingEffort", value: "high" }] },
		});
		expect((await waitForId(raw.socket, 5)).error).toBeUndefined();
		await raw.send({
			id: 6,
			method: "pi.preferences.save",
			params: { values: [{ key: "compactionReserveTokens", value: 1 }] },
		});
		expect((await waitForId(raw.socket, 6)).error?.message).toContain("read-only");
		await raw.send({ id: 11, method: "pi.preferences.read", params: {} });
		const projection = (await waitForId(raw.socket, 11)).result as unknown as {
			values: Array<{ key: string; value: unknown; writable: boolean; source: string }>;
		};
		expect(projection.values).toMatchObject([
			{ key: "piThinkingEffort", value: "high", writable: true, source: "pi" },
			{ key: "compactionReserveTokens", value: null, writable: false, source: "read-only" },
		]);
		// Re-sending the unchanged read-only value is tolerated, not a mutation.
		await raw.send({
			id: 12,
			method: "pi.preferences.save",
			params: { values: [{ key: "compactionReserveTokens", value: null }] },
		});
		expect((await waitForId(raw.socket, 12)).error).toBeUndefined();
		await raw.send({
			id: 13,
			method: "pi.preferences.reset",
			params: { keys: ["compactionReserveTokens"] },
		});
		expect((await waitForId(raw.socket, 13)).error).toBeUndefined();
		await raw.send({ id: 7, method: "pi.slash-commands.list", params: {} });
		const commands = (await waitForId(raw.socket, 7)).result as unknown as {
			availableCommands: Array<{ name: string }>;
		};
		expect(commands.availableCommands.map((command) => command.name)).toContain("compact");
		await raw.send({
			id: 8,
			method: "provider.loginStart",
			params: { providerId: "test", loginId: "login-1" },
		});
		expect((await waitForId(raw.socket, 8)).result).toMatchObject({ loginId: "login-1" });
		await raw.send({ id: 9, method: "provider.loginBegin", params: { loginId: "login-1" } });
		expect((await waitForId(raw.socket, 9)).result).toMatchObject({ ok: true });
		await raw.send({ id: 10, method: "provider.loginBegin", params: { loginId: "missing" } });
		expect((await waitForId(raw.socket, 10)).error?.message).toContain("cannot be started");
	});

	test("reads typed provider configuration presence without leaking secrets or paths", async () => {
		const secretValue = "sk-live-do-not-leak";
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [
						{
							id: "test",
							name: "Test",
							auth: {
								apiKey: { login: true, value: secretValue },
								path: "/home/operator/.pi/auth.json",
							},
						},
					],
					getProviderAuthStatus: () => ({
						configured: true,
						source: "environment",
						label: "/home/operator/.pi/auth.json",
					}),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "test" } });
		const result = (await waitForId(raw.socket, 2)).result as unknown as {
			providerId: string;
			configured: boolean;
			source: string;
			fields: Array<{ name: string; isSet: boolean; source: string }>;
		};
		expect(result).toEqual({
			providerId: "test",
			configured: true,
			source: "environment",
			fields: [{ name: "api_key", isSet: true, source: "environment" }],
		});
		const serialized = JSON.stringify(result);
		expect(serialized).not.toContain(secretValue);
		expect(serialized).not.toContain("/home/operator");
		for (const field of result.fields)
			expect(Object.keys(field).sort()).toEqual(["isSet", "name", "source"]);
	});

	test("reports an unconfigured provider without claiming an explicit field", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getProviderAuthStatus: () => ({ configured: false, source: "fallback" }),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config-default"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "test" } });
		const result = (await waitForId(raw.socket, 2)).result as unknown as {
			fields: Array<{ isSet: boolean }>;
		};
		expect(result.fields.length).toBeGreaterThan(0);
		expect(result.fields.every((field) => field.isSet === false)).toBe(true);
	});

	test("rejects an unknown provider instead of fabricating configuration", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [],
					getProviderAuthStatus: () => ({ configured: true, source: "stored" }),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config-unknown"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "ghost" } });
		expect((await waitForId(raw.socket, 2)).error?.message).toContain("Unknown provider");
	});

	test("validates provider deletion against the typed projection before mutating", async () => {
		let logouts = 0;
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getProviderAuthStatus: () => ({ configured: true, source: "stored" }),
					isUsingOAuth: () => false,
					getModels: () => [],
					logout: async () => {
						logouts += 1;
					},
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-delete"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.providers.config.delete",
			params: { providerId: "ghost" },
		});
		expect((await waitForId(raw.socket, 2)).error?.message).toContain("Unknown provider");
		expect(logouts).toBe(0);
		await raw.send({ id: 3, method: "pi.providers.config.delete", params: { providerId: "test" } });
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({ ok: true });
		expect(logouts).toBe(1);
	});

	test("completes loginStart, loginBegin, prompt, and loginReply on one login", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					login: async (
						_providerId: string,
						_type: string,
						interaction: {
							prompt: (prompt: Record<string, unknown>) => Promise<string>;
						},
					) => {
						const answer = await interaction.prompt({ type: "secret", message: "enter key" });
						if (answer !== "s3cr3t") throw new Error("unexpected answer");
						return { ok: true };
					},
				}),
			},
		};
		const raw = rawHost(new FakeSession("login-flow"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "provider.loginStart",
			params: { providerId: "test", loginId: "login-1" },
		});
		expect((await waitForId(raw.socket, 2)).result).toMatchObject({ loginId: "login-1" });
		await raw.send({ id: 3, method: "provider.loginBegin", params: { loginId: "login-1" } });
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({ ok: true });
		let prompted = false;
		for (let attempt = 0; attempt < 200 && !prompted; attempt += 1) {
			prompted = rawFrames(raw.socket).some(
				(frame) =>
					frame.method === "provider.login" &&
					(frame.params as unknown as { frame?: { kind?: string } })?.frame?.kind === "prompt",
			);
			if (!prompted) await Bun.sleep(5);
		}
		expect(prompted).toBe(true);
		await raw.send({
			id: 4,
			method: "provider.loginReply",
			params: { loginId: "login-1", value: "s3cr3t" },
		});
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ ok: true });
		let succeeded = false;
		for (let attempt = 0; attempt < 200 && !succeeded; attempt += 1) {
			succeeded = rawFrames(raw.socket).some(
				(frame) =>
					frame.method === "provider.login" &&
					(frame.params as unknown as { frame?: { kind?: string } })?.frame?.kind === "success",
			);
			if (!succeeded) await Bun.sleep(5);
		}
		expect(succeeded).toBe(true);
	});

	test("keeps version gating exact and restart opt-in", async () => {
		const strict = rawHost(new FakeSession("restart-off"), { protocol: "v2" });
		await strict.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 2, supportedProtocolVersions: [2, 1] },
		});
		await strict.send({ id: 2, method: "runtime.restart", params: {} });
		expect(rawFrames(strict.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
		let restarted = false;
		const agentDir = tempAgentDir();
		const sdk = {
			createAgentSession: async () => ({ session: new FakeSession("restart-on") }),
			getDefaultSessionDir: (_cwd: string) => join(agentDir, "sessions"),
			SessionManager: {
				create: () => ({}),
				list: async () => [],
				open: () => ({}),
			},
		};
		const { host } = hostWith(new FakeSession("restart-on"), {
			agentDir,
			sdk,
			protocol: "auto",
		});
		const started = startBunHost({
			host: "127.0.0.1",
			port: 0,
			secret,
			agentDir: tempAgentDir(),
			verifiedPi: {
				packageName: "@earendil-works/pi-coding-agent",
				packageVersion: "0.85.1",
				packageDir: "/pi",
				entryPath: "/pi/index.js",
				manifestDigest: "a".repeat(64),
				entryDigest: "b".repeat(64),
			},
			sdk: sdk as never,
			allowSelfRestart: true,
			onRestart: () => {
				restarted = true;
			},
		});
		trackHost(started);
		await host.close();
		const ws = await socket(started);
		await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		const reply = await request(ws, 2, "runtime.restart", {});
		expect(reply).toMatchObject({ id: 2, result: { ok: true } });
		for (let attempt = 0; !restarted && attempt < 50; attempt += 1) await Bun.sleep(10);
		expect(restarted).toBe(true);
		ws.close();
	});
});

describe("Bun host admin dispatch", () => {
	function settingsSdk() {
		let provider: string | undefined = "seed-provider";
		let model: string | undefined = "seed-model";
		return {
			SettingsManager: {
				create: () => ({
					reload: async () => {},
					flush: async () => {},
					drainErrors: () => [],
					getGlobalSettings: () => ({
						defaultThinkingLevel: undefined,
						compaction: {},
						packages: [],
						extensions: [],
						apiKey: credential,
					}),
					getProjectSettings: () => ({ packages: [], extensions: [] }),
					getDefaultProvider: () => provider,
					getDefaultModel: () => model,
					setDefaultProvider: (value: string | undefined) => {
						provider = value;
					},
					setDefaultModel: (value: string | undefined) => {
						model = value;
					},
					getDefaultThinkingLevel: () => undefined,
					setDefaultThinkingLevel: (_value: string | undefined) => {},
					getCompactionReserveTokens: () => 0,
					setPackages: (_packages: unknown[]) => {},
					setProjectPackages: (_packages: unknown[]) => {},
					setExtensionPaths: (_paths: unknown[]) => {},
					setProjectExtensionPaths: (_paths: unknown[]) => {},
				}),
			},
		};
	}

	function providerRuntimeSdk(overrides: Record<string, unknown> = {}) {
		return {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getModels: () => [],
					getModel: () => undefined,
					getAvailable: async () => [],
					checkAuth: async () => true,
					refresh: async () => ({ errors: new Map() }),
					login: async () => ({}),
					logout: async () => {},
					...overrides,
				}),
			},
		};
	}

	test("saves and clears Pi defaults with bounded, secret-free responses", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		const raw = rawHost(new FakeSession("defaults"), {
			sdk: settingsSdk(),
			logger,
			protocol: "auto",
		});
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["pi.defaults.save"]).toBe(true);
		expect(hello.result?.operationSet?.["pi.defaults.clear"]).toBe(true);

		await raw.send({
			id: 2,
			method: "pi.defaults.save",
			params: { providerId: "test", modelId: "m1", ignored: operatorUrl },
		});
		const saved = await waitForId(raw.socket, 2);
		expect(saved.error).toBeUndefined();
		expect(saved.result).toMatchObject({ providerId: "test", modelId: "m1" });
		expectSecretFree(saved, logger, forbidden);

		await raw.send({
			id: 3,
			method: "pi.defaults.save",
			params: { providerId: "test", modelId: `${credential}\0${operatorPath}` },
		});
		const badModel = await waitForId(raw.socket, 3);
		expect(badModel.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(badModel.error?.message).toContain("modelId");
		expectSecretFree(badModel, logger, forbidden);

		await raw.send({ id: 4, method: "pi.defaults.save", params: {} });
		const missingProvider = await waitForId(raw.socket, 4);
		expect(missingProvider.error?.message).toContain("providerId");
		expectSecretFree(missingProvider, logger, forbidden);

		await raw.send({
			id: 5,
			method: "pi.defaults.clear",
			params: { cwd: `${operatorPath}\0` },
		});
		const badCwd = await waitForId(raw.socket, 5);
		expect(badCwd.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(badCwd.error?.message).toContain("absolute");
		expectSecretFree(badCwd, logger, forbidden);

		await raw.send({ id: 6, method: "pi.defaults.clear", params: {} });
		const cleared = await waitForId(raw.socket, 6);
		expect(cleared.error).toBeUndefined();
		expect(cleared.result).toMatchObject({ providerId: null, modelId: null });
		expectSecretFree(cleared, logger, forbidden);
	});

	test("checks provider readiness and fails closed on a missing capability", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		let ready = false;
		const sdk = providerRuntimeSdk({ checkAuth: async () => ready });
		const raw = rawHost(new FakeSession("readiness"), { sdk, logger, protocol: "auto" });
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["pi.providers.readiness.check"]).toBe(true);

		await raw.send({
			id: 2,
			method: "pi.providers.readiness.check",
			params: { providerId: "test", ignored: credential },
		});
		const notReady = await waitForId(raw.socket, 2);
		expect(notReady.result).toMatchObject({
			providerId: "test",
			ready: false,
			error: null,
			hasIssue: true,
		});
		expectSecretFree(notReady, logger, forbidden);

		ready = true;
		await raw.send({
			id: 3,
			method: "pi.providers.readiness.check",
			params: { providerId: "test" },
		});
		const isReady = await waitForId(raw.socket, 3);
		expect(isReady.result).toMatchObject({
			providerId: "test",
			ready: true,
			error: null,
			hasIssue: false,
		});
		expectSecretFree(isReady, logger, forbidden);

		await raw.send({ id: 4, method: "pi.providers.readiness.check", params: {} });
		const missing = await waitForId(raw.socket, 4);
		expect(missing.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(missing.error?.message).toContain("providerId");
		expectSecretFree(missing, logger, forbidden);

		const noRuntime = rawHost(new FakeSession("readiness-no-runtime"), {
			logger,
			protocol: "auto",
		});
		await helloV2(noRuntime);
		await noRuntime.send({
			id: 2,
			method: "pi.providers.readiness.check",
			params: { providerId: credential },
		});
		const unavailable = await waitForId(noRuntime.socket, 2);
		expect(unavailable.error).toMatchObject({
			code: -32004,
			reason: "capability_unavailable",
		});
		expectSecretFree(unavailable, logger, [
			credential,
			operatorPath,
			operatorUrl,
			noRuntime.agentDir,
			secret,
		]);
	});

	test("refreshes provider inventory with allowNetwork and a bounded response", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		let refreshArgs: { allowNetwork?: unknown; signal?: unknown } | undefined;
		const sdk = providerRuntimeSdk({
			refresh: async (options: { allowNetwork?: unknown; signal?: unknown }) => {
				refreshArgs = options;
				return { aborted: false, errors: new Map() };
			},
		});
		const raw = rawHost(new FakeSession("inventory"), { sdk, logger, protocol: "auto" });
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["pi.providers.inventory.refresh"]).toBe(true);

		await raw.send({
			id: 2,
			method: "pi.providers.inventory.refresh",
			params: { providerId: credential, ignored: operatorUrl },
		});
		const refreshed = await waitForId(raw.socket, 2);
		expect(refreshed.error).toBeUndefined();
		expect(refreshed.result).toMatchObject({
			started: [],
			skipped: [],
			aborted: false,
			failed: [],
		});
		expect(refreshArgs?.allowNetwork).toBe(true);
		expect(refreshArgs?.signal).toBeInstanceOf(AbortSignal);
		expectSecretFree(refreshed, logger, forbidden);

		const noRuntime = rawHost(new FakeSession("inventory-no-runtime"), {
			logger,
			protocol: "auto",
		});
		await helloV2(noRuntime);
		await noRuntime.send({ id: 2, method: "pi.providers.inventory.refresh", params: {} });
		const unavailable = await waitForId(noRuntime.socket, 2);
		expect(unavailable.error).toMatchObject({
			code: -32004,
			reason: "capability_unavailable",
		});
		expectSecretFree(unavailable, logger, [
			credential,
			operatorPath,
			operatorUrl,
			noRuntime.agentDir,
			secret,
		]);
	});

	test("reports provider refresh failures without exposing SDK error details", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		const leaked = "Bearer secret-token https://example.test /home/operator/.pi";
		const sdk = providerRuntimeSdk({
			refresh: async () => ({
				aborted: false,
				errors: new Map([["provider", new Error(leaked)]]),
			}),
		});
		const raw = rawHost(new FakeSession("inventory-partial"), { sdk, logger, protocol: "auto" });
		await helloV2(raw);

		await raw.send({ id: 2, method: "pi.providers.inventory.refresh", params: {} });
		const refreshed = await waitForId(raw.socket, 2);
		expect(refreshed.error).toBeUndefined();
		expect(refreshed.result).toMatchObject({
			started: [],
			skipped: [],
			aborted: false,
			failed: [{ providerId: "provider", reason: "refresh_failed" }],
		});
		expectSecretFree(refreshed, logger, [
			credential,
			operatorPath,
			operatorUrl,
			raw.agentDir,
			secret,
			"Bearer secret-token",
			"https://example.test",
			"/home/operator/.pi",
		]);
	});

	test("marks a provider refresh aborted without reporting failures", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		const sdk = providerRuntimeSdk({
			refresh: async () => ({ aborted: true, errors: new Map() }),
		});
		const raw = rawHost(new FakeSession("inventory-abort"), { sdk, logger, protocol: "auto" });
		await helloV2(raw);

		await raw.send({ id: 2, method: "pi.providers.inventory.refresh", params: {} });
		const refreshed = await waitForId(raw.socket, 2);
		expect(refreshed.error).toBeUndefined();
		expect(refreshed.result).toMatchObject({
			started: [],
			skipped: [],
			aborted: true,
			failed: [],
		});
	});

	test("cancels a pending provider login and aborts its signal", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		let signal: AbortSignal | undefined;
		const sdk = providerRuntimeSdk({
			login: async (
				_providerId: string,
				_type: string,
				interaction: { signal: AbortSignal; notify: (event: Record<string, unknown>) => void },
			) => {
				signal = interaction.signal;
				interaction.notify({ type: "progress", message: "awaiting input" });
				await new Promise(() => {});
			},
		});
		const raw = rawHost(new FakeSession("login-cancel"), { sdk, logger, protocol: "auto" });
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["provider.loginCancel"]).toBe(true);

		await raw.send({
			id: 2,
			method: "provider.loginStart",
			params: { providerId: "test", loginId: "login-cancel" },
		});
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({ id: 3, method: "provider.loginBegin", params: { loginId: "login-cancel" } });
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({ ok: true });
		for (let attempt = 0; !signal && attempt < 200; attempt += 1) await Bun.sleep(5);
		expect(signal).toBeInstanceOf(AbortSignal);

		await raw.send({ id: 4, method: "provider.loginCancel", params: { loginId: "login-cancel" } });
		const cancelled = await waitForId(raw.socket, 4);
		expect(cancelled).toMatchObject({ id: 4, result: { ok: true } });
		expect(signal?.aborted).toBe(true);
		expectSecretFree(cancelled, logger, forbidden);

		await raw.send({ id: 5, method: "provider.loginCancel", params: { loginId: credential } });
		const unknown = await waitForId(raw.socket, 5);
		expect(unknown.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(unknown.error?.message).toContain("Unknown or expired login ID");
		expectSecretFree(unknown, logger, forbidden);

		await raw.send({ id: 6, method: "provider.loginCancel", params: {} });
		const missingId = await waitForId(raw.socket, 6);
		expect(missingId.error?.message).toContain("loginId");
		expectSecretFree(missingId, logger, forbidden);
	});
});
