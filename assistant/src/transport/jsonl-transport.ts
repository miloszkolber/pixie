// Bounded native JSONL transport used by the assistant runtime.
//
// This module is deliberately a process boundary, rather than an in-memory
// queue.  One child owns one stdout reader and one serialized stdin writer.
// Records are admitted before they are retained, requests are correlated by a
// native bigint id, and a child exit invalidates every pending callback.  A
// timeout or lost response is never retried: callers receive an explicit
// uncertain outcome and must reconcile the durable mutation themselves.

import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import {
	NATIVE_JSONL_ABORT_GRACE_MS,
	NATIVE_JSONL_AGGREGATE_MAX_BYTES,
	NATIVE_JSONL_CONTROL_MAX_BYTES_EACH,
	NATIVE_JSONL_CONTROL_MAX_OPS,
	NATIVE_JSONL_CONTROL_RESERVE_BYTES,
	NATIVE_JSONL_DRAIN_TIMEOUT_MS,
	NATIVE_JSONL_HELLO_TIMEOUT_MS,
	NATIVE_JSONL_MAX_PENDING_PER_CHILD,
	NATIVE_JSONL_MAX_RECORD_BYTES,
	NATIVE_JSONL_READ_STALL_TIMEOUT_MS,
	NATIVE_JSONL_WRITE_TIMEOUT_MS,
	NativeTransportError,
	NativeTransportErrorKind,
} from "./jsonl-framing.ts";

export type NativeCorrelationId = bigint;
export type NativeDeliveryOutcome = "interrupted" | "uncertain";
export type NativeTransportLane = "ordinary" | "control";

export interface NativeTransportConfig {
	maxRecordBytes: number;
	aggregateMaxBytes: number;
	controlReserveBytes: number;
	controlMaxOps: number;
	controlMaxBytesEach: number;
	maxPending: number;
	writeTimeoutMs: number;
	readStallTimeoutMs: number;
	helloTimeoutMs: number;
	drainTimeoutMs: number;
}

export function defaultNativeTransportConfig(): NativeTransportConfig {
	return {
		maxRecordBytes: NATIVE_JSONL_MAX_RECORD_BYTES,
		aggregateMaxBytes: NATIVE_JSONL_AGGREGATE_MAX_BYTES,
		controlReserveBytes: NATIVE_JSONL_CONTROL_RESERVE_BYTES,
		controlMaxOps: NATIVE_JSONL_CONTROL_MAX_OPS,
		controlMaxBytesEach: NATIVE_JSONL_CONTROL_MAX_BYTES_EACH,
		maxPending: NATIVE_JSONL_MAX_PENDING_PER_CHILD,
		writeTimeoutMs: NATIVE_JSONL_WRITE_TIMEOUT_MS,
		readStallTimeoutMs: NATIVE_JSONL_READ_STALL_TIMEOUT_MS,
		helloTimeoutMs: NATIVE_JSONL_HELLO_TIMEOUT_MS,
		drainTimeoutMs: NATIVE_JSONL_DRAIN_TIMEOUT_MS,
	};
}

/** Aggregate serialized-byte admission for one engine scope. */
export class NativeAggregateBudget {
	private used = 0;
	private controlUsed = 0;
	private controlOps = 0;

	constructor(private readonly config: NativeTransportConfig = defaultNativeTransportConfig()) {}

	reserveOrdinary(bytes: number): void {
		if (!Number.isSafeInteger(bytes) || bytes < 0 || bytes > this.config.aggregateMaxBytes)
			throw new NativeTransportError("too_big", "Native admission unit out of bounds");
		if (this.used + bytes > this.config.aggregateMaxBytes)
			throw new NativeTransportError("backpressure", "Native aggregate budget exceeded");
		if (this.used + bytes > this.config.aggregateMaxBytes - this.config.controlReserveBytes)
			throw new NativeTransportError(
				"backpressure",
				"Native control reserve is not available to ordinary traffic",
			);
		this.used += bytes;
	}

