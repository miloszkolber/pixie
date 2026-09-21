import { describe, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
import {
	createPiSdkApi,
	type PiSession,
	startBunHostFromPublicApi,
} from "../../src/assistant/host.ts";
import { createHostLogger } from "../../src/assistant/log.ts";
import {
	credential,
	expectSecretFree,
	FakeSession,
	type HostFrame,
	helloV2,
	hostWith,
	next,
	operatorPath,
	operatorUrl,
	parseFrame,
	publicPiRootApi,
	type RawSocket,
	rawFrames,
	rawHost,
	registerHostCleanup,
	request,
	secret,
	socket,
	tempAgentDir,
	verifiedPi,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

describe("Pi public root SDK adapter", () => {
	test("accepts only 0.86.1 and derives its native session directory without a public helper", () => {
		const agentDir = tempAgentDir();
		const root = new Proxy(publicPiRootApi(), {
			get(target, property, receiver) {
				if (property === "getDefaultSessionDir")
					throw new Error("adapter must not read a non-public session-directory helper");
				return Reflect.get(target, property, receiver);
			},
		});
		const sdk = createPiSdkApi(verifiedPi(), root);
		const cwd = "/work:tree/nested\\segment";
		const directory = sdk.getDefaultSessionDir(cwd, agentDir);
		expect(directory).toBe(join(resolve(agentDir), "sessions", "--work-tree-nested-segment--"));
		expect(existsSync(directory)).toBe(true);
	});

	test("rejects an unsupported version before a host can open a session", () => {
		let opened = false;
		expect(() =>
			startBunHostFromPublicApi(
				{
					host: "127.0.0.1",
					port: 0,
					secret,
					agentDir: tempAgentDir(),
					verifiedPi: verifiedPi("0.85.2"),
				},
				publicPiRootApi(() => {
					opened = true;
				}),
			),
		).toThrow("requires version 0.86.1");
		expect(opened).toBe(false);
	});

	test("requires every public session API shape", () => {
		const missingCreateAgentSession = { ...publicPiRootApi(), createAgentSession: undefined };
		const missingManagerCreate = {
			...publicPiRootApi(),
			SessionManager: { ...publicPiRootApi().SessionManager, create: undefined },
		};
		const missingManagerList = {
			...publicPiRootApi(),
			SessionManager: { ...publicPiRootApi().SessionManager, list: undefined },
		};
		const missingManagerOpen = {
			...publicPiRootApi(),
			SessionManager: { ...publicPiRootApi().SessionManager, open: undefined },
		};
		for (const root of [
			missingCreateAgentSession,
			missingManagerCreate,
			missingManagerList,
			missingManagerOpen,
		])
			expect(() => createPiSdkApi(verifiedPi(), root)).toThrow("required session API");
	});
});

describe("Bun host v1", () => {
	test("requires auth and reports the compatible hello operation set", async () => {
		const { host } = hostWith();
		expect(
			(await fetch(host.endpoint.replace("ws:", "http:").replace("/pi", "/livez"))).status,
		).toBe(200);
		expect(
			(await fetch(host.endpoint.replace("ws:", "http:").replace("/pi", "/readyz"))).status,
		).toBe(401);
		await expect(socket(host, "wrong")).rejects.toThrow("websocket open failed");
		const ws = await socket(host);
		const hello = await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		expect(hello.result?.protocolVersion).toBe(1);
		expect(hello.result?.version).toBe("0.86.1");
		expect(hello.result?.capabilities).toEqual({ sessions: 1, agents: 1, images: 1 });
		for (const name of [
			"session.list",
			"session.create",
			"session.load",
			"session.prompt",
			"session.cancel",
		]) {
			expect(hello.result?.operationSet?.[name]).toBe(true);
		}
		expect(hello.result?.operationSet?.["session.delete"]).toBe(false);
		ws.close();
	});

	test("preserves raw SDK message events and drops queue updates", async () => {
		const { host, session } = hostWith();
		const ws = await socket(host);
		await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		await request(ws, 2, "session.create", { cwd: process.cwd() });
		const event = next(ws);
		session.emit({ type: "message_end", message: { role: "assistant", nested: { exact: true } } });
		expect(await event).toMatchObject({
			method: "session.event",
			params: {
				sessionId: "native-1",
				event: { type: "message_end", message: { role: "assistant", nested: { exact: true } } },
			},
		});
		let received = false;
		ws.onmessage = () => {
			received = true;
		};
		session.emit({ type: "queue_update", steering: ["never"], followUp: [] });
		await Bun.sleep(20);
		expect(received).toBe(false);
		ws.close();
	});

	test("queues settlement events before the prompt response and cancels via clearQueue then abort", async () => {
		const { host, session } = hostWith();
		const ws = await socket(host);
		await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		await request(ws, 2, "session.create", { cwd: process.cwd() });
		const messages: HostFrame[] = [];
		ws.onmessage = (event) => messages.push(parseFrame(event.data));
		ws.send(
			JSON.stringify({
				id: 3,
				method: "session.prompt",
				params: { sessionId: "native-1", content: [{ type: "text", text: "hi" }] },
			}),
		);
		for (let attempt = 0; messages.length < 3 && attempt < 50; attempt += 1) {
			await Bun.sleep(2);
		}
		expect(messages.slice(0, -1).every((message) => message.method === "session.event")).toBe(true);
		expect(messages.at(-1)).toMatchObject({ id: 3, result: { stopReason: "stop" } });
		await request(ws, 4, "session.cancel", { sessionId: "native-1" });
		expect(session.calls.slice(-2)).toEqual(["clearQueue", "abort"]);
		ws.close();
	});

	test("does not let a response settle another dialog request and releases the session", async () => {
		const { host, session } = hostWith();
		const ws = await socket(host);
		await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		await request(ws, 2, "session.create", { cwd: process.cwd() });
		const ui = session.bound;
		if (!ui) throw new Error("session extensions were not bound");
		const first = (ui.input as (title: string) => Promise<string | undefined>)("first");
		const second = (ui.input as (title: string) => Promise<string | undefined>)("second");
		const events: HostFrame[] = [];
		ws.onmessage = (event) => events.push(parseFrame(event.data));
		await Bun.sleep(2);
		const firstId = events[0]?.params?.event?.requestId;
		const secondId = events[1]?.params?.event?.requestId;
		const bad = await request(ws, 3, "session.uiResponse", {
			sessionId: "native-1",
			requestId: "not-the-second",
			result: { value: "x" },
		});
		expect(bad.error?.message).toContain("unknown extension dialog");
		let secondSettled = false;
		void second.then(() => {
			secondSettled = true;
		});
		await Bun.sleep(2);
		expect(secondSettled).toBe(false);
		await request(ws, 4, "session.uiResponse", {
			sessionId: "native-1",
			requestId: firstId,
			result: { value: "one" },
		});
		expect(await first).toBe("one");
		expect(secondSettled).toBe(false);
		await request(ws, 5, "session.uiResponse", {
			sessionId: "native-1",
			requestId: secondId,
			result: { value: "two" },
		});
		expect(await second).toBe("two");
		await request(ws, 6, "session.release", { sessionId: "native-1", cwd: process.cwd() });
		expect(session.disposed).toBe(true);
		ws.close();
	});
});

describe("Bun host protocol and lifecycle boundaries", () => {
	test("normalizes omitted and null v1 params only", async () => {
		const { socket, send } = rawHost();
		await send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await send({ id: 2, method: "session.list" });
		await send({ id: 3, method: "session.list", params: null });
		expect(rawFrames(socket).slice(-2)).toMatchObject([
			{ id: 2, result: { sessions: [] } },
			{ id: 3, result: { sessions: [] } },
		]);
	});

	test("auto selects the highest offered protocol and strict v2 refuses downgrade", async () => {
		const auto = rawHost(new FakeSession("auto"), { protocol: "auto" });
		await auto.send({
			id: 1,
			method: "runtime.hello",
			params: {
				protocolVersion: 1,
				supportedProtocolVersions: [2, 1],
				preferProtocolVersion: 2,
			},
		});
		const hello = rawFrames(auto.socket).at(-1);
		expect(hello?.result).toMatchObject({
			protocolVersion: 2,
			supportedProtocolVersions: [2, 1],
			hostIdentity: expect.stringMatching(/^[a-f0-9]{32}$/),
		});
		expect(hello?.result?.operationSet?.["runtime.restart"]).toBe(false);
		await auto.send({ id: 2, method: "runtime.restart", params: {} });
		expect(rawFrames(auto.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: {
					code: -32004,
					reason: "capability_unavailable",
					message: expect.any(String),
				},
			}),
		);

		const strict = rawHost(new FakeSession("strict"), { protocol: "v2" });
		await strict.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		expect(strict.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));
		const strictV2 = rawHost(new FakeSession("strict-v2"), { protocol: "v2" });
		await strictV2.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 2, supportedProtocolVersions: [2, 1] },
		});
		expect(rawFrames(strictV2.socket).at(-1)?.result?.protocolVersion).toBe(2);
		await strictV2.send({ id: 2, method: "session.list", params: null });
		expect(strictV2.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1008 }));
	});

	test("persists the Go-compatible host identity across host restarts", async () => {
		const agentDir = tempAgentDir();
		const first = rawHost(new FakeSession("first"), { agentDir });
		await first.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		const firstHello = rawFrames(first.socket).at(-1)?.result;
		await first.host.close();
		const second = rawHost(new FakeSession("second"), { agentDir });
		await second.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		const secondHello = rawFrames(second.socket).at(-1)?.result;
		expect(secondHello?.runtimeId).toBe(firstHello?.runtimeId);
		expect(secondHello?.bootId).not.toBe(firstHello?.bootId);
		const info = Bun.file(join(agentDir, "pixie", "host-identity.json"));
		expect(await info.json()).toEqual({ version: 1, identity: firstHello?.runtimeId });
	});

	test("uses one explicit Pi session directory for create, list, and open", async () => {
		const calls: unknown[][] = [];
		const agentDir = tempAgentDir();
		const sessionDir = join(agentDir, "chosen-sessions");
		const sdk = {
			getDefaultSessionDir(cwd: string, selectedAgentDir: string) {
				calls.push(["dir", cwd, selectedAgentDir]);
				return sessionDir;
			},
			SessionManager: {
				create(cwd: string, directory: string) {
					calls.push(["create", cwd, directory]);
					return { kind: "create" };
				},
				async list(cwd: string, directory: string) {
					calls.push(["list", cwd, directory]);
					return [{ id: "loaded", path: "/sessions/loaded.jsonl", cwd }];
				},
				open(path: string, directory: string, cwd: string) {
					calls.push(["open", path, directory, cwd]);
					return { kind: "open" };
				},
			},
		};
		const sessionFactory = async ({ sessionManager }: { sessionManager?: unknown }) => ({
			session: new FakeSession(
				(sessionManager as { kind: string }).kind === "open" ? "loaded" : "created",
			),
		});
		const raw = rawHost(new FakeSession("unused"), { agentDir, sdk, sessionFactory });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		const cwd = process.cwd();
		await raw.send({ id: 2, method: "session.create", params: { cwd } });
		await raw.send({ id: 3, method: "session.list", params: { cwd } });
		await raw.send({
			id: 4,
			method: "session.load",
			params: { cwd, sessionId: "loaded" },
		});
		expect(calls).toContainEqual(["create", cwd, sessionDir]);
		expect(calls).toContainEqual(["list", cwd, sessionDir]);
		expect(calls).toContainEqual(["open", "/sessions/loaded.jsonl", sessionDir, cwd]);
	});

	test("closes a v2 observer when serialized output exceeds its bounded backlog", async () => {
		const session = new FakeSession("oversize");
		const raw = rawHost(session, { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const payload = "x".repeat(17 * 1024 * 1024);
		session.emit({ type: "message_update", payload });
		session.emit({ type: "message_update", payload });
		await Bun.sleep(0);
		expect(raw.socket.closes.at(-1)).toEqual(expect.objectContaining({ code: 1013 }));
	});

	test("disposes a session constructed after the host starts draining", async () => {
		let resolveSession: ((value: { session: PiSession }) => void) | undefined;
		const pending = new Promise<{ session: PiSession }>((resolve) => {
			resolveSession = resolve;
		});
		const created = new FakeSession("late");
		const raw = rawHost(new FakeSession("unused"), { sessionFactory: async () => pending });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		raw.socket.sent.length = 0;
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const closing = raw.host.close();
		if (!resolveSession) throw new Error("session factory did not start");
		resolveSession({ session: created });
		await closing;
		expect(created.disposed).toBe(true);
		// AUX-18: the in-flight create is failed as delivery-uncertain instead
		// of being dropped without a reply.
		expect(rawFrames(raw.socket)).toContainEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({
					code: -32000,
					message: expect.stringContaining("stopping"),
				}),
			}),
		);
		expect(raw.fetch()).toEqual(expect.objectContaining({ status: 503 }));
	});
});

