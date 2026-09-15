import { afterEach, expect } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import {
	type BunServerFactory,
	type BunWebSocket,
	type PiSession,
	startBunHost,
} from "../src/host.ts";
import type { createHostLogger } from "../src/log.ts";

export type HostResult = {
	readonly bootId?: string;
	readonly capabilities?: unknown;
	readonly hostIdentity?: string;
	readonly operationSet?: Record<string, boolean>;
	readonly protocolVersion?: number;
	readonly runtimeId?: string;
	readonly supportedProtocolVersions?: readonly number[];
	readonly version?: string;
};

export type HostFrame = {
	readonly error?: { readonly code?: number; readonly message?: string; readonly reason?: string };
	readonly id?: number;
	readonly method?: string;
	readonly params?: { readonly event?: { readonly requestId?: string } };
	readonly result?: HostResult;
};

export const secret = "s".repeat(32);

// Fixture values used by the secret-absence checks. They are intentionally not
// real credentials or paths, but each check asserts they never reach a response.
export const credential = "sk-live-uncovered-credential";
export const operatorPath = "/home/operator/.pi/auth.json";
export const operatorUrl = "https://operator:token-value@example.test/mcp";

const hosts: Array<ReturnType<typeof startBunHost>> = [];
const agentDirs: string[] = [];

/**
 * Registers the per-file afterEach cleanup for every host and temp agent
 * directory created through this harness. Each test file calls it once so a
 * failing test cannot leak a listening host or a temp directory into the next.
 */
export function registerHostCleanup(): void {
	afterEach(async () => {
		await Promise.all(hosts.splice(0).map((host) => host.close()));
		for (const directory of agentDirs.splice(0)) {
			rmSync(directory, { recursive: true, force: true });
		}
	});
}

/** Tracks a host started outside `hostWith` so the per-file cleanup closes it. */
export function trackHost(host: ReturnType<typeof startBunHost>): void {
	hosts.push(host);
}

export class FakeSession implements PiSession {
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
	// AUX-14: the SDK's fork selector maps a text-bearing user message to its
	// native session-entry id. Tests set it to prove the host projects entryId.
	userMessagesForForking: Array<{ entryId: string; text: string }> = [];

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

	getUserMessagesForForking(): readonly { readonly entryId: string; readonly text: string }[] {
		return this.userMessagesForForking;
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

export function tempAgentDir(): string {
	const directory = mkdtempSync(join("/dev/shm", "pixie-bun-host-"));
	agentDirs.push(directory);
	return directory;
}

export function verifiedPi(packageVersion = "0.85.1") {
	return {
		packageName: "@earendil-works/pi-coding-agent" as const,
		packageVersion,
		packageDir: "/pi",
		entryPath: "/pi/index.js",
		manifestDigest: "a".repeat(64),
		entryDigest: "b".repeat(64),
	};
}

export function publicPiRootApi(opened: () => void = () => {}) {
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

export function hostWith(
	session = new FakeSession("native-1"),
	options: {
		readonly agentDir?: string;
		readonly serverFactory?: BunServerFactory;
		readonly sessionFactory?: Parameters<typeof startBunHost>[0]["sessionFactory"];
		readonly sdk?: Record<string, unknown>;
		readonly protocol?: string;
		readonly logger?: Parameters<typeof startBunHost>[0]["logger"];
		readonly allowSelfRestart?: boolean;
		readonly onRestart?: () => void;
		readonly requestTimeoutMs?: number;
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
			logger: options.logger,
			allowSelfRestart: options.allowSelfRestart,
			onRestart: options.onRestart,
			requestTimeoutMs: options.requestTimeoutMs,
		});
	} finally {
		if (previousProtocol === undefined) delete process.env.PIXIE_PI_PROTOCOL;
		else process.env.PIXIE_PI_PROTOCOL = previousProtocol;
	}
	hosts.push(host);
	return { host, session, agentDir };
}

export async function socket(
	host: ReturnType<typeof startBunHost>,
	token = secret,
): Promise<WebSocket> {
	const ws = new WebSocket(host.endpoint, {
		headers: { Authorization: `Bearer ${token}` },
	} as unknown as string[]);
	await new Promise<void>((resolve, reject) => {
		ws.onopen = () => resolve();
		ws.onerror = () => reject(new Error("websocket open failed"));
	});
	return ws;
}

export function parseFrame(raw: string): HostFrame {
	return JSON.parse(raw) as HostFrame;
}

export function next(ws: WebSocket): Promise<HostFrame> {
	return new Promise((resolve) => {
		ws.onmessage = (event) => resolve(parseFrame(event.data));
	});
}

export async function request(
	ws: WebSocket,
	id: number,
	method: string,
	params: Record<string, unknown>,
): Promise<HostFrame> {
	const result = next(ws);
	ws.send(JSON.stringify({ id, method, params }));
	return result;
}

export class RawSocket implements BunWebSocket {
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

export interface RawHost {
	readonly socket: RawSocket;
	readonly send: (value: unknown) => Promise<void>;
	readonly fetch: (path?: string) => Response | undefined;
}

export function rawHost(
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

export function rawFrames(socket: RawSocket): HostFrame[] {
	return socket.sent.map((frame) => parseFrame(frame));
}

export async function waitForId(
	socket: RawSocket,
	id: number,
	timeoutMs = 5000,
): Promise<HostFrame> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const found = rawFrames(socket).find((frame) => frame.id === id);
		if (found) return found;
		if (Date.now() > deadline) throw new Error(`timed out waiting for host frame ${id}`);
		await Bun.sleep(5);
	}
}

/** Asserts a value and the logger's retained entries never contain denied needles. */
export function expectSecretFree(
	value: unknown,
	logger: ReturnType<typeof createHostLogger>,
	forbidden: readonly string[],
): void {
	for (const serialized of [JSON.stringify(value), JSON.stringify(logger.entries())]) {
		for (const needle of forbidden) expect(serialized).not.toContain(needle);
	}
}

/** Sends a v2-capable hello and waits for its response. */
export async function helloV2(raw: ReturnType<typeof rawHost>): Promise<HostFrame> {
	await raw.send({
		id: 1,
		method: "runtime.hello",
		params: { protocolVersion: 1, supportedProtocolVersions: [2, 1], preferProtocolVersion: 2 },
	});
	return waitForId(raw.socket, 1);
}
