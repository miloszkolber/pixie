import { expect, test } from "bun:test";
import {
	ACCEPTANCE_IDS,
	CORE_GATE_IDS,
	type CoverageInput,
	type CutoverInput,
	formatCutoverReport,
	inspectCutover,
	MANDATORY_FEATURE_IDS,
} from "../../scripts/check-coverage.ts";

function passingCoverage(): CoverageInput {
	const ids = [...MANDATORY_FEATURE_IDS, ...ACCEPTANCE_IDS];
	const kinds = ["native", "bridge", "ui", "artifact"] as const;
	return {
		evidence: ids.map((id, index) => ({
			id,
			kind: kinds[index % kinds.length] ?? "native",
			source: `fixture/${id.toLowerCase()}.json`,
			test: `${id.toLowerCase()}-live`,
			actual: true,
			live: true,
		})),
	};
}

function passingCutover(): CutoverInput {
	return {
		coverage: passingCoverage(),
		gates: CORE_GATE_IDS.map((id) => ({
			id,
			passed: true,
			live: true,
			source: `artifacts/${id.toLowerCase().replaceAll(" ", "-")}.json`,
			profile: "amd64-and-arm64",
		})),
		removalRequested: true,
		legacyPaths: ["assistant/src", "package/webui/src"],
	};
}

test("cutover permits legacy removal only after complete coverage and live core gates", () => {
	const report = inspectCutover(passingCutover());

	expect(report.ok).toBe(true);
	expect(report.removalAllowed).toBe(true);
	expect(report.violations).toEqual([]);
	expect(formatCutoverReport(report)).toContain("legacy removal allowed");
});

test("cutover refuses removal when a core gate is missing or not live", () => {
	const input = passingCutover();
	const gates = (input.gates ?? []).slice(0, -1).map((gate) => ({ ...gate }));
	const report = inspectCutover({ ...input, gates });

	expect(report.ok).toBe(false);
	expect(report.removalAllowed).toBe(false);
	expect(report.missingLiveEvidence.join("\n")).toContain("Gate 5");
	expect(formatCutoverReport(report)).toContain("removal is refused");
});

test("cutover refuses a reduction without named approval", () => {
	const input = passingCutover();
	if (typeof input.coverage === "object" && "evidence" in input.coverage) {
		input.coverage = {
			...input.coverage,
			reductions: [{ id: "FC15", approved: false, reason: "not recorded" }],
		};
	}
	const report = inspectCutover(input);

	expect(report.ok).toBe(false);
	expect(report.removalAllowed).toBe(false);
	expect(report.violations.join("\n")).toContain("explicit approval");
});
