import { describe, expect, test } from "bun:test";
import {
	encodeNativeJsonlRecord,
	NATIVE_JSONL_MAX_RECORD_BYTES,
	nativeTransportErrorKindOf,
	parseNativeJsonlRecord,
	splitNativeJsonlBuffer,
} from "../../../assistant/src/transport/jsonl-framing.ts";
import {
	defaultNativeTransportConfig,
	NativeAggregateBudget,
	NativePendingTable,
	nativeUnsettledOutcome,
	NativeWriter,
	withNativeTimeout,
} from "../../../assistant/src/transport/jsonl-transport.ts";

describe("native JSONL framing", () => {
	test("splits on LF only and strips one CR", () => {
		const { records, remainder } = splitNativeJsonlBuffer('{"a":1}\n{"b":2}\r\n{"c":3');
		expect(records).toEqual(['{"a":1}', '{"b":2}']);
		expect(remainder).toBe('{"c":3');
	});

	test("unicode separators inside strings never delimit", () => {
		const payload = '{"text":"a b"}\n';
		const { records, remainder } = splitNativeJsonlBuffer(payload);
		expect(records).toHaveLength(1);
		expect(remainder).toBe("");
		expect(parseNativeJsonlRecord(records[0])).toMatchObject({ text: "a b" });
	});

	test("oversize records fail without truncation", () => {
		const huge = `{"text":${JSON.stringify("a".repeat(NATIVE_JSONL_MAX_RECORD_BYTES))}}\n`;
		expect(() => splitNativeJsonlBuffer(huge)).toThrow();
		try {
			splitNativeJsonlBuffer(huge);
		} catch (error) {
			expect(nativeTransportErrorKindOf(error)).toBe("too_big");
		}
		expect(() =>
			encodeNativeJsonlRecord({ text: "a".repeat(NATIVE_JSONL_MAX_RECORD_BYTES + 1) }),
		).toThrow();
	});

	test("logs are not events", () => {
		for (const line of ["not json", "[1,2,3]", "", "null"]) {
			expect(() => parseNativeJsonlRecord(line)).toThrow();
		}
		expect(parseNativeJsonlRecord('{"method":"agent_settled"}')).toMatchObject({
			method: "agent_settled",
		});
	});

	test("encoding appends one LF delimiter", () => {
		const framed = encodeNativeJsonlRecord({ method: "prompt", id: 1 });
		expect(framed.endsWith("\n")).toBe(true);
		expect(framed).toContain('"prompt"');
	});
});

describe("native JSONL admission and correlation", () => {
	test("ordinary traffic cannot consume the control reserve", () => {
		const config = defaultNativeTransportConfig();
		const budget = new NativeAggregateBudget(config);
		budget.reserveOrdinary(config.aggregateMaxBytes - config.controlReserveBytes);
		expect(() => budget.reserveOrdinary(1)).toThrow();
		try {
			budget.reserveOrdinary(1);
		} catch (error) {
			expect(nativeTransportErrorKindOf(error)).toBe("backpressure");
		}
		budget.reserveControl(1024);
		expect(budget.admittedControlOps()).toBe(1);
		budget.releaseControl(1024);
		budget.releaseOrdinary(config.aggregateMaxBytes - config.controlReserveBytes);
		expect(budget.admittedBytes()).toBe(0);
	});

	test("control lane has op and size caps", () => {
		const config = defaultNativeTransportConfig();
		const budget = new NativeAggregateBudget(config);
		expect(() => budget.reserveControl(config.controlMaxBytesEach + 1)).toThrow();
		for (let i = 0; i < config.controlMaxOps; i++) budget.reserveControl(1);
		expect(() => budget.reserveControl(1)).toThrow();
	});

	test("pending correlation rejects duplicates and overflows", () => {
		const config = { ...defaultNativeTransportConfig(), maxPending: 2 };
		const pending = new NativePendingTable(config);
		expect(() => pending.add(0n)).toThrow();
		pending.add(7n);
		expect(() => pending.add(7n)).toThrow();
		pending.add(8n);
		try {
			pending.add(9n);
		} catch (error) {
			expect(nativeTransportErrorKindOf(error)).toBe("backpressure");
		}
		pending.remove(7n);
		expect(pending.size()).toBe(1);
	});

	test("child exit drains pending as interrupted or uncertain", () => {
		const pending = new NativePendingTable();
		pending.add(11n);
		pending.add(12n);
		expect(pending.drainOnExit()).toHaveLength(2);
		expect(pending.size()).toBe(0);
		expect(nativeUnsettledOutcome(true)).toBe("interrupted");
		expect(nativeUnsettledOutcome(false)).toBe("uncertain");
	});

	test("serialized writer never interleaves records", async () => {
		const writer = new NativeWriter();
		const chunks: string[] = [];
		const first = encodeNativeJsonlRecord({ id: 1 });
		const second = encodeNativeJsonlRecord({ id: 2 });
		await writer.writeRecord(first, async (chunk) => {
			chunks.push(chunk);
		});
		await writer.writeRecord(second, async (chunk) => {
			chunks.push(chunk);
		});
		expect(chunks.join("").split("\n").filter(Boolean)).toHaveLength(2);
	});

	test("stalled pipes fail with explicit timeouts", async () => {
		await expect(
			withNativeTimeout(new Promise(() => {}), 10, "Native read"),
		).rejects.toThrow();
		try {
			await withNativeTimeout(new Promise(() => {}), 10, "Native read");
		} catch (error) {
			expect(nativeTransportErrorKindOf(error)).toBe("stalled");
		}
	});
});
