import { describe, expect, test } from "bun:test";
import { SteeringRegistry } from "../src/steering.ts";
import { FakeSession, rawFrames, rawHost, registerHostCleanup, waitForId } from "./harness.ts";

registerHostCleanup();

/**
 * AUX-03 steering spike. The host-owned generation plus the prompt preflight
 * acceptance is prototyped here, but the route stays unavailable because Pi
 * exposes no public active-run binding and no steering receipt. These checks
 * pin the two safety properties the prototype would have to satisfy.
 */
describe("Steering binding prototype (AUX-03)", () => {
	test("binds steering only to the preflight-accepted resident generation", () => {
		const registry = new SteeringRegistry();
		registry.begin("s", 1);
		const bound = registry.evaluate("s", 1, true);
		expect(bound.ok).toBe(true);
		const idle = registry.evaluate("s", 1, false);
		expect(idle.ok).toBe(false);
		if (!idle.ok) expect(idle.reason).toContain("streaming");
	});

	test("rejects steering after the resident was replaced", () => {
		const registry = new SteeringRegistry();
		registry.begin("s", 1);
		// A replacement allocation has a new generation and retires the old
		// binding; neither the old nor the new generation safely binds.
		registry.retire("s", 1);
		expect(registry.evaluate("s", 1, true).ok).toBe(false);
		registry.begin("s", 2);
		expect(registry.evaluate("s", 1, true)).toMatchObject({ ok: false });
		expect(registry.evaluate("s", 2, true).ok).toBe(true);
	});

	test("rejects steering while compaction is in progress", () => {
		const registry = new SteeringRegistry();
		registry.begin("s", 1);
		registry.noteCompaction("s", true);
		expect(registry.evaluate("s", 1, true)).toMatchObject({
			ok: false,
			reason: "compaction is in progress",
		});
		registry.noteCompaction("s", false);
		expect(registry.evaluate("s", 1, true).ok).toBe(true);
	});

	test("keeps session.steer unavailable and dispatches nothing to Pi", async () => {
		const session = new FakeSession("steer-off");
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({
			id: 3,
			method: "session.steer",
			params: { sessionId: "steer-off", content: [{ type: "text", text: "steer" }] },
		});
		const refused = await waitForId(raw.socket, 3);
		expect(refused.error?.message).toContain("run identifier");
		expect(session.steered).toEqual([]);
		expect(rawFrames(raw.socket).some((frame) => frame.method === "session.event")).toBe(false);
	});
});
