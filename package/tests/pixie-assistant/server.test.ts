import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startHost } from "../../../assistant/src/server.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const fn of cleanup.splice(0).reverse()) await fn();
});
async function rpc(url: string, secret: string) {
	const ws = new WebSocket(url, { headers: { Authorization: `Bearer ${secret}` } });
	await new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("WebSocket connection failed"));
	});
	cleanup.push(async () => ws.close());
	let serial = 0;
	const pending = new Map<number, { resolve: (v: any) => void; reject: (e: Error) => void }>();
	const events: any[] = [];
	ws.onmessage = (e) => {
		const value = JSON.parse(String(e.data));
		if (value.id) {
			const p = pending.get(value.id);
			pending.delete(value.id);
			if (value.error) p?.reject(new Error(value.error.message));
			else p?.resolve(value.result);
		} else events.push(value);
	};
	return {
		events,
		call: (method: string, params: unknown = {}) =>
			new Promise<any>((resolve, reject) => {
				const id = ++serial;
				pending.set(id, { resolve, reject });
				ws.send(JSON.stringify({ id, method, params }));
			}),
	};
}

test("host authenticates transport and routes native provider prompts to the owning connection", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-pi-rpc-");
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const secret = "isolated-test-secret";
	const host = await startHost({ agentDir: dir, secret, port: 0 });
	cleanup.push(() => host.close());
	await expect(startHost({ agentDir: dir, secret, port: 0 })).rejects.toThrow("already being held");
	const base = `http://127.0.0.1:${host.server.port}`;
	expect((await fetch(`${base}/readyz`)).status).toBe(401);
	expect(
		(
			await fetch(`${base}/readyz`, {
				headers: { Authorization: `Bearer ${secret}`, Origin: "https://example.com" },
			})
		).status,
	).toBe(403);
	const a = await rpc(base.replace("http:", "ws:") + "/pi", secret),
		b = await rpc(base.replace("http:", "ws:") + "/pi", secret);
	expect(await a.call("runtime.hello")).toMatchObject({
		protocolVersion: 1,
		capabilities: { sessions: 1, providers: 1 },
	});
	const login = await a.call("provider.loginStart", {
		providerId: "openai",
		type: "api_key",
		loginId: "test-login",
	});
	expect(login.loginId).toBe("test-login");
	await expect(
		b.call("provider.loginReply", { loginId: "test-login", value: "test" }),
	).rejects.toThrow("another connection");
	await a.call("provider.loginBegin", { loginId: "test-login" });
	await Bun.sleep(20);
	expect(
		a.events.some((e) => e.method === "provider.login" && e.params.frame.kind === "prompt"),
	).toBe(true);
	expect(b.events).toHaveLength(0);
	await a.call("provider.loginReply", {
		loginId: "test-login",
		value: "sk-fixture-not-a-real-key",
	});
	for (let i = 0; i < 100 && !a.events.some((e) => e.params?.frame?.kind === "success"); i++)
		await Bun.sleep(10);
	expect(a.events.some((e) => e.params?.frame?.kind === "success")).toBe(true);
	const inventory = await a.call("pi.providers.list", { providerIds: ["openai"] });
	expect(inventory.entries[0]).toMatchObject({ configured: true });
	expect(JSON.stringify(inventory)).not.toContain("sk-fixture");
	await a.call("pi.providers.config.delete", { providerId: "openai" });
	await a.call("pi.preferences.save", {
		values: [
			{ key: "compactionReserveTokens", value: 16384 },
			{ key: "piThinkingEffort", value: "high" },
		],
	});
	expect(await a.call("pi.preferences.read")).toEqual({
		values: [
			{ key: "piThinkingEffort", value: "high" },
			{ key: "compactionReserveTokens", value: 16384 },
		],
	});
	await a.call("pi.preferences.reset", { keys: ["compactionReserveTokens", "piThinkingEffort"] });
	expect(await a.call("pi.preferences.read")).toEqual({
		values: [
			{ key: "piThinkingEffort", value: null },
			{ key: "compactionReserveTokens", value: null },
		],
	});
	const session = await a.call("session.create", { cwd: dir });
	expect((await a.call("session.list")).sessions).toHaveLength(1);
	await b.call("session.load", { sessionId: session.sessionId, cwd: dir });
	expect(await a.call("pi.sources.list", {})).toMatchObject({ sources: [] });
	const entry = await host.sessions.get(session.sessionId);
	const large = "x".repeat(9 * 1024 * 1024);
	entry.session.sessionManager.appendMessage({
		role: "user",
		content: large,
		timestamp: Date.now(),
	});
	const loaded = await b.call("session.load", { sessionId: session.sessionId, cwd: dir });
	expect(loaded.messages).toEqual([]);
	expect(
		b.events.filter((e) => e.method === "session.history").flatMap((e) => e.params.messages)[0]
			.content,
	).toHaveLength(large.length);
});

