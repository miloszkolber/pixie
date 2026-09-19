import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { gzipSync } from "node:zlib";
import { productArchiveLayout } from "../../scripts/build-release.ts";
import {
	ACCEPTANCE_IDS,
	type CoverageInput,
	inspectCoverage,
	inspectCutover,
	MANDATORY_FEATURE_IDS,
} from "../../scripts/check-coverage.ts";
import {
	expandPackageArtifactReductions,
	loadReductionsManifest as loadPackageReductionsManifest,
} from "../../scripts/check-package-artifacts.ts";
import { inspectPerformance } from "../../scripts/check-performance.ts";
import { writeDeterministicTarGz } from "../../scripts/deterministic-tar.ts";
import {
	defaultReductionManifestPath,
	expandReductions,
	loadReductionsManifest,
	produceEvidenceInputs,
	productProbeEntrypoint,
} from "../../scripts/produce-evidence-inputs.ts";
import { stageArtifacts, writeFixtureTar } from "./staged-evidence-fixture.ts";

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
	artifactsDir: string;
	web: string;
	host: string;
}

async function makeFixture(options: { fullDoctor?: boolean } = {}): Promise<Fixture> {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-inputs-"));
	try {
		const { artifactsDir } = await stageArtifacts(root, true, sourceCommit, releaseId);
		const web = join(root, "pixie_web");
		const host = join(root, "pixie");
		await writeExecutable(web, binaryScript("pixie_web", true));
		await writeExecutable(host, binaryScript("pixie", options.fullDoctor ?? true));
		return { root, artifactsDir, web, host };
	} catch (error) {
		await rm(root, { recursive: true, force: true });
		throw error;
	}
}

function binaryPaths(fixture: Fixture): string[] {
	return [fixture.web, fixture.host];
}

