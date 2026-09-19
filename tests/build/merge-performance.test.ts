import { expect, test } from "bun:test";
import {
	inspectPerformance,
	type PerformanceInput,
	type PerformanceMeasurement,
} from "../../scripts/check-performance.ts";
import { mergePerformance } from "../../scripts/merge-performance.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";

function measurement(
	architecture: "amd64" | "arm64",
	variant: "assistant",
	overrides: Partial<PerformanceMeasurement> = {},
): PerformanceMeasurement {
	return {
		architecture,
		variant,
		profile: `linux-${architecture}-fresh-process-startup`,
		artifact: `sha256:${"b".repeat(64)}`,
		sourceCommit,
		sampleDurationsMs: [100, 120, 140, 160, 180],
		p50Ms: 140,
		p95Ms: 180,
		peakProcessRssBytes: 80 << 20,
		decodedMemoryBytes: 0,
		bufferMemoryBytes: 0,
		workerMemoryBytes: 0,
		workerPids: 0,
		workerCpuQuotaMillis: 0,
		workerWallTimeMs: 0,
		workerOutputBytes: 0,
		scratchBytes: 0,
		scratchInodes: 0,
		fullProcessTree: true,
		contentFilledUI: false,
		live: true,
		...overrides,
	};
}

const reducedFields = [
	"decodedMemoryBytes",
	"bufferMemoryBytes",
	"workerMemoryBytes",
	"workerPids",
	"workerCpuQuotaMillis",
	"workerWallTimeMs",
	"workerOutputBytes",
	"scratchBytes",
	"scratchInodes",
	"contentFilledUI",
] as const;

const fieldReductions = reducedFields.map((field) => ({
	target: "*",
	field,
	approved: true,
	approver: "operator",
	reason: "legacy measurement the lean engine cannot produce",
}));

test("merge concatenates native architecture measurements and preserves field reductions", () => {
	const into: PerformanceInput = {
		measurements: [measurement("amd64", "assistant")],
		fieldReductions: [...fieldReductions],
	};
	const from: PerformanceInput = {
		measurements: [measurement("arm64", "assistant")],
	};

	const merged = mergePerformance(into, from);

	expect(merged.measurements).toHaveLength(2);
	expect(new Set(merged.measurements?.map((row) => `${row.variant}/${row.architecture}`))).toEqual(
		new Set(["assistant/amd64", "assistant/arm64"]),
	);
	expect(merged.fieldReductions).toEqual([...fieldReductions]);
	// The merged input is the complete two-target record the gate requires.
	const report = inspectPerformance(merged);
	expect(report.violations).toEqual([]);
	expect(report.ok).toBe(true);
	expect(report.facts.measuredTargets).toHaveLength(2);
});

test("merge deduplicates identical duplicate targets and fails on conflicting duplicates", () => {
	const duplicate = measurement("amd64", "assistant");
	const deduped = mergePerformance(
		{ measurements: [duplicate], fieldReductions: [] },
		{ measurements: [{ ...duplicate }] },
	);
	expect(deduped.measurements).toHaveLength(1);

	expect(() =>
		mergePerformance(
			{ measurements: [duplicate], fieldReductions: [] },
			{ measurements: [measurement("amd64", "assistant", { p50Ms: 999, p95Ms: 1000 })] },
		),
	).toThrow("conflicting measurements for target assistant/amd64");
});

test("merge falls back to the secondary field reductions and unions static violations", () => {
	const merged = mergePerformance(
		{ measurements: [], staticViolations: ["primary unavailable"] },
		{
			measurements: [],
			fieldReductions: [...fieldReductions],
			staticViolations: ["secondary unavailable"],
		},
	);

	expect(merged.fieldReductions).toEqual([...fieldReductions]);
	expect(merged.staticViolations).toEqual(["primary unavailable", "secondary unavailable"]);
});