	reserveControl(bytes: number): void {
		if (!Number.isSafeInteger(bytes) || bytes < 0)
			throw new NativeTransportError("too_big", "Native admission unit out of bounds");
		if (bytes > this.config.controlMaxBytesEach)
			throw new NativeTransportError("too_big", "Native control record exceeds the 64 KiB limit");
		if (this.controlOps >= this.config.controlMaxOps)
			throw new NativeTransportError("backpressure", "Native control lane is saturated");
		if (this.controlUsed + bytes > this.config.controlReserveBytes)
			throw new NativeTransportError("backpressure", "Native control reserve exceeded");
		if (this.used + bytes > this.config.aggregateMaxBytes)
			throw new NativeTransportError("backpressure", "Native aggregate budget exceeded");
		this.used += bytes;
		this.controlUsed += bytes;
		this.controlOps += 1;
	}

	releaseOrdinary(bytes: number): void {
		if (bytes <= 0) return;
		this.used = Math.max(0, this.used - bytes);
	}

	releaseControl(bytes: number): void {
		if (bytes <= 0) return;
		this.used = Math.max(0, this.used - bytes);
		this.controlUsed = Math.max(0, this.controlUsed - bytes);
		this.controlOps = Math.max(0, this.controlOps - 1);
	}

	admittedBytes(): number {
		return this.used;
	}

	admittedControlBytes(): number {
		return this.controlUsed;
	}

	admittedControlOps(): number {
		return this.controlOps;
	}
}

/** Bounded correlation table for one managed child. */
export class NativePendingTable {
	private readonly ids = new Map<NativeCorrelationId, true>();

	constructor(private readonly config: NativeTransportConfig = defaultNativeTransportConfig()) {}

	add(id: NativeCorrelationId): void {
		if (id === 0n) throw new NativeTransportError("invalid_json", "Native correlation is absent");
		if (this.ids.has(id))
			throw new NativeTransportError("duplicate", "Native correlation is already in flight");
		if (this.ids.size >= this.config.maxPending)
			throw new NativeTransportError("backpressure", "Too many pending native calls");
		this.ids.set(id, true);
	}

	remove(id: NativeCorrelationId): void {
		this.ids.delete(id);
	}

	size(): number {
		return this.ids.size;
	}

	/** Fail every pending correlation when the child exits. Never resend. */
	drainOnExit(): NativeCorrelationId[] {
		const drained = [...this.ids.keys()];
		this.ids.clear();
		return drained;
	}
}

/** Accepted work is interrupted; pre-acceptance loss is uncertain. */
export function nativeUnsettledOutcome(wasAccepted: boolean): NativeDeliveryOutcome {
	return wasAccepted ? "interrupted" : "uncertain";
}

export function nativeChildExit(message = "Native child exited"): NativeTransportError {
	return new NativeTransportError("child_exit", message);
}

export function nativeStalled(operation: string, timeoutMs: number): NativeTransportError {
	return new NativeTransportError("stalled", `${operation} stalled after ${timeoutMs}ms`, timeoutMs);
}

/** Transport failure carrying the durable delivery outcome for one request. */
export class NativeDeliveryError extends NativeTransportError {
	readonly outcome: NativeDeliveryOutcome;
	readonly correlationId: NativeCorrelationId;

	constructor(base: NativeTransportError, outcome: NativeDeliveryOutcome, correlationId: NativeCorrelationId) {
		super(base.kind, `${base.message}; delivery outcome is ${outcome}`, base.timeoutMs);
		this.name = "NativeDeliveryError";
		this.outcome = outcome;
		this.correlationId = correlationId;
	}
}

/** Race one operation against its explicit stall timeout. */
export async function withNativeTimeout<T>(
	operation: Promise<T>,
	timeoutMs: number,
	label: string,
): Promise<T> {
	const bounded = Number.isFinite(timeoutMs) ? Math.max(1, Math.floor(timeoutMs)) : 1;
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		const cutoff = new Promise<never>((_resolve, reject) => {
			timer = setTimeout(() => reject(nativeStalled(label, bounded)), bounded);
			timer.unref?.();
		});
		return await Promise.race([operation, cutoff]);
	} finally {
		if (timer !== undefined) clearTimeout(timer);
	}
}

