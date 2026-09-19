import { describe, expect, test } from "bun:test";
import { FakeSession, rawHost, registerHostCleanup, secret, waitForId } from "./harness.ts";

registerHostCleanup();

describe("Bun host extension dispatch", () => {
	const extensionPath = "/native/pi/extensions/fixture.ts";
	const longSource = `ext-${"x".repeat(2000)}`;

	function extensionSdk() {
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
						extensions: [extensionPath],
					}),
					getProjectSettings: () => ({ packages: [], extensions: [] }),
					getDefaultProvider: () => undefined,
					getDefaultModel: () => undefined,
					setDefaultProvider: () => {},
					setDefaultModel: () => {},
					getDefaultThinkingLevel: () => undefined,
					setDefaultThinkingLevel: () => {},
					getCompactionReserveTokens: () => 0,
					setPackages: () => {},
					setProjectPackages: () => {},
					setExtensionPaths: () => {},
					setProjectExtensionPaths: () => {},
				}),
			},
			DefaultPackageManager: class {
				listConfiguredPackages(): unknown[] {
					return [
						{
							source: longSource,
							scope: "user",
							filtered: false,
							installedPath: "/native/pi/extensions/fixture",
						},
					];
				}
				async resolve(): Promise<unknown> {
					return {
						extensions: [
							{
								path: extensionPath,
								enabled: true,
								metadata: { source: "local", scope: "user", origin: "top-level" },
							},
						],
						skills: [],
						prompts: [],
					};
				}
			},
		};
	}

	test("bounds native extension inventory and rejects malformed scope, confirmation and revision", async () => {
		const sentinel = "sk-live-extension-sentinel";
		const raw = rawHost(new FakeSession("extensions"), { sdk: extensionSdk() });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.extensions.list", params: {} });
		const inventory = (await waitForId(raw.socket, 2)).result as unknown as {
			version: number;
			packages: Array<{ source: string; installed: boolean; state: string }>;
			configurationRevisions: { user: string; project: string };
			context: { reader: string };
			paths: Array<{ path: string; scope: string }>;
			resources: Array<{ path: string; source: string; scope: string }>;
		};
		expect(inventory.version).toBe(1);
		expect(inventory.context.reader).toBe("service");
		expect(inventory.packages).toHaveLength(1);
		expect(inventory.packages[0]?.source).toHaveLength(1024);
		expect(inventory.packages[0]?.installed).toBe(true);
		expect(inventory.packages[0]?.state).toBe("not-observed");
		expect(inventory.paths).toMatchObject([{ path: extensionPath, scope: "user" }]);
		expect(inventory.resources[0]?.path).toBe(extensionPath);
		expect(inventory.configurationRevisions.user).toMatch(/^[a-f0-9]{64}$/);
		expect(JSON.stringify(inventory)).not.toContain(secret);

		await raw.send({
			id: 3,
			method: "pi.extensions.configure",
			params: {
				scope: "workspace",
				confirmed: true,
				enabled: true,
				resourceKey: "k",
				expectedRevision: "r",
				note: sentinel,
			},
		});
		const badScope = (await waitForId(raw.socket, 3)).error?.message ?? "";
		expect(badScope).toContain("scope");
		expect(badScope).not.toContain(sentinel);
		expect(badScope).not.toContain(raw.agentDir);

		await raw.send({
			id: 4,
			method: "pi.extensions.configure",
			params: {
				scope: "user",
				confirmed: false,
				enabled: true,
				resourceKey: "k",
				expectedRevision: "r",
			},
		});
		expect((await waitForId(raw.socket, 4)).error?.message).toContain("Confirm");

		await raw.send({
			id: 5,
			method: "pi.extensions.configure",
			params: {
				scope: "user",
				confirmed: true,
				enabled: true,
				resourceKey: "",
				expectedRevision: "r",
			},
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toContain("revision");

		await raw.send({
			id: 6,
			method: "pi.extensions.configure",
			params: {
				scope: "user",
				confirmed: true,
				enabled: "yes",
				resourceKey: "k",
				expectedRevision: "r",
			},
		});
		expect((await waitForId(raw.socket, 6)).error?.message).toContain("boolean");

		await raw.send({
			id: 7,
			method: "pi.extensions.configure",
			params: {
				scope: "user",
				confirmed: true,
				enabled: true,
				resourceKey: "k",
				expectedRevision: "stale",
				note: sentinel,
			},
		});
		const stale = (await waitForId(raw.socket, 7)).error?.message ?? "";
		expect(stale).toContain("Native configuration changed");
		expect(stale).not.toContain(sentinel);
		expect(stale).not.toContain(raw.agentDir);
	});

	test("manages stored MCP configuration and never echoes a rejected credential", async () => {
		const sentinel = "sk-live-config-sentinel";
		const raw = rawHost(new FakeSession("config-extensions"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.config.extensions.add",
			params: {
				extension: { name: "fixture", command: "/bin/echo", args: ["hello"] },
				enabled: true,
			},
		});
		expect((await waitForId(raw.socket, 2)).result).toMatchObject({ ok: true });
		await raw.send({ id: 3, method: "pi.config.extensions.list", params: {} });
		const listed = (await waitForId(raw.socket, 3)).result as unknown as {
			extensions: Array<{ configKey: string; enabled: boolean }>;
			warnings: string[];
		};
		expect(listed.warnings).toEqual([]);
		expect(listed.extensions).toEqual([
			expect.objectContaining({ configKey: "fixture", enabled: true }),
		]);
		await raw.send({
			id: 4,
			method: "pi.config.extensions.set-enabled",
			params: { configKey: "fixture", enabled: false },
		});
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ ok: true });
		await raw.send({
			id: 5,
			method: "pi.config.extensions.set-enabled",
			params: { configKey: "fixture" },
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toContain("boolean");
		await raw.send({
			id: 6,
			method: "pi.config.extensions.remove",
			params: { configKey: "fixture" },
		});
		expect((await waitForId(raw.socket, 6)).result).toMatchObject({ ok: true });
		await raw.send({ id: 7, method: "pi.config.extensions.list", params: {} });
		expect((await waitForId(raw.socket, 7)).result).toMatchObject({ extensions: [], warnings: [] });
		await raw.send({
			id: 8,
			method: "pi.config.extensions.add",
			params: { extension: { name: "leaky", url: `http://operator:${sentinel}@127.0.0.1/mcp` } },
		});
		const leaky = (await waitForId(raw.socket, 8)).error?.message ?? "";
		expect(leaky).toContain("credentials");
		expect(leaky).not.toContain(sentinel);
		expect(leaky).not.toContain(raw.agentDir);
	});

	test("manages per-session extensions and rejects a credential-bearing definition", async () => {
		const sentinel = "sk-live-session-sentinel";
		const raw = rawHost(new FakeSession("session-extensions"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.session.extensions.add",
			params: { sessionId: "s1", extension: { name: "fixture", command: "/bin/echo", args: [] } },
		});
		expect((await waitForId(raw.socket, 2)).result).toMatchObject({ ok: true });
		await raw.send({ id: 3, method: "pi.session.extensions.list", params: { sessionId: "s1" } });
		const listed = (await waitForId(raw.socket, 3)).result as unknown as {
			extensions: Array<{ extensionKey: string }>;
		};
		expect(listed.extensions).toEqual([expect.objectContaining({ extensionKey: "fixture" })]);
		await raw.send({
			id: 4,
			method: "pi.session.extensions.remove",
			params: { sessionId: "s1", extensionKey: "fixture" },
		});
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ ok: true });
		await raw.send({ id: 5, method: "pi.session.extensions.list", params: { sessionId: "s1" } });
		expect((await waitForId(raw.socket, 5)).result).toMatchObject({ extensions: [], warnings: [] });
		await raw.send({ id: 6, method: "pi.session.extensions.list", params: {} });
		expect((await waitForId(raw.socket, 6)).error?.message).toContain("sessionId");
		await raw.send({
			id: 7,
			method: "pi.session.extensions.add",
			params: {
				sessionId: "s1",
				extension: { name: "leaky", url: `http://operator:${sentinel}@127.0.0.1/mcp` },
			},
		});
		const leaky = (await waitForId(raw.socket, 7)).error?.message ?? "";
		expect(leaky).toContain("credentials");
		expect(leaky).not.toContain(sentinel);
		expect(leaky).not.toContain(raw.agentDir);
	});
});
