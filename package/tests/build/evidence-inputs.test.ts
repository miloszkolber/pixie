import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	ACCEPTANCE_IDS,
	type CoverageInput,
	inspectCoverage,
	inspectCutover,
	MANDATORY_FEATURE_IDS,
} from "../../scripts/check-coverage.ts";
import { inspectPerformance } from "../../scripts/check-performance.ts";
import {
	defaultReductionManifestPath,
	expandReductions,
	loadReductionsManifest,
	produceEvidenceInputs,
} from "../../scripts/produce-evidence-inputs.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;

function binaryScript(versionPrefix: string, doctor: boolean): string {
	return `#!/bin/sh
case "$1" in
  --version) echo "${versionPrefix} ${releaseId} (revision ${sourceCommit})"; exit 0 ;;
  doctor) ${
		doctor
			? `echo "pixie doctor: configuration is readable ()"; exit 0`
			: `echo "unknown command \\"$1\\"; use serve" >&2; exit 2`
	} ;;
  *) echo "unknown command \\"$1\\"; use serve" >&2; exit 2 ;;
esac
`;
}

async function writeExecutable(path: string, script: string): Promise<void> {
	await writeFile(path, script, { mode: 0o755 });
}

interface Fixture {
	root: string;
	assistant: string;
	host: string;
}

async function makeFixture(
	options: { assistantDoctor?: boolean; hostDoctor?: boolean } = {},
): Promise<Fixture> {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-inputs-"));
	const assistant = join(root, "pixie-assistant");
	const host = join(root, "pixie");
	await writeExecutable(
		assistant,
		binaryScript("pixie-assistant", options.assistantDoctor ?? true),
	);
	await writeExecutable(host, binaryScript("pixie", options.hostDoctor ?? true));
	return { root, assistant, host };
}

function mandatoryReducedIDs(): string[] {
	return [...MANDATORY_FEATURE_IDS, ...ACCEPTANCE_IDS].filter((id) => id !== "FC01");
}

