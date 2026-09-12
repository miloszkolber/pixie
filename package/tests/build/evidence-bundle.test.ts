import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	packageArtifactInputFromEvidence,
	runPackageArtifactCheck,
} from "../../scripts/check-package-artifacts.ts";
import { collectEvidence } from "../../scripts/collect-evidence.ts";
import { writeDeterministicTarGz } from "../../scripts/deterministic-tar.ts";
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

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";
const hash = "a".repeat(64);

const VARIANTS = ["assistant", "host"] as const;
const ARCHITECTURES = ["amd64", "arm64"] as const;

function sha256(value: Uint8Array): string {
	return createHash("sha256").update(value).digest("hex");
}

function binaryName(variant: (typeof VARIANTS)[number]): string {
	return variant === "assistant" ? "pixie-assistant" : "pixie";
}

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

function tarHeader(name: string, size: number, mode: number): Buffer {
	const header = Buffer.alloc(512);
	header.write(name, 0, "utf8");
	const writeOctal = (offset: number, width: number, value: number): void => {
		const text = value.toString(8).padStart(width - 1, "0");
		header.write(text, offset, width - 1, "utf8");
		header[offset + width - 1] = 0;
	};
	writeOctal(100, 8, mode);
	writeOctal(108, 8, 0);
	writeOctal(116, 8, 0);
	writeOctal(124, 12, size);
	writeOctal(136, 12, 0);
	header.write("        ", 148, 8, "utf8");
	header[156] = "0".charCodeAt(0);
	header.write("ustar", 257, "utf8");
	let checksum = 0;
	for (const byte of header) checksum += byte;
	header.write(checksum.toString(8).padStart(6, "0"), 148, 6, "utf8");
	header[154] = 0;
	header[155] = 0x20;
	return header;
}

function writeTar(entries: readonly { name: string; content: Buffer }[]): Buffer {
	const blocks: Buffer[] = [];
	for (const entry of entries) {
		blocks.push(tarHeader(entry.name, entry.content.byteLength, 0o644), entry.content);
		const padding = (512 - (entry.content.byteLength % 512)) % 512;
		if (padding !== 0) blocks.push(Buffer.alloc(padding));
	}
	blocks.push(Buffer.alloc(1024));
	return Buffer.concat(blocks);
}

interface StagedFixture {
	artifactsDir: string;
	imageTar: string;
	archiveSha256: ReadonlyMap<string, string>;
	binarySha256: ReadonlyMap<string, string>;
}

async function stageArtifacts(root: string, includeAll: boolean): Promise<StagedFixture> {
	const artifactsDir = join(root, "artifacts");
	const staging = join(root, "staging");
	await mkdir(artifactsDir, { recursive: true });
	await mkdir(staging, { recursive: true });
	const archiveSha256 = new Map<string, string>();
	const binarySha256 = new Map<string, string>();
	for (const variant of VARIANTS) {
		for (const architecture of ARCHITECTURES) {
			if (!includeAll && !(variant === "assistant" && architecture === "amd64")) continue;
			const binary = binaryName(variant);
			const unit = `${binary}.service`;
			const config = variant === "assistant" ? "assistant.json" : "pixie.json";
			const directory = join(staging, `${variant}-${architecture}`);
			await mkdir(directory, { recursive: true });
			const files = [
				{ name: binary, content: `binary-${variant}-${architecture}`, mode: 0o755 },
				{ name: unit, content: "[Unit]\n", mode: 0o644 },
				{ name: config, content: "{}\n", mode: 0o644 },
				{ name: "INSTALL.md", content: "install\n", mode: 0o644 },
				{ name: "LICENSE", content: "license\n", mode: 0o644 },
				{ name: "NOTICE.md", content: "notice\n", mode: 0o644 },
			];
			for (const file of files) await writeFile(join(directory, file.name), file.content);
			const archiveName = `${binary}-${releaseId}-linux-${architecture}.tar.gz`;
			const archivePath = join(artifactsDir, archiveName);
			await writeDeterministicTarGz(
				archivePath,
				files.map((file) => ({
					name: file.name,
					path: join(directory, file.name),
					mode: file.mode,
				})),
				1_700_000_000,
			);
			archiveSha256.set(archiveName, sha256(await readFile(archivePath)));
			binarySha256.set(archiveName, sha256(Buffer.from(`binary-${variant}-${architecture}`)));
		}
	}
	return { artifactsDir, imageTar: join(root, "controller.tar"), archiveSha256, binarySha256 };
}

