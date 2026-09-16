import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { resolve } from "node:path";
import { StreamGuard, type StreamGuardFrame } from "@/connection/stream-guard";
import { WsTransport } from "@/connection/transport";
import { TestWebSocket } from "./test-socket";

const webRoot = resolve(import.meta.dir, "../../..");
const fixtureEnv = "PIXIE_AUX16_FRAME_FIXTURE";
const fixtureMarker = "AUX16_FRAME_FIXTURE=";

/**
 * Runs the controller's fixture test to obtain frames actually produced by the
 * server's stream tracker. The Web UI assertions below consume those exact
 * bytes, so a wire-contract change on either side fails the round trip.
 */
function serverFrames(): [string, string, string] {
	const result = Bun.spawnSync(
		[
			"go",
			"test",
			"./internal/controller/",
			"-run",
			"^TestAUX16ServerFrameFixture$",
			"-count=1",
			"-v",
		],
		{
			cwd: webRoot,
			env: { ...process.env, CGO_ENABLED: "0", [fixtureEnv]: "1" },
			stdout: "pipe",
			stderr: "pipe",
		},
	);
	if (result.exitCode !== 0) {
		throw new Error(`server frame fixture failed:\n${result.stderr.toString()}`);
	}
	const line = result.stdout
		.toString()
		.split("\n")
		.find((candidate) => candidate.includes(fixtureMarker));
	if (line === undefined) throw new Error("server frame fixture emitted no frames");
	const encoded = line.slice(line.indexOf(fixtureMarker) + fixtureMarker.length).trim();
	const parsed: unknown = JSON.parse(encoded);
	if (!Array.isArray(parsed)) throw new Error("server frame fixture is not an array");
	const [snapshot, delta, later] = parsed;
	if (typeof snapshot !== "string" || typeof delta !== "string" || typeof later !== "string") {
		throw new Error("server frame fixture did not emit three string frames");
	}
	return [snapshot, delta, later];
}

function parseFrame(encoded: string): StreamGuardFrame {
	return JSON.parse(encoded) as StreamGuardFrame;
}

const originalWebSocket = globalThis.WebSocket;

beforeEach(() => {
	TestWebSocket.instances = [];
	globalThis.WebSocket = TestWebSocket as unknown as typeof WebSocket;
});

afterEach(() => {
	globalThis.WebSocket = originalWebSocket;
});

describe("AUX-16 server-to-client stream round trip", () => {
	// The fixture spawns `go test` to produce real server frames; a cold Go build
	// cache can exceed the default test timeout.
	test("real server frames are accepted, merged and gap-resynced", () => {
		const [snapshot, delta, later] = serverFrames();

		// The real snapshot carries baseRev 0 and must enter the guard's framed
		// path so the append delta can chain to it.
		const guard = new StreamGuard();
		guard.acceptWelcome();
		expect(guard.accept(parseFrame(snapshot)).resync).toBe(false);
		expect(guard.accept(parseFrame(delta)).resync).toBe(false);

		// Skipping the delta leaves a real sequence gap the guard must report.
		const lossyGuard = new StreamGuard();
		lossyGuard.acceptWelcome();
		expect(lossyGuard.accept(parseFrame(snapshot)).resync).toBe(false);
		expect(lossyGuard.accept(parseFrame(later)).resync).toBe(true);

		// The transport must record the snapshot, merge the real delta and
		// deliver the assembled list instead of requesting a resync.
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		const received: unknown[] = [];
		transport.subscribe("project.updated", (data) => received.push(data));
		transport.connect();
		const socket = TestWebSocket.instances[0];
		if (!socket) throw new Error("socket was not created");
		socket.open();
		socket.message(snapshot);
		expect(received).toEqual([[{ id: "a" }]]);
		socket.message(delta);
		expect(received).toEqual([[{ id: "a" }], [{ id: "a" }, { id: "b" }]]);
		expect(socket.sent).not.toContain(JSON.stringify({ resync: true }));
		transport.stop();

		// Dropping the middle frame must ask the server for a fresh snapshot.
		const lossy = new WsTransport({ url: "ws://localhost:7312/ws" });
		lossy.connect();
		const lossySocket = TestWebSocket.instances[1];
		if (!lossySocket) throw new Error("second socket was not created");
		lossySocket.open();
		lossySocket.message(snapshot);
		lossySocket.message(later);
		expect(lossySocket.sent).toContain(JSON.stringify({ resync: true }));
		lossy.stop();
	}, 60_000);
});
