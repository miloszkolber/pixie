import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	packageArtifactInputFromEvidence,
	runPackageArtifactCheck,
} from "../../scripts/check-package-artifacts.ts";
import { collectEvidence } from "../../scripts/collect-evidence.ts";
import {
	buildEvidenceBundle,
	CONTROLLER_IMAGE_ASSERTION_ID,
	decodeControllerImageDetail,
	decodePackageArchiveDetail,
	EvidenceBundleError,
	encodePackageArchiveDetail,
	packageArchiveAssertionId,
	parseEvidenceBundle,
	readEvidenceBundle,
} from "../../scripts/evidence-bundle.ts";
import { identityInputFromEvidence, runReleaseGate } from "../../scripts/release-gate.ts";
import {
	FIXTURE_ARCHITECTURES,
	FIXTURE_VARIANTS,
	fixtureBinaryName,
	stageArtifacts,
	writeDockerSaveTar,
	writeOciTar,
} from "./staged-evidence-fixture.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";
const hash = "a".repeat(64);

const VARIANTS = FIXTURE_VARIANTS;
const ARCHITECTURES = FIXTURE_ARCHITECTURES;
const binaryName = fixtureBinaryName;

function validBundle(): Record<string, unknown> {
	return {
		schemaVersion: 1,
		sourceCommit,
		releaseId,
		generatedAt,
		platform: { os: "linux", arch: "amd64" },
		profile: "full-host",
		assertions: [
			{
				kind: "GATE",
				id: "PKG-ARCHIVE-assistant-amd64",
				status: "pass",
				command: "sha256sum archive",
				artifact: { name: "archive.tar.gz", sha256: hash },
				detail: "inspected",
			},
		],
	};
}

async function withSilencedConsoleError<T>(callback: () => Promise<T>): Promise<T> {
	const original = console.error;
	console.error = () => {};
	try {
		return await callback();
	} finally {
		console.error = original;
	}
}

test("a valid bundle round-trips through parse and build", () => {
	const parsed = parseEvidenceBundle(validBundle());
	expect(parsed.schemaVersion).toBe(1);
	expect(parsed.releaseId).toBe(releaseId);
	expect(parsed.assertions).toHaveLength(1);

	const built = buildEvidenceBundle({
		sourceCommit,
		releaseId,
		generatedAt,
		platform: { os: "linux", arch: "amd64" },
		profile: "full-host",
		assertions: parsed.assertions,
	});
	expect(built).toEqual(parsed);
	expect(() => parseEvidenceBundle(JSON.parse(JSON.stringify(parsed)))).not.toThrow();
});

test("schema validation rejects every malformed bundle class", () => {
	const cases: readonly [string, Record<string, unknown>][] = [
		["schemaVersion", { ...validBundle(), schemaVersion: 3 }],
		["sourceCommit", { ...validBundle(), sourceCommit: "not-a-commit" }],
		["releaseId", { ...validBundle(), releaseId: `sha-${"f".repeat(12)}` }],
		["platform", { ...validBundle(), platform: { os: "darwin", arch: "amd64" } }],
		["profile", { ...validBundle(), profile: "legacy" }],
		[
			"duplicate",
			(() => {
				const bundle = validBundle();
				const assertions = bundle.assertions as unknown[];
				assertions.push({ ...(assertions[0] as Record<string, unknown>) });
				return bundle;
			})(),
		],
		[
			"command",
			{
				...validBundle(),
				assertions: [
					{
						kind: "GATE",
						id: "PKG-ARCHIVE-assistant-amd64",
						status: "pass",
						command: "",
						detail: "inspected",
					},
				],
			},
		],
		[
			"artifact digest",
			{
				...validBundle(),
				assertions: [
					{
						kind: "GATE",
						id: "PKG-ARCHIVE-assistant-amd64",
						status: "pass",
						command: "sha256sum archive",
						artifact: { name: "archive.tar.gz" },
						detail: "inspected",
					},
				],
			},
		],
	];
	for (const [label, bundle] of cases) {
		expect(() => parseEvidenceBundle(bundle), label).toThrow(EvidenceBundleError);
	}
});

