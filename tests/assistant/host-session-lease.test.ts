import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, utimesSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import {
	FakeSession,
	helloV2,
	rawHost,
	registerHostCleanup,
	tempAgentDir,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

function sessionFile(raw: { agentDir: string }, name = "session.jsonl"): string {
	return join(raw.agentDir, name);
}

function header(version = 3): string {
	return `${JSON.stringify({ type: "session", version, id: "native", timestamp: new Date().toISOString(), cwd: process.cwd() })}\n`;
}

function messageRecord(
	role: string,
	content: unknown,
	id = "m1",
	parentId: string | null = null,
): string {
	return `${JSON.stringify({ type: "message", id, parentId, timestamp: new Date().toISOString(), message: { role, content } })}\n`;
}

describe("Bun host session-file lease (AUX-13)", () => {
	test("refuses a mutation while a live foreign writer holds the lease", async () => {
		const session = new FakeSession("leased");
		const raw = rawHost(session, { protocol: "auto" });
		const file = sessionFile(raw);
		session.sessionFile = file;
		const lease = `${file}.lease`;
		writeFileSync(lease, JSON.stringify({ pid: 1, acquiredAt: Date.now(), runtimeId: "foreign" }));
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["session.prompt"]).toBe(true);
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "leased", content: [{ type: "text", text: "hi" }] },
		});
		const refused = await waitForId(raw.socket, 3);
		expect(refused.error?.code).toBe(-32003);
		expect(refused.error?.reason).toBe("resource_conflict");
		expect(refused.error?.message).toContain("live Pi writer");
		expect(session.calls).not.toContain("prompt");
		// The foreign lease is untouched: we never clobber another owner.
		expect(JSON.parse(readFileSync(lease, "utf8")).runtimeId).toBe("foreign");
	});

	test("reclaims a stale lease and removes it after the mutation", async () => {
		const session = new FakeSession("stale");
		const raw = rawHost(session);
		const file = sessionFile(raw);
		session.sessionFile = file;
		const lease = `${file}.lease`;
		writeFileSync(lease, JSON.stringify({ pid: 1, acquiredAt: 0, runtimeId: "dead-owner" }));
		const old = new Date(Date.now() - 10 * 60_000);
		utimesSync(lease, old, old);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "stale", content: [{ type: "text", text: "hi" }] },
		});
		expect((await waitForId(raw.socket, 3)).error).toBeUndefined();
		expect(session.calls).toContain("prompt");
		expect(existsSync(lease)).toBe(false);
	});

	test("reclaims a lease whose pid is no longer alive", async () => {
		const session = new FakeSession("dead");
		const raw = rawHost(session);
		const file = sessionFile(raw);
		session.sessionFile = file;
		writeFileSync(
			`${file}.lease`,
			JSON.stringify({ pid: 2_147_483_647, acquiredAt: Date.now(), runtimeId: "gone" }),
		);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: { sessionId: "dead", content: [{ type: "text", text: "hi" }] },
		});
		expect((await waitForId(raw.socket, 3)).error).toBeUndefined();
	});

	test("re-reads a non-streaming session when the file mtime advances", async () => {
		const file = join(tempAgentDir(), "tail.jsonl");
		writeFileSync(file, header());
		const first = new FakeSession("native");
		first.sessionFile = file;
		(first as { isStreaming?: boolean }).isStreaming = false;
		first.messages.push({ role: "user", content: "before" });
		const second = new FakeSession("native");
		second.sessionFile = file;
		(second as { isStreaming?: boolean }).isStreaming = false;
		second.messages.push({ role: "user", content: "after" });
		let created = false;
		const raw = rawHost(first, {
			sessionFactory: async ({ sessionManager }) => {
				const kind = (sessionManager as { kind?: string })?.kind;
				if (kind === "open") return { session: second };
				created = true;
				return { session: first };
			},
		});
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).result).toMatchObject({ sessionId: "native" });
		expect(created).toBe(true);
		const future = new Date(Date.now() + 5_000);
		utimesSync(file, future, future);
		await raw.send({
			id: 3,
			method: "session.load",
			params: { sessionId: "native", cwd: process.cwd() },
		});
		const reloaded = await waitForId(raw.socket, 3);
		expect(reloaded.error).toBeUndefined();
		const messages =
			(reloaded.result as { messages?: Array<{ content?: unknown }> }).messages ?? [];
		expect(messages).toEqual([expect.objectContaining({ content: "after" })]);
		expect(first.disposed).toBe(true);
	});
});

