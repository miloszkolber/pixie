import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	decodeProbeDetail,
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
	parsePackageArchiveAssertionId,
	readEvidenceBundle,
} from "../../scripts/evidence-bundle.ts";
import { identityInputFromEvidence, runReleaseGate } from "../../scripts/release-gate.ts";
import {
	FIXTURE_ARCHITECTURES,
	FIXTURE_PRODUCTS,
	fixtureBinaryName,
	stageArtifacts,
	writeDockerSaveTar,
	writeOciTar,
	writeProbeBinary,
	writeProbeEvidenceBundle,
} from "./staged-evidence-fixture.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";
const hash = "a".repeat(64);

const PRODUCTS = FIXTURE_PRODUCTS;
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
				id: "PKG-ARCHIVE-pixie_web-amd64",
				status: "pass",
				command: "sha256sum archive",
				artifact: { name: `pixie_web-${releaseId}-linux-amd64.tar.gz`, sha256: hash },
				detail: encodePackageArchiveDetail({
					product: "pixie_web",
					architecture: "amd64",
					entries: ["INSTALL.md", "LICENSE", "NOTICE.md", "pixie.json", "pixie_web"],
					binary: "pixie_web",
					binarySha256: hash,
				}),
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
						id: "PKG-ARCHIVE-pixie_web-amd64",
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
						id: "PKG-ARCHIVE-pixie_web-amd64",
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

		for (const product of PRODUCTS) {
			for (const architecture of ARCHITECTURES) {
				const assertion = bundle.assertions.find(
					(candidate) => candidate.id === packageArchiveAssertionId(product, architecture),
				);
				const archiveName = `${binaryName(product)}-${releaseId}-linux-${architecture}.tar.gz`;
				expect(assertion?.status).toBe("pass");
				expect(assertion?.artifact?.name).toBe(archiveName);
				expect(assertion?.artifact?.sha256).toBe(fixture.archiveSha256.get(archiveName));
				const detail = decodePackageArchiveDetail(assertion?.detail ?? "");
				expect(detail?.product).toBe(product);
				expect(detail?.binary).toBe(binaryName(product));
				expect(detail?.binarySha256).toBe(fixture.binarySha256.get(archiveName));
				expect(detail?.entries).toContain(binaryName(product));
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
		expect(imageDetail?.labels["org.opencontainers.image.version"]).toBe(releaseId);
		expect(imageDetail?.labels["org.opencontainers.image.revision"]).toBe(sourceCommit);
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

test("collect-evidence rejects a combined manifest missing a product artifact", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-manifest-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const manifestPath = join(fixture.artifactsDir, "release-manifest.json");
		const manifest = JSON.parse(await Bun.file(manifestPath).text()) as {
			artifacts: unknown[];
		};
		manifest.artifacts.pop();
		await writeFile(manifestPath, `${JSON.stringify(manifest)}\n`);

		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});
		const manifestAssertion = bundle.assertions.find(
			(candidate) => candidate.id === "RELEASE-MANIFEST",
		);
		expect(manifestAssertion?.status).toBe("fail");
		expect(manifestAssertion?.detail).toContain(
			"release-manifest.json artifacts must contain exactly four records",
		);
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

test("probe details carry and validate the executing architecture", () => {
	expect(
		decodeProbeDetail(
			JSON.stringify({
				path: "/release/pixie",
				stdout: `pixie ${releaseId}`,
				architecture: "arm64",
			}),
		),
	).toEqual({
		path: "/release/pixie",
		stdout: `pixie ${releaseId}`,
		architecture: "arm64",
	});
	// Bundles written before the field fall back to the bundle platform.
	expect(decodeProbeDetail(JSON.stringify({ path: "/release/pixie", stdout: "" }))).toEqual({
		path: "/release/pixie",
		stdout: "",
	});
	expect(decodeProbeDetail(JSON.stringify({ path: "/release/pixie", architecture: "sparc" }))).toBe(
		null,
	);
});

test("collect-evidence merges a second host's probe facts per architecture", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-probe-merge-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const binariesDir = join(root, "binaries");
		await mkdir(binariesDir, { recursive: true });
		const binaries = PRODUCTS.map((product) => join(binariesDir, product));
		for (const product of PRODUCTS)
			await writeProbeBinary(join(binariesDir, product), {
				name: product,
				releaseId,
				sourceCommit,
			});
		const hostArchitecture = process.arch === "arm64" ? "arm64" : "amd64";
		const otherArchitecture = hostArchitecture === "amd64" ? "arm64" : "amd64";
		const probeEvidencePath = join(root, `probe-evidence-${otherArchitecture}.json`);
		await writeProbeEvidenceBundle(probeEvidencePath, {
			architecture: otherArchitecture,
			sourceCommit,
			releaseId,
			generatedAt,
			binaries,
		});

		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
			binaryPaths: binaries,
			probeEvidencePath,
		});
		expect(bundle.platform.arch).toBe(hostArchitecture);

		const mapping = packageArtifactInputFromEvidence(bundle, {});
		expect(mapping.violations).toEqual([]);
		const mappedBinaries = mapping.input.binaries ?? [];
		expect(mappedBinaries).toHaveLength(4);
		for (const binary of mappedBinaries) {
			const label = `${binary.product}/${binary.architecture}`;
			expect(binary.path, label).toBe(binary.product);
		}
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("package archive identity never promotes an unexecuted probe into a second matrix", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-probe-native-only-"));
	try {
		const fixture = await stageArtifacts(root, true, sourceCommit, releaseId);
		await writeDockerSaveTar(fixture.imageTar, sourceCommit, releaseId);
		const binariesDir = join(root, "binaries");
		await mkdir(binariesDir, { recursive: true });
		const binaries = PRODUCTS.map((product) => join(binariesDir, product));
		for (const product of PRODUCTS)
			await writeProbeBinary(join(binariesDir, product), {
				name: product,
				releaseId,
				sourceCommit,
			});

		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
			binaryPaths: binaries,
		});
		const mapping = packageArtifactInputFromEvidence(bundle, {});
		expect(mapping.input.binaries).toHaveLength(4);
		expect(mapping.violations).toEqual([]);
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
				product: "pixie",
				architecture: "arm64",
				entries: ["pixie", "pixie.service"],
				binary: "pixie",
				binarySha256: hash,
			}),
		),
	).not.toBeNull();
	expect(decodePackageArchiveDetail("not json")).toBeNull();
	expect(decodePackageArchiveDetail(JSON.stringify({ variant: "host" }))).toBeNull();
	expect(parsePackageArchiveAssertionId("PKG-ARCHIVE-assistant-amd64")).toBeNull();
});
