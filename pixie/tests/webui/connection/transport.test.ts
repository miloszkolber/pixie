import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { APP_CLOSE_TIMEOUT_MS, revokeMcpAppView } from "@/chat/tools/apps/mcp-app-client";
import { WsTransport } from "@/connection/transport";

class TestWebSocket {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;
	static instances: TestWebSocket[] = [];

	readonly url: string;
	readyState = TestWebSocket.CONNECTING;
	onopen: ((event: Event) => unknown) | null = null;
	onmessage: ((event: MessageEvent) => unknown) | null = null;
	onclose: ((event: CloseEvent) => unknown) | null = null;
	onerror: ((event: Event) => unknown) | null = null;
	readonly sent: string[] = [];
	readonly protocols: string | string[] | undefined;

	constructor(url: string | URL, protocols?: string | string[]) {
		this.url = String(url);
		this.protocols = protocols;
		TestWebSocket.instances.push(this);
	}

	open(): void {
		this.readyState = TestWebSocket.OPEN;
		this.onopen?.(new Event("open"));
	}

	send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
		if (this.readyState !== TestWebSocket.OPEN) throw new Error("socket is not open");
		if (typeof data !== "string") throw new Error("test socket expects text frames");
		this.sent.push(data);
	}

	message(data: string): void {
		this.onmessage?.(new MessageEvent("message", { data }));
	}

	close(): void {
		if (this.readyState === TestWebSocket.CLOSED) return;
		this.readyState = TestWebSocket.CLOSED;
		this.onclose?.(new CloseEvent("close"));
	}
}

const originalWebSocket = globalThis.WebSocket;
const tick = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

interface ClientFrame {
	id?: string;
	method?: string;
	ack?: string[];
	resume?: string[];
}
const parse = (frame: string): ClientFrame => JSON.parse(frame) as ClientFrame;
const requestsIn = (sent: readonly string[]): ClientFrame[] =>
	sent.map(parse).filter((frame) => frame.method !== undefined);
const acksIn = (sent: readonly string[]): string[] =>
	sent.flatMap((frame) => parse(frame).ack ?? []);
const resumesIn = (sent: readonly string[]): string[][] =>
	sent.map(parse).flatMap((frame) => (frame.resume === undefined ? [] : [frame.resume]));

function trackedAbortSignal(controller = new AbortController()): {
	signal: AbortSignal;
	activeListeners: () => number;
} {
	let activeListeners = 0;
	const signal = new Proxy(controller.signal, {
		get(target, property) {
			if (property === "addEventListener") {
				return (type: string, listener: EventListenerOrEventListenerObject, options?: unknown) => {
					if (type === "abort") activeListeners += 1;
					target.addEventListener(type, listener, options as AddEventListenerOptions);
				};
			}
			if (property === "removeEventListener") {
				return (type: string, listener: EventListenerOrEventListenerObject, options?: unknown) => {
					if (type === "abort") activeListeners -= 1;
					target.removeEventListener(type, listener, options as EventListenerOptions);
				};
			}
			return Reflect.get(target, property, target);
		},
	});
	return { signal, activeListeners: () => activeListeners };
}

beforeEach(() => {
	TestWebSocket.instances = [];
	globalThis.WebSocket = TestWebSocket as unknown as typeof WebSocket;
});

afterEach(() => {
	globalThis.WebSocket = originalWebSocket;
});

describe("WsTransport channel replay", () => {
	test("uses cookie authentication and never sends a credential in the WebSocket URL or subprotocol", () => {
		const transport = new WsTransport({
			url: "ws://localhost:7312/ws?token=must-not-leak",
		});
		transport.connect();
		const socket = TestWebSocket.instances[0];
		if (!socket) throw new Error("socket was not created");

		expect(socket.url).not.toContain("token=");
		expect(socket.protocols).toBeUndefined();
	});
});