describe("Bun host dialog shape compatibility", () => {
	test("accepts controller top-level and legacy nested dialog answers", async () => {
		const session = new FakeSession("dialog-shape");
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const ui = session.bound;
		if (!ui) throw new Error("extensions not bound");
		const topLevel = (ui.input as (title: string) => Promise<string | undefined>)("top");
		const legacy = (ui.input as (title: string) => Promise<string | undefined>)("legacy");
		await Bun.sleep(2);
		const frames = rawFrames(raw.socket);
		const topId = (frames.at(-2)?.params as unknown as { event: { requestId: string } })?.event
			?.requestId;
		const legacyId = (frames.at(-1)?.params as unknown as { event: { requestId: string } })?.event
			?.requestId;
		await raw.send({
			id: 3,
			method: "session.uiResponse",
			params: { sessionId: "dialog-shape", requestId: topId, value: "top-value" },
		});
		expect(rawFrames(raw.socket).at(-1)).toMatchObject({ id: 3, result: {} });
		expect(await topLevel).toBe("top-value");
		await raw.send({
			id: 4,
			method: "session.uiResponse",
			params: { sessionId: "dialog-shape", requestId: legacyId, result: { value: "legacy-value" } },
		});
		expect(await legacy).toBe("legacy-value");
		const confirmSession = new FakeSession("dialog-confirm");
		const rawConfirm = rawHost(confirmSession);
		await rawConfirm.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await rawConfirm.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const confirmUi = confirmSession.bound;
		if (!confirmUi) throw new Error("confirm extensions not bound");
		const confirm = (confirmUi.confirm as (t: string, m: string) => Promise<boolean | undefined>)(
			"ok?",
			"are you sure",
		);
		await Bun.sleep(2);
		const confirmId = (
			rawFrames(rawConfirm.socket).at(-1)?.params as unknown as { event: { requestId: string } }
		)?.event?.requestId;
		await rawConfirm.send({
			id: 3,
			method: "session.uiResponse",
			params: { sessionId: "dialog-confirm", requestId: confirmId, value: true },
		});
		expect(await confirm).toBe(true);
	});
});

