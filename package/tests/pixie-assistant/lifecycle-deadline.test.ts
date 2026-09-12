import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { createDeadline } from "../../../assistant/src/lifecycle.ts";
import { startHost } from "../../../assistant/src/server.ts";
import { type ManagedSession, Sessions } from "../../../assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const close of cleanup.splice(0).reverse()) await close();
});

type Host = Awaited<ReturnType<typeof startHost>>;
async function rpc(host: Host, secret: string) {
	const ws = new WebSocket(`ws://127.0.0.1:${host.server.port}/pi`, {
		headers: { Authorization: `Bearer ${secret}` },
	});
	await new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("WebSocket connection failed"));
	});
	let nextId = 0;
	const pending = new Map<
		number,
		{ resolve: (value: unknown) => void; reject: (error: Error) => void }
	>();
	ws.onmessage = (event) => {
		const frame = JSON.parse(String(event.data)) as {
			id?: number;
			result?: unknown;
			error?: { message?: string };
		};
		if (frame.id === undefined) return;
		const request = pending.get(frame.id);
		if (!request) return;
		pending.delete(frame.id);
		if (frame.error) request.reject(new Error(frame.error.message ?? "Pi request failed"));
		else request.resolve(frame.result);
	};
	const call = (method: string, params: Record<string, unknown> = {}) =>
		new Promise<unknown>((resolve, reject) => {
			const id = ++nextId;
			pending.set(id, { resolve, reject });
			ws.send(JSON.stringify({ id, method, params }));
		});
	await call("runtime.hello", { protocolVersion: 1 });
	return { call, ws };
}

test("session construction is not allowed to wedge service drain", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-deadline-build-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const sessions = new Sessions(dir, [], () => {});
	const pending = new Promise<ManagedSession>(() => {});
	(sessions as unknown as { building: Set<Promise<ManagedSession>> }).building.add(pending);
	const deadline = createDeadline(25, "test service drain");
	const started = Date.now();
	await expect(sessions.close(deadline)).rejects.toThrow(/deadline/);
	deadline.dispose();
	expect(Date.now() - started).toBeLessThan(500);
	expect(sessions.entries.size).toBe(0);
});

test("extension teardown is forced to finish within the shared service deadline", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-deadline-teardown-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const sessions = new Sessions(dir, [], () => {});
	const stalled = { close: () => new Promise<void>(() => {}) } as unknown as ManagedSession;
	sessions.entries.set("stalled", stalled);
	const deadline = createDeadline(25, "test service drain");
	const started = Date.now();
	await expect(sessions.close(deadline)).rejects.toThrow(/deadline/);
	deadline.dispose();
	expect(Date.now() - started).toBeLessThan(500);
	expect(sessions.entries.size).toBe(0);
});

test("hung administration is bounded and remains visible to service drain", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-deadline-admin-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const secret = "deadline-test-secret";
	const host = await startHost({
		agentDir: dir,
		secret,
		port: 0,
		adminDeadlineMs: 25,
		drainDeadlineMs: 50,
	});
	cleanup.push(() => host.close().catch(() => {}));
	const { call, ws } = await rpc(host, secret);
	cleanup.push(async () => ws.close());
	host.control.capabilities.call = async () => new Promise<unknown>(() => {});
	await expect(call("pi.sources.list", {})).rejects.toThrow(/deadline/);
	const started = Date.now();
	await expect(host.close()).rejects.toThrow(/deadline|shutdown/i);
	expect(Date.now() - started).toBeLessThan(500);
});

test("provider administration stalls are bounded before service drain", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-deadline-provider-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const secret = "provider-deadline-secret";
	const host = await startHost({
		agentDir: dir,
		secret,
		port: 0,
		adminDeadlineMs: 25,
		drainDeadlineMs: 50,
	});
	cleanup.push(() => host.close().catch(() => {}));
	const { call, ws } = await rpc(host, secret);
	cleanup.push(async () => ws.close());
	host.control.modelRuntime.getAvailable = async () => new Promise<never>(() => {});
	await expect(call("pi.providers.list", {})).rejects.toThrow(/deadline/);
	await expect(host.close()).rejects.toThrow(/deadline|shutdown/i);
});