describe("WsTransport reconnect delivery", () => {
	test("keeps an App close live across an outage for longer than controller cleanup", async () => {
		expect(APP_CLOSE_TIMEOUT_MS).toBeGreaterThan(60_000);
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const first = TestWebSocket.instances[0];
		first?.open();

		const closing = revokeMcpAppView("a".repeat(64), transport);
		const originalFrame = first?.sent.find((frame) => parse(frame).method === "session.appClose");
		if (!originalFrame) throw new Error("App close frame missing");
		const id = parse(originalFrame).id;
		first?.close();
		await tick(520);
		const replacement = TestWebSocket.instances[1];
		replacement?.open();

		expect(replacement?.sent).toEqual([JSON.stringify({ resume: [id] }), originalFrame]);
		replacement?.message(JSON.stringify({ id, ok: true, result: { ok: true } }));
		await closing;
		transport.stop();
	});

	test("aborting a request rejects it and excludes it from reconnect replay", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const first = TestWebSocket.instances[0];
		first?.open();
		const controller = new AbortController();
		const tracked = trackedAbortSignal(controller);
		const reason = new Error("view closed");
		const result = transport.request("project.list", {}, { signal: tracked.signal });
		expect(requestsIn(first?.sent ?? [])).toHaveLength(1);
		expect(tracked.activeListeners()).toBe(1);

		controller.abort(reason);
		await expect(result).rejects.toBe(reason);
		expect(tracked.activeListeners()).toBe(0);
		first?.close();
		await tick(520);
		const replacement = TestWebSocket.instances[1];
		replacement?.open();
		expect(resumesIn(replacement?.sent ?? [])).toEqual([[]]);
		expect(requestsIn(replacement?.sent ?? [])).toEqual([]);
		transport.stop();
	});

	test("cleans abort listeners after responses, timeouts, and transport stop", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const socket = TestWebSocket.instances[0];
		socket?.open();

		const completed = trackedAbortSignal();
		const result = transport.request("project.list", {}, { signal: completed.signal });
		const id = requestsIn(socket?.sent ?? []).at(-1)?.id;
		socket?.message(JSON.stringify({ id, ok: true, result: [] }));
		expect(await result).toEqual([]);
		expect(completed.activeListeners()).toBe(0);

		const timedOut = trackedAbortSignal();
		const timeout = transport.request(
			"project.list",
			{},
			{
				signal: timedOut.signal,
				timeoutMs: 1,
			},
		);
		await expect(timeout).rejects.toThrow('request "project.list" timed out');
		expect(timedOut.activeListeners()).toBe(0);

		const stopped = trackedAbortSignal();
		const stop = transport.request("project.list", {}, { signal: stopped.signal });
		transport.stop();
		await expect(stop).rejects.toThrow("transport stopped");
		expect(stopped.activeListeners()).toBe(0);
	});

	test("does not reuse pending request IDs after replacing an authenticated transport", async () => {
		const first = new WsTransport({ url: "ws://localhost:7312/ws" });
		first.connect();
		const oldSocket = TestWebSocket.instances[0];
		oldSocket?.open();
		const abandoned = first
			.request("session.create", { projectId: "p1" })
			.catch((error: unknown) => error);
		const oldId = requestsIn(oldSocket?.sent ?? [])[0]?.id;
		if (!oldId) throw new Error("original request ID missing");
		first.stop();
		expect(await abandoned).toBeInstanceOf(Error);

		const replacement = new WsTransport({ url: "ws://localhost:7312/ws" });
		try {
			replacement.connect();
			const newSocket = TestWebSocket.instances[1];
			newSocket?.open();
			expect(newSocket?.url).toBe(oldSocket?.url);
			expect(resumesIn(newSocket?.sent ?? [])).toEqual([[]]);
			const created = replacement.request("session.create", { projectId: "p1" });
			const newId = requestsIn(newSocket?.sent ?? [])[0]?.id;
			expect(newId).toBeDefined();
			expect(newId).not.toBe(oldId);
			let settled = false;
			void created.then(() => {
				settled = true;
			});
			// A retained old result must not resolve the new, identical mutation.
			const oldResult = {
				sessionId: "old",
				model: null,
				thinkingLevel: "off",
				commands: [],
			};
			const newResult = {
				sessionId: "new",
				model: null,
				thinkingLevel: "off",
				commands: [],
			};
			newSocket?.message(JSON.stringify({ id: oldId, ok: true, result: oldResult }));
			await tick(0);
			expect(settled).toBe(false);
			newSocket?.message(JSON.stringify({ id: newId, ok: true, result: newResult }));
			expect(await created).toEqual(newResult);
		} finally {
			replacement.stop();
		}
	});

	test("stops retrying after an initial connection failure when requested", async () => {
		let failed = 0;
		const transport = new WsTransport({
			url: "ws://localhost:7312/ws",
			onInitialConnectionFailure: () => {
				failed += 1;
			},
		});

		transport.connect();
		TestWebSocket.instances[0]?.close();
		await tick(520);
		expect(failed).toBe(1);
		expect(TestWebSocket.instances).toHaveLength(1);
	});

	test("replays an unresolved frame under the same id and resolves from the replacement socket", async () => {
		const statuses: string[] = [];
		const transport = new WsTransport({
			url: "ws://localhost:7312/ws",
			onStatus: (status) => statuses.push(status),
		});
		const pushes: unknown[] = [];
		const unsubscribe = transport.subscribe("agent.event", (data) => pushes.push(data));
		const result = transport.request("project.list", {});
		let settled = false;
		void result.then(
			() => {
				settled = true;
			},
			() => {
				settled = true;
			},
		);

		transport.connect();
		const first = TestWebSocket.instances[0];
		expect(first).toBeDefined();
		first?.open();
		const originalFrame = first?.sent.find((frame) => parse(frame).method !== undefined);
		if (!originalFrame) throw new Error("request frame missing");
		const id = parse(originalFrame ?? "{}").id;
		first?.message(JSON.stringify({ channel: "agent.event", data: "before reconnect" }));

		first?.close();
		await tick(20);
		expect(settled).toBe(false);

		await tick(520);
		const replacement = TestWebSocket.instances[1];
		expect(replacement).toBeDefined();
		replacement?.open();
		expect(replacement?.sent).toEqual([JSON.stringify({ resume: [id] }), originalFrame]);

		replacement?.message(JSON.stringify({ id, ok: true, result: [] }));
		expect(await result).toEqual([]);
		replacement?.message(JSON.stringify({ channel: "agent.event", data: "after reconnect" }));
		unsubscribe();
		replacement?.message(JSON.stringify({ channel: "agent.event", data: "after unsubscribe" }));
		expect(pushes).toEqual(["before reconnect", "after reconnect"]);
		expect(statuses).toEqual([
			"connecting",
			"connected",
			"disconnected",
			"connecting",
			"connected",
		]);
	});
});