/** Serialized native writer: records never interleave. */
export class NativeWriter {
	private tail: Promise<void> = Promise.resolve();
	private poisoned?: NativeTransportError;

	constructor(private readonly config: NativeTransportConfig = defaultNativeTransportConfig()) {}

	writeRecord(record: string, write: (chunk: string) => Promise<void>): Promise<void> {
		if (Buffer.byteLength(record, "utf8") > this.config.maxRecordBytes + 1)
			return Promise.reject(new NativeTransportError("too_big", "Native write exceeds the record limit"));
		const next = this.tail.then(async () => {
			if (this.poisoned) throw this.poisoned;
			try {
				await withNativeTimeout(write(record), this.config.writeTimeoutMs, "Native write");
			} catch (error) {
				if (error instanceof NativeTransportError && error.kind === "stalled") this.poisoned = error;
				throw error;
			}
		});
		// A timed-out pipe is poisoned. Later records are rejected before they can
		// overtake a write whose underlying promise has not settled yet.
		this.tail = next.catch(() => {});
		return next;
	}
}

export interface NativeTransportStdin {
	write(chunk: string): Promise<void>;
	end?(): Promise<void> | void;
}

export interface NativeTransportExit {
	readonly code: number | null;
	readonly signal: string | null;
}

/** The minimal child surface required by the bounded transport. */
export interface NativeTransportChild {
	readonly stdin: NativeTransportStdin;
	readonly stdout: AsyncIterable<Uint8Array | string>;
	readonly stderr?: AsyncIterable<Uint8Array | string>;
	readonly exited: Promise<NativeTransportExit>;
	kill?(signal?: string): void;
}

export interface SpawnNativeChildOptions {
	readonly command: string;
	readonly args?: readonly string[];
	readonly cwd?: string;
	/** Pass only an explicit allowlist. Parent credentials are never inherited. */
	readonly env?: Record<string, string>;
}

function childExitPromise(child: ChildProcessWithoutNullStreams): Promise<NativeTransportExit> {
	return new Promise((resolve, reject) => {
		child.once("error", reject);
		child.once("exit", (code, signal) => resolve({ code, signal }));
	});
}

/** Adapt a real OS child process; no in-memory transport is used by this path. */
export function spawnNativeChild(options: SpawnNativeChildOptions): NativeTransportChild {
	const child = spawn(options.command, [...(options.args ?? [])], {
		cwd: options.cwd,
		env: options.env,
		stdio: ["pipe", "pipe", "pipe"],
	});
	const stdin = child.stdin;
	return {
		stdin: {
			write(chunk) {
				return new Promise<void>((resolve, reject) => {
					stdin.write(chunk, "utf8", (error?: Error | null) => {
						if (error) reject(error);
						else resolve();
					});
				});
			},
			end() {
				stdin.end();
			},
		},
		stdout: child.stdout,
		stderr: child.stderr,
		exited: childExitPromise(child),
		kill(signal = "SIGTERM") {
			child.kill(signal as NodeJS.Signals);
		},
	};
}

export interface NativeDeliveryMetadata {
	readonly sessionKey?: string;
	readonly generation?: number;
	readonly mutationId?: string;
	readonly deliveryId?: string;
}

export interface NativeRequestOptions {
	readonly lane?: NativeTransportLane;
	readonly delivery?: NativeDeliveryMetadata;
}

export interface NativePendingFailure {
	readonly id: NativeCorrelationId;
	readonly method: string;
	readonly outcome: NativeDeliveryOutcome;
	readonly delivery?: NativeDeliveryMetadata;
}

export interface NativeTransportFailure {
	readonly error: NativeTransportError;
	readonly pending: readonly NativePendingFailure[];
}

type NativeFrame = Record<string, unknown>;
type NativeRequestResult = unknown;
type NativePendingRequest = {
	id: NativeCorrelationId;
	method: string;
	lane: NativeTransportLane;
	bytes: number;
	delivery?: NativeDeliveryMetadata;
	accepted: boolean;
	resolve: (value: NativeRequestResult) => void;
	reject: (error: unknown) => void;
};