test("producer merges every documented reduction and emits measurable inputs", async () => {
	const fixture = await makeFixture();
	try {
		const outputDir = join(fixture.root, "out");
		const result = await produceEvidenceInputs({
			outputDir,
			sourceCommit,
			binaryPaths: [fixture.assistant, fixture.host],
		});

		const manifest = await loadReductionsManifest(defaultReductionManifestPath());
		const expected = expandReductions(manifest);
		const reducedIDs = new Set(result.coverage.reductions?.map((reduction) => reduction.id) ?? []);

		// Every required coverage row except the genuinely executed FC01 is reduced.
		for (const id of mandatoryReducedIDs()) {
			expect(reducedIDs.has(id)).toBe(true);
		}
		// Optional rows are never reduced; they are recorded as optional instead.
		for (const id of ["FC24", "FC27", "FC28"]) {
			expect(reducedIDs.has(id)).toBe(false);
		}
		expect(new Set(result.coverage.gateReductions?.map((gate) => gate.id))).toEqual(
			new Set(expected.gateReductions.map((gate) => gate.id)),
		);
		const reducedFields = new Set(result.performance.fieldReductions?.map((row) => row.field));
		for (const field of ["decodedMemoryBytes", "workerMemoryBytes", "scratchInodes"]) {
			expect(reducedFields.has(field)).toBe(true);
		}
		// The arm64 targets are no longer reduced: a single native runner's output
		// stays incomplete until the release workflow merges the other architecture.
		expect(result.performance.targetReductions ?? []).toEqual([]);

		// FC01 is the only coverage claim, and it comes from executed probes.
		expect(result.coverage.evidence?.length).toBeGreaterThan(0);
		expect(new Set(result.coverage.evidence?.map((row) => row.id))).toEqual(new Set(["FC01"]));
		expect(result.coverage.evidence?.every((row) => row.actual === true && row.live === true)).toBe(
			true,
		);

		// The producer wrote both inputs where the workflow expects them.
		expect(await Bun.file(result.coveragePath).exists()).toBe(true);
		expect(await Bun.file(result.performancePath).exists()).toBe(true);
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("a check the producer did not run stays blocked and is not a live claim", async () => {
	const fixture = await makeFixture({ hostDoctor: false });
	try {
		const result = await produceEvidenceInputs({
			outputDir: join(fixture.root, "out"),
			sourceCommit,
			binaryPaths: [fixture.assistant, fixture.host],
		});

		const readiness = result.probes.find((probe) => probe.name === "readiness");
		expect(readiness?.status).toBe("blocked");
		expect(result.coverage.evidence?.some((row) => row.test === "running-host-readiness")).toBe(
			false,
		);

		const doctor = result.probes.find(
			(probe) => probe.name === "doctor" && probe.variant === "full-host",
		);
		expect(doctor?.status).toBe("blocked");
		expect(result.coverage.evidence?.some((row) => row.test === "full-host-amd64-doctor")).toBe(
			false,
		);
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("coverage passes only for executed or approved-reduced rows and fails for an unreduced row", async () => {
	const fixture = await makeFixture();
	try {
		const result = await produceEvidenceInputs({
			outputDir: join(fixture.root, "out"),
			sourceCommit,
			binaryPaths: [fixture.assistant, fixture.host],
		});

		const coverage = result.coverage as CoverageInput;
		const report = inspectCoverage(coverage);
		expect(report.ok).toBe(true);
		expect(report.facts.approvedReductions).toContain("FC23");

		const cutover = inspectCutover({
			coverage,
			...(coverage.gateReductions === undefined ? {} : { gateReductions: coverage.gateReductions }),
			removalRequested: true,
		});
		expect(cutover.ok).toBe(true);
		expect(cutover.removalAllowed).toBe(true);

		// Drop a reduction for a row with no executed evidence and the gate fails closed.
		const withoutFC23: CoverageInput = {
			...coverage,
			reductions: (coverage.reductions ?? []).filter((reduction) => reduction.id !== "FC23"),
		};
		const missing = inspectCoverage(withoutFC23);
		expect(missing.ok).toBe(false);
		expect(missing.violations.join("\n")).toContain("FC23");

		// Drop the executed FC01 evidence while FC01 stays unreduced: still fail closed.
		const withoutEvidence: CoverageInput = { ...coverage, evidence: [] };
		const noEvidence = inspectCoverage(withoutEvidence);
		expect(noEvidence.ok).toBe(false);
		expect(noEvidence.violations.join("\n")).toContain("FC01");
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("performance producer measures native startup evidence, leaves the other architecture required and rejects invented values", async () => {
	const fixture = await makeFixture();
	try {
		const result = await produceEvidenceInputs({
			outputDir: join(fixture.root, "out"),
			sourceCommit,
			binaryPaths: [fixture.assistant, fixture.host],
		});

		const report = inspectPerformance(result.performance);
		expect(report.staticOk).toBe(true);
		expect(report.complete).toBe(false);
		expect(report.facts.measuredTargets).toHaveLength(2);
		expect(report.facts.missingTargets).toEqual(["assistant/arm64", "full-host/arm64"]);
		expect(report.facts.reducedTargets).toEqual([]);
		for (const measurement of result.performance.measurements ?? []) {
			expect(measurement.sampleDurationsMs.length).toBeGreaterThanOrEqual(5);
			expect(measurement.peakProcessRssBytes).toBeGreaterThan(0);
			expect(measurement.live).toBe(true);
			expect(measurement.sourceCommit).toBe(sourceCommit);
		}

		// Removing the reductions exposes the legacy fields as unmeasured requirements.
		const unreduced = inspectPerformance({
			...result.performance,
			fieldReductions: [],
		});
		expect(unreduced.ok).toBe(false);
		expect(unreduced.violations.join("\n")).toContain("decoded memory measurement is required");

		// A forged percentile must not be accepted.
		const measurements = (result.performance.measurements ?? []).map((measurement, index) =>
			index === 0 ? { ...measurement, p95Ms: measurement.p95Ms + 1000 } : measurement,
		);
		const forged = inspectPerformance({ ...result.performance, measurements });
		expect(forged.ok).toBe(false);
		expect(forged.violations.join("\n")).toContain("p50/p95 do not match the recorded samples");
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});