test("native MCP attaches and preserves peer authorization after idle reload", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-adapter-rpc-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const previous = process.env.PI_CODING_AGENT_DIR;
	process.env.PI_CODING_AGENT_DIR = dir;
	cleanup.push(async () => {
		if (previous === undefined) delete process.env.PI_CODING_AGENT_DIR;
		else process.env.PI_CODING_AGENT_DIR = previous;
	});
	const secret = "adapter-rpc-fixture-secret";
	await writeFile(
		join(dir, "settings.json"),
		JSON.stringify({
			extensions: [createRequire(import.meta.url).resolve("pi-mcp-adapter")],
		}),
	);
	const host = await startHost({ agentDir: dir, secret, port: 0 });
	cleanup.push(() => host.close());
	const url = `ws://127.0.0.1:${host.server.port}/pi`;
	const owner = await rpc(url, secret);
	const other = await rpc(url, secret);
	const mcpServers = [
		{
			name: "objective",
			type: "stdio",
			command: process.execPath,
			args: [join(import.meta.dir, "mcp-stdio-fixture.ts")],
			cwd: dir,
			env: { MCP_FIXTURE: "session-scoped" },
		},
	];
	const session = await owner.call("session.create", { cwd: dir, mcpServers });
	const params = { sessionId: session.sessionId, name: "objective__echo", arguments: {} };
	expect(await owner.call("pi.tools.call", params)).toMatchObject({
		content: [{ text: `session-scoped:${dir}` }],
		isError: false,
	});
	await expect(other.call("pi.tools.call", params)).rejects.toThrow("Attach the session");
	const entry = await host.sessions.get(session.sessionId);
	expect(entry.session.getActiveToolNames().filter((name) => name === "mcp")).toHaveLength(1);
	expect(entry.session.getActiveToolNames().some((name) => name.startsWith("objective__"))).toBe(
		false,
	);
	entry.lastUsed = 0;
	await host.sessions.sweep();
	expect(await owner.call("pi.tools.call", params)).toMatchObject({
		content: [{ text: `session-scoped:${dir}` }],
	});
	expect((await host.sessions.get(session.sessionId)).session).not.toBe(entry.session);
	await owner.call("pi.session.extensions.remove", {
		sessionId: session.sessionId,
		extensionKey: "objective",
	});
	await owner.call("session.load", { sessionId: session.sessionId, cwd: dir, mcpServers });
	await expect(owner.call("pi.tools.call", params)).rejects.toThrow("Unknown MCP connection");
});

test("native configuration defers application until reopen and preserves the resident runtime", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-native-mcp-settings-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const previous = process.env.PI_CODING_AGENT_DIR;
	process.env.PI_CODING_AGENT_DIR = dir;
	cleanup.push(async () => {
		if (previous === undefined) delete process.env.PI_CODING_AGENT_DIR;
		else process.env.PI_CODING_AGENT_DIR = previous;
	});
	const adapter = createRequire(import.meta.url).resolve("pi-mcp-adapter");
	await writeFile(join(dir, "settings.json"), JSON.stringify({ extensions: [adapter] }));
	const secret = "native-mcp-config-fixture";
	const host = await startHost({ agentDir: dir, secret, port: 0 });
	cleanup.push(() => host.close());
	const client = await rpc(`ws://127.0.0.1:${host.server.port}/pi`, secret);
	const created = await client.call("session.create", { cwd: dir });
	const entry = await host.sessions.get(created.sessionId);
	const inventory = await client.call("pi.extensions.list", {
		sessionId: created.sessionId,
		cwd: dir,
	});
	const resource = inventory.resources.find((item: { path: string }) => item.path === adapter);
	expect(
		await client.call("pi.extensions.configure", {
			sessionId: created.sessionId,
			cwd: dir,
			scope: "user",
			resourceKey: resource.resourceKey,
			expectedRevision: inventory.configurationRevisions.user,
			enabled: false,
			confirmed: true,
		}),
	).toMatchObject({ saved: true, loaded: false, reload: "deferred" });
	// The resident session keeps the runtime until it reopens; the saved
	// change applies through the ordinary reopen path.
	expect(await host.sessions.get(created.sessionId)).toBe(entry);
	expect(entry.session.getActiveToolNames()).toContain("mcp");
	await host.sessions.release(created.sessionId);
	const reopened = await host.sessions.get(created.sessionId);
	expect(reopened).not.toBe(entry);
	expect(reopened.session.getActiveToolNames()).not.toContain("mcp");
});
