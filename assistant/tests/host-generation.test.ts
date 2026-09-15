import { describe, expect, test } from "bun:test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import {
	FakeSession,
	helloV2,
	rawFrames,
	rawHost,
	registerHostCleanup,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

function header(id = "native"): string {
	return `${JSON.stringify({ type: "session", version: 3, id, timestamp: new Date().toISOString(), cwd: process.cwd() })}\n`;
}

/** A session that can deliver a subscribe callback after the host unsubscribes it. */
class DeferredEmitSession extends FakeSession {
	/** Capture the live listener now, so the test can invoke it later. */
	capture(): (event: Record<string, unknown>) => void {
		const listeners = [...this.listeners];
		return (event) => {
			for (const listener of listeners) listener(event);
		};
	}
}

/** A session whose prompt settles only when the test releases the gate. */
class GatedSession extends FakeSession {
	readonly gate = Promise.withResolvers<void>();

	override async prompt(
		_text: string,
		options: { preflightResult: (accepted: boolean) => void },
	): Promise<void> {
		this.calls.push("prompt");
		options.preflightResult(true);
		await this.gate.promise;
		this.emit({
			type: "message_end",
			message: { role: "assistant", stopReason: "late-run", content: "late" },
		});
	}
}

/**
 * AUX-05: a resident's foreground callbacks carry its allocation generation.
 * A callback that was queued before the resident was replaced must not publish
 * or mutate the replacement, and a late prompt result is fenced.
 */
describe("Bun host generation guards and replacement epochs (AUX-05)", () => {
	test("discards a delayed callback captured from the replaced session", async () => {
		const file = join(rawHost(new FakeSession("setup")).agentDir, "replace.jsonl");
		writeFileSync(file, header("native"));
		const first = new DeferredEmitSession("native");
		first.sessionFile = file;
		(first as { isStreaming?: boolean }).isStreaming = false;
		const second = new FakeSession("native");
		second.sessionFile = file;
		(second as { isStreaming?: boolean }).isStreaming = false;

		const raw = rawHost(new FakeSession("unused"), {
			sdk: {
				SessionManager: {
					create: () => ({ kind: "create" }),
					list: async () => [],
					open: () => ({ kind: "open" }),
				},
			},
			sessionFactory: async ({ sessionManager }) =>
				(sessionManager as { kind?: string })?.kind === "open"
					? { session: second }
					: { session: first },
		});

		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		// The callback is captured against the old resident, then the resident
		// is replaced in place before the callback is delivered.
		const lateCallback = first.capture();
		// The file re-read path requires an advanced mtime and an idle session.
		const future = new Date(Date.now() + 5_000);
		const { utimesSync } = await import("node:fs");
		utimesSync(file, future, future);
		await raw.send({
			id: 3,
			method: "session.load",
			params: { sessionId: "native", cwd: process.cwd() },
		});
		expect((await waitForId(raw.socket, 3)).error).toBeUndefined();

		lateCallback({
			type: "message_end",
			message: { role: "assistant", stopReason: "stale-run" },
		});
		await Bun.sleep(10);
		const stale = rawFrames(raw.socket).filter(
			(frame) =>
				frame.method === "session.event" &&
				(frame.params as { event?: { message?: { stopReason?: string } } } | undefined)?.event
					?.message?.stopReason === "stale-run",
		);
		expect(stale).toEqual([]);

		// The replacement remains the live foreground target: its own events
		// still cross the boundary.
		second.emit({
			type: "message_end",
			message: { role: "assistant", stopReason: "fresh-run" },
		});
		await Bun.sleep(5);
		expect(
			rawFrames(raw.socket).some(
				(frame) =>
					(frame.params as { event?: { message?: { stopReason?: string } } } | undefined)?.event
						?.message?.stopReason === "fresh-run",
			),
		).toBe(true);
	});

	test("fences and discards a delayed prompt result from a released session", async () => {
		const session = new GatedSession("gated");
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "gated", content: [{ type: "text", text: "hi" }] },
		});
		for (let attempt = 0; !session.calls.includes("prompt") && attempt < 50; attempt += 1) {
			await Bun.sleep(5);
		}
		expect(session.calls).toContain("prompt");

		await raw.send({
			id: 4,
			method: "session.release",
			params: { sessionId: "gated", cwd: process.cwd() },
		});
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ ok: true });

		session.gate.resolve();
		const late = await waitForId(raw.socket, 3);
		expect(late.result).toBeUndefined();
		expect(late.error?.message).toContain("replaced");
	});

	test("rejects a duplicate registration for one session path with a typed conflict", async () => {
		const file = join(rawHost(new FakeSession("setup2")).agentDir, "shared.jsonl");
		writeFileSync(file, header("a"));
		const first = new FakeSession("a");
		first.sessionFile = file;
		const duplicate = new FakeSession("b");
		duplicate.sessionFile = file;
		const raw = rawHost(new FakeSession("unused"), {
			protocol: "auto",
			sdk: {
				SessionManager: {
					create: () => ({ kind: "create" }),
					list: async () => [{ id: "b", path: file, cwd: process.cwd() }],
					open: () => ({ kind: "open" }),
				},
			},
			sessionFactory: async ({ sessionManager }) =>
				(sessionManager as { kind?: string })?.kind === "open"
					? { session: duplicate }
					: { session: first },
		});
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["session.load"]).toBe(true);
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({
			id: 3,
			method: "session.load",
			params: { sessionId: "b", cwd: process.cwd() },
		});
		const refused = await waitForId(raw.socket, 3);
		expect(refused.error).toMatchObject({ code: -32003, reason: "resource_conflict" });
		expect(refused.error?.message).toContain("already registered");
		// The original registration is untouched, and the rejected session is
		// not left resident.
		expect(first.disposed).toBe(false);
		expect(duplicate.disposed).toBe(true);
		await raw.send({
			id: 4,
			method: "session.prompt",
			params: { sessionId: "a", content: [{ type: "text", text: "still here" }] },
		});
		expect((await waitForId(raw.socket, 4)).result).toMatchObject({ stopReason: "stop" });
	});
});
