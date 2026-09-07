import type { WsMethodName, WsParams, WsResult, WsServerMessage } from "@pixie/contracts";
import { isWsServerMessage } from "@pixie/contracts";
import { randomId } from "../lib";
import { RequestError } from "./request-error";

export type ConnectionStatus = "connecting" | "connected" | "disconnected";
type PushHandler = (data: unknown) => void;

export interface TransportOptions {
	url?: string;
	onStatus?: (status: ConnectionStatus) => void;
	onInitialConnectionFailure?: () => void;
	onAuthenticationLoss?: () => void;
	isAuthenticated?: () => Promise<boolean>;
}

const DEFAULT_TIMEOUT_MS = 60_000;

let clientId: string | undefined;
// Authentication resets replace the transport, not the page's replay namespace.
let requestSequence = 0;

function pageClientId(): string {
	if (clientId === undefined) clientId = randomId("client");
	return clientId;
}

function withClientId(url: string): string {
	const u = new URL(url);
	u.search = "";
	u.searchParams.set("client", pageClientId());
	// Authentication must never be copied into a WebSocket URL query or fragment.
	u.hash = "";
	return u.toString();
}

export interface RequestOptions {
	sessionId?: string;
	timeoutMs?: number;
	signal?: AbortSignal;
}

interface PendingRequest {
	frame: string;
	resolve: (value: unknown) => void;
	reject: (error: Error) => void;
	timer: ReturnType<typeof setTimeout>;
	signal: AbortSignal | undefined;
	onAbort: (() => void) | undefined;
}

function requestAborted(signal: AbortSignal): Error {
	if (signal.reason instanceof Error) return signal.reason;
	const error = new Error("request aborted");
	error.name = "AbortError";
	return error;
}

export class WsTransport {
	private ws: WebSocket | null = null;
	private readonly url: string;
	private readonly onStatus: ((status: ConnectionStatus) => void) | undefined;
	private readonly onInitialConnectionFailure: (() => void) | undefined;
	private readonly onAuthenticationLoss: (() => void) | undefined;
	private readonly isAuthenticated: (() => Promise<boolean>) | undefined;
	private stopped = false;
	private hasOpened = false;
	private readonly pending = new Map<string, PendingRequest>();
	private readonly subscribers = new Map<string, Set<PushHandler>>();
	private ackQueue: string[] = [];
	private ackScheduled = false;
	private backoff = 500;

	constructor(opts: TransportOptions = {}) {
		this.url = opts.url ?? inferUrl();
		this.onStatus = opts.onStatus;
		this.onInitialConnectionFailure = opts.onInitialConnectionFailure;
		this.onAuthenticationLoss = opts.onAuthenticationLoss;
		this.isAuthenticated = opts.isAuthenticated;
	}

	httpBase(): string {
		const u = new URL(this.url);
		u.protocol = u.protocol === "wss:" ? "https:" : "http:";
		return u.origin;
	}

	connect(): void {
		if (this.stopped) return;
		this.onStatus?.("connecting");
		const url = withClientId(this.url);
		const ws = new WebSocket(url);
		this.ws = ws;
		ws.onopen = () => {
			if (this.ws !== ws) {
				ws.close();
				return;
			}
			this.backoff = 500;
			this.hasOpened = true;
			this.onStatus?.("connected");
			this.ackQueue = [];
			this.sendFrame(JSON.stringify({ resume: [...this.pending.keys()] }));
			for (const entry of this.pending.values()) this.sendFrame(entry.frame);
		};
		ws.onmessage = (ev) => this.handleMessage(ev.data);
		ws.onclose = () => {
			if (this.ws !== ws) return;
			this.ws = null;
			this.onStatus?.("disconnected");
			if (!this.hasOpened && this.onInitialConnectionFailure) {
				this.onInitialConnectionFailure();
				return;
			}
			void this.reconnectAfterClose();
		};
		ws.onerror = () => ws.close();
	}