describe("Bun host session UI cancel", () => {
	async function waitForFrame(
		socket: RawSocket,
		predicate: (frame: HostFrame) => boolean,
		timeoutMs = 2000,
	): Promise<HostFrame> {
		const deadline = Date.now() + timeoutMs;
		for (;;) {
			const found = rawFrames(socket).find(predicate);
			if (found) return found;
			if (Date.now() > deadline) throw new Error("timed out waiting for host frame");
			await Bun.sleep(5);
		}
	}

	function uiRequestId(frame: HostFrame): string | undefined {
		const event = frame.params?.event as { type?: unknown; requestId?: unknown } | undefined;
		return event?.type === "pixie:ui:request" && typeof event.requestId === "string"
			? event.requestId
			: undefined;
	}

	test("cancels a pending extension dialog through session.uiCancel", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		const session = new FakeSession("ui-cancel");
		const raw = rawHost(session, { logger, protocol: "auto" });
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["session.uiCancel"]).toBe(true);

		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		const ui = session.bound;
		if (!ui) throw new Error("session extensions were not bound");
		const pending = (ui.input as (title: string) => Promise<string | undefined>)("confirm cancel");
		const request = await waitForFrame(raw.socket, (frame) => uiRequestId(frame) !== undefined);
		const requestId = uiRequestId(request);
		if (!requestId) throw new Error("dialog request id missing");

		await raw.send({
			id: 3,
			method: "session.uiCancel",
			params: { sessionId: "ui-cancel", requestId },
		});
		const cancelled = await waitForId(raw.socket, 3);
		expect(cancelled).toMatchObject({ id: 3, result: {} });
		expect(await pending).toBeUndefined();
		expectSecretFree(cancelled, logger, forbidden);

		await raw.send({
			id: 4,
			method: "session.uiCancel",
			params: { sessionId: "ui-cancel", requestId: credential },
		});
		const unknownDialog = await waitForId(raw.socket, 4);
		expect(unknownDialog.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(unknownDialog.error?.message).toContain("unknown extension dialog");
		expectSecretFree(unknownDialog, logger, forbidden);

		await raw.send({
			id: 5,
			method: "session.uiCancel",
			params: { sessionId: credential, requestId },
		});
		const unknownSession = await waitForId(raw.socket, 5);
		expect(unknownSession.error?.message).toContain("unknown extension dialog");
		expectSecretFree(unknownSession, logger, forbidden);

		await raw.send({ id: 6, method: "session.uiCancel", params: { requestId } });
		const missingSession = await waitForId(raw.socket, 6);
		expect(missingSession.error?.message).toContain("sessionId");
		expectSecretFree(missingSession, logger, forbidden);
	});
});
