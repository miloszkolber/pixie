import { expect, test } from "bun:test";
import {
	formatPerformanceReport,
	inspectPerformance,
	type PerformanceInput,
	type PerformanceMeasurement,
} from "../../scripts/check-performance.ts";

const sourceCommit = "a".repeat(40);

function measurement(
	architecture: "amd64" | "arm64",
	variant: "assistant" | "full-host",
): PerformanceMeasurement {
	const samples = [100, 120, 140, 160, 180];
	return {
		architecture,
		variant,
		profile: "fresh-process-content-filled",
		artifact: `sha256:${"b".repeat(64)}`,
		sourceCommit,
		sampleDurationsMs: samples,
		p50Ms: 140,
		p95Ms: 180,
		peakProcessRssBytes: 80 << 20,
		decodedMemoryBytes: 24 << 20,
		bufferMemoryBytes: 8 << 20,
		workerMemoryBytes: 512 << 20,
		workerPids: 64,
		workerCpuQuotaMillis: 20_000,
		workerWallTimeMs: 30_000,
		workerOutputBytes: 32 << 20,
		scratchBytes: 128 << 20,
		scratchInodes: 8_192,
		fullProcessTree: true,
		contentFilledUI: true,
		live: true,
	};
}

function passingInput(): PerformanceInput {
	return {
		measurements: [
			measurement("amd64", "assistant"),
			measurement("arm64", "assistant"),
			measurement("amd64", "full-host"),
			measurement("arm64", "full-host"),
		],
	};
}

test("performance gate accepts complete repeated architecture and resource evidence", () => {
	const report = inspectPerformance(passingInput());
	expect(report.ok).toBe(true);
	expect(report.complete).toBe(true);
	expect(report.facts.measuredTargets).toHaveLength(4);
});

test("performance gate refuses missing architecture, incomplete process and memory evidence", () => {
	const input = passingInput();
	const first = input.measurements?.[0];
	if (first === undefined) throw new Error("fixture measurement is missing");
	const report = inspectPerformance({
		measurements: [
			{ ...first, architecture: "arm64e", fullProcessTree: false, decodedMemoryBytes: 0 },
		],
	});

	expect(report.ok).toBe(false);
	expect(report.complete).toBe(false);
	const output = formatPerformanceReport(report);
	expect(output).toContain("unsupported architecture");
	expect(output).toContain("complete process tree");
	expect(output).toContain("decoded memory");
	expect(output).toContain("full-process evidence is missing");
});

test("performance gate does not turn synthetic fixtures into live evidence", () => {
	const input = passingInput();
	const measurements = (input.measurements ?? []).map((item) => ({ ...item, live: false }));
	const report = inspectPerformance({ measurements });

	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(false);
	expect(report.missingLiveEvidence).toHaveLength(4);
	expect(formatPerformanceReport(report)).toContain("missing live evidence");
});

test("performance gate rejects duplicate targets and forged percentile summaries", () => {
	const item = measurement("amd64", "assistant");
	const report = inspectPerformance({ measurements: [{ ...item, p95Ms: 200 }, item] });

	expect(report.ok).toBe(false);
	const output = formatPerformanceReport(report);
	expect(output).toContain("p50/p95 do not match");
	expect(output).toContain("duplicate performance evidence");
});

test("performance evidence parser fails closed for malformed roots", async () => {
	const { collectPerformanceInput } = await import("../../scripts/check-performance.ts");
	const report = inspectPerformance(
		await collectPerformanceInput("/definitely/missing/performance.json"),
	);

	expect(report.ok).toBe(false);
	expect(formatPerformanceReport(report)).toContain("absent or invalid");
});
