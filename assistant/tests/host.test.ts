import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import {
	HOST_OPERATION_STATUS,
	HOST_OPERATIONS,
} from "../../shared/src/generated/protocol-catalog.ts";
import {
	type BunServerFactory,
	type BunWebSocket,
	createPiSdkApi,
	type PiSession,
	startBunHost,
	startBunHostFromPublicApi,
} from "../src/host.ts";

type HostResult = {
	readonly bootId?: string;
	readonly capabilities?: unknown;
	readonly hostIdentity?: string;
	readonly operationSet?: Record<string, boolean>;
	readonly protocolVersion?: number;
	readonly runtimeId?: string;
	readonly supportedProtocolVersions?: readonly number[];
	readonly version?: string;
};

type HostFrame = {
	readonly error?: { readonly code?: number; readonly message?: string; readonly reason?: string };
	readonly id?: number;
	readonly method?: string;
	readonly params?: { readonly event?: { readonly requestId?: string } };
	readonly result?: HostResult;
};

const secret = "s".repeat(32);
const hosts: Array<ReturnType<typeof startBunHost>> = [];
const agentDirs: string[] = [];

afterEach(async () => {
	await Promise.all(hosts.splice(0).map((host) => host.close()));
	for (const directory of agentDirs.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
});

class FakeSession implements PiSession {
	readonly messages: unknown[] = [];
	readonly listeners = new Set<(event: Record<string, unknown>) => void>();
	bound?: Record<string, unknown>;
	disposed = false;
	readonly calls: string[] = [];
	sessionName?: string;
	compactInstructions?: string;
	steered: Array<{ text: string; images: unknown[] }> = [];
	followed: Array<{ text: string; images: unknown[] }> = [];
	model?: unknown;
	thinkingLevel?: unknown;
	thinkingLevels: unknown[] = ["off"];
	sessionFile?: string;
	modelRuntime?: PiSession["modelRuntime"];
	extensionRunner?: PiSession["extensionRunner"];
	promptTemplates?: PiSession["promptTemplates"];
	resourceLoader?: PiSession["resourceLoader"];

	constructor(readonly sessionId: string) {}

	subscribe(listener: (event: Record<string, unknown>) => void): () => void {
		this.listeners.add(listener);
		return () => this.listeners.delete(listener);
	}

	emit(event: Record<string, unknown>): void {
		for (const listener of this.listeners) listener(event);
	}

	async prompt(
		_text: string,
		options: { preflightResult: (accepted: boolean) => void },
	): Promise<void> {
		this.calls.push("prompt");
		options.preflightResult(true);
		this.emit({
			type: "message_end",
			message: { role: "assistant", stopReason: "stop", content: "kept" },
		});
		this.emit({ type: "agent_settled" });
	}

	clearQueue(): void {
		this.calls.push("clearQueue");
	}

	async abort(): Promise<void> {
		this.calls.push("abort");
	}

	async waitForIdle(): Promise<void> {
		this.calls.push("waitForIdle");
	}

	dispose(): void {
		this.disposed = true;
	}

	async bindExtensions(bindings: {
		mode: "rpc";
		uiContext: Record<string, unknown>;
	}): Promise<void> {
		this.bound = bindings.uiContext;
	}

	async steer(text: string, images: unknown[] = []): Promise<void> {
		this.calls.push("steer");
		this.steered.push({ text, images });
	}

	async followUp(text: string, images: unknown[] = []): Promise<void> {
		this.calls.push("followUp");
		this.followed.push({ text, images });
	}

	async compact(customInstructions?: string): Promise<unknown> {
		this.calls.push("compact");
		this.compactInstructions = customInstructions;
		return { ok: true, customInstructions };
	}

	setSessionName(name: string): void {
		this.calls.push("setSessionName");
		this.sessionName = name;
	}

	getSessionStats(): unknown {
		this.calls.push("getSessionStats");
		return { sessionId: this.sessionId, userMessages: 1, assistantMessages: 1 };
	}

	getAvailableThinkingLevels(): readonly unknown[] {
		return this.thinkingLevels;
	}

	async setModel(model: unknown): Promise<void> {
		this.calls.push("setModel");
		this.model = model;
	}

	setThinkingLevel(level: unknown): void {
		this.calls.push("setThinkingLevel");
		this.thinkingLevel = level;
	}
}

function tempAgentDir(): string {
	const directory = mkdtempSync(join("/dev/shm", "pixie-bun-host-"));
	agentDirs.push(directory);
	return directory;
}

function verifiedPi(packageVersion = "0.85.1") {
	return {
		packageName: "@earendil-works/pi-coding-agent" as const,
		packageVersion,
		packageDir: "/pi",
		entryPath: "/pi/index.js",
		manifestDigest: "a".repeat(64),
		entryDigest: "b".repeat(64),
	};
}

function publicPiRootApi(opened: () => void = () => {}) {
	return {
		createAgentSession: async () => ({ session: new FakeSession("public-api") }),
		SessionManager: {
			create: () => ({ kind: "create" }),
			list: async () => [],
			open: () => {
				opened();
				return { kind: "open" };
			},
		},
	};
}

function hostWith(
	session = new FakeSession("native-1"),
	options: {
		readonly agentDir?: string;
		readonly serverFactory?: BunServerFactory;
		readonly sessionFactory?: Parameters<typeof startBunHost>[0]["sessionFactory"];
		readonly sdk?: Record<string, unknown>;
		readonly protocol?: string;
	} = {},
) {
	const agentDir = options.agentDir ?? tempAgentDir();
	const sessionDir = (cwd: string) => join(agentDir, "sessions", cwd.replaceAll("/", "-"));
	const sdk = {
		createAgentSession: async () => ({ session }),
		getDefaultSessionDir: sessionDir,
		SessionManager: {
			create: (cwd: string, directory: string) => ({ cwd, directory, kind: "create" }),
			list: async () => [],
			open: (path: string, directory: string, cwd: string) => ({
				path,
				directory,
				cwd,
				kind: "open",
			}),
		},
		...options.sdk,
	};
	const previousProtocol = process.env.PIXIE_PI_PROTOCOL;
	if (options.protocol === undefined) delete process.env.PIXIE_PI_PROTOCOL;
	else process.env.PIXIE_PI_PROTOCOL = options.protocol;
	let host: ReturnType<typeof startBunHost>;
	try {
		host = startBunHost({
			host: "127.0.0.1",
			port: 0,
			secret,
			agentDir,
			verifiedPi: {
				packageName: "@earendil-works/pi-coding-agent",
				packageVersion: "0.85.1",
				packageDir: "/pi",
				entryPath: "/pi/index.js",
				manifestDigest: "a".repeat(64),
				entryDigest: "b".repeat(64),
			},
			sdk: sdk as Parameters<typeof startBunHost>[0]["sdk"],
			serverFactory: options.serverFactory,
			sessionFactory: options.sessionFactory,
		});
	} finally {
		if (previousProtocol === undefined) delete process.env.PIXIE_PI_PROTOCOL;
		else process.env.PIXIE_PI_PROTOCOL = previousProtocol;
	}
	hosts.push(host);
	return { host, session, agentDir };
}

async function socket(host: ReturnType<typeof startBunHost>, token = secret): Promise<WebSocket> {
	const ws = new WebSocket(host.endpoint, {
		headers: { Authorization: `Bearer ${token}` },
	} as unknown as string[]);
	await new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("websocket open failed"));
	});
	return ws;
}