export type NativeTransportState = "created" | "starting" | "ready" | "failed" | "closing" | "closed";

function asFrame(value: unknown): NativeFrame {
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new NativeTransportError("invalid_json", "Native frame must be an object");
	return value as NativeFrame;
}

function correlation(value: unknown): NativeCorrelationId | undefined {
	if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) return BigInt(value);
	return undefined;
}

function utf8Bytes(value: string): number {
	return Buffer.byteLength(value, "utf8");
}

function toBytes(value: Uint8Array<ArrayBufferLike> | string): Uint8Array<ArrayBufferLike> {
	return typeof value === "string" ? Buffer.from(value, "utf8") : value;
}

function appendBytes(
	left: Uint8Array<ArrayBufferLike>,
	right: Uint8Array<ArrayBufferLike>,
): Uint8Array<ArrayBufferLike> {
	const next = new Uint8Array(left.byteLength + right.byteLength);
	next.set(left);
	next.set(right, left.byteLength);
	return next;
}

function frameJson(value: NativeFrame): string {
	return `${JSON.stringify(value)}\n`;
}

/**
 * A real child-backed bounded JSONL transport. The child is expected to
 * answer the initial `{type:"hello"}` request with a correlated hello frame.
 * Events are delivered without retaining their payload after the callback.
 */
export class NativeJsonlTransport {
	readonly budget: NativeAggregateBudget;
	readonly pending: NativePendingTable;
	private readonly writer: NativeWriter;
	private readonly config: NativeTransportConfig;
	private readonly childFactory: () => NativeTransportChild | Promise<NativeTransportChild>;
	private readonly failures = new Set<(failure: NativeTransportFailure) => void>();
	private readonly events = new Set<(frame: NativeFrame) => void>();
	private requests = new Map<NativeCorrelationId, NativePendingRequest>();
	private child?: NativeTransportChild;
	private readTask?: Promise<void>;
	private readFailure?: NativeTransportError;
	private startPromise?: Promise<void>;
	private closePromise?: Promise<void>;
	private nextCorrelation = 1n;
	private stopping = false;
	private terminalNotified = false;
	private _state: NativeTransportState = "created";

	constructor(options: {
		readonly child: NativeTransportChild | (() => NativeTransportChild | Promise<NativeTransportChild>);
		readonly config?: NativeTransportConfig;
	}) {
		this.config = options.config ?? defaultNativeTransportConfig();
		const child = options.child;
		if (typeof child === "function")
			this.childFactory = child as () => NativeTransportChild | Promise<NativeTransportChild>;
		else this.childFactory = async () => child as NativeTransportChild;
		this.budget = new NativeAggregateBudget(this.config);
		this.pending = new NativePendingTable(this.config);
		this.writer = new NativeWriter(this.config);
	}

	get state(): NativeTransportState {
		return this._state;
	}

	get pendingCount(): number {
		return this.requests.size;
	}

	onFailure(listener: (failure: NativeTransportFailure) => void): () => void {
		this.failures.add(listener);
		return () => this.failures.delete(listener);
	}

	onEvent(listener: (frame: NativeFrame) => void): () => void {
		this.events.add(listener);
		return () => this.events.delete(listener);
	}

	async start(): Promise<void> {
		if (this._state === "ready") return;
		if (this.startPromise) return this.startPromise;
		if (this._state !== "created")
			throw this.readFailure ?? nativeChildExit("Native transport is no longer startable");
		this._state = "starting";
		this.startPromise = this.startInternal();
		try {
			await this.startPromise;
		} catch (error) {
			this.startPromise = undefined;
			throw error;
		}
	}