async function rewriteArchive(
	root: string,
	archive: string,
	entries: readonly string[],
): Promise<void> {
	const source = join(root, "invalid-layout");
	const files: { name: string; path: string; mode: number }[] = [];
	for (const name of entries) {
		const path = join(source, name);
		await mkdir(dirname(path), { recursive: true });
		await writeFile(path, `${name}\n`);
		files.push({ name, path, mode: 0o644 });
	}
	await writeDeterministicTarGz(archive, files, 1_700_000_000);
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
			artifactsDir: fixture.artifactsDir,
			binaryPaths: binaryPaths(fixture),
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

		// The arm64 version/doctor package rows are no longer reduced either; the
		// arm64 evidence job supplies them to the merged bundle.
		const packageReductions = expandPackageArtifactReductions(
			await loadPackageReductionsManifest(defaultReductionManifestPath()),
		).map((reduction) => reduction.id);
		for (const id of ["binary.version/pixie/arm64", "binary.doctor/pixie/arm64"]) {
			expect(packageReductions).not.toContain(id);
		}

		// FC01 is the only coverage claim, and it comes from executed probes.
		expect(result.coverage.evidence?.length).toBeGreaterThan(0);
		expect(new Set(result.coverage.evidence?.map((row) => row.id))).toEqual(new Set(["FC01"]));
		expect(result.coverage.evidence?.every((row) => row.actual === true && row.live === true)).toBe(
			true,
		);
		expect(result.binaries.map((binary) => binary.product)).toEqual(["pixie_web", "pixie"]);
		for (const binary of result.binaries) {
			expect(await Bun.file(binary.path).exists()).toBe(true);
		}
		// Explicit binaries override the extracted probe entrypoints for tests.
		expect(result.binaries.map((binary) => binary.path)).toEqual(binaryPaths(fixture));
		for (const executable of ["products/pixie/pixie", "products/pixie/runtime/bin/bun"]) {
			expect((await stat(join(outputDir, "binaries", executable))).mode & 0o111).not.toBe(0);
		}
		// The assistant is a Bun JavaScript bundle run by the staged runtime, not
		// an archive executable: a regular 0o644 file with no execute bits.
		for (const assistant of ["products/pixie/libexec/pixie_assistant.js"]) {
			const info = await stat(join(outputDir, "binaries", assistant));
			expect(info.isFile()).toBe(true);
			expect(info.mode & 0o777).toBe(0o644);
			expect(info.mode & 0o111).toBe(0);
		}
		// Preserve the frozen row while measuring only archive-local runtime bundles.
		expect(
			result.performance.measurements?.map((measurement) => measurement.variant).sort(),
		).toEqual(["assistant"]);

		// The producer wrote both inputs where the workflow expects them.
		expect(await Bun.file(result.coveragePath).exists()).toBe(true);
		expect(await Bun.file(result.performancePath).exists()).toBe(true);
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("a check the producer did not run stays blocked and is not a live claim", async () => {
	const fixture = await makeFixture({ fullDoctor: false });
	try {
		const result = await produceEvidenceInputs({
			outputDir: join(fixture.root, "out"),
			sourceCommit,
			artifactsDir: fixture.artifactsDir,
			binaryPaths: binaryPaths(fixture),
		});

		const readiness = result.probes.find((probe) => probe.name === "readiness");
		expect(readiness?.status).toBe("blocked");
		expect(result.coverage.evidence?.some((row) => row.test === "running-host-readiness")).toBe(
			false,
		);

		const doctor = result.probes.find(
			(probe) => probe.name === "doctor" && probe.product === "pixie",
		);
		expect(doctor?.status).toBe("blocked");
		expect(result.coverage.evidence?.some((row) => row.test === "pixie-amd64-doctor")).toBe(false);
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
			artifactsDir: fixture.artifactsDir,
			binaryPaths: binaryPaths(fixture),
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
			artifactsDir: fixture.artifactsDir,
			binaryPaths: binaryPaths(fixture),
		});

		const report = inspectPerformance(result.performance);
		expect(report.staticOk).toBe(true);
		expect(report.complete).toBe(false);
		expect(report.facts.measuredTargets).toHaveLength(1);
		expect(report.facts.missingTargets).toEqual(["assistant/arm64"]);
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

test("producer validates every current archive layout before it writes evidence", async () => {
	const fixture = await makeFixture();
	try {
		const archive = join(fixture.artifactsDir, `pixie_web-${releaseId}-linux-amd64.tar.gz`);
		await rewriteArchive(fixture.root, archive, [
			...productArchiveLayout("pixie_web"),
			"runtime/manifest.json",
		]);

		const outputDir = join(fixture.root, "out");
		await expect(
			produceEvidenceInputs({
				outputDir,
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("invalid pixie_web layout");
		expect(await Bun.file(join(outputDir, "coverage.json")).exists()).toBe(false);
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("producer rejects a Node runtime and a legacy no-extension assistant path", async () => {
	const fixture = await makeFixture();
	try {
		const cliArchive = join(fixture.artifactsDir, `pixie-${releaseId}-linux-amd64.tar.gz`);
		const runtime = [
			"runtime/manifest.json",
			"runtime/bin/bun",
			"runtime/bun/LICENSE.md",
			"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
		];
		// Only the pinned Bun runtime may ship, so a bundled Node runtime fails.
		await rewriteArchive(fixture.root, cliArchive, [
			...productArchiveLayout("pixie", runtime),
			"runtime/node/bin/node",
		]);
		await expect(
			produceEvidenceInputs({
				outputDir: join(fixture.root, "out-node"),
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("bundles a Node runtime");

		// The legacy compiled-executable name is not the portable JS bundle.
		await rewriteArchive(
			fixture.root,
			cliArchive,
			productArchiveLayout("pixie", runtime).map((name) =>
				name === "libexec/pixie_assistant.js" ? "libexec/pixie_assistant" : name,
			),
		);
		await expect(
			produceEvidenceInputs({
				outputDir: join(fixture.root, "out-legacy"),
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("invalid pixie layout");
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("producer rejects legacy pixie-assistant archive names before evidence generation", async () => {
	const fixture = await makeFixture();
	try {
		await writeFile(
			join(fixture.artifactsDir, `pixie-assistant-${releaseId}-linux-amd64.tar.gz`),
			"legacy archive",
		);
		const outputDir = join(fixture.root, "out");
		await expect(
			produceEvidenceInputs({
				outputDir,
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("legacy pixie-assistant archive");
		expect(await Bun.file(join(outputDir, "coverage.json")).exists()).toBe(false);
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("producer probes the product launcher that owns release identity", () => {
	expect(productProbeEntrypoint("pixie_web")).toBe("pixie_web");
	expect(productProbeEntrypoint("pixie")).toBe("pixie");
});

test("producer rejects an archive member that escapes the extraction root", async () => {
	const fixture = await makeFixture();
	try {
		const archive = join(fixture.artifactsDir, `pixie_web-${releaseId}-linux-amd64.tar.gz`);
		const entries = [
			...productArchiveLayout("pixie_web"),
			"runtime/../..",
			"runtime/../../escaped.txt",
		];
		await writeFile(
			archive,
			gzipSync(
				writeFixtureTar(entries.map((name) => ({ name, content: Buffer.from(`${name}\n`) }))),
			),
		);

		const outputDir = join(fixture.root, "out");
		await expect(
			produceEvidenceInputs({
				outputDir,
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("unsafe member path");
		expect(await Bun.file(join(outputDir, "coverage.json")).exists()).toBe(false);
		// Validation rejects the archive before the extraction root is created.
		await expect(stat(join(outputDir, "binaries"))).rejects.toThrow();
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("producer rejects a duplicate archive member before extraction", async () => {
	const fixture = await makeFixture();
	try {
		const archive = join(fixture.artifactsDir, `pixie_web-${releaseId}-linux-amd64.tar.gz`);
		const layout = productArchiveLayout("pixie_web");
		const duplicate = layout[layout.length - 1] ?? "pixie_web";
		await writeFile(
			archive,
			gzipSync(
				writeFixtureTar(
					[...layout, duplicate].map((name) => ({
						name,
						content: Buffer.from(`${name}\n`),
					})),
				),
			),
		);

		await expect(
			produceEvidenceInputs({
				outputDir: join(fixture.root, "out"),
				sourceCommit,
				artifactsDir: fixture.artifactsDir,
				binaryPaths: binaryPaths(fixture),
			}),
		).rejects.toThrow("duplicate member");
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});

test("an unsupported doctor command is blocked and never recorded as failed or executed", async () => {
	const fixture = await makeFixture();
	try {
		// The host binary answers --version but has no doctor command;
		// its usage error must not be misread as a failed probe.
		const usageOnly = `#!/bin/sh
case "$1" in
  --version) echo "pixie ${releaseId} (revision ${sourceCommit})"; exit 0 ;;
  *) echo "use serve --config ABS" >&2; exit 2 ;;
esac
`;
		await writeExecutable(fixture.host, usageOnly);
		const result = await produceEvidenceInputs({
			outputDir: join(fixture.root, "out"),
			sourceCommit,
			artifactsDir: fixture.artifactsDir,
			binaryPaths: binaryPaths(fixture),
		});

		const doctor = result.probes.find(
			(probe) => probe.name === "doctor" && probe.product === "pixie",
		);
		expect(doctor?.status).toBe("blocked");
		expect(
			result.coverage.evidence?.some((row) => /^pixie-(amd64|arm64)-doctor$/.test(row.test ?? "")),
		).toBe(false);
		// The identity probe still executed against the supervisor.
		const version = result.probes.find(
			(probe) => probe.name === "version" && probe.product === "pixie",
		);
		expect(version?.status).toBe("executed");
	} finally {
		await rm(fixture.root, { recursive: true, force: true });
	}
});
