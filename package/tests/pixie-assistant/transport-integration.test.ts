import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { spawnNativeChild, NativeJsonlTransport, nativeTransportErrorKindOf } from "../../../assistant/src/transport/jsonl-transport.ts";
import { startHost } from "../../../assistant/src/server.ts";

const children: NativeJsonlTransport[] = [];
const hosts: Array<{ close: () => Promise<void> }> = [];
const dirs: string[] = [];

afterEach(async () => {
	for (const host of hosts.splice(0).reverse()) await host.close().catch(() => {});
	for (const transport of children.splice(0).reverse()) await transport.close().catch(() => {});
	for (const dir of dirs.splice(0).reverse()) await rm(dir, { recursive: true, force: true });
});

const responder = `
let pending = "";
process.stdin.on("data", (chunk) => {
  pending += chunk.toString("utf8");
  for (;;) {
    const index = pending.indexOf("\\n");
    if (index < 0) break;
    const raw = pending.slice(0, index).replace(/\\r$/, "");
    pending = pending.slice(index + 1);
    if (!raw) continue;
    const frame = JSON.parse(raw);
    if (frame.type === "hello") {
      process.stdout.write(JSON.stringify({ id: frame.id, type: "hello", protocolVersion: 1 }) + "\\n");
    } else if (frame.method === "shutdown") {
      process.stdout.write(JSON.stringify({ id: frame.id, type: "response", result: { ok: true } }) + "\\n");
      process.stdout.end();
    } else {
      process.stdout.write(JSON.stringify({ id: frame.id, type: "response", accepted: true, result: { method: frame.method, params: frame.params } }) + "\\n");
    }
  }
});
`;

const shortConfig = {
	maxRecordBytes: 1024 * 1024,
	aggregateMaxBytes: 2 * 1024 * 1024,
	controlReserveBytes: 64 * 1024,
	controlMaxOps: 4,
	controlMaxBytesEach: 16 * 1024,
	maxPending: 8,
	writeTimeoutMs: 100,
	readStallTimeoutMs: 100,
	helloTimeoutMs: 500,
	drainTimeoutMs: 500,
};

function realTransport(script = responder): NativeJsonlTransport {
	const transport = new NativeJsonlTransport({
		config: shortConfig,
		child: () =>
			spawnNativeChild({
				command: process.execPath,
				args: ["-e", script],
				env: { PATH: process.env.PATH ?? "", HOME: process.env.HOME ?? "/tmp" },
			}),
	});
	children.push(transport);
	return transport;
}

test("a real child handshake correlates requests and keeps control admitted", async () => {
	const transport = realTransport();
	await transport.start();
	expect(transport.state).toBe("ready");
	const result = await transport.request("echo", { value: "safe" });
	expect(result).toEqual({ method: "echo", params: { value: "safe" } });
	const ordinaryFill = transport.budget.admittedBytes();
	const fill = shortConfig.aggregateMaxBytes - shortConfig.controlReserveBytes - ordinaryFill;
	transport.budget.reserveOrdinary(fill);
	await expect(transport.request("ordinary-blocked", {})).rejects.toMatchObject({ kind: "backpressure" });
	const control = await transport.request("delivery.abort", { requestId: "abort-1" }, { lane: "control" });
	expect(control).toMatchObject({ method: "delivery.abort" });
	transport.budget.releaseOrdinary(fill);
	expect(transport.pendingCount).toBe(0);
	expect(transport.budget.admittedBytes()).toBe(0);
});

test("child exit fails an unsettled request without replaying it", async () => {
	const exitsAfterHello = `
let pending = "";
process.stdin.on("data", (chunk) => {
  pending += chunk.toString("utf8");
  const index = pending.indexOf("\\n");
  if (index < 0) return;
  const frame = JSON.parse(pending.slice(0, index));
  pending = pending.slice(index + 1);
  if (frame.type === "hello") {
    process.stdout.write(JSON.stringify({ id: frame.id, type: "hello", protocolVersion: 1 }) + "\\n");
    setTimeout(() => process.exit(17), 10);
  }
});
`;
	const transport = realTransport(exitsAfterHello);
	await transport.start();
	await expect(transport.request("never-settles", { opaque: true })).rejects.toMatchObject({
		kind: "child_exit",
		outcome: "uncertain",
	});
	expect(transport.pendingCount).toBe(0);
	});

test("the assistant host performs the native handshake during runtime startup", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-transport-host-`);
	dirs.push(dir);
	const transport = realTransport();
	const host = await startHost({ agentDir: dir, secret: "transport-host-secret", port: 0, nativeTransport: transport });
	hosts.push(host);
	expect(host.nativeTransport?.state).toBe("ready");
	expect(transport.state).toBe("ready");
});

test("a stalled child read has an explicit transport error", async () => {
	const transport = realTransport(`setTimeout(() => {}, 1000);`);
	let failure: unknown;
	try {
		await transport.start();
	} catch (error) {
		failure = error;
	}
	expect(nativeTransportErrorKindOf(failure)).toBe("stalled");
	await transport.close();
});
