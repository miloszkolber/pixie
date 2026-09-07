import { expect, test } from "bun:test";
import { isWsClientEnvelope, isWsServerMessage } from "../../contracts/src/ws-runtime";

test("WebSocket envelope validators reject malformed and ambiguous frames", () => {
	for (const value of [null, undefined, true, 1, "x", [], {}, { id: 1, ok: true }]) {
		expect(isWsClientEnvelope(value)).toBeFalse();
		expect(isWsServerMessage(value)).toBeFalse();
	}
	expect(isWsClientEnvelope({ ack: ["a"] })).toBeTrue();
	expect(isWsClientEnvelope({ ack: ["a"], id: "ambiguous" })).toBeFalse();
	expect(isWsClientEnvelope({ id: "1", method: "project.list", params: {} })).toBeTrue();
	expect(isWsServerMessage({ id: "1", ok: true, result: [] })).toBeTrue();
	expect(isWsServerMessage({ id: "1", ok: true, result: [], error: "ambiguous" })).toBeFalse();
	expect(isWsServerMessage({ channel: "unknown", data: {} })).toBeFalse();
});