	private async startInternal(): Promise<void> {
		const child = await this.childFactory();
		this.child = child;
		void child.exited.then(
			(status) => {
				if (this._state !== "closed" && !this.stopping)
					this.terminate(nativeChildExit(`Native child exited (${status.code ?? status.signal ?? "unknown"})`));
			},
			(error) => this.terminate(nativeChildExit(error instanceof Error ? error.message : "Native child exited")),
		);
		this.readTask = this.readLoop(child.stdout);
		void this.readTask.catch(() => {});
		if (child.stderr) void this.drainStderr(child.stderr);
		let hello: NativeRequestResult;
		try {
			hello = await withNativeTimeout(
				this.requestInternal("hello", { protocolVersion: 1 }, { lane: "control" }, true),
				this.config.helloTimeoutMs,
				"Native hello",
			);
		} catch (error) {
			if (error instanceof NativeTransportError && error.kind === "stalled") this.terminate(error);
			throw error;
		}
		const result = asFrame(hello);
		if (
			result.type !== "hello" &&
			result.type !== "response" &&
			result.hello !== true &&
			result.protocolVersion !== 1
		)
			throw new NativeTransportError("invalid_json", "Native hello response is invalid");
		if (result.protocolVersion !== undefined && result.protocolVersion !== 1)
			throw new NativeTransportError("invalid_json", "Native hello protocol version is unsupported");
		if (this._state !== "starting") throw this.readFailure ?? nativeChildExit("Native child exited during hello");
		this._state = "ready";
	}

	private async drainStderr(stream: AsyncIterable<Uint8Array | string>): Promise<void> {
		// Stderr is intentionally consumed and not retained. Child diagnostics can
		// contain provider credentials, so it must never be replayed in an error.
		try {
			for await (const _chunk of stream) {
				// Drain until EOF, including during shutdown, so stderr cannot
				// back up and prevent the child from exiting.
			}
		} catch {
			// The exit path owns the user-visible failure.
		}
	}

	private async nextChunk(
		iterator: AsyncIterator<Uint8Array | string>,
		startedAt: number | undefined,
	): Promise<IteratorResult<Uint8Array | string>> {
		const elapsed = startedAt === undefined ? 0 : Date.now() - startedAt;
		const timeout = Math.max(1, this.config.readStallTimeoutMs - elapsed);
		return withNativeTimeout(Promise.resolve(iterator.next()), timeout, "Native read");
	}

	private async readLoop(stream: AsyncIterable<Uint8Array | string>): Promise<void> {
		const iterator = stream[Symbol.asyncIterator]();
		let buffer: Uint8Array<ArrayBufferLike> = new Uint8Array(0);
		let reserved = 0;
		let reservedLane: NativeTransportLane = "ordinary";
		let recordStartedAt: number | undefined;
		try {
			for (;;) {
				const result = await this.nextChunk(iterator, recordStartedAt);
				if (result.done) {
					if (buffer.byteLength > 0)
						throw new NativeTransportError("incomplete", "Incomplete trailing native record");
					throw nativeChildExit("Native child closed stdout");
				}
				const chunk = toBytes(result.value);
				if (!chunk.byteLength) continue;
				if (recordStartedAt === undefined) recordStartedAt = Date.now();
				if (reserved === 0) {
					try {
						this.budget.reserveOrdinary(chunk.byteLength);
						reservedLane = "ordinary";
					} catch (error) {
						// A control response must remain readable while ordinary
						// traffic has consumed its reserve. Only use this lane while
						// a control request is awaiting a response.
						if (!this.hasPendingControl()) throw error;
						this.budget.reserveControl(chunk.byteLength);
						reservedLane = "control";
					}
				} else if (reservedLane === "control") this.budget.reserveControl(chunk.byteLength);
				else this.budget.reserveOrdinary(chunk.byteLength);
				reserved += chunk.byteLength;
				if (reserved > this.config.maxRecordBytes + 1)
					throw new NativeTransportError("too_big", "Native record exceeds the record limit");
				buffer = appendBytes(buffer, chunk);
				let newline = buffer.indexOf(10);
				while (newline >= 0) {
					const frameBytes = newline + 1;
					let line = buffer.slice(0, newline);
					if (line.byteLength > 0 && line[line.byteLength - 1] === 13)
						line = line.slice(0, line.byteLength - 1);
					buffer = buffer.slice(frameBytes);
					reserved -= frameBytes;
					if (reservedLane === "control") this.budget.releaseControl(frameBytes);
					else this.budget.releaseOrdinary(frameBytes);
					if (line.byteLength > this.config.maxRecordBytes)
						throw new NativeTransportError("too_big", "Native record exceeds the record limit");
					if (line.byteLength === 0) {
						recordStartedAt = buffer.byteLength ? recordStartedAt : undefined;
						newline = buffer.indexOf(10);
						continue;
					}
					let text: string;
					try {
						text = new TextDecoder("utf-8", { fatal: true }).decode(line);
					} catch {
						throw new NativeTransportError("invalid_utf8", "Native record is not valid UTF-8");
					}
					let frame: NativeFrame;
					try {
						frame = asFrame(JSON.parse(text));
					} catch (error) {
						if (error instanceof NativeTransportError) throw error;
						throw new NativeTransportError("invalid_json", "Invalid native JSON");
					}
					this.handleFrame(frame);
					recordStartedAt = buffer.byteLength ? recordStartedAt : undefined;
					newline = buffer.indexOf(10);
				}
			}
		} catch (error) {
			if (reserved > 0) {
				if (reservedLane === "control") this.budget.releaseControl(reserved);
				else this.budget.releaseOrdinary(reserved);
			}
			const failure =
				error instanceof NativeTransportError
					? error
					: nativeChildExit(error instanceof Error ? error.message : "Native child read failed");
			if (this.stopping && failure.kind === "child_exit") throw failure;
			this.terminate(failure);
			throw failure;
		}
	}

