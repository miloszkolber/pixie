import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
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
	ws.onclose = () => {
		for (const { reject } of pending.values()) reject(new Error("WebSocket closed"));
		pending.clear();
	};
	cleanup.push(async () => ws.close());
	const client = {
		close: () => ws.close(),
		call: (method: string, params: unknown = {}) =>
			new Promise<any>((resolve, reject) => {
				const id = ++serial;
				pending.set(id, { resolve, reject });
				ws.send(JSON.stringify({ id, method, params }));
			}),
	};
	await client.call("runtime.hello", { protocolVersion: 1 });
	return client;
};

const freePort = async () => {
	const listener = createServer();
	await new Promise<void>((resolve, reject) => {
		listener.once("error", reject);
		listener.listen(0, "127.0.0.1", () => resolve());
	});
	const address = listener.address();
	if (!address || typeof address === "string") throw new Error("Failed to allocate a test port");
	const port = address.port;
	await new Promise<void>((resolve, reject) =>
		listener.close((error) => (error ? reject(error) : resolve())),
	);
	return port;
};

const startProductionHost = async (agentDir: string, port: number) => {
	const root = join(import.meta.dir, "../../..");
	const child = Bun.spawn(
		[
			process.execPath,
			join(root, "assistant/src/main.ts"),
			"--agent-dir",
			agentDir,
			"--port",
			String(port),
		],
		{
			cwd: root,
			env: {
				...process.env,
				MCP_UI_VIEWER: "none",
				PIXIE_ALLOW_SELF_RESTART: "1",
				PIXIE_PI_SECRET_KEY: "production-restart-secret",
			},
			stdout: "pipe",
			stderr: "pipe",
		},
	);
	let exited = false;
	const exit = child.exited.then((code) => {
		exited = true;
		return code;
	});
	const output = Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
	]);
	const stop = async () => {
		if (!exited) child.kill("SIGTERM");
		await exit;
		await output;
	};
	cleanup.push(stop);
	for (let attempt = 0; attempt < 200; attempt++) {
		try {
			const response = await fetch(`http://127.0.0.1:${port}/readyz`, {
				headers: { Authorization: "Bearer production-restart-secret" },
			});
			if (response.ok) return { child, exit, output, stop, port };
		} catch {
			// The service may still be starting.
		}
		await Bun.sleep(25);
	}
	const [code, [stdout, stderr]] = await Promise.all([exit, output]);
	throw new Error(`production host did not become ready (${code}):\n${stderr}\n${stdout}`);
};

test("runtime.restart stays disabled unless the deployment opts in", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-restart-off-`);
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

test("an enabled self restart requires an executable termination hook", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-restart-hook-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const host = await startHost({
		agentDir: dir,
		secret: "restart-hook-secret-16",
		port: 0,
		allowSelfRestart: true,
	});
	const client = await connect(host.server.port, "restart-hook-secret-16");
	try {
		await expect(client.call("runtime.restart", {})).rejects.toThrow(
			"no executable termination hook",
		);
	} finally {
		await host.close();
	}
});

test("an enabled self restart schedules one termination after replying", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-restart-on-`);
	// Bun below 1.4.0 can wedge a host close after a server-initiated peer
	// close, so on that runtime the disposable host and its directory are left
	// to process exit instead of being torn down mid-run.
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
		await expect(client.call("session.create", { cwd: dir })).rejects.toThrow("WebSocket closed");
	} finally {
		// Bun below 1.4.0 can wedge a host close after a server-initiated peer
		// close, so the disposable host is left to process exit there.
		if (Bun.version.localeCompare("1.4.0", undefined, { numeric: true }) >= 0) await host.close();
	}
});

test("the production entrypoint exits with a fresh boot identity and retains native state", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-restart-process-`);
	cleanup.push(() => rm(dir, { recursive: true, force: true }));
	const port = await freePort();
	const first = await startProductionHost(dir, port);
	const firstClient = await connect(first.port, "production-restart-secret");
	try {
		const firstHello = await firstClient.call("runtime.hello");
		expect(firstHello.runtimeId).toBeString();
		expect(firstHello.bootId).toBeString();
		const session = await firstClient.call("session.create", { cwd: dir });
		expect(await firstClient.call("runtime.restart", {})).toEqual({ ok: true });
		expect(await first.exit).toBe(75);
		await first.stop();
		const [, firstStderr] = await first.output;
		expect(firstStderr).not.toContain("shutdown exceeded");

		const second = await startProductionHost(dir, port);
		const secondClient = await connect(second.port, "production-restart-secret");
		try {
			const secondHello = await secondClient.call("runtime.hello");
			expect(secondHello.runtimeId).toBe(firstHello.runtimeId);
			expect(secondHello.bootId).not.toBe(firstHello.bootId);
			expect(second.child.pid).not.toBe(first.child.pid);
			expect((await secondClient.call("session.list")).sessions).toEqual(
				expect.arrayContaining([expect.objectContaining({ sessionId: session.sessionId })]),
			);
		} finally {
			secondClient.close();
			await second.stop();
		}
	} finally {
		firstClient.close();
		await first.stop();
	}
}, 120000);
