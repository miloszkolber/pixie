import { expect, test } from "bun:test";
import {
	BRIDGE_ALLOWED_METHODS,
	BRIDGE_CHANNEL_MAX_BUFFER_BYTES,
	BRIDGE_CHANNEL_MAX_FRAME_BYTES,
	BRIDGE_CHANNEL_MAX_PENDING,
	checkPrivateBridgeChannel,
	createBridgeHello,
	decodeBridgeFrame,
	encodeBridgeFrame,
	validateBridgeHandshake,
} from "../../../assistant/src/bridge/channel.ts";

const channel = {
	distribution: "npm" as const,
	sdkVersion: "0.85.1",
	transport: "inherited-duplex" as const,
	descriptor: 7,
	private: true,
	authenticated: true,
	closeOnExec: true,
	descendantsInherit: false,
	listener: false,
	credentialExport: false,
	shellEval: false,
	maxFrameBytes: BRIDGE_CHANNEL_MAX_FRAME_BYTES,
	maxBufferedBytes: BRIDGE_CHANNEL_MAX_BUFFER_BYTES,
	maxPending: BRIDGE_CHANNEL_MAX_PENDING,
	nonce: "nonce-0123456789ab",
	epoch: "epoch-0123456789",
} as const;

test("private channel checks require an inherited descriptor, bounded reservations and no authority leak", () => {
	const allowed = checkPrivateBridgeChannel(channel);
	expect(allowed.allowed).toBe(true);
	expect(allowed.blockers).toEqual([]);

	const leaked = checkPrivateBridgeChannel({
		...channel,
		descendantsInherit: true,
		listener: true,
		maxBufferedBytes: BRIDGE_CHANNEL_MAX_BUFFER_BYTES + 1,
	});
	expect(leaked.allowed).toBe(false);
	expect(leaked.blockers.map((blocker) => blocker.missingPublicSymbol)).toEqual(
		expect.arrayContaining([
			"descendant descriptor non-inheritance",
			"no broad bridge listener",
			"bounded bridge aggregate buffer",
		]),
	);
});

test("nonce and epoch handshake is exact and frame methods are explicitly allowlisted", () => {
	const hello = createBridgeHello(channel.nonce, channel.epoch);
	expect(validateBridgeHandshake(hello, channel)).toEqual({ ok: true });
	expect(() =>
		validateBridgeHandshake(hello, { nonce: channel.nonce, epoch: "epoch-other-012345" }),
	).toThrow("nonce or epoch mismatch");

	const request = {
		version: 1 as const,
		type: "request" as const,
		id: "request-1",
		method: BRIDGE_ALLOWED_METHODS[0],
		payload: { scope: "selected" },
	};
	const encoded = encodeBridgeFrame(request);
	expect(encoded.endsWith("\n")).toBe(true);
	expect(decodeBridgeFrame(encoded.slice(0, -1))).toEqual(request);
	expect(decodeBridgeFrame(`${encoded.slice(0, -1)}\r`)).toEqual(request);
	expect(() => encodeBridgeFrame({ ...request, method: "private.call" as never })).toThrow(
		"Unsupported bridge method",
	);
});

test("JSONL framing rejects oversized, malformed and non-strict frames", () => {
	const response = {
		version: 1 as const,
		type: "response" as const,
		id: "request-1",
		ok: false,
		error: "unsupported public API",
	};
	expect(decodeBridgeFrame(encodeBridgeFrame(response).slice(0, -1))).toEqual(response);
	expect(() => decodeBridgeFrame("{not-json}")).toThrow("Invalid bridge JSONL frame");
	expect(() =>
		decodeBridgeFrame(
			JSON.stringify({
				version: 1,
				type: "hello",
				nonce: "nonce-0123456789ab",
				epoch: "epoch-0123456789",
				extra: true,
			}),
		),
	).toThrow("Unexpected bridge frame field");
	expect(() =>
		decodeBridgeFrame(
			`${JSON.stringify({ version: 1, type: "request", id: "x", method: "bridge.probe", payload: { value: "x".repeat(BRIDGE_CHANNEL_MAX_FRAME_BYTES) } })}`,
		),
	).toThrow("exceeds");
});