	private allocateCorrelation(): NativeCorrelationId {
		for (;;) {
			const id = this.nextCorrelation;
			this.nextCorrelation = this.nextCorrelation >= BigInt(Number.MAX_SAFE_INTEGER) ? 1n : id + 1n;
			if (!this.requests.has(id)) return id;
		}
	}

	private hasPendingControl(): boolean {
		for (const request of this.requests.values()) if (request.lane === "control") return true;
		return false;
	}

	private async requestInternal(
		method: string,
		params: unknown,
		options: NativeRequestOptions,
		allowBeforeReady = false,
	): Promise<NativeRequestResult> {
		if (!allowBeforeReady && this._state !== "ready") await this.start();
		if (!this.child || (!allowBeforeReady && this._state !== "ready"))
			throw this.readFailure ?? nativeChildExit("Native transport is unavailable");
		const id = this.allocateCorrelation();
		const lane = options.lane ?? "ordinary";
		const type = method === "hello" ? "hello" : "request";
		const payload: NativeFrame = {
			id: Number(id),
			type,
			...(method === "hello" ? { protocolVersion: 1 } : { method, params }),
		};
		const record = frameJson(payload);
		const bytes = utf8Bytes(record);
		if (bytes > this.config.maxRecordBytes + 1)
			throw new NativeTransportError("too_big", "Native write exceeds the record limit");
		if (lane === "control") this.budget.reserveControl(bytes);
		else this.budget.reserveOrdinary(bytes);
		try {
			this.pending.add(id);
		} catch (error) {
			lane === "control" ? this.budget.releaseControl(bytes) : this.budget.releaseOrdinary(bytes);
			throw error;
		}
		const result = new Promise<NativeRequestResult>((resolve, reject) => {
			this.requests.set(id, {
				id,
				method,
				lane,
				bytes,
				delivery: options.delivery,
				accepted: false,
				resolve,
				reject,
			});
		});
		try {
			await this.writer.writeRecord(record, (chunk) => this.child!.stdin.write(chunk));
		} catch (error) {
			const failure = error instanceof NativeTransportError ? error : nativeChildExit("Native write failed");
			this.terminate(failure);
		}
		return result;
	}

	async request(method: string, params: unknown = {}, options: NativeRequestOptions = {}): Promise<NativeRequestResult> {
		if (typeof method !== "string" || method.length === 0)
			throw new NativeTransportError("invalid_json", "Native method is required");
		try {
			return await withNativeTimeout(
				this.requestInternal(method, params, options),
				method === "hello" ? this.config.helloTimeoutMs : this.config.drainTimeoutMs,
				`Native ${method}`,
			);
		} catch (error) {
			if (error instanceof NativeTransportError && error.kind === "stalled") this.terminate(error);
			throw error;
		}
	}

