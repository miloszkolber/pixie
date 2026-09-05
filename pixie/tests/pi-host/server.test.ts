import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { startHost } from "../../../pi/host/src/server.ts";

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
	await expect(a.call("pi.sources.list", {})).rejects.toThrow("Unsupported capability");
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
