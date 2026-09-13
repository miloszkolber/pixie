import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	collectPackageArtifactInput,
	defaultReductionsManifestPath,
	expandPackageArtifactReductions,
	expandReleaseGateReductions,
	inspectPackageArtifacts,
	loadReductionsManifest,
	packageArtifactInputFromEvidence,
	runPackageArtifactCheck,
} from "../../scripts/check-package-artifacts.ts";
import { collectEvidence } from "../../scripts/collect-evidence.ts";
import {
	collectReleaseGateInput,
	identityInputFromEvidence,
	inspectReleaseGate,
	runReleaseGate,
} from "../../scripts/release-gate.ts";
import { stageArtifacts, writeDockerSaveTar, writeProbeBinary } from "./staged-evidence-fixture.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";

interface StagedBundle {
	root: string;
	bundlePath: string;
}

async function buildStagedBundle(root: string): Promise<StagedBundle> {
	const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
	await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
	const binariesDir = join(root, "binaries");
	await mkdir(binariesDir, { recursive: true });
	const assistant = join(binariesDir, "pixie-assistant");
	const host = join(binariesDir, "pixie");
	await writeProbeBinary(assistant, { name: "pixie-assistant", releaseId, sourceCommit });
	await writeProbeBinary(host, { name: "pixie", releaseId, sourceCommit });
	const bundlePath = join(root, "evidence.json");
	const bundle = await collectEvidence({
		artifactsDir: fixture.artifactsDir,
		imagePath: fixture.imageTar,
		sourceCommit,
		releaseId,
		generatedAt,
		binaryPaths: [assistant, host],
	});
	await Bun.write(bundlePath, `${JSON.stringify(bundle, null, 2)}\n`);
	return { root, bundlePath };
}

async function withSilencedConsole<T>(callback: () => Promise<T>): Promise<T> {
	const originalLog = console.log;
	const originalError = console.error;
	console.log = () => {};
	console.error = () => {};
	try {
		return await callback();
	} finally {
		console.log = originalLog;
		console.error = originalError;
	}
}