function parseFrame(raw: string): HostFrame {
	return JSON.parse(raw) as HostFrame;
}

function next(ws: WebSocket): Promise<HostFrame> {
	return new Promise((resolve) => {
		ws.onmessage = (event) => resolve(parseFrame(event.data));
	});
}

async function request(
	ws: WebSocket,
	id: number,
	method: string,
	params: Record<string, unknown>,
): Promise<HostFrame> {
	const result = next(ws);
	ws.send(JSON.stringify({ id, method, params }));
	return result;
}

class RawSocket implements BunWebSocket {
	readonly sent: string[] = [];
	readonly closes: Array<{ code?: number; reason?: string }> = [];

	send(data: string): number {
		this.sent.push(data);
		return 0;
	}

	close(code?: number, reason?: string): void {
		this.closes.push({ code, reason });
	}
}

interface RawHost {
	readonly socket: RawSocket;
	readonly send: (value: unknown) => Promise<void>;
	readonly fetch: (path?: string) => Response | undefined;
}

function rawHost(
	session = new FakeSession("native-1"),
	options: Omit<Parameters<typeof hostWith>[1], "serverFactory"> = {},
): ReturnType<typeof hostWith> & RawHost {
	let served: Parameters<BunServerFactory["serve"]>[0] | undefined;
	let upgraded: { data: { connection: unknown } } | undefined;
	const factory: BunServerFactory = {
		serve(options_) {
			served = options_;
			return {
				port: 30123,
				stop() {},
				upgrade(_request, upgrade) {
					upgraded = upgrade as { data: { connection: unknown } };
					return true;
				},
			};
		},
	};
	const created = hostWith(session, { ...options, serverFactory: factory });
	if (!served) throw new Error("fake server did not start");
	const rawServer = served;
	const response = rawServer.fetch(
		new Request("http://127.0.0.1:30123/pi", {
			headers: { Authorization: `Bearer ${secret}` },
		}),
		{
			port: 30123,
			stop() {},
			upgrade(_request, upgrade) {
				upgraded = upgrade as { data: { connection: unknown } };
				return true;
			},
		},
	);
	if (response !== undefined || !upgraded) throw new Error("fake websocket upgrade failed");
	const rawSocket = new RawSocket();
	const rawConnection = upgraded;
	const socket = Object.assign(rawSocket, { data: rawConnection.data }) as Parameters<
		typeof rawServer.websocket.open
	>[0];
	rawServer.websocket.open(socket);
	return {
		...created,
		socket: rawSocket,
		async send(value: unknown): Promise<void> {
			rawServer.websocket.message(
				socket,
				typeof value === "string" ? value : JSON.stringify(value),
			);
			await Bun.sleep(0);
		},
		fetch(path = "/pi"): Response | undefined {
			return rawServer.fetch(
				new Request(`http://127.0.0.1:30123${path}`, {
					headers: { Authorization: `Bearer ${secret}` },
				}),
				{
					port: 30123,
					stop() {},
					upgrade(_request, upgrade) {
						upgraded = upgrade as { data: { connection: unknown } };
						return true;
					},
				},
			);
		},
	};
}