test("collect-evidence inspects staged archives and a docker-save image", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});

		expect(bundle.schemaVersion).toBe(1);
		expect(bundle.profile).toBe("full-host");
		expect(bundle.platform).toEqual({ os: "linux", arch: "amd64" });
		expect(bundle.assertions).toHaveLength(9);

		for (const variant of VARIANTS) {
			for (const architecture of ARCHITECTURES) {
				const assertion = bundle.assertions.find(
					(candidate) => candidate.id === packageArchiveAssertionId(variant, architecture),
				);
				const archiveName = `${binaryName(variant)}-${releaseId}-linux-${architecture}.tar.gz`;
				expect(assertion?.status).toBe("pass");
				expect(assertion?.artifact?.name).toBe(archiveName);
				expect(assertion?.artifact?.sha256).toBe(fixture.archiveSha256.get(archiveName));
				const detail = decodePackageArchiveDetail(assertion?.detail ?? "");
				expect(detail?.binary).toBe(binaryName(variant));
				expect(detail?.binarySha256).toBe(fixture.binarySha256.get(archiveName));
				expect(detail?.entries).toContain(binaryName(variant));
			}
		}

		const image = bundle.assertions.find(
			(candidate) => candidate.id === CONTROLLER_IMAGE_ASSERTION_ID,
		);
		expect(image?.status).toBe("pass");
		const imageDetail = decodeControllerImageDetail(image?.detail ?? "");
		expect(imageDetail?.tag).toBe(`pixie:${releaseId}`);
		expect(imageDetail?.architecture).toBe("amd64");
		expect(imageDetail?.labels["org.opencontainers.image.revision"]).toBe(sourceCommit);
		expect(imageDetail?.indexDigest).toMatch(/^sha256:[0-9a-f]{64}$/);

		// Producers that were not fed real inputs stay explicitly blocked.
		for (const id of ["COVERAGE-01", "PERF-01", "BIN-PROBE-UNAVAILABLE"]) {
			expect(bundle.assertions.find((candidate) => candidate.id === id)?.status).toBe("blocked");
		}
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("collect-evidence reads a multi-platform OCI layout tar", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-oci-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		const ociTar = join(root, "controller.oci");
		const expected = await writeOciTar(ociTar, sourceCommit, releaseId);
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: ociTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});
		const image = bundle.assertions.find(
			(candidate) => candidate.id === CONTROLLER_IMAGE_ASSERTION_ID,
		);
		expect(image?.status).toBe("pass");
		const imageDetail = decodeControllerImageDetail(image?.detail ?? "");
		expect(imageDetail?.architecture).toBe("amd64");
		expect(imageDetail?.tag).toBe(`pixie:${releaseId}`);
		expect(imageDetail?.indexDigest).toBe(expected.indexDigest);
		expect(imageDetail?.platformDigests.amd64).toBe(expected.manifestDigest);
		expect(bundle.platform.arch).toBe("amd64");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("collect-evidence marks missing archives blocked instead of pass", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-missing-"));
	try {
		const fixture = await stageArtifacts(root, false, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});
		const blocked = bundle.assertions.filter((assertion) => assertion.status === "blocked");
		expect(blocked).toHaveLength(6);
		expect(blocked.every((assertion) => assertion.artifact === undefined)).toBe(true);
		const archiveBlocked = blocked.filter((assertion) => assertion.id.startsWith("PKG-ARCHIVE-"));
		expect(archiveBlocked).toHaveLength(3);
		expect(archiveBlocked.every((assertion) => assertion.detail.includes("missing"))).toBe(true);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("gates derive real archive, binary and image evidence from a bundle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-gates-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});

		const packageMapping = packageArtifactInputFromEvidence(bundle, {});
		expect(packageMapping.violations).toEqual([]);
		expect(packageMapping.input.releaseId).toBe(releaseId);
		expect(packageMapping.input.archives).toHaveLength(4);
		expect(packageMapping.input.binaries).toHaveLength(4);

		const identity = identityInputFromEvidence(bundle, {});
		expect(identity.violations).toEqual([]);
		expect(identity.identity.archives).toHaveLength(4);
		expect(identity.identity.binaries).toHaveLength(4);
		expect(identity.identity.docker?.indexDigest).toMatch(/^sha256:[0-9a-f]{64}$/);
		expect(identity.identity.docker?.labels?.["org.opencontainers.image.version"]).toBe(releaseId);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("gates fail closed on a malformed evidence bundle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-broken-"));
	try {
		const broken = join(root, "broken.json");
		await writeFile(broken, "{ not valid json");
		const packageCode = await withSilencedConsoleError(() =>
			runPackageArtifactCheck(undefined, broken),
		);
		const releaseCode = await withSilencedConsoleError(() => runReleaseGate(undefined, broken));
		expect(packageCode).toBe(1);
		expect(releaseCode).toBe(1);
		await expect(readEvidenceBundle(broken)).rejects.toThrow(EvidenceBundleError);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("archive detail encoding rejects malformed payloads", () => {
	expect(
		decodePackageArchiveDetail(
			encodePackageArchiveDetail({
				variant: "host",
				architecture: "arm64",
				entries: ["pixie", "pixie.service"],
				binary: "pixie",
				binarySha256: hash,
			}),
		),
	).not.toBeNull();
	expect(decodePackageArchiveDetail("not json")).toBeNull();
	expect(decodePackageArchiveDetail(JSON.stringify({ variant: "host" }))).toBeNull();
});
