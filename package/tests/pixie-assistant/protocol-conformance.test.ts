import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { startHost } from "../../../assistant/src/server.ts";

// Conformance for the assistant wire contract documented in docs/pi-protocol.md:
// the welcome envelope, capability introspection, error frames, and the
// transport bounds that keep one peer from wedging the host.
// One host serves the whole file: the wire contract is per-connection, and a
// single lock lifecycle keeps the run deterministic.

let host: Awaited<ReturnType<typeof startHost>>;
let url: string;
let dir: string;

beforeAll(async () => {
	dir = await mkdtemp(tmpdir() + "/pixie-protocol-");
	const secret = "protocol-conformance";
	host = await startHost({ agentDir: dir, secret, port: 0 });
	url = `ws://127.0.0.1:${host.server.port}/pi`;
});

afterAll(async () => {
	// Bun below 1.4.0 wedges server.stop after server-initiated WebSocket
	// closes; a deferred close there leaks unhandled rejections into sibling
	// tests, so the disposable host is left to the process exit instead.
	if (Bun.version.localeCompare("1.4.0", undefined, { numeric: true }) >= 0) {
		await host.close();
		await rm(dir, { recursive: true, force: true });
	} else {
		console.warn(`bun ${Bun.version}: leaving the conformance host to process exit`);
	}
});

function connect(secret = "protocol-conformance") {
	const ws = new WebSocket(url, { headers: { Authorization: `Bearer ${secret}` } });
	const opened = new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("WebSocket connection failed"));
	});
	const closed = new Promise<number>((resolve) => {
		ws.onclose = (event) => resolve(event.code);
	});
	let serial = 0;
	const pending = new Map<
		number,
		{ resolve: (value: any) => void; reject: (error: { code: number; message: string }) => void }
	>();
	ws.onmessage = (e) => {
		const value = JSON.parse(String(e.data));
		if (value.id) {
			const p = pending.get(value.id);
			pending.delete(value.id);
			if (!p) return;
			if (value.error) p.reject({ code: value.error.code, message: value.error.message });
			else p.resolve(value.result);
		}
	};
	return {
		ws,
		closed,
		opened,
		call: (method: string, params: unknown = {}) =>
			new Promise<any>((resolve, reject) => {
				const id = ++serial;
				pending.set(id, { resolve, reject });
				ws.send(JSON.stringify({ id, method, params }));
			}),
	};
}

async function callError(client: ReturnType<typeof connect>, method: string) {
	try {
		await client.call(method, {});
	} catch (error) {
		return error as { code: number; message: string };
	}
	throw new Error(`${method} unexpectedly succeeded`);
}

describe("assistant wire contract", () => {
	test("welcome envelope matches the documented protocol version and capability baseline", async () => {
		const a = connect();
		await a.opened;
		const hello = await a.call("runtime.hello");
		expect(hello.protocolVersion).toBe(1);
		expect(typeof hello.runtimeId).toBe("string");
		expect(typeof hello.bootId).toBe("string");
		expect(typeof hello.version).toBe("string");
		expect(hello.capabilities).toMatchObject({ sessions: 1, providers: 1, agents: 1 });
		expect(await a.call("runtime.capabilities")).toEqual(hello.capabilities);
		a.ws.close();
	});

	test("unknown methods return error frames with the documented code", async () => {
		const a = connect();
		await a.opened;
		const failure = await callError(a, "no.such.method");
		expect(failure.code).toBe(-32000);
		expect(failure.message).toContain("no.such.method");
		a.ws.close();
	});

	test("unauthenticated connections are rejected before frames are read", async () => {
		const bad = connect("wrong-secret-at-least-16");
		await expect(bad.opened).rejects.toThrow();
	});

	test("malformed frames close the connection with the documented codes", async () => {
		const bad = connect();
		await bad.opened;
		bad.ws.send("not json");
		expect(await bad.closed).toBe(1007);

		const invalid = connect();
		await invalid.opened;
		invalid.ws.send(JSON.stringify({ id: 0, method: "runtime.hello", params: {} }));
		expect(await invalid.closed).toBe(1008);
	});

	test("a reused in-flight request id answers with an error frame and keeps the connection", async () => {
		const ws = new WebSocket(url, { headers: { Authorization: "Bearer protocol-conformance" } });
		await new Promise<void>((resolve, reject) => {
			ws.onopen = () => resolve();
			ws.onerror = () => reject(new Error("WebSocket connection failed"));
		});
		const frames: Array<{ id?: number; error?: { code: number; message: string } }> = [];
		ws.onmessage = (e) => frames.push(JSON.parse(String(e.data)));
		ws.send(
			JSON.stringify({ id: 9, method: "pi.slash-commands.list", params: { sessionId: "missing" } }),
		);
		await Bun.sleep(50);
		ws.send(JSON.stringify({ id: 9, method: "runtime.hello", params: {} }));
		await Bun.sleep(150);
		expect(frames.some((frame) => frame.id === 9 && frame.error)).toBe(true);
		const code = await Promise.race([
			new Promise<number>((resolve) => {
				ws.onclose = (event) => resolve(event.code);
			}),
			Bun.sleep(150).then(() => 0),
		]);
		expect(code).toBe(0); // still open
		ws.close();
	});
});