	private handleFrame(frame: NativeFrame): void {
		const id = correlation(frame.id);
		if (Object.prototype.hasOwnProperty.call(frame, "id") && id === undefined)
			throw new NativeTransportError("invalid_json", "Native correlation must be a positive number");
		if (id !== undefined) {
			const pending = this.requests.get(id);
			if (!pending) throw new NativeTransportError("duplicate", "Native response has no pending correlation");
			this.requests.delete(id);
			this.pending.remove(id);
			if (pending.lane === "control") this.budget.releaseControl(pending.bytes);
			else this.budget.releaseOrdinary(pending.bytes);
			if (frame.error !== undefined) {
				const message =
					frame.error && typeof frame.error === "object" && typeof (frame.error as NativeFrame).message === "string"
						? String((frame.error as NativeFrame).message)
						: "Native request failed";
				pending.reject(new Error(message));
				return;
			}
			pending.accepted = frame.accepted === true || frame.type === "accepted";
			pending.resolve(frame.result ?? frame);
			return;
		}
		for (const listener of this.events) {
			try {
				listener(frame);
			} catch {
				// Projection listeners cannot compromise the transport reader.
			}
		}
	}

	private terminate(error: NativeTransportError): void {
		if (this.readFailure) return;
		this.readFailure = error;
		if (this._state !== "closed") this._state = "failed";
		const pending = [...this.requests.values()];
		this.requests.clear();
		const failures: NativePendingFailure[] = [];
		for (const request of pending) {
			this.pending.remove(request.id);
			if (request.lane === "control") this.budget.releaseControl(request.bytes);
			else this.budget.releaseOrdinary(request.bytes);
			failures.push({
				id: request.id,
				method: request.method,
				outcome: nativeUnsettledOutcome(request.accepted),
				delivery: request.delivery,
			});
			request.reject(new NativeDeliveryError(error, nativeUnsettledOutcome(request.accepted), request.id));
		}
		if (!this.terminalNotified) {
			this.terminalNotified = true;
			const detail = { error, pending: failures };
			for (const listener of this.failures) {
				try {
					listener(detail);
				} catch {
					// Failure reporting is best effort and must not wedge teardown.
				}
			}
		}
	}

	async close(): Promise<void> {
		if (this.closePromise) return this.closePromise;
		this.closePromise = this.closeInternal();
		return this.closePromise;
	}

	private async closeInternal(): Promise<void> {
		this.stopping = true;
		if (!this.child) {
			this._state = "closed";
			return;
		}
		if (this._state === "ready" && !this.readFailure && this.pendingCount === 0) {
			try {
				await this.request("shutdown", { reason: "service-drain" }, { lane: "control" });
			} catch {
				// A child which exits while draining is already terminal.
			}
		}
		this._state = "closing";
		try {
			const ended = this.child.stdin.end?.();
			await withNativeTimeout(Promise.resolve(ended), this.config.drainTimeoutMs, "Native drain");
		} catch {
			// The exit/kill path below still bounds teardown.
		}
		let exited = false;
		try {
			await withNativeTimeout(this.child.exited, this.config.drainTimeoutMs, "Native drain");
			exited = true;
		} catch {
			this.child.kill?.("SIGTERM");
			try {
				await withNativeTimeout(this.child.exited, NATIVE_JSONL_ABORT_GRACE_MS, "Native abort");
				exited = true;
			} catch {
				this.child.kill?.("SIGKILL");
			}
		}
		if (!exited) this.terminate(nativeChildExit("Native child did not exit during drain"));
		else if (this.requests.size) this.terminate(nativeChildExit("Native child exited during drain"));
		this._state = "closed";
	}
}

export function nativeTransportErrorKindOf(error: unknown): NativeTransportErrorKind | undefined {
	return error instanceof NativeTransportError ? error.kind : undefined;
}
