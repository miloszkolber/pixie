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
import {
	stageArtifacts,
	writeDockerSaveTar,
	writeProbeBinary,
	writeProbeEvidenceBundle,
} from "./staged-evidence-fixture.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";
const nativeArch = process.arch === "arm64" ? "arm64" : "amd64";
const otherArch = nativeArch === "amd64" ? "arm64" : "amd64";

interface StagedBundle {
	root: string;
	bundlePath: string;
}

async function buildStagedBundle(
	root: string,
	options: { otherArchProbe?: boolean } = {},
): Promise<StagedBundle> {
	const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
	await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
	const binariesDir = join(root, "binaries");
	await mkdir(binariesDir, { recursive: true });
	const cli = join(binariesDir, "pixie_cli");
	const full = join(binariesDir, "pixie");
	await writeProbeBinary(cli, { name: "pixie_cli", releaseId, sourceCommit });
	await writeProbeBinary(full, { name: "pixie", releaseId, sourceCommit });
	const bundlePath = join(root, "evidence.json");
	// A second collector host contributes the non-native probes that the single
	// bundle must carry for the four formerly-reduced version/doctor rows.
	let probeEvidencePath: string | undefined;
	if (options.otherArchProbe !== false) {
		probeEvidencePath = join(root, `probe-evidence-${otherArch}.json`);
		await writeProbeEvidenceBundle(probeEvidencePath, {
			architecture: otherArch,
			sourceCommit,
			releaseId,
			generatedAt,
			binaries: [cli, full],
		});
	}
	const bundle = await collectEvidence({
		artifactsDir: fixture.artifactsDir,
		imagePath: fixture.imageTar,
		sourceCommit,
		releaseId,
		generatedAt,
		binaryPaths: [cli, full],
		...(probeEvidencePath === undefined ? {} : { probeEvidencePath }),
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
		// The product archive contract is structural. Native probes remain in the
		// frozen coverage/performance producers rather than adding package rows.
		expect(packageReport.facts.reducedLiveEvidence).toEqual([]);
		expect(packageReport.missingLiveEvidence).toEqual([]);

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

test("a bundle missing the non-native architecture's probes keeps archive validation structural", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-gate-other-arch-missing-"));
	try {
		const staged = await buildStagedBundle(root, { otherArchProbe: false });
		const manifest = await loadReductionsManifest(defaultReductionsManifestPath());
		const packageReductions = expandPackageArtifactReductions(manifest);

		const base = await collectPackageArtifactInput();
		const packageInput = packageArtifactInputFromEvidence(
			await Bun.file(staged.bundlePath).json(),
			base,
		).input;
		const report = inspectPackageArtifacts({ ...packageInput, reductions: packageReductions });
		expect(report.ok).toBe(true);
		expect(report.missingLiveEvidence).toEqual([]);
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
		expect(reduced).toBe("");
		expect(packageReport.missingLiveEvidence).toEqual([]);

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
		expect(reducedInputs).toContain("complete-set evidence");
		expect(gateReport.missingLiveInputs).toEqual([]);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("unused legacy package reductions do not weaken the product archive contract", async () => {
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
		expect(report.ok).toBe(true);
		expect(report.missingLiveEvidence).toEqual([]);
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