describe("Bun host session schema guard and repair (AUX-12)", () => {
	test("refuses a session written by a newer runtime instead of mis-parsing it", async () => {
		const session = new FakeSession("future");
		const raw = rawHost(session);
		const file = sessionFile(raw, "future.jsonl");
		session.sessionFile = file;
		const original = header(4);
		writeFileSync(file, original);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const refused = await waitForId(raw.socket, 2);
		expect(refused.error?.message).toContain("newer Pi runtime");
		expect(session.disposed).toBe(true);
		// The native file is never rewritten.
		expect(readFileSync(file, "utf8")).toBe(original);
	});

	test("drops unknown records and parse failures without rewriting the file", async () => {
		const session = new FakeSession("degraded");
		const raw = rawHost(session);
		const file = sessionFile(raw, "degraded.jsonl");
		session.sessionFile = file;
		const body = `${header(3)}${JSON.stringify({ type: "future_record", id: "u1", parentId: null })}\nnot json\n${messageRecord("user", "kept")}`;
		writeFileSync(file, body);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const loaded = await waitForId(raw.socket, 2);
		expect(loaded.error).toBeUndefined();
		expect(
			(loaded.result as { metadata?: { sessionSchema?: unknown } }).metadata?.sessionSchema,
		).toMatchObject({
			version: 3,
			writtenByNewerRuntime: false,
			unknownRecords: 1,
			invalidRecords: 1,
		});
		expect(readFileSync(file, "utf8")).toBe(body);
	});

	test("repairs a dangling tool call by appending a synthetic result on reopen", async () => {
		const session = new FakeSession("dangling");
		const raw = rawHost(session);
		const file = sessionFile(raw, "dangling.jsonl");
		session.sessionFile = file;
		(session as { isStreaming?: boolean }).isStreaming = false;
		session.messages.push({
			role: "assistant",
			content: [{ type: "toolCall", id: "call-1", name: "bash", arguments: {} }],
		});
		writeFileSync(
			file,
			`${header(3)}${messageRecord("assistant", [{ type: "toolCall", id: "call-1", name: "bash", arguments: {} }])}`,
		);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({ id: 3, method: "session.getMessages", params: { sessionId: "dangling" } });
		const repaired = (await waitForId(raw.socket, 3)).result as {
			messages: Array<{ role?: string; toolCallId?: string; details?: unknown }>;
		};
		expect(repaired.messages.at(-1)).toMatchObject({
			role: "toolResult",
			toolCallId: "call-1",
			details: { synthetic: true },
		});
	});

	test("does not synthesize a result while the session is streaming", async () => {
		const session = new FakeSession("streaming");
		const raw = rawHost(session);
		const file = sessionFile(raw, "streaming.jsonl");
		session.sessionFile = file;
		(session as { isStreaming?: boolean }).isStreaming = true;
		session.messages.push({
			role: "assistant",
			content: [{ type: "toolCall", id: "call-live", name: "bash", arguments: {} }],
		});
		writeFileSync(file, header(3));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({ id: 3, method: "session.getMessages", params: { sessionId: "streaming" } });
		const live = (await waitForId(raw.socket, 3)).result as {
			messages: Array<{ role?: string }>;
		};
		expect(live.messages.some((message) => message.role === "toolResult")).toBe(false);
	});
});