describe("WsTransport response receipts", () => {
	test("acknowledges each response, batching a burst into one frame", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const socket = TestWebSocket.instances[0];
		socket?.open();

		const first = transport.request("project.list", {});
		const second = transport.request("model.list", {});
		const ids = requestsIn(socket?.sent ?? []).map((frame) => frame.id ?? "");
		expect(ids).toHaveLength(2);
		const before = socket?.sent.length ?? 0;

		socket?.message(JSON.stringify({ id: ids[0], ok: true, result: [] }));
		socket?.message(JSON.stringify({ id: ids[1], ok: true, result: [] }));
		await Promise.all([first, second]);
		await tick(0);

		expect(acksIn(socket?.sent ?? [])).toEqual(ids);
		expect((socket?.sent.length ?? 0) - before).toBe(1);
	});

	test("a receipt lost with its socket is repaired by the reconnect reconciliation", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const first = TestWebSocket.instances[0];
		first?.open();

		const result = transport.request("project.list", {});
		const id = requestsIn(first?.sent ?? [])[0]?.id;
		first?.message(JSON.stringify({ id, ok: true, result: [] }));
		first?.close();
		expect(await result).toEqual([]);
		expect(acksIn(first?.sent ?? [])).toEqual([]);

		await tick(520);
		const replacement = TestWebSocket.instances[1];
		replacement?.open();
		await tick(0);

		expect(acksIn(replacement?.sent ?? [])).toEqual([]);
		expect(resumesIn(replacement?.sent ?? [])).toEqual([[]]);
	});

	test("the reconciliation names every unresolved id, including ones queued while offline", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const first = TestWebSocket.instances[0];
		first?.open();

		const resolved = transport.request("project.list", {});
		const stillOpen = transport.request("model.list", {});
		const [resolvedId, stillOpenId] = requestsIn(first?.sent ?? []).map((frame) => frame.id ?? "");
		if (!resolvedId || !stillOpenId) throw new Error("request IDs missing");
		first?.message(JSON.stringify({ id: resolvedId, ok: true, result: [] }));
		expect(await resolved).toEqual([]);
		first?.close();

		const queued = transport.request("git.status", { projectId: "p1", repository: "/project" });
		await tick(520);
		const replacement = TestWebSocket.instances[1];
		replacement?.open();
		const queuedId = requestsIn(replacement?.sent ?? [])[1]?.id;

		expect(resumesIn(replacement?.sent ?? [])).toEqual([[stillOpenId, queuedId ?? ""]]);
		expect(resumesIn(replacement?.sent ?? [])[0]).not.toContain(resolvedId);

		replacement?.message(JSON.stringify({ id: stillOpenId, ok: true, result: [] }));
		replacement?.message(JSON.stringify({ id: queuedId, ok: true, result: { files: [] } }));
		await Promise.all([stillOpen, queued]);
	});

	test("re-acknowledges a duplicate reply, whose first receipt may be what went missing", async () => {
		const transport = new WsTransport({ url: "ws://localhost:7312/ws" });
		transport.connect();
		const socket = TestWebSocket.instances[0];
		socket?.open();

		const result = transport.request("project.list", {});
		const id = requestsIn(socket?.sent ?? [])[0]?.id;
		socket?.message(JSON.stringify({ id, ok: true, result: [] }));
		expect(await result).toEqual([]);
		await tick(0);
		socket?.message(JSON.stringify({ id, ok: true, result: [] }));
		await tick(0);

		expect(acksIn(socket?.sent ?? [])).toEqual([id ?? "", id ?? ""]);
	});
});
