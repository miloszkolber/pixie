import { expect, test } from "bun:test";
import {
	ACCEPTANCE_IDS,
	type CoverageEvidence,
	type CoverageInput,
	formatCoverageReport,
	inspectCoverage,
	MANDATORY_FEATURE_IDS,
} from "../../scripts/check-coverage.ts";

function passingEvidence(): CoverageEvidence[] {
	const ids = [...MANDATORY_FEATURE_IDS, ...ACCEPTANCE_IDS];
	const kinds = ["native", "bridge", "ui", "artifact"] as const;
	return ids.map((id, index) => ({
		id,
		kind: kinds[index % kinds.length] ?? "native",
		source: `fixture/${id.toLowerCase()}.json`,
		test: `${id.toLowerCase()}-live`,
		actual: true,
		live: true,
		profile: index % 2 === 0 ? "vanilla" : "combined",
	}));
}

function passingCoverage(): CoverageInput {
	return { evidence: passingEvidence() };
}

test("coverage maps every mandatory FC and applicable X row to actual live evidence", () => {
	const report = inspectCoverage(passingCoverage());

	expect(report.ok).toBe(true);
	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(true);
	expect(report.violations).toEqual([]);
	expect(report.missingLiveEvidence).toEqual([]);
	expect(Object.keys(report.facts.evidenceById)).toHaveLength(
		MANDATORY_FEATURE_IDS.length + ACCEPTANCE_IDS.length,
	);
});

test("coverage fails closed when a row has only static evidence", () => {
	const evidence = passingEvidence();
	const first = evidence[0];
	if (first === undefined) throw new Error("coverage fixture is empty");
	evidence[0] = { ...first, live: false };
	const report = inspectCoverage({ evidence });

	expect(report.staticOk).toBe(true);
	expect(report.complete).toBe(false);
	expect(report.ok).toBe(false);
	expect(report.missingLiveEvidence.join("\n")).toContain(first.id);
	expect(formatCoverageReport(report)).toContain("missing live evidence");
});

test("coverage rejects forged, unknown and duplicate FC/X evidence mappings", () => {
	const evidence = passingEvidence();
	const first = evidence[0];
	if (first === undefined) throw new Error("coverage fixture is empty");
	const report = inspectCoverage({
		evidence: [...evidence, first, { ...first, id: "FC99" }],
	});

	expect(report.ok).toBe(false);
	const output = report.violations.join("\n");
	expect(output).toContain("duplicate");
	expect(output).toContain("unknown FC/X row");
});

test("the checked-in roadmap has no fabricated native or deployment coverage", async () => {
	const { collectCoverageInput } = await import("../../scripts/check-coverage.ts");
	const report = inspectCoverage(await collectCoverageInput());

	expect(report.ok).toBe(false);
	expect(report.missingLiveEvidence.length).toBeGreaterThan(0);
	expect(formatCoverageReport(report)).toContain("check-coverage: FAILED");
});

export { passingCoverage };
