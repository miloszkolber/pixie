import type { WsMethodName, WsParams, WsResult } from "@pixie/shared";
import {
	isWsMethodResult,
	isWsRequest,
	isWsServerMessage,
	validateWsMethodParams,
	WS_CHANNELS,
} from "@pixie/shared";
import { randomId } from "../lib";
import { RequestError } from "./request-error";
import { StreamGuard } from "./stream-guard";

const knownChannels = new Set<string>(Object.values(WS_CHANNELS));

/** A server frame with the AUX-16 framing metadata attached. */
interface RawStreamFrame {
	channel: string;
	data?: unknown;
	appended?: unknown;
	seq?: number;
	rev?: number;
	baseRev?: number;
}

/**
 * AUX-16 delta frames carry `appended` instead of `data`, and the shared
 * envelope validator requires `data` on every channel frame. `cause` is the
 * already-parsed JSON value; this narrows it without loosening the shared
 * contract.
 *
 * The controller emits `baseRev` on every framed channel frame, including 0 on
 * a full snapshot, so requiring it here is the agreed wire contract. A frame
 * without it stays on the legacy unframed path.
 */
function isStreamFrame(cause: unknown): cause is RawStreamFrame {
	if (typeof cause !== "object" || cause === null) return false;
	// SAFETY: isStreamFrame is the narrowing boundary for untrusted JSON; the
	// object is only read through validated field checks below.
	const frame = cause as Record<string, unknown>;
	return (
		typeof frame.channel === "string" &&
		knownChannels.has(frame.channel) &&
		typeof frame.seq === "number" &&
		typeof frame.rev === "number" &&
		typeof frame.baseRev === "number" &&
		("data" in frame || "appended" in frame)
	);
}

/** A decoded method response envelope. */
interface ResponseEnvelope {
	id: string;
	ok: boolean;
	result?: unknown;
	error?: string;
	errorCode?: string;
}

/** Recognizes a method response envelope after the shared validator passes. */
function isResponseEnvelope(cause: unknown): cause is ResponseEnvelope {
	if (typeof cause !== "object" || cause === null) return false;
	const envelope = cause as Record<string, unknown>;
	if (typeof envelope.id !== "string" || typeof envelope.ok !== "boolean") return false;
	if (envelope.ok) return "result" in envelope && !("error" in envelope);
	return typeof envelope.error === "string" && !("result" in envelope);
}

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
	method: string;
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
	private readonly guard = new StreamGuard();
	// Last delivered array payload per channel, used to reassemble a delta.
	private readonly delivered = new Map<string, unknown[]>();

	/**
	 * Reassembles a channel payload. A full snapshot replaces the channel's
	 * accumulated value; a delta extends it. A delta without a matching
	 * snapshot is dropped (the guard already requested a resync).
	 */
	private mergeChannelData(channel: string, frame: RawStreamFrame) {
		if (Array.isArray(frame.appended)) {
			const merged = [...(this.delivered.get(channel) ?? []), ...frame.appended];
			this.delivered.set(channel, merged);
			return merged;
		}
		if (Array.isArray(frame.data)) this.delivered.set(channel, frame.data);
		else if (frame.baseRev === 0) this.delivered.delete(channel);
		return frame.data;
	}

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
			// A new socket restarts the server's per-identity sequence chain.
			this.guard.acceptWelcome();
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
		const envelope = { id, method, params, ...(sessionId ? { sessionId } : {}) };
		// AUX-34: a malformed method payload fails at the browser boundary before
		// it creates a pending request or reaches the socket. The generated
		// validator checks required fields only, so additive unknown keys remain
		// compatible with an older or newer controller.
		if (!isWsRequest(envelope)) {
			const reason = validateWsMethodParams(method, params);
			return Promise.reject(new Error(reason ?? `invalid ${method} request payload`));
		}
		const frame = JSON.stringify(envelope);
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
				method,
				// SAFETY: the shared WsResult<M> is assignable to a handler that
				// receives the decoded response envelope and the pending-error
				// type is identical, so the widening is unreachable in practice.
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
		if (!isWsServerMessage(parsed) && !isStreamFrame(parsed)) return;
		if (isStreamFrame(parsed)) {
			const frame = parsed;
			if (frame.channel === "server.welcome") this.guard.acceptWelcome();
			const decision = this.guard.accept({
				channel: frame.channel,
				seq: frame.seq,
				rev: frame.rev,
				baseRev: frame.baseRev,
				appended: frame.appended,
				data: frame.data,
			});
			if (decision.resync) {
				// A missed frame or broken chain resyncs to a fresh snapshot
				// instead of applying partial state.
				this.sendFrame(JSON.stringify({ resync: true }));
				return;
			}
			const set = this.subscribers.get(frame.channel);
			if (set) {
				const data = this.mergeChannelData(frame.channel, frame);
				for (const handler of set) handler(data);
			}
			return;
		}
		if ("channel" in parsed && typeof parsed.channel === "string") {
			if (parsed.channel === "server.welcome") this.guard.acceptWelcome();
			const set = this.subscribers.get(parsed.channel);
			if (set) for (const handler of set) handler(parsed.data);
			return;
		}
		if (!isResponseEnvelope(parsed)) return;
		this.queueAck(parsed.id);
		const entry = this.takePending(parsed.id);
		if (!entry) return;
		// AUX-34: check the reply against the generated schema for the method
		// that is waiting on this id. Additive unknown result fields stay
		// compatible; a malformed result is rejected before it can reach UI state.
		if (!isWsMethodResult(entry.method, parsed)) {
			entry.reject(new Error(`invalid ${entry.method} result`));
			return;
		}
		if (parsed.ok) {
			entry.resolve(parsed.result);
			return;
		}
		const message = parsed.error ?? "request failed";
		entry.reject(
			parsed.errorCode ? new RequestError(parsed.errorCode, message) : new Error(message),
		);
	}
}

export function inferUrl(): string {
	const proto = location.protocol === "https:" ? "wss:" : "ws:";
	return `${proto}//${location.host}/ws`;
}