	stop(): void {
		this.stopped = true;
		this.ws?.close();
		this.ws = null;
		for (const id of [...this.pending.keys()]) {
			this.takePending(id)?.reject(new Error("transport stopped"));
		}
	}

	private async reconnectAfterClose(): Promise<void> {
		if (this.isAuthenticated && !(await this.isAuthenticated())) {
			this.onAuthenticationLoss?.();
			return;
		}
		if (this.stopped) return;
		setTimeout(() => this.connect(), this.backoff);
		this.backoff = Math.min(this.backoff * 2, 10_000);
	}

	request<M extends WsMethodName>(
		method: M,
		params: WsParams<M>,
		options: RequestOptions = {},
	): Promise<WsResult<M>> {
		const { sessionId, signal, timeoutMs = DEFAULT_TIMEOUT_MS } = options;
		const id = `trpi_${++requestSequence}`;
		const frame = JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) });
		return new Promise<WsResult<M>>((resolve, reject) => {
			if (signal?.aborted) {
				reject(requestAborted(signal));
				return;
			}
			const timer = setTimeout(() => {
				this.takePending(id)?.reject(new Error(`request "${method}" timed out`));
			}, timeoutMs);
			const entry: PendingRequest = {
				frame,
				resolve: resolve as (v: unknown) => void,
				reject,
				timer,
				signal,
				onAbort: undefined,
			};
			if (signal) {
				entry.onAbort = () => this.takePending(id)?.reject(requestAborted(signal));
				signal.addEventListener("abort", entry.onAbort, { once: true });
			}
			this.pending.set(id, entry);
			if (signal?.aborted) entry.onAbort?.();
			if (!this.pending.has(id)) return;
			this.sendFrame(frame);
		});
	}

	private takePending(id: string): PendingRequest | undefined {
		const entry = this.pending.get(id);
		if (!entry) return undefined;
		this.pending.delete(id);
		clearTimeout(entry.timer);
		if (entry.signal && entry.onAbort) {
			entry.signal.removeEventListener("abort", entry.onAbort);
		}
		return entry;
	}

	subscribe(channel: string, handler: PushHandler): () => void {
		let set = this.subscribers.get(channel);
		if (!set) {
			set = new Set();
			this.subscribers.set(channel, set);
		}
		set.add(handler);
		return () => {
			this.subscribers.get(channel)?.delete(handler);
		};
	}

	private queueAck(id: string): void {
		this.ackQueue.push(id);
		if (this.ackScheduled) return;
		this.ackScheduled = true;
		queueMicrotask(() => {
			this.ackScheduled = false;
			this.flushAcks();
		});
	}

	private flushAcks(): void {
		if (this.ackQueue.length === 0 || this.ws?.readyState !== WebSocket.OPEN) return;
		const ack = this.ackQueue;
		this.ackQueue = [];
		this.sendFrame(JSON.stringify({ ack }));
	}

	private sendFrame(frame: string): void {
		if (this.ws?.readyState !== WebSocket.OPEN) return;
		try {
			this.ws.send(frame);
		} catch {
			this.ws.close();
		}
	}

	private handleMessage(raw: unknown): void {
		if (typeof raw !== "string") return;
		let parsed: unknown;
		try {
			parsed = JSON.parse(raw);
		} catch {
			return;
		}
		if (!isWsServerMessage(parsed)) return;
		const msg: WsServerMessage = parsed;
		if ("channel" in msg) {
			const set = this.subscribers.get(msg.channel);
			if (set) for (const handler of set) handler(msg.data);
			return;
		}
		this.queueAck(msg.id);
		const entry = this.takePending(msg.id);
		if (!entry) return;
		if (msg.ok) {
			entry.resolve(msg.result);
			return;
		}
		const message = msg.error ?? "request failed";
		entry.reject(msg.errorCode ? new RequestError(msg.errorCode, message) : new Error(message));
	}
}

export function inferUrl(): string {
	const proto = location.protocol === "https:" ? "wss:" : "ws:";
	return `${proto}//${location.host}/ws`;
}
