import { expect, test } from "bun:test";
import { buildEventFrame, serializeFrame } from "../../../assistant/src/serialize.ts";

test("event frames serialize normally", () => {
	const event = { type: "message_update", text: "hi" };
	const frame = buildEventFrame("s1", event, 7);
	const expected = JSON.stringify({
		method: "session.event",
		params: { sessionId: "s1", event, sequence: 7 },
	});
	expect(frame.data).toBe(expected);
	expect(frame.message).toEqual(JSON.parse(expected));
});

test("self-referential events degrade to a stub that keeps envelope fields", () => {
	const event: Record<string, unknown> = { type: "custom.thing" };
	event.self = event;
	const frame = buildEventFrame("s2", event, 3);
	const parsed = JSON.parse(frame.data);
	expect(parsed.method).toBe("session.event");
	expect(parsed.params.sessionId).toBe("s2");
	expect(parsed.params.sequence).toBe(3);
	expect(parsed.params.event.type).toBe("host.unserializable");
	expect(parsed.params.event.originalType).toBe("custom.thing");
	// The buffered object path carries the same stub so backlog replay and
	// direct fan-out stay consistent.
	expect(frame.message).toEqual(parsed);
});

test("events without a string type report a null original type", () => {
	const event: Record<string, unknown> = { payload: [] };
	event.self = event;
	const parsed = JSON.parse(buildEventFrame("s3", event, 1).data);
	expect(parsed.params.event.type).toBe("host.unserializable");
	expect(parsed.params.event.originalType).toBeNull();
});

test("rpc frames fall back to an error reply with the original id", () => {
	const result: Record<string, unknown> = {};
	const value: Record<string, unknown> = { id: 12, result };
	result.self = value;
	expect(serializeFrame(value)).toBe(
		JSON.stringify({ id: 12, error: { code: -32000, message: "Frame is not serializable" } }),
	);
});

test("non-rpc frames fall back to a bare unserializable marker", () => {
	expect(serializeFrame({ method: "provider.login", params: {} })).toBe(
		JSON.stringify({ method: "provider.login", params: {} }),
	);
	const circular: Record<string, unknown> = {};
	circular.self = circular;
	expect(serializeFrame(circular)).toBe(JSON.stringify({ method: "host.unserializable" }));
});
