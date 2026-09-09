// Bounded native JSONL admission, correlation, child-exit and stalled-pipe
// handling for GO-02.
//
// One reader and one serialized writer per managed child correlate native
// calls by bigint correlation. Aggregate serialized-byte admission covers
// reading, queued requests, replay and outbound buffers; a small control
// lane stays reserved for Stop, UI cancellation and service draining.
// Child exit invalidates callbacks, fails pending calls and marks unsettled
// delivery interrupted/uncertain. Stalled pipes fail with explicit timeouts;
// partially dispatched work keeps its uncertain outcome and tears down
// safely instead of being resent.

import {
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
} from "./jsonl-framing.ts";

export type NativeCorrelationId = bigint;
export type NativeDeliveryOutcome = "interrupted" | "uncertain";

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
		if (!Number.isSafeInteger(bytes) || bytes < 0 || bytes > this.config.aggregateMaxBytes) {
			throw new NativeTransportError("too_big", "Native admission unit out of bounds");
		}
		if (this.used + bytes > this.config.aggregateMaxBytes) {
			throw new NativeTransportError("backpressure", "Native aggregate budget exceeded");
		}
		if (this.used + bytes > this.config.aggregateMaxBytes - this.config.controlReserveBytes) {
			throw new NativeTransportError(
				"backpressure",
				"Native control reserve is not available to ordinary traffic",
			);
		}
		this.used += bytes;
	}

	reserveControl(bytes: number): void {
		if (!Number.isSafeInteger(bytes) || bytes < 0) {
			throw new NativeTransportError("too_big", "Native admission unit out of bounds");
		}
		if (bytes > this.config.controlMaxBytesEach) {
			throw new NativeTransportError(
				"too_big",
				"Native control record exceeds the 64 KiB limit",
			);
		}
		if (this.controlOps >= this.config.controlMaxOps) {
			throw new NativeTransportError("backpressure", "Native control lane is saturated");
		}
		if (this.controlUsed + bytes > this.config.controlReserveBytes) {
			throw new NativeTransportError("backpressure", "Native control reserve exceeded");
		}
		if (this.used + bytes > this.config.aggregateMaxBytes) {
			throw new NativeTransportError("backpressure", "Native aggregate budget exceeded");
		}
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
		if (id === 0n) {
			throw new NativeTransportError("invalid_json", "Native correlation is absent");
		}
		if (this.ids.has(id)) {
			throw new NativeTransportError("duplicate", "Native correlation is already in flight");
		}
		if (this.ids.size >= this.config.maxPending) {
			throw new NativeTransportError("backpressure", "Too many pending native calls");
		}
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

	constructor(private readonly config: NativeTransportConfig = defaultNativeTransportConfig()) {}

	writeRecord(record: string, write: (chunk: string) => Promise<void>): Promise<void> {
		if (Buffer.byteLength(record, "utf8") > this.config.maxRecordBytes + 1) {
			return Promise.reject(
				new NativeTransportError("too_big", "Native write exceeds the record limit"),
			);
		}
		const next = this.tail.then(() =>
			withNativeTimeout(write(record), this.config.writeTimeoutMs, "Native write"),
		);
		// Keep the chain alive for later writers while surfacing this failure.
		this.tail = next.catch(() => {});
		return next;
	}
}