async function writeDockerSaveTar(path: string): Promise<void> {
	const config = Buffer.from(
		JSON.stringify({
			architecture: "amd64",
			os: "linux",
			config: {
				Labels: {
					"org.opencontainers.image.version": releaseId,
					"org.opencontainers.image.revision": sourceCommit,
				},
			},
		}),
	);
	const configDigest = sha256(config);
	const manifest = Buffer.from(
		JSON.stringify([
			{ Config: `${configDigest}.json`, RepoTags: [`pixie:${releaseId}`], Layers: [] },
		]),
	);
	await writeFile(
		path,
		writeTar([
			{ name: "manifest.json", content: manifest },
			{ name: `${configDigest}.json`, content: config },
		]),
	);
}

async function writeOciTar(path: string): Promise<{ indexDigest: string; manifestDigest: string }> {
	const config = Buffer.from(
		JSON.stringify({
			architecture: "amd64",
			os: "linux",
			config: {
				Labels: {
					"org.opencontainers.image.version": releaseId,
					"org.opencontainers.image.revision": sourceCommit,
				},
			},
		}),
	);
	const configDigest = sha256(config);
	const manifest = Buffer.from(
		JSON.stringify({
			schemaVersion: 2,
			mediaType: "application/vnd.oci.image.manifest.v1+json",
			config: {
				mediaType: "application/vnd.oci.image.config.v1+json",
				digest: `sha256:${configDigest}`,
				size: config.byteLength,
			},
			layers: [],
		}),
	);
	const manifestDigest = sha256(manifest);
	const index = Buffer.from(
		JSON.stringify({
			schemaVersion: 2,
			manifests: [
				{
					mediaType: "application/vnd.oci.image.manifest.v1+json",
					digest: `sha256:${manifestDigest}`,
					size: manifest.byteLength,
					platform: { architecture: "amd64", os: "linux" },
					annotations: { "org.opencontainers.image.ref.name": `pixie:${releaseId}` },
				},
			],
		}),
	);
	await writeFile(
		path,
		writeTar([
			{ name: "oci-layout", content: Buffer.from('{"imageLayoutVersion":"1.0.0"}') },
			{ name: "index.json", content: index },
			{ name: `blobs/sha256/${manifestDigest}`, content: manifest },
			{ name: `blobs/sha256/${configDigest}`, content: config },
		]),
	);
	return { indexDigest: `sha256:${sha256(index)}`, manifestDigest: `sha256:${manifestDigest}` };
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
		const fixture = await stageArtifacts(root, true);
		await writeDockerSaveTar(fixture.imageTar);
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
		expect(bundle.assertions).toHaveLength(5);

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
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("collect-evidence reads a multi-platform OCI layout tar", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-oci-"));
	try {
		const fixture = await stageArtifacts(root, true);
		const ociTar = join(root, "controller.oci");
		const expected = await writeOciTar(ociTar);
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
		const fixture = await stageArtifacts(root, false);
		await writeDockerSaveTar(fixture.imageTar);
		const bundle = await collectEvidence({
			artifactsDir: fixture.artifactsDir,
			imagePath: fixture.imageTar,
			sourceCommit,
			releaseId,
			generatedAt,
		});
		const blocked = bundle.assertions.filter((assertion) => assertion.status === "blocked");
		expect(blocked).toHaveLength(3);
		expect(blocked.every((assertion) => assertion.artifact === undefined)).toBe(true);
		expect(blocked.every((assertion) => assertion.detail.includes("missing"))).toBe(true);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("gates derive real archive, binary and image evidence from a bundle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-evidence-gates-"));
	try {
		const fixture = await stageArtifacts(root, true);
		await writeDockerSaveTar(fixture.imageTar);
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
