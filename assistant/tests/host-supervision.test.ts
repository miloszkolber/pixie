import { describe, expect, test } from "bun:test";
import { FakeSession, rawFrames, rawHost, registerHostCleanup, waitForId } from "./harness.ts";

registerHostCleanup();

/**
 * AUX-18 host-side supervision contract. These checks exercise the host's own
 * request correlation: a hung handler is failed with a typed timeout instead
 * of holding its id forever, and a graceful stop fails every in-flight request
 * with a delivery-uncertain reply instead of dropping it silently.
 */
class HangingStatsSession extends FakeSession {
	readonly statsGate = Promise.withResolvers<void>();

	override getSessionStats(): unknown {
		this.calls.push("getSessionStats");
		return this.statsGate.promise.then(() => ({}));
	}
}

class UnserializableStatsSession extends FakeSession {
	override getSessionStats(): unknown {
		this.calls.push("getSessionStats");
		return { total: 1n };
	}
}

class GatedPromptSession extends FakeSession {
	readonly gate = Promise.withResolvers<void>();

	override async prompt(
		_text: string,
		options: { preflightResult: (accepted: boolean) => void },
	): Promise<void> {
		this.calls.push("prompt");
		options.preflightResult(true);
		await this.gate.promise;
	}
}

describe("Bun host supervision hardening (AUX-18)", () => {
	test("fails a hung request with a typed timeout and releases its id", async () => {
		const session = new HangingStatsSession("hung");
		const raw = rawHost(session, { requestTimeoutMs: 40, protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({ id: 3, method: "session.stats", params: { sessionId: "hung" } });
		const timedOut = await waitForId(raw.socket, 3);
		expect(timedOut.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(timedOut.error?.message).toContain("timed out");
		// The id is released, so the connection still serves later requests.
		await raw.send({ id: 4, method: "session.list", params: {} });
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ sessions: expect.any(Array) });
		// Let the drained work settle so host cleanup is not held by the gate.
		session.statsGate.resolve();
	});

	test("answers an unserializable handler result with a typed error instead of hanging", async () => {
		const session = new UnserializableStatsSession("unserializable");
		const raw = rawHost(session, { requestTimeoutMs: 60 });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({ id: 3, method: "session.stats", params: { sessionId: "unserializable" } });
		const failed = await waitForId(raw.socket, 3);
		expect(failed.error?.message).toContain("serialized");
		expect(failed.error?.message).not.toContain("timed out");
	});

	test("a reload preempts a prompt but a prompt cannot preempt a reload", async () => {
		const session = new FakeSession("preempt");
		const raw = rawHost(session, { protocol: "auto", allowSelfRestart: true });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({ id: 3, method: "runtime.restart", params: {} });
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({ ok: true });
		await raw.send({
			id: 4,
			method: "session.prompt",
			params: { sessionId: "preempt", content: [{ type: "text", text: "too late" }] },
		});
		const refused = await waitForId(raw.socket, 4);
		expect(refused.error).toMatchObject({
			code: -32004,
			reason: "capability_unavailable",
		});
		expect(session.calls).not.toContain("prompt");
	});

	test("does not deadline-fail a run-extending prompt", async () => {
		const session = new GatedPromptSession("long-prompt");
		const raw = rawHost(session, { requestTimeoutMs: 20, protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "long-prompt", content: [{ type: "text", text: "keep going" }] },
		});
		for (let attempt = 0; !session.calls.includes("prompt") && attempt < 50; attempt += 1) {
			await Bun.sleep(5);
		}
		await Bun.sleep(80);
		expect(rawFrames(raw.socket).some((frame) => frame.id === 3)).toBe(false);
		session.gate.resolve();
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({ stopReason: "stop" });
	});

	test("a restart preempts an in-flight prompt and reports it delivery-uncertain", async () => {
		const session = new GatedPromptSession("restarting");
		const raw = rawHost(session, { protocol: "auto", allowSelfRestart: true });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "restarting", content: [{ type: "text", text: "preempted" }] },
		});
		for (let attempt = 0; !session.calls.includes("prompt") && attempt < 50; attempt += 1) {
			await Bun.sleep(5);
		}
		expect(session.calls).toContain("prompt");

		// The reload is acknowledged immediately and is never queued behind the
		// in-flight prompt; the prompt is later failed rather than dropped.
		await raw.send({ id: 4, method: "runtime.restart", params: {} });
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ ok: true });
		const dropped = await waitForId(raw.socket, 3);
		expect(dropped.error).toMatchObject({ code: -32005, reason: "delivery_uncertain" });
		session.gate.resolve();
		await raw.host.close();
	});

	test("a graceful stop fails an in-flight prompt as delivery-uncertain", async () => {
		const session = new GatedPromptSession("closing");
		const raw = rawHost(session, { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "closing", content: [{ type: "text", text: "hello" }] },
		});
		for (let attempt = 0; !session.calls.includes("prompt") && attempt < 50; attempt += 1) {
			await Bun.sleep(5);
		}
		expect(session.calls).toContain("prompt");

		const closing = raw.host.close();
		const dropped = await waitForId(raw.socket, 3);
		expect(dropped.result).toBeUndefined();
		expect(dropped.error).toMatchObject({ code: -32005, reason: "delivery_uncertain" });
		session.gate.resolve();
		await closing;
	});

	test("reports readiness with the negotiated version in runtime.hello", async () => {
		const raw = rawHost(new FakeSession("ready"), { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		expect((await waitForId(raw.socket, 1)).result).toMatchObject({
			ready: true,
			version: "0.85.1",
		});
		expect(raw.fetch("/readyz")?.status).toBe(200);
	});

	test("closes on malformed, orphaned, and incompatible messages", async () => {
		const malformed = rawHost(new FakeSession("malformed"));
		await malformed.send("{");
		expect(malformed.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1007 }));

		const orphaned = rawHost(new FakeSession("orphaned"));
		await orphaned.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await orphaned.send({ id: 2, result: { unexpected: true } });
		expect(orphaned.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));

		const incompatible = rawHost(new FakeSession("incompatible"), { protocol: "v2" });
		await incompatible.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 2, supportedProtocolVersions: [2, 1] },
		});
		expect(rawFrames(incompatible.socket).at(-1)?.result?.protocolVersion).toBe(2);
		await incompatible.send({ id: 2, method: "session.list", params: null });
		expect(incompatible.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));
	});
});