function rawFrames(socket: RawSocket): HostFrame[] {
	return socket.sent.map((frame) => parseFrame(frame));
}

async function waitForId(socket: RawSocket, id: number, timeoutMs = 5000): Promise<HostFrame> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const found = rawFrames(socket).find((frame) => frame.id === id);
		if (found) return found;
		if (Date.now() > deadline) throw new Error(`timed out waiting for host frame ${id}`);
		await Bun.sleep(5);
	}
}

describe("Pi public root SDK adapter", () => {
	test("accepts only 0.85.1 and derives its native session directory without a public helper", () => {
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
		).toThrow("requires version 0.85.1");
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
		expect(hello.result?.version).toBe("0.85.1");
		expect(hello.result?.capabilities).toEqual({ sessions: 1, agents: 1 });
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
		expect(await event).toEqual({
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
		expect(messages.at(-1)).toEqual({ id: 3, result: { stopReason: "stop" } });
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
		expect(rawFrames(socket).slice(-2)).toEqual([
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
		await strictV2.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 2 } });
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
		expect(raw.socket.sent).toEqual([]);
		expect(raw.fetch()).toEqual(expect.objectContaining({ status: 503 }));
	});
});

describe("Bun host operationSet truthfulness", () => {
	test("advertises every catalog operation with truthful enablement", async () => {
		const raw = rawHost(new FakeSession("truth"), { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		const hello = rawFrames(raw.socket).at(-1)?.result;
		expect(Object.keys(hello?.operationSet ?? {}).sort()).toEqual([...HOST_OPERATIONS].sort());
		// Every advertised bit is derived from the catalog status, except the
		// opt-in restart route which stays false unless explicitly enabled.
		for (const name of HOST_OPERATIONS) {
			const expected =
				name === "runtime.restart" ? false : HOST_OPERATION_STATUS[name] === "available";
			expect(hello?.operationSet?.[name]).toBe(expected);
		}
		for (const name of [
			"session.configure",
			"session.fork",
			"session.clone",
			"session.compact",
			"session.rename",
			"session.commands",
			"session.followUp",
			"session.clearQueue",
			"session.switch",
			"session.getMessages",
			"session.stats",
			"session.prompt.image",
			"pi.sources.list",
			"pi.sources.create",
			"pi.sources.update",
			"pi.sources.delete",
			"pi.agent-mentions.list",
			"pi.mcp.servers.read",
			"pi.mcp.servers.probe",
			"pi.providers.list",
			"pi.providers.config.read",
			"pi.defaults.read",
			"pi.preferences.read",
			"pi.extensions.list",
			"pi.slash-commands.list",
			"provider.loginStart",
		]) {
			expect(hello?.operationSet?.[name]).toBe(true);
		}
		for (const name of [
			"session.delete",
			"session.archive",
			"session.steer",
			"session.prompt.resource",
			"mcp.attach",
			"pi.session.info",
			"pi.session.steer",
			"pi.tools.list",
			"pi.tools.call",
			"runtime.capabilities",
			"pi.subagent.execute",
			"pi.todo.plan",
			"pi.llama",
			"pi.native-extensions",
		]) {
			expect(hello?.operationSet?.[name]).toBe(false);
		}
		// A catalogued-but-unavailable route fails closed with the catalog's
		// stated reason instead of inventing an implementation.
		await raw.send({ id: 5, method: "pi.session.steer", params: {} });
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("public run identifier");
		await raw.send({ id: 2, method: "session.delete", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
		await raw.send({ id: 3, method: "nope.unknown", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 3,
				error: expect.objectContaining({ code: -32601, reason: "method_not_found" }),
			}),
		);
		await raw.send({ id: 4, method: "pi.tools.list", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 4,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
	});

	test("supports image prompts and fails resource prompts closed", async () => {
		const raw = rawHost(new FakeSession("images"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [
					{ type: "text", text: "look" },
					{ type: "image", data: "aGVsbG8=", mimeType: "image/png" },
				],
			},
		});
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({ id: 3, result: expect.objectContaining({ stopReason: "stop" }) }),
		);
		await raw.send({
			id: 4,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [{ type: "resource", resource: { text: "x" } }],
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("resource");
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
		expect(rawFrames(raw.socket).at(-1)).toEqual({ id: 3, result: {} });
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

describe("Bun host session parity dispatch", () => {
	test("projects the native model and thinking state for create, configure, load, and fork", async () => {
		const models = [
			{ id: "alpha", provider: "test", name: "Alpha" },
			{ id: "beta", provider: "test", name: "Beta" },
			{ id: "gamma", provider: "other", name: "Gamma" },
		];
		const configuredSession = (id: string, modelId: string, provider: string, thinking: string) => {
			const session = new FakeSession(id);
			session.model = models.find((model) => model.id === modelId && model.provider === provider);
			session.thinkingLevel = thinking;
			session.thinkingLevels = ["off", "low", "medium", "high"];
			session.modelRuntime = {
				getModels: (wantedProvider?: string) =>
					wantedProvider ? models.filter((model) => model.provider === wantedProvider) : models,
				getModel: (wantedProvider: string, wantedModel: string) =>
					models.find((model) => model.provider === wantedProvider && model.id === wantedModel),
			};
			return session;
		};
		const created = configuredSession("created", "alpha", "test", "medium");
		created.sessionFile = "/sessions/created.jsonl";
		const loaded = configuredSession("loaded", "gamma", "other", "low");
		loaded.sessionFile = "/sessions/loaded.jsonl";
		const forked = configuredSession("forked", "beta", "test", "high");
		forked.sessionFile = "/sessions/forked.jsonl";
		const raw = rawHost(created, {
			sdk: {
				SessionManager: {
					create: () => ({ kind: "create" }),
					list: async (cwd: string) => [{ id: "loaded", path: loaded.sessionFile ?? "", cwd }],
					open: () => ({ kind: "open" }),
					forkFrom: () => ({ kind: "fork" }),
				},
			},
			sessionFactory: async ({ sessionManager }) => {
				const kind = (sessionManager as { kind?: string })?.kind;
				if (kind === "open") return { session: loaded };
				if (kind === "fork") return { session: forked };
				return { session: created };
			},
		});
		const projection = (frame: HostFrame): Record<string, unknown> => {
			if (!frame.result) throw new Error("session result is missing");
			return frame.result as Record<string, unknown>;
		};
		const expectProjection = (
			frame: HostFrame,
			provider: string,
			model: string,
			thinking: string,
		) => {
			const result = projection(frame);
			expect(result.metadata).toEqual({
				providerId: provider,
				modelId: model,
				thinkingLevel: thinking,
			});
			expect(result.configOptions).toEqual(
				expect.arrayContaining([
					expect.objectContaining({ id: "provider", type: "select", currentValue: provider }),
					expect.objectContaining({ id: "model", type: "select", currentValue: model }),
					expect.objectContaining({ id: "thinking", type: "select", currentValue: thinking }),
				]),
			);
		};

		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "alpha", "medium");
		await raw.send({
			id: 3,
			method: "session.configure",
			params: { sessionId: "created", configId: "model", value: "beta", provider: "test" },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "medium");
		await raw.send({
			id: 4,
			method: "session.configure",
			params: { sessionId: "created", configId: "thinking", value: "high" },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "high");
		await raw.send({
			id: 5,
			method: "session.release",
			params: { sessionId: "created", cwd: process.cwd() },
		});
		await raw.send({
			id: 6,
			method: "session.load",
			params: { sessionId: "loaded", cwd: process.cwd() },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "other", "gamma", "low");
		await raw.send({ id: 7, method: "session.fork", params: { sessionId: "loaded" } });
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "high");
	});

	test("configures thinking and model, then serves stats/messages/commands/queues", async () => {
		const session = new FakeSession("parity");
		session.modelRuntime = {
			getModels: (provider: string) =>
				provider === "test" ? [{ id: "m1", provider: "test" }] : [],
			getModel: (provider: string, model: string) =>
				provider === "test" && model === "m1" ? { id: "m1", provider: "test" } : undefined,
			getAvailable: async () => [{ id: "m1", provider: "test" }],
		};
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.configure",
			params: { sessionId: "parity", configId: "thinking", value: "high" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		expect(session.thinkingLevel).toBe("high");
		await raw.send({
			id: 4,
			method: "session.configure",
			params: { sessionId: "parity", configId: "model", value: "m1", provider: "test" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		expect(session.model).toEqual({ id: "m1", provider: "test" });
		await raw.send({
			id: 5,
			method: "session.rename",
			params: { sessionId: "parity", name: "new" },
		});
		expect(session.sessionName).toBe("new");
		await raw.send({
			id: 6,
			method: "session.compact",
			params: { sessionId: "parity", customInstructions: "short" },
		});
		expect(session.compactInstructions).toBe("short");
		await raw.send({ id: 7, method: "session.stats", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ sessionId: "parity" });
		await raw.send({ id: 8, method: "session.getMessages", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toEqual({ messages: [] });
		await raw.send({ id: 9, method: "session.commands", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toEqual(
			expect.objectContaining({ commands: expect.any(Array) }),
		);
		await raw.send({
			id: 10,
			method: "session.steer",
			params: { sessionId: "parity", content: [{ type: "text", text: "steer me" }] },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("run identifier");
		expect(session.steered).toEqual([]);
		await raw.send({
			id: 11,
			method: "session.followUp",
			params: { sessionId: "parity", message: "later" },
		});
		expect(session.followed.at(-1)?.text).toBe("later");
		await raw.send({ id: 12, method: "session.clearQueue", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		await raw.send({
			id: 13,
			method: "session.switch",
			params: { sessionId: "parity" },
		});
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ sessionId: "parity" });
	});

	test("branches via the public SessionManager file layer", async () => {
		const agentDir = tempAgentDir();
		const sessionDir = join(agentDir, "sessions");
		const calls: string[] = [];
		const sdk = {
			getDefaultSessionDir: () => sessionDir,
			SessionManager: {
				create: () => ({ kind: "create" }),
				list: async () => [],
				open: (path: string) => ({
					kind: "open",
					path,
					getEntry: (id: string) => (id === "e1" ? { id: "e1" } : undefined),
					createBranchedSession: (leaf: string) => {
						calls.push(`branch:${leaf}`);
						return undefined;
					},
				}),
				forkFrom: (source: string, target: string) => {
					calls.push(`fork:${source}:${target}`);
					return { kind: "forked" };
				},
			},
		};
		let next = 0;
		const raw = rawHost(new FakeSession("unused"), {
			agentDir,
			sdk,
			sessionFactory: async () => {
				next += 1;
				const session = new FakeSession(next === 1 ? "parent" : `branch-${next}`);
				session.sessionFile = join(sessionDir, `${session.sessionId}.jsonl`);
				return { session };
			},
		});
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const parent = rawFrames(raw.socket).at(-1)?.result as unknown as { sessionId: string };
		expect(parent.sessionId).toBe("parent");
		await raw.send({ id: 3, method: "session.clone", params: { sessionId: "parent" } });
		expect(await waitForId(raw.socket, 3)).toMatchObject({
			result: expect.objectContaining({ sessionId: "branch-2" }),
		});
		expect(calls.some((call) => call.startsWith("fork:"))).toBe(true);
	});

	test("rejects create-time overrides instead of silently ignoring them", async () => {
		const raw = rawHost(new FakeSession("overrides"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "session.create",
			params: { cwd: process.cwd(), provider: "test" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("create-time");
	});
});

describe("Bun host filesystem parity", () => {
	test("authors agent sources without touching unrelated files", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("sources"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.sources.create",
			params: { name: "helper", description: "helps", content: "body" },
		});
		const created = rawFrames(raw.socket).at(-1)?.result as unknown as {
			source: { path: string; revision: string; name: string };
		};
		expect(created.source.name).toBe("helper");
		expect(existsSync(created.source.path)).toBe(true);
		await raw.send({ id: 3, method: "pi.sources.list", params: {} });
		const listed = rawFrames(raw.socket).at(-1)?.result as unknown as {
			sources: Array<{ name: string }>;
		};
		expect(listed.sources.map((source) => source.name)).toContain("helper");
		await raw.send({ id: 4, method: "pi.agent-mentions.list", params: {} });
		const mentions = rawFrames(raw.socket).at(-1)?.result as unknown as {
			agents: Array<{ mention: string }>;
		};
		expect(mentions.agents.map((agent) => agent.mention)).toContain("@helper");
		await raw.send({
			id: 5,
			method: "pi.sources.update",
			params: {
				path: created.source.path,
				name: "helper",
				description: "helps more",
				content: "body2",
				expectedRevision: created.source.revision,
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		await raw.send({
			id: 6,
			method: "pi.sources.delete",
			params: {
				path: created.source.path,
				expectedRevision: (
					rawFrames(raw.socket).at(-1)?.result as unknown as {
						source: { revision: string };
					}
				).source.revision,
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.result).toEqual({ ok: true });
		expect(existsSync(created.source.path)).toBe(false);
	});

	test("merges MCP layers for reads and writes only the agent layer", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.mcp.servers.upsert",
			params: { name: "alpha", definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(Object.keys(stored.mcpServers)).toEqual(["alpha"]);
		await raw.send({ id: 3, method: "pi.mcp.servers.read", params: {} });
		const read = rawFrames(raw.socket).at(-1)?.result as unknown as {
			servers: Array<{ name: string; layer: string }>;
		};
		expect(read.servers.map((server) => server.name)).toContain("alpha");
		expect(read.servers.find((server) => server.name === "alpha")?.layer).toBe("agent-dir");
		await raw.send({
			id: 4,
			method: "pi.mcp.servers.probe",
			params: { definition: { url: "http://127.0.0.1:1/mcp" } },
		});
		const probe = (await waitForId(raw.socket, 4)).result as unknown as { reachable: boolean };
		expect(probe.reachable).toBe(false);
		await raw.send({
			id: 5,
			method: "pi.mcp.servers.probe",
			params: { definition: {} },
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toContain("required");
		await raw.send({ id: 6, method: "pi.mcp.servers.remove", params: { name: "alpha" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toEqual({ ok: true });
	});

	test("rejects non-ASCII MCP server names and accepts the ASCII length boundary", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp-name"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.mcp.servers.upsert",
			params: { name: "bröwser", definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("invalid MCP server name");
		expect(existsSync(join(agentDir, "mcp.json"))).toBe(false);

		const name = "a".repeat(128);
		await raw.send({
			id: 3,
			method: "pi.mcp.servers.upsert",
			params: { name, definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(stored.mcpServers[name]).toEqual({ url: "http://127.0.0.1:9/mcp" });
	});

	test("preserves concurrent MCP upserts without lost updates", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp-race"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await Promise.all([
			raw.send({
				id: 2,
				method: "pi.mcp.servers.upsert",
				params: { name: "alpha", definition: { url: "http://127.0.0.1:9/mcp" } },
			}),
			raw.send({
				id: 3,
				method: "pi.mcp.servers.upsert",
				params: { name: "beta", definition: { url: "http://127.0.0.1:10/mcp" } },
			}),
		]);
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		expect((await waitForId(raw.socket, 3)).error).toBeUndefined();
		await raw.send({ id: 4, method: "pi.mcp.servers.read", params: {} });
		const read = (await waitForId(raw.socket, 4)).result as unknown as {
			servers: Array<{ name: string }>;
		};
		expect(read.servers.map((server) => server.name).sort()).toEqual(["alpha", "beta"]);
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(Object.keys(stored.mcpServers).sort()).toEqual(["alpha", "beta"]);
	});

	test("conditionally rejects forged, colliding, and changed Browser MCP ownership", async () => {
		const agentDir = tempAgentDir();
		const projectDir = tempAgentDir();
		const expectedToken = "pixie-owned-token";
		const raceToken = "token-observed-before-race";
		const agentDocument = {
			mcpServers: {
				static: {
					url: "https://third-party.example/mcp?secret=never-returned",
					headers: { Authorization: "Bearer never-returned" },
					_pixieManagedBy: "browser-mcp/v1",
				},
				random: {
					url: "https://third-party.example/random",
					_pixieManagedBy: "browser-mcp/v2",
					_pixieOwnershipToken: "forged-random-token",
				},
				race: {
					url: "https://browser.example/original",
					_pixieManagedBy: "browser-mcp/v2",
					_pixieOwnershipToken: raceToken,
				},
			},
		};
		writeFileSync(join(agentDir, "mcp.json"), JSON.stringify(agentDocument));
		writeFileSync(
			join(projectDir, ".mcp.json"),
			JSON.stringify({
				mcpServers: {
					lower: {
						url: "https://third-party.example/lower",
						headers: { Authorization: "Bearer lower-secret" },
					},
				},
			}),
		);
		const raw = rawHost(new FakeSession("mcp-conditional"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });

		await raw.send({ id: 2, method: "pi.mcp.servers.read", params: { name: "static" } });
		const inspected = (await waitForId(raw.socket, 2)).result as unknown as {
			servers: Array<Record<string, unknown>>;
		};
		expect(inspected.servers).toEqual([
			{
				layer: "agent-dir",
				path: join(agentDir, "mcp.json"),
				disabled: false,
				markers: { _pixieManagedBy: "browser-mcp/v1" },
			},
		]);
		expect(JSON.stringify(inspected)).not.toContain("never-returned");

		const staticBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 3,
			method: "pi.mcp.servers.upsert",
			params: {
				name: "static",
				definition: { url: "http://127.0.0.1:3000/mcp" },
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 3)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(staticBefore);

		const randomBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 4,
			method: "pi.mcp.servers.remove",
			params: {
				name: "random",
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 4)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(randomBefore);

		const lowerBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 5,
			method: "pi.mcp.servers.upsert",
			params: {
				name: "lower",
				projectDir,
				definition: { url: "http://127.0.0.1:3000/mcp" },
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(lowerBefore);

		await raw.send({ id: 6, method: "pi.mcp.servers.read", params: { name: "race" } });
		expect((await waitForId(raw.socket, 6)).error).toBeUndefined();
		const changedDocument = structuredClone(agentDocument);
		changedDocument.mcpServers.race._pixieOwnershipToken = "changed-during-race";
		writeFileSync(join(agentDir, "mcp.json"), JSON.stringify(changedDocument));
		const changedBeforeRemove = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 7,
			method: "pi.mcp.servers.remove",
			params: {
				name: "race",
				expectedAgentOwnershipToken: raceToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 7)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(changedBeforeRemove);
	});
});

describe("Bun host admin parity", () => {
	function adminSdk() {
		let provider: string | undefined = "test";
		let model: string | undefined = "m1";
		let thinking: string | undefined;
		return {
			SettingsManager: {
				create: () => ({
					reload: async () => {},
					flush: async () => {},
					drainErrors: () => [],
					getGlobalSettings: () => ({
						defaultThinkingLevel: thinking,
						compaction: {},
						packages: [],
						extensions: [],
					}),
					getProjectSettings: () => ({ packages: [], extensions: [] }),
					getDefaultProvider: () => provider,
					getDefaultModel: () => model,
					setDefaultProvider: (value: string | undefined) => {
						provider = value;
					},
					setDefaultModel: (value: string | undefined) => {
						model = value;
					},
					getDefaultThinkingLevel: () => thinking,
					setDefaultThinkingLevel: (value: string | undefined) => {
						thinking = value;
					},
					getCompactionReserveTokens: () => 2048,
					setPackages: () => {},
					setProjectPackages: () => {},
					setExtensionPaths: () => {},
					setProjectExtensionPaths: () => {},
				}),
			},
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getModels: (id: string) =>
						id === "test" ? [{ id: "m1", name: "M1", contextWindow: 8000, maxTokens: 1000 }] : [],
					getModel: (providerId: string, modelId: string) =>
						providerId === "test" && modelId === "m1"
							? { contextWindow: 8000, maxTokens: 1000, cost: { input: 1 } }
							: undefined,
					getAvailable: async () => [{ provider: "test", id: "m1" }],
					checkAuth: async () => true,
					refresh: async () => ({ errors: new Map() }),
					login: async () => ({}),
					logout: async () => {},
				}),
			},
			DefaultPackageManager: class {
				listConfiguredPackages(): unknown[] {
					return [];
				}
				async resolve(): Promise<unknown> {
					return { extensions: [], skills: [], prompts: [], themes: [] };
				}
			},
		};
	}

	test("serves providers, defaults, preferences, and slash commands in-process", async () => {
		const raw = rawHost(new FakeSession("admin"), { sdk: adminSdk() });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.list", params: { providerIds: [] } });
		const providers = (await waitForId(raw.socket, 2)).result as unknown as {
			entries: Array<{ providerId: string }>;
		};
		expect(providers.entries.map((entry) => entry.providerId)).toContain("test");
		await raw.send({
			id: 3,
			method: "pi.providers.canonical-model-info",
			params: { provider: "test", model: "m1" },
		});
		expect((await waitForId(raw.socket, 3)).result).toMatchObject({
			modelInfo: expect.objectContaining({ provider: "test", currency: "USD" }),
		});
		await raw.send({ id: 4, method: "pi.defaults.read", params: {} });
		expect((await waitForId(raw.socket, 4)).result).toEqual({ providerId: "test", modelId: "m1" });
		await raw.send({
			id: 5,
			method: "pi.preferences.save",
			params: { values: [{ key: "piThinkingEffort", value: "high" }] },
		});
		expect((await waitForId(raw.socket, 5)).error).toBeUndefined();
		await raw.send({
			id: 6,
			method: "pi.preferences.save",
			params: { values: [{ key: "compactionReserveTokens", value: 1 }] },
		});
		expect((await waitForId(raw.socket, 6)).error?.message).toContain("no public setter");
		await raw.send({ id: 7, method: "pi.slash-commands.list", params: {} });
		const commands = (await waitForId(raw.socket, 7)).result as unknown as {
			availableCommands: Array<{ name: string }>;
		};
		expect(commands.availableCommands.map((command) => command.name)).toContain("compact");
		await raw.send({
			id: 8,
			method: "provider.loginStart",
			params: { providerId: "test", loginId: "login-1" },
		});
		expect((await waitForId(raw.socket, 8)).result).toMatchObject({ loginId: "login-1" });
		await raw.send({ id: 9, method: "provider.loginBegin", params: { loginId: "login-1" } });
		expect((await waitForId(raw.socket, 9)).result).toEqual({ ok: true });
		await raw.send({ id: 10, method: "provider.loginBegin", params: { loginId: "missing" } });
		expect((await waitForId(raw.socket, 10)).error?.message).toContain("cannot be started");
	});

	test("reads typed provider configuration presence without leaking secrets or paths", async () => {
		const secretValue = "sk-live-do-not-leak";
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [
						{
							id: "test",
							name: "Test",
							auth: {
								apiKey: { login: true, value: secretValue },
								path: "/home/operator/.pi/auth.json",
							},
						},
					],
					getProviderAuthStatus: () => ({
						configured: true,
						source: "environment",
						label: "/home/operator/.pi/auth.json",
					}),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "test" } });
		const result = (await waitForId(raw.socket, 2)).result as unknown as {
			providerId: string;
			configured: boolean;
			source: string;
			fields: Array<{ name: string; isSet: boolean; source: string }>;
		};
		expect(result).toEqual({
			providerId: "test",
			configured: true,
			source: "environment",
			fields: [{ name: "api_key", isSet: true, source: "environment" }],
		});
		const serialized = JSON.stringify(result);
		expect(serialized).not.toContain(secretValue);
		expect(serialized).not.toContain("/home/operator");
		for (const field of result.fields)
			expect(Object.keys(field).sort()).toEqual(["isSet", "name", "source"]);
	});

	test("reports an unconfigured provider without claiming an explicit field", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getProviderAuthStatus: () => ({ configured: false, source: "fallback" }),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config-default"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "test" } });
		const result = (await waitForId(raw.socket, 2)).result as unknown as {
			fields: Array<{ isSet: boolean }>;
		};
		expect(result.fields.length).toBeGreaterThan(0);
		expect(result.fields.every((field) => field.isSet === false)).toBe(true);
	});

	test("rejects an unknown provider instead of fabricating configuration", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [],
					getProviderAuthStatus: () => ({ configured: true, source: "stored" }),
					isUsingOAuth: () => false,
					getModels: () => [],
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-config-unknown"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "pi.providers.config.read", params: { providerId: "ghost" } });
		expect((await waitForId(raw.socket, 2)).error?.message).toContain("Unknown provider");
	});

	test("validates provider deletion against the typed projection before mutating", async () => {
		let logouts = 0;
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					getProviders: () => [{ id: "test", name: "Test", auth: { apiKey: { login: true } } }],
					getProviderAuthStatus: () => ({ configured: true, source: "stored" }),
					isUsingOAuth: () => false,
					getModels: () => [],
					logout: async () => {
						logouts += 1;
					},
				}),
			},
		};
		const raw = rawHost(new FakeSession("provider-delete"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.providers.config.delete",
			params: { providerId: "ghost" },
		});
		expect((await waitForId(raw.socket, 2)).error?.message).toContain("Unknown provider");
		expect(logouts).toBe(0);
		await raw.send({ id: 3, method: "pi.providers.config.delete", params: { providerId: "test" } });
		expect((await waitForId(raw.socket, 3)).result).toEqual({ ok: true });
		expect(logouts).toBe(1);
	});

	test("completes loginStart, loginBegin, prompt, and loginReply on one login", async () => {
		const sdk = {
			ModelRuntime: {
				create: async () => ({
					login: async (
						_providerId: string,
						_type: string,
						interaction: {
							prompt: (prompt: Record<string, unknown>) => Promise<string>;
						},
					) => {
						const answer = await interaction.prompt({ type: "secret", message: "enter key" });
						if (answer !== "s3cr3t") throw new Error("unexpected answer");
						return { ok: true };
					},
				}),
			},
		};
		const raw = rawHost(new FakeSession("login-flow"), { sdk });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "provider.loginStart",
			params: { providerId: "test", loginId: "login-1" },
		});
		expect((await waitForId(raw.socket, 2)).result).toMatchObject({ loginId: "login-1" });
		await raw.send({ id: 3, method: "provider.loginBegin", params: { loginId: "login-1" } });
		expect((await waitForId(raw.socket, 3)).result).toEqual({ ok: true });
		let prompted = false;
		for (let attempt = 0; attempt < 200 && !prompted; attempt += 1) {
			prompted = rawFrames(raw.socket).some(
				(frame) =>
					frame.method === "provider.login" &&
					(frame.params as unknown as { frame?: { kind?: string } })?.frame?.kind === "prompt",
			);
			if (!prompted) await Bun.sleep(5);
		}
		expect(prompted).toBe(true);
		await raw.send({
			id: 4,
			method: "provider.loginReply",
			params: { loginId: "login-1", value: "s3cr3t" },
		});
		expect((await waitForId(raw.socket, 4)).result).toEqual({ ok: true });
		let succeeded = false;
		for (let attempt = 0; attempt < 200 && !succeeded; attempt += 1) {
			succeeded = rawFrames(raw.socket).some(
				(frame) =>
					frame.method === "provider.login" &&
					(frame.params as unknown as { frame?: { kind?: string } })?.frame?.kind === "success",
			);
			if (!succeeded) await Bun.sleep(5);
		}
		expect(succeeded).toBe(true);
	});

	test("keeps version gating exact and restart opt-in", async () => {
		const strict = rawHost(new FakeSession("restart-off"), { protocol: "v2" });
		await strict.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 2 } });
		await strict.send({ id: 2, method: "runtime.restart", params: {} });
		expect(rawFrames(strict.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
		let restarted = false;
		const agentDir = tempAgentDir();
		const sdk = {
			createAgentSession: async () => ({ session: new FakeSession("restart-on") }),
			getDefaultSessionDir: (_cwd: string) => join(agentDir, "sessions"),
			SessionManager: {
				create: () => ({}),
				list: async () => [],
				open: () => ({}),
			},
		};
		const { host } = hostWith(new FakeSession("restart-on"), {
			agentDir,
			sdk,
			protocol: "auto",
		});
		const started = startBunHost({
			host: "127.0.0.1",
			port: 0,
			secret,
			agentDir: tempAgentDir(),
			verifiedPi: {
				packageName: "@earendil-works/pi-coding-agent",
				packageVersion: "0.85.1",
				packageDir: "/pi",
				entryPath: "/pi/index.js",
				manifestDigest: "a".repeat(64),
				entryDigest: "b".repeat(64),
			},
			sdk: sdk as never,
			allowSelfRestart: true,
			onRestart: () => {
				restarted = true;
			},
		});
		hosts.push(started);
		await host.close();
		const ws = await socket(started);
		await request(ws, 1, "runtime.hello", { protocolVersion: 1 });
		const reply = await request(ws, 2, "runtime.restart", {});
		expect(reply).toEqual({ id: 2, result: { ok: true } });
		for (let attempt = 0; !restarted && attempt < 50; attempt += 1) await Bun.sleep(10);
		expect(restarted).toBe(true);
		ws.close();
	});
});