test("both gates pass with the committed manifest on a synthetic exact-commit staged bundle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-reductions-"));
	try {
		const staged = await buildStagedBundle(root);
		const manifest = await loadReductionsManifest(defaultReductionsManifestPath());
		const packageReductions = expandPackageArtifactReductions(manifest);
		const releaseReductions = expandReleaseGateReductions(manifest);

		const base = await collectPackageArtifactInput();
		const packageInput = packageArtifactInputFromEvidence(
			await Bun.file(staged.bundlePath).json(),
			base,
		).input;
		const packageReport = inspectPackageArtifacts({
			...packageInput,
			reductions: packageReductions,
		});
		expect(packageReport.violations).toEqual([]);
		expect(packageReport.ok).toBe(true);
		// The native probes are real evidence, not reductions.
		expect(packageReport.facts.reducedLiveEvidence.join("\n")).not.toContain(
			"assistant/amd64 --version",
		);
		expect(packageReport.facts.reducedLiveEvidence.join("\n")).not.toContain("host/amd64 doctor");

		const gateInput = await collectReleaseGateInput(undefined, staged.bundlePath);
		const gateReport = inspectReleaseGate({
			...gateInput,
			policy: {
				...gateInput.policy,
				automaticMainAuthorized: true,
				sourceReachableFromMain: true,
			},
			reductions: releaseReductions,
		});
		expect(gateReport.violations).toEqual([]);
		expect(gateReport.ok).toBe(true);
		expect(gateReport.facts.reducedLiveInputs.length).toBeGreaterThan(0);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("a reduced row is reported as reduced and never as passing", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-reduced-fact-"));
	try {
		const staged = await buildStagedBundle(root);
		const manifest = await loadReductionsManifest(defaultReductionsManifestPath());
		const packageReductions = expandPackageArtifactReductions(manifest);
		const releaseReductions = expandReleaseGateReductions(manifest);

		const base = await collectPackageArtifactInput();
		const packageInput = packageArtifactInputFromEvidence(
			await Bun.file(staged.bundlePath).json(),
			base,
		).input;
		const packageReport = inspectPackageArtifacts({
			...packageInput,
			reductions: packageReductions,
		});
		const reduced = packageReport.facts.reducedLiveEvidence.join("\n");
		expect(reduced).toContain("start/stop/restart");
		expect(reduced).toContain("uninstall");
		expect(reduced).toContain("embedded UI");
		expect(reduced).toContain("facade");
		expect(reduced).toContain("assistant/arm64 --version");
		expect(reduced).toContain("readiness");
		expect(packageReport.missingLiveEvidence).toEqual([]);
		// A reduced row is not a static pass.
		expect(packageReport.facts.staticChecks.join("\n")).not.toContain("start/stop/restart");

		const gateInput = await collectReleaseGateInput(undefined, staged.bundlePath);
		const gateReport = inspectReleaseGate({
			...gateInput,
			policy: {
				...gateInput.policy,
				automaticMainAuthorized: true,
				sourceReachableFromMain: true,
			},
			reductions: releaseReductions,
		});
		const reducedInputs = gateReport.facts.reducedLiveInputs.join("\n");
		expect(reducedInputs).toContain("Git tag");
		expect(reducedInputs).toContain("GitHub Release");
		expect(reducedInputs).toContain("provenance");
		expect(reducedInputs).toContain("linux/arm64");
		expect(reducedInputs).toContain("latest");
		expect(reducedInputs).toContain("complete four-archive/image set");
		expect(gateReport.missingLiveInputs).toEqual([]);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("a dropped reduction sends a previously reduced row back to fail closed", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-drop-reduction-"));
	try {
		const staged = await buildStagedBundle(root);
		const manifest = await loadReductionsManifest(defaultReductionsManifestPath());
		const packageReductions = expandPackageArtifactReductions(manifest).filter(
			(reduction) => reduction.id !== "binary.lifecycle/*",
		);

		const base = await collectPackageArtifactInput();
		const packageInput = packageArtifactInputFromEvidence(
			await Bun.file(staged.bundlePath).json(),
			base,
		).input;
		const report = inspectPackageArtifacts({ ...packageInput, reductions: packageReductions });
		expect(report.ok).toBe(false);
		expect(report.missingLiveEvidence.join("\n")).toContain("start/stop/restart");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("mandatory structural rows still fail closed when a non-reduced row is missing", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-mandatory-"));
	try {
		const staged = await buildStagedBundle(root);
		const manifest = await loadReductionsManifest(defaultReductionsManifestPath());
		const packageReductions = expandPackageArtifactReductions(manifest);
		const releaseReductions = expandReleaseGateReductions(manifest);

		const base = await collectPackageArtifactInput();
		const mapped = packageArtifactInputFromEvidence(
			await Bun.file(staged.bundlePath).json(),
			base,
		).input;
		const archives = mapped.archives ?? [];
		const missingArchive = inspectPackageArtifacts({
			...mapped,
			archives: archives.slice(0, 3),
			reductions: packageReductions,
		});
		expect(missingArchive.ok).toBe(false);
		expect(missingArchive.missingLiveEvidence.join("\n")).toContain("release archive");

		// A checksum disagreement between the staged manifest and the observed
		// archive is a mandatory identity check and can never be reduced.
		const gateInput = await collectReleaseGateInput(undefined, staged.bundlePath);
		if (gateInput.identity.manifest === undefined) throw new Error("fixture manifest is missing");
		const mismatched = inspectReleaseGate({
			...gateInput,
			identity: {
				...gateInput.identity,
				archives: (gateInput.identity.archives ?? []).map((archive, index) =>
					index === 0 ? { ...archive, sha256: "f".repeat(64) } : archive,
				),
			},
			policy: {
				...gateInput.policy,
				automaticMainAuthorized: true,
				sourceReachableFromMain: true,
			},
			reductions: releaseReductions,
		});
		expect(mismatched.ok).toBe(false);
		expect(mismatched.violations.join("\n")).toContain("archive hash disagrees");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("an absent staged manifest is blocked and never invented as a pass", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-manifest-absent-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		await rm(join(fixture.artifactsDir, "release-manifest.json"));
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});
		const assertion = bundle.assertions.find((candidate) => candidate.id === "RELEASE-MANIFEST");
		expect(assertion?.status).toBe("blocked");
		const mapping = identityInputFromEvidence(bundle, {});
		expect(mapping.violations.join("\n")).toContain("RELEASE-MANIFEST");
		expect(mapping.identity.manifest).toBeUndefined();
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("the --evidence CLIs run green on the staged bundle with the committed manifest", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-cli-"));
	const previousAuthorized = process.env.PIXIE_AUTOMATIC_MAIN_AUTHORIZED;
	const previousReachable = process.env.PIXIE_SOURCE_REACHABLE_FROM_MAIN;
	try {
		const staged = await buildStagedBundle(root);
		process.env.PIXIE_AUTOMATIC_MAIN_AUTHORIZED = "true";
		process.env.PIXIE_SOURCE_REACHABLE_FROM_MAIN = "true";
		const packageCode = await withSilencedConsole(() =>
			runPackageArtifactCheck(undefined, staged.bundlePath),
		);
		const gateCode = await withSilencedConsole(() => runReleaseGate(undefined, staged.bundlePath));
		expect(packageCode).toBe(0);
		expect(gateCode).toBe(0);
	} finally {
		if (previousAuthorized === undefined) delete process.env.PIXIE_AUTOMATIC_MAIN_AUTHORIZED;
		else process.env.PIXIE_AUTOMATIC_MAIN_AUTHORIZED = previousAuthorized;
		if (previousReachable === undefined) delete process.env.PIXIE_SOURCE_REACHABLE_FROM_MAIN;
		else process.env.PIXIE_SOURCE_REACHABLE_FROM_MAIN = previousReachable;
		await rm(root, { recursive: true, force: true });
	}
});
