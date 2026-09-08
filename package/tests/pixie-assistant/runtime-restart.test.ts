import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { startHost } from "../../../assistant/src/server.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const fn of cleanup.splice(0).reverse()) await fn();
});

const connect = async (port: number | undefined, secret: string) => {
	const ws = new WebSocket(`ws://127.0.0.1:${port}/pi`, {
		headers: { Authorization: `Bearer ${secret}` },
	});
	await new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("WebSocket connection failed"));
	});
	let serial = 0;
	const pending = new Map<
		number,
		{ resolve: (value: any) => void; reject: (error: Error) => void }
	>();
	ws.onmessage = (e) => {
		const value = JSON.parse(String(e.data));
		const p = pending.get(value.id);
		pending.delete(value.id);
		if (!p) return;
		if (value.error) p.reject(new Error(value.error.message));
		else p.resolve(value.result);
	};
	cleanup.push(async () => ws.close());
	return {
		call: (method: string, params: unknown = {}) =>
			new Promise<any>((resolve, reject) => {
				const id = ++serial;
				pending.set(id, { resolve, reject });
				ws.send(JSON.stringify({ id, method, params }));
			}),
	};
};

test("runtime.restart stays disabled unless the deployment opts in", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-restart-off-");
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const host = await startHost({ agentDir: dir, secret: "restart-off-secret-16", port: 0 });
	const client = await connect(host.server.port, "restart-off-secret-16");
	try {
		await expect(client.call("runtime.restart", {})).rejects.toThrow(
			"Service self restart is not enabled for this deployment",
		);
	} finally {
		await host.close();
	}
});

test("an enabled self restart schedules one termination after replying", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-restart-on-");
	// Bun below 1.4.0 wedges a host close after the restart hook closed
	// peers, so on that runtime the disposable host and its directory are
	// left to the process exit instead of being torn down mid-run.
	const staleBun = Bun.version.localeCompare("1.4.0", undefined, { numeric: true }) < 0;
	if (!staleBun) cleanup.push(() => rm(dir, { recursive: true, force: true }));
	let restarts = 0;
	const host = await startHost({
		agentDir: dir,
		secret: "restart-on-secret-16",
		port: 0,
		allowSelfRestart: true,
		onRestart: () => {
			restarts += 1;
		},
	});
	const client = await connect(host.server.port, "restart-on-secret-16");
	try {
		expect(await client.call("runtime.restart", {})).toEqual({ ok: true });
		await expect(client.call("runtime.restart", {})).resolves.toEqual({ ok: true });
		await Bun.sleep(400);
		expect(restarts).toBe(1);
	} finally {
		// The restart hook already closed peers; on Bun below 1.4.0 a host
		// close after that wedges and leaks rejections into sibling tests,
		// so the disposable host is left to the process exit instead.
		if (Bun.version.localeCompare("1.4.0", undefined, { numeric: true }) >= 0) await host.close();
	}
});
