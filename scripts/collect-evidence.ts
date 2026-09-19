#!/usr/bin/env bun

import { createHash } from "node:crypto";
import { mkdir, readdir, readFile, stat, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import { assertProductArchiveLayout, RELEASE_PRODUCTS } from "./build-release.ts";
import { type CoverageInput, formatCoverageReport, inspectCoverage } from "./check-coverage.ts";
import {
	expectedArchiveName,
	type PackageArchitecture,
	type PackageProduct,
	RELEASE_MANIFEST_ASSERTION_ID,
} from "./check-package-artifacts.ts";
import {
	formatPerformanceReport,
	inspectPerformance,
	type PerformanceInput,
} from "./check-performance.ts";
import {
	buildEvidenceBundle,
	CONTROLLER_IMAGE_ASSERTION_ID,
	COVERAGE_ASSERTION_ID,
	type ControllerImageDetail,
	decodePackageArchiveDetail,
	decodeProbeDetail,
	type EvidenceAssertion,
	type EvidenceBundle,
	type EvidencePlatformArchitecture,
	encodeControllerImageDetail,
	encodePackageArchiveDetail,
	PACKAGED_BINARY_ASSERTION_PREFIX,
	PERFORMANCE_ASSERTION_ID,
	packageArchiveAssertionId,
	readEvidenceBundle,
} from "./evidence-bundle.ts";

const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
const SHA256_PATTERN = /^[0-9a-f]{64}$/;
const TAR_BLOCK_SIZE = 512;

const PRODUCTS = RELEASE_PRODUCTS;
const ARCHITECTURES = ["amd64", "arm64"] as const;

/**
 * Architecture of the host executing the probes. Probe facts are only ever
 * recorded for the machine that actually ran the binary, and `platform.arch`
 * always names that host rather than an architecture found inside the image.
 */
function hostArchitecture(): EvidencePlatformArchitecture {
	return process.arch === "arm64" ? "arm64" : "amd64";
}

const USAGE = [
	"usage: bun scripts/collect-evidence.ts --artifacts <dir> --image <dir-or-tar>",
	"         --source-commit <40-hex> --release-id sha-<12> --output <evidence.json>",
	"         [--coverage <json>] [--performance <json>] [--binary <path>]...",
	"         [--probe-evidence <other-bundle.json>] [--base-url <origin>]",
	"",
	"Inputs may also come from ARTIFACT_DIR, IMAGE_DIR, SOURCE_COMMIT, RELEASE_ID,",
	"EVIDENCE_OUTPUT and PROBE_EVIDENCE. The artifact directory must hold the",
	"current commit-named product archives. A release evidence bundle requires the merged",
	"four-archive checksums.txt and release-manifest.json; native per-architecture",
	"staging metadata is not final release evidence.",
	"",
	"Archive and image inspection is local: archives are decompressed and hashed,",
	"and the image tar is read as docker-save or OCI layout. No docker daemon,",
	"registry or publication state is involved. Missing expected artifacts are",
	"recorded as blocked, never as pass.",
	"",
	"--coverage and --performance embed a real coverage/performance input after",
	"re-running its gate; absent inputs produce blocked rows. Each --binary is",
	"executed for --version and doctor, and a readiness GET is attempted against",
	"--base-url when present. A probe is pass only after it ran and succeeded; a",
	"skipped or unsupported probe is blocked and a failing probe is fail. Every",
	"probe detail records the executing host architecture.",
	"",
	"--probe-evidence merges the BIN-PROBE assertions from another validated",
	"bundle (for example the native arm64 collector) into this bundle. The merged",
	"ids are qualified with the fact's architecture so both architectures coexist;",
	"platform.arch always stays the current host.",
	"",
	"Known gap: Git tag/GitHub Release, registry provenance/SBOM and latest",
	"promotion evidence are not local and stay blocked.",
].join("\n");

export interface CollectEvidenceOptions {
	artifactsDir: string;
	imagePath: string;
	sourceCommit: string;
	releaseId: string;
	generatedAt: string;
	/** Raw coverage evidence input; absent produces a blocked COVERAGE-01 row. */
	coveragePath?: string;
	/** Raw four-target performance evidence input; absent produces a blocked PERF-01 row. */
	performancePath?: string;
	/** Packaged binaries to execute. Absent produces a blocked BIN-PROBE-UNAVAILABLE row. */
	binaryPaths?: readonly string[];
	/** Validated bundle whose native BIN-PROBE facts are merged into this one. */
	probeEvidencePath?: string;
	/** Origin used for the readiness GET; absent keeps readiness blocked. */
	baseUrl?: string;
}

export interface TarEntry {
	name: string;
	content: Buffer;
}

function sha256(buffer: Uint8Array): string {
	return createHash("sha256").update(buffer).digest("hex");
}

function readTarString(buffer: Buffer, offset: number, length: number): string {
	const field = buffer.subarray(offset, offset + length);
	const terminator = field.indexOf(0);
	const bytes = terminator === -1 ? field : field.subarray(0, terminator);
	return new TextDecoder().decode(bytes);
}

function parseTar(tar: Buffer): TarEntry[] {
	const entries: TarEntry[] = [];
	let offset = 0;
	while (offset + TAR_BLOCK_SIZE <= tar.byteLength) {
		const header = tar.subarray(offset, offset + TAR_BLOCK_SIZE);
		if (header.every((byte) => byte === 0)) break;
		const name = readTarString(header, 0, 100);
		const sizeField = readTarString(header, 124, 12).trim();
		const size = sizeField === "" ? 0 : Number.parseInt(sizeField, 8);
		if (!Number.isSafeInteger(size) || size < 0)
			throw new Error(`tar entry ${JSON.stringify(name)} has an invalid size`);
		const type = String.fromCharCode(header[156] ?? 0);
		const contentStart = offset + TAR_BLOCK_SIZE;
		const contentEnd = contentStart + size;
		if (contentEnd > tar.byteLength)
			throw new Error(`tar entry ${JSON.stringify(name)} exceeds the archive`);
		const prefix = readTarString(header, 345, 155);
		const fullName = prefix === "" ? name : `${prefix}/${name}`;
		if (type === "0" || type === "\0" || type === " ") {
			entries.push({
				name: fullName.replace(/^\.\//, ""),
				content: tar.subarray(contentStart, contentEnd),
			});
		}
		offset = contentStart + Math.ceil(size / TAR_BLOCK_SIZE) * TAR_BLOCK_SIZE;
	}
	return entries;
}

export function parseTarGz(buffer: Buffer): TarEntry[] {
	return parseTar(gunzipSync(buffer));
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

async function readOptional(path: string): Promise<string | null> {
	return readFile(path, "utf8").catch(() => null);
}

/**
 * Cross-check staged archive digests against whichever declaration the staging
 * step produced. `checksums.txt` matches build-release.ts; `SHA256SUMS` and
 * `release-manifest.json` cover the other producers.
 */
async function readDeclaredHashes(artifactsDir: string): Promise<Map<string, string>> {
	const hashes = new Map<string, string>();
	const manifestText = await readOptional(join(artifactsDir, "release-manifest.json"));
	if (manifestText !== null) {
		try {
			const manifest = JSON.parse(manifestText) as { archiveHashes?: unknown };
			if (isRecord(manifest.archiveHashes)) {
				for (const [name, value] of Object.entries(manifest.archiveHashes)) {
					if (typeof value === "string" && SHA256_PATTERN.test(value)) hashes.set(name, value);
				}
			}
		} catch {
			// A malformed manifest is ignored here; the gate consumes the bundle.
		}
	}
	for (const name of ["checksums.txt", "SHA256SUMS"]) {
		const text = await readOptional(join(artifactsDir, name));
		if (text === null) continue;
		for (const line of text.split("\n")) {
			const match = line.trim().match(/^([0-9a-f]{64})\s+\*?(.+)$/);
			if (match === null) continue;
			hashes.set((match[2] ?? "").trim(), match[1] ?? "");
		}
	}
	return hashes;
}

interface StagedArchiveFact {
	product: PackageProduct;
	architecture: PackageArchitecture;
	name: string;
	archiveSha256: string;
	entrypointSha256: string;
	entries: readonly string[];
}

function exactStrings(left: readonly string[], right: readonly string[]): boolean {
	return left.length === right.length && left.every((value, index) => value === right[index]);
}

function recordStringMap(value: unknown): Record<string, string> | null {
	if (!isRecord(value)) return null;
	const result: Record<string, string> = {};
	for (const [key, entry] of Object.entries(value)) {
		if (typeof entry !== "string") return null;
		result[key] = entry;
	}
	return result;
}

function archiveFacts(assertions: readonly EvidenceAssertion[]): StagedArchiveFact[] {
	const facts: StagedArchiveFact[] = [];
	for (const assertion of assertions) {
		if (assertion.kind !== "GATE" || assertion.status !== "pass") continue;
		const key = assertion.id.startsWith("PKG-ARCHIVE-")
			? assertion.id.slice("PKG-ARCHIVE-".length)
			: "";
		const separator = key.lastIndexOf("-");
		const product = key.slice(0, separator);
		const architecture = key.slice(separator + 1);
		if (!(PRODUCTS as readonly string[]).includes(product)) continue;
		if (!(ARCHITECTURES as readonly string[]).includes(architecture)) continue;
		const detail = decodePackageArchiveDetail(assertion.detail);
		if (
			detail === null ||
			assertion.artifact === undefined ||
			detail.product !== product ||
			detail.architecture !== architecture
		)
			continue;
		facts.push({
			product: product as PackageProduct,
			architecture: architecture as PackageArchitecture,
			name: assertion.artifact.name,
			archiveSha256: assertion.artifact.sha256,
			entrypointSha256: detail.binarySha256,
			entries: detail.entries,
		});
	}
	return facts;
}

async function finalManifestAssertion(
	artifactsDir: string,
	sourceCommit: string,
	releaseId: string,
	assertions: readonly EvidenceAssertion[],
): Promise<EvidenceAssertion> {
	const command = "sha256sum --check checksums.txt && jq -e . release-manifest.json";
	const manifestText = await readOptional(join(artifactsDir, "release-manifest.json"));
	if (manifestText === null) {
		return gateAssertion(
			RELEASE_MANIFEST_ASSERTION_ID,
			"blocked",
			"test -f release-manifest.json",
			"merged release-manifest.json was not found",
		);
	}
	const checksumText = await readOptional(join(artifactsDir, "checksums.txt"));
	if (checksumText === null) {
		return gateAssertion(
			RELEASE_MANIFEST_ASSERTION_ID,
			"blocked",
			"test -f checksums.txt",
			"merged checksums.txt was not found",
		);
	}
	let manifest: unknown;
	try {
		manifest = JSON.parse(manifestText);
	} catch (error) {
		return gateAssertion(
			RELEASE_MANIFEST_ASSERTION_ID,
			"fail",
			command,
			`merged release-manifest.json is not valid JSON: ${errorMessage(error)}`,
		);
	}
	const issues: string[] = [];
	if (!isRecord(manifest)) {
		issues.push("release-manifest.json root must be a JSON object");
	} else {
		if (manifest.schemaVersion !== 2) issues.push("release-manifest.json schemaVersion must be 2");
		if (manifest.releaseId !== releaseId)
			issues.push("release-manifest.json releaseId does not match the selected source commit");
		if (manifest.sourceCommit !== sourceCommit)
			issues.push("release-manifest.json sourceCommit does not match the selected source commit");
		if (manifest.cleanSourceTree !== true)
			issues.push("release-manifest.json must record a clean source tree");
		if (manifest.completeSet !== true)
			issues.push("release-manifest.json must record the merged complete four-archive set");
		if (
			!Array.isArray(manifest.products) ||
			!exactStrings(
				manifest.products.filter((value): value is string => typeof value === "string"),
				PRODUCTS,
			)
		)
			issues.push("release-manifest.json products must list the two public products exactly once");
		if (
			!Array.isArray(manifest.architectures) ||
			!exactStrings(
				manifest.architectures.filter((value): value is string => typeof value === "string"),
				ARCHITECTURES,
			)
		)
			issues.push(
				"release-manifest.json architectures must list linux amd64 and arm64 exactly once",
			);

		const facts = archiveFacts(assertions);
		const expected = new Map(
			PRODUCTS.flatMap((product) =>
				ARCHITECTURES.map(
					(architecture) =>
						[
							expectedArchiveName(product, architecture, releaseId),
							{ product, architecture },
						] as const,
				),
			),
		);
		const factsByName = new Map(facts.map((fact) => [fact.name, fact]));
		if (factsByName.size !== expected.size)
			issues.push("not every expected archive was inspected before validating the merged manifest");

		const hashes = recordStringMap(manifest.archiveHashes);
		if (hashes === null) {
			issues.push("release-manifest.json archiveHashes must be a string map");
		} else {
			if (!exactStrings(Object.keys(hashes).sort(), [...expected.keys()].sort()))
				issues.push(
					"release-manifest.json archiveHashes must contain exactly the four public archives",
				);
			for (const [name, fact] of factsByName) {
				if (hashes[name] !== fact.archiveSha256)
					issues.push(`release-manifest.json archive hash disagrees with ${name}`);
			}
		}

		if (!Array.isArray(manifest.artifacts) || manifest.artifacts.length !== expected.size) {
			issues.push("release-manifest.json artifacts must contain exactly four records");
		} else {
			const manifestArchives = new Set<string>();
			for (const artifact of manifest.artifacts) {
				if (!isRecord(artifact)) {
					issues.push("release-manifest.json artifacts must be objects");
					continue;
				}
				const archive = typeof artifact.archive === "string" ? artifact.archive : "";
				const expectedProduct = expected.get(archive);
				if (expectedProduct === undefined || manifestArchives.has(archive)) {
					issues.push(
						`release-manifest.json has an unknown or duplicate artifact ${archive || "<missing>"}`,
					);
					continue;
				}
				manifestArchives.add(archive);
				if (
					artifact.product !== expectedProduct.product ||
					artifact.architecture !== expectedProduct.architecture ||
					artifact.entrypoint !== expectedProduct.product
				) {
					issues.push(`release-manifest.json artifact identity is invalid for ${archive}`);
				}
				const fact = factsByName.get(archive);
				if (
					fact !== undefined &&
					(artifact.archiveSha256 !== fact.archiveSha256 ||
						artifact.entrypointSha256 !== fact.entrypointSha256 ||
						!Array.isArray(artifact.entries) ||
						!exactStrings(
							artifact.entries.filter((value): value is string => typeof value === "string").sort(),
							[...fact.entries].sort(),
						))
				) {
					issues.push(`release-manifest.json artifact detail disagrees with ${archive}`);
				}
			}
			if (manifestArchives.size !== expected.size)
				issues.push(
					"release-manifest.json artifacts do not cover every public product and architecture",
				);
		}
	}

	const checksums = new Map<string, string>();
	for (const line of checksumText.split("\n")) {
		if (line.trim() === "") continue;
		const match = line.match(/^([0-9a-f]{64})\s{2}([^\s]+)$/);
		if (match === null) {
			issues.push("checksums.txt contains a malformed checksum line");
			continue;
		}
		const name = match[2] ?? "";
		if (checksums.has(name)) issues.push(`checksums.txt duplicates ${name}`);
		checksums.set(name, match[1] ?? "");
	}
	const expectedNames = PRODUCTS.flatMap((product) =>
		ARCHITECTURES.map((architecture) => expectedArchiveName(product, architecture, releaseId)),
	);
	if (!exactStrings([...checksums.keys()].sort(), [...expectedNames].sort()))
		issues.push("checksums.txt must contain exactly the four public archives");
	for (const fact of archiveFacts(assertions)) {
		if (checksums.get(fact.name) !== fact.archiveSha256)
			issues.push(`checksums.txt hash disagrees with ${fact.name}`);
	}
	const archiveNames = (await readdir(artifactsDir))
		.filter((name) => name.endsWith(".tar.gz"))
		.sort();
	if (!exactStrings(archiveNames, [...expectedNames].sort()))
		issues.push("staged release directory must contain exactly the four current public archives");
	return gateAssertion(
		RELEASE_MANIFEST_ASSERTION_ID,
		issues.length === 0 ? "pass" : "fail",
		command,
		issues.length === 0 ? manifestText : issues.join("; "),
	);
}

function binaryName(product: PackageProduct): string {
	return product;
}

async function inspectArchive(
	artifactsDir: string,
	releaseId: string,
	product: PackageProduct,
	architecture: PackageArchitecture,
	declaredHashes: ReadonlyMap<string, string>,
): Promise<EvidenceAssertion> {
	const name = expectedArchiveName(product, architecture, releaseId);
	const binary = binaryName(product);
	const id = packageArchiveAssertionId(product, architecture);
	const command = `sha256sum ${name} && gzip -dc ${name} | tar -xO -f - ${binary} | sha256sum`;
	let buffer: Buffer;
	try {
		buffer = await readFile(join(artifactsDir, name));
	} catch {
		return {
			kind: "GATE",
			id,
			status: "blocked",
			command: `test -f ${name}`,
			detail: `staged archive ${name} is missing`,
		};
	}
	const archiveSha256 = sha256(buffer);
	const declared = declaredHashes.get(name);
	if (declared !== undefined && declared !== archiveSha256) {
		return {
			kind: "GATE",
			id,
			status: "fail",
			command,
			artifact: { name, sha256: archiveSha256 },
			detail: `staged archive SHA-256 ${archiveSha256} disagrees with declared ${declared}`,
		};
	}
	let entries: TarEntry[];
	try {
		entries = parseTarGz(buffer);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return {
			kind: "GATE",
			id,
			status: "fail",
			command,
			artifact: { name, sha256: archiveSha256 },
			detail: `staged archive could not be inspected: ${message}`,
		};
	}
	const member = entries.find((entry) => entry.name === binary);
	if (member === undefined) {
		return {
			kind: "GATE",
			id,
			status: "fail",
			command,
			artifact: { name, sha256: archiveSha256 },
			detail: `archive does not contain the expected ${binary} executable`,
		};
	}
	try {
		assertProductArchiveLayout(
			product,
			entries.map((entry) => entry.name),
		);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return {
			kind: "GATE",
			id,
			status: "fail",
			command,
			artifact: { name, sha256: archiveSha256 },
			detail: `archive layout is invalid for ${product}: ${message}`,
		};
	}
	const binarySha256 = sha256(member.content);
	return {
		kind: "GATE",
		id,
		status: "pass",
		command,
		artifact: { name, sha256: archiveSha256 },
		detail: encodePackageArchiveDetail({
			product,
			architecture,
			entries: entries.map((entry) => entry.name),
			binary,
			binarySha256,
		}),
	};
}

interface ImageInspection {
	assertion: EvidenceAssertion;
	detail?: ControllerImageDetail;
}

function blobPath(digest: string): string {
	return `blobs/${digest.replace(":", "/")}`;
}

function labelsFrom(config: Record<string, unknown>): Record<string, string> {
	const container = isRecord(config.config) ? config.config : undefined;
	const labels =
		container !== undefined && isRecord(container.Labels) ? container.Labels : undefined;
	const result: Record<string, string> = {};
	if (labels === undefined) return result;
	for (const [key, value] of Object.entries(labels)) {
		if (typeof value === "string") result[key] = value;
	}
	return result;
}

// imageConfigDigest resolves the OCI config blob for one platform manifest. An
// OCI index/layout entry names the manifest blob, whose `config.digest` names
// the config; a Docker-save record may carry `config` directly.
function imageConfigDigest(
	entries: ReadonlyMap<string, TarEntry>,
	record: OciManifestRecord,
): string | undefined {
	if (isRecord(record.config) && typeof record.config.digest === "string") {
		return record.config.digest;
	}
	if (typeof record.digest !== "string") return undefined;
	const manifestEntry = entries.get(blobPath(record.digest));
	if (manifestEntry === undefined) return undefined;
	try {
		const manifest = JSON.parse(manifestEntry.content.toString("utf8")) as unknown;
		if (
			isRecord(manifest) &&
			isRecord(manifest.config) &&
			typeof manifest.config.digest === "string"
		) {
			return manifest.config.digest;
		}
	} catch {
		// A non-JSON blob is not a manifest.
	}
	return undefined;
}

function inspectDockerSave(entries: ReadonlyMap<string, TarEntry>): ControllerImageDetail | null {
	const manifestEntry = entries.get("manifest.json");
	if (manifestEntry === undefined) return null;
	let manifest: unknown;
	try {
		manifest = JSON.parse(manifestEntry.content.toString("utf8"));
	} catch {
		return null;
	}
	if (!Array.isArray(manifest)) return null;
	const records = manifest.filter(isRecord);
	for (const record of records) {
		const configName = typeof record.Config === "string" ? record.Config : undefined;
		if (configName === undefined) continue;
		const configEntry = entries.get(configName.replace(/^\.\//, ""));
		if (configEntry === undefined) continue;
		let config: unknown;
		try {
			config = JSON.parse(configEntry.content.toString("utf8"));
		} catch {
			continue;
		}
		if (!isRecord(config)) continue;
		const architecture = config.architecture;
		const os = config.os;
		if (os !== "linux" || (architecture !== "amd64" && architecture !== "arm64")) continue;
		const digestHex = basename(configName).replace(/\.json$/, "");
		const digest = SHA256_PATTERN.test(digestHex)
			? `sha256:${digestHex}`
			: `sha256:${sha256(configEntry.content)}`;
		const repoTags = Array.isArray(record.RepoTags)
			? record.RepoTags.filter((tag): tag is string => typeof tag === "string")
			: [];
		return {
			tag: repoTags[0] ?? null,
			indexDigest: digest,
			platformDigests: { [architecture]: digest },
			architecture,
			os: "linux",
			labels: labelsFrom(config),
			provenance: false,
			sbom: false,
		};
	}
	return null;
}

interface OciManifestRecord extends Record<string, unknown> {
	digest?: string;
	platform?: Record<string, unknown>;
	annotations?: Record<string, unknown>;
	config?: Record<string, unknown>;
}

function attestationPredicates(
	entries: ReadonlyMap<string, TarEntry>,
	digest: string,
): readonly string[] {
	const blob = entries.get(blobPath(digest));
	if (blob === undefined) return [];
	let manifest: unknown;
	try {
		manifest = JSON.parse(blob.content.toString("utf8"));
	} catch {
		return [];
	}
	if (!isRecord(manifest) || !Array.isArray(manifest.layers)) return [];
	const predicates: string[] = [];
	for (const layer of manifest.layers) {
		if (!isRecord(layer) || !isRecord(layer.annotations)) continue;
		const predicate = layer.annotations["in-toto.io/predicate-type"];
		if (typeof predicate === "string") predicates.push(predicate.toLowerCase());
	}
	return predicates;
}

function inspectOci(entries: ReadonlyMap<string, TarEntry>): ControllerImageDetail | null {
	const indexEntry = entries.get("index.json");
	if (indexEntry === undefined) return null;
	let index: unknown;
	try {
		index = JSON.parse(indexEntry.content.toString("utf8"));
	} catch {
		return null;
	}
	if (!isRecord(index) || !Array.isArray(index.manifests)) return null;
	const topLevel = index.manifests.filter(isRecord) as OciManifestRecord[];
	let platformManifests = topLevel.filter(
		(record) => isRecord(record.platform) && record.platform.os === "linux",
	);
	if (platformManifests.length === 0) {
		for (const record of topLevel) {
			if (typeof record.digest !== "string") continue;
			const blob = entries.get(blobPath(record.digest));
			if (blob === undefined) continue;
			try {
				const nested = JSON.parse(blob.content.toString("utf8")) as unknown;
				if (isRecord(nested) && Array.isArray(nested.manifests)) {
					platformManifests = nested.manifests
						.filter(isRecord)
						.filter(
							(candidate) => isRecord(candidate.platform) && candidate.platform.os === "linux",
						) as OciManifestRecord[];
					if (platformManifests.length > 0) break;
				}
			} catch {
				// A non-JSON blob is not a nested index; keep looking.
			}
		}
	}
	const platformDigests: Partial<Record<EvidencePlatformArchitecture, string>> = {};
	const attestationDigests: string[] = [];
	let chosen: OciManifestRecord | undefined;
	for (const record of topLevel.concat(platformManifests)) {
		if (typeof record.digest !== "string") continue;
		const annotations = isRecord(record.annotations) ? record.annotations : {};
		if (annotations["vnd.docker.reference.type"] === "attestation-manifest") {
			attestationDigests.push(record.digest);
			continue;
		}
		const platform = isRecord(record.platform) ? record.platform : undefined;
		if (platform === undefined) continue;
		const architecture = platform.architecture;
		if (platform.os !== "linux" || (architecture !== "amd64" && architecture !== "arm64")) continue;
		platformDigests[architecture] = record.digest;
		if (architecture === "amd64" || chosen === undefined) chosen = record;
	}
	const hasAmd64 = platformDigests.amd64 !== undefined;
	const resolvedArchitecture: EvidencePlatformArchitecture = hasAmd64 ? "amd64" : "arm64";
	if (platformDigests[resolvedArchitecture] === undefined) return null;
	let labels: Record<string, string> = {};
	const configDigest = chosen === undefined ? undefined : imageConfigDigest(entries, chosen);
	if (configDigest !== undefined) {
		const configEntry = entries.get(blobPath(configDigest));
		if (configEntry !== undefined) {
			try {
				const config = JSON.parse(configEntry.content.toString("utf8")) as unknown;
				if (isRecord(config)) labels = labelsFrom(config);
			} catch {
				// Labels are optional local metadata; the label checks stay fail-closed.
			}
		}
	}
	const predicates = attestationDigests.flatMap((digest) => attestationPredicates(entries, digest));
	let tag: string | null = null;
	const indexAnnotations = isRecord(index.annotations) ? index.annotations : {};
	const indexRef = indexAnnotations["org.opencontainers.image.ref.name"];
	if (typeof indexRef === "string" && indexRef !== "") tag = indexRef;
	if (tag === null) {
		for (const record of topLevel) {
			const annotations = isRecord(record.annotations) ? record.annotations : {};
			const ref = annotations["org.opencontainers.image.ref.name"];
			if (typeof ref === "string" && ref !== "") {
				tag = ref;
				break;
			}
		}
	}
	return {
		tag,
		indexDigest: `sha256:${sha256(indexEntry.content)}`,
		platformDigests,
		architecture: resolvedArchitecture,
		os: "linux",
		labels,
		provenance: predicates.some((predicate) => /slsa|provenance/.test(predicate)),
		sbom: predicates.some((predicate) => /spdx|cyclonedx/.test(predicate)),
	};
}

async function resolveImageTar(path: string): Promise<string | null> {
	let stats: Awaited<ReturnType<typeof stat>>;
	try {
		stats = await stat(path);
	} catch {
		return null;
	}
	if (stats.isFile()) return path;
	if (!stats.isDirectory()) return null;
	const names = (await readdir(path, { withFileTypes: true }))
		.filter((entry) => entry.isFile())
		.map((entry) => entry.name)
		.sort();
	const candidate = names.find((name) => /\.(?:tar|oci|tar\.gz)$/i.test(name));
	return candidate === undefined ? null : join(path, candidate);
}

function imageAssertion(
	detail: ControllerImageDetail,
	source: string,
	archiveSha256: string,
): EvidenceAssertion {
	return {
		kind: "GATE",
		id: CONTROLLER_IMAGE_ASSERTION_ID,
		status: "pass",
		command: `tar -xOf ${source} manifest.json index.json`,
		artifact: { name: basename(source), sha256: archiveSha256 },
		detail: encodeControllerImageDetail(detail),
	};
}

async function inspectImage(path: string): Promise<ImageInspection> {
	const command = `tar -xOf ${path} manifest.json index.json`;
	const tarPath = await resolveImageTar(path);
	if (tarPath === null) {
		return {
			assertion: {
				kind: "GATE",
				id: CONTROLLER_IMAGE_ASSERTION_ID,
				status: "blocked",
				command,
				detail: `controller image tar was not found under ${path}`,
			},
		};
	}
	let buffer: Buffer;
	try {
		buffer = await readFile(tarPath);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return {
			assertion: {
				kind: "GATE",
				id: CONTROLLER_IMAGE_ASSERTION_ID,
				status: "fail",
				command,
				detail: `controller image tar could not be read: ${message}`,
			},
		};
	}
	const archiveSha256 = sha256(buffer);
	let entries: Map<string, TarEntry>;
	try {
		const parsed = tarPath.toLowerCase().endsWith(".gz") ? parseTarGz(buffer) : parseTar(buffer);
		entries = new Map(parsed.map((entry) => [entry.name, entry]));
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		return {
			assertion: {
				kind: "GATE",
				id: CONTROLLER_IMAGE_ASSERTION_ID,
				status: "fail",
				command,
				artifact: { name: basename(tarPath), sha256: archiveSha256 },
				detail: `controller image tar could not be inspected: ${message}`,
			},
		};
	}
	const detail = entries.has("manifest.json") ? inspectDockerSave(entries) : inspectOci(entries);
	if (detail === null) {
		return {
			assertion: {
				kind: "GATE",
				id: CONTROLLER_IMAGE_ASSERTION_ID,
				status: "fail",
				command,
				artifact: { name: basename(tarPath), sha256: archiveSha256 },
				detail: "controller image tar has no usable docker-save manifest.json or OCI index.json",
			},
		};
	}
	const assertion = imageAssertion(detail, tarPath, archiveSha256);
	return { assertion, detail };
}

const PROBE_OUTPUT_LIMIT = 4096;
const READINESS_TIMEOUT_MS = 5000;

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function probeOutput(value: string): string {
	return value.length <= PROBE_OUTPUT_LIMIT
		? value
		: `${value.slice(0, PROBE_OUTPUT_LIMIT)}...[truncated]`;
}

function gateAssertion(
	id: string,
	status: EvidenceAssertion["status"],
	command: string,
	detail: string,
): EvidenceAssertion {
	return { kind: "GATE", id, status, command, detail };
}

/**
 * Re-run the coverage gate over a real coverage evidence input and embed the
 * exact input in a passing assertion. An absent input is blocked; an input that
 * cannot be read, parsed or satisfied is failed. The status is never promoted
 * from the mere presence of a file.
 */
async function inspectCoverageEvidence(path: string | undefined): Promise<EvidenceAssertion> {
	const command =
		path === undefined
			? "test -f <coverage-evidence.json>"
			: `bun scripts/check-coverage.ts --input ${path}`;
	if (path === undefined) {
		return gateAssertion(
			COVERAGE_ASSERTION_ID,
			"blocked",
			command,
			"coverage evidence was not provided; live FC/X coverage stays blocked",
		);
	}
	let text: string;
	try {
		text = await readFile(path, "utf8");
	} catch (error) {
		return gateAssertion(
			COVERAGE_ASSERTION_ID,
			"fail",
			command,
			`coverage evidence could not be read: ${errorMessage(error)}`,
		);
	}
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (error) {
		return gateAssertion(
			COVERAGE_ASSERTION_ID,
			"fail",
			command,
			`coverage evidence is not valid JSON: ${errorMessage(error)}`,
		);
	}
	if (!isRecord(value)) {
		return gateAssertion(
			COVERAGE_ASSERTION_ID,
			"fail",
			command,
			"coverage evidence root must be a JSON object",
		);
	}
	let summary: string;
	try {
		const report = inspectCoverage(value as CoverageInput);
		if (!report.ok) {
			return gateAssertion(COVERAGE_ASSERTION_ID, "fail", command, formatCoverageReport(report));
		}
		summary = JSON.stringify(value);
	} catch (error) {
		return gateAssertion(
			COVERAGE_ASSERTION_ID,
			"fail",
			command,
			`coverage evidence could not be evaluated: ${errorMessage(error)}`,
		);
	}
	return gateAssertion(COVERAGE_ASSERTION_ID, "pass", command, summary);
}

/**
 * Re-run the performance gate over a real four-target performance input and
 * embed the exact input in a passing assertion. Absent input is blocked;
 * unreadable, malformed or incomplete input is failed.
 */
async function inspectPerformanceEvidence(path: string | undefined): Promise<EvidenceAssertion> {
	const command =
		path === undefined
			? "test -f <performance-evidence.json>"
			: `bun scripts/check-performance.ts --input ${path}`;
	if (path === undefined) {
		return gateAssertion(
			PERFORMANCE_ASSERTION_ID,
			"blocked",
			command,
			"performance evidence was not provided; the four process targets stay blocked",
		);
	}
	let text: string;
	try {
		text = await readFile(path, "utf8");
	} catch (error) {
		return gateAssertion(
			PERFORMANCE_ASSERTION_ID,
			"fail",
			command,
			`performance evidence could not be read: ${errorMessage(error)}`,
		);
	}
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (error) {
		return gateAssertion(
			PERFORMANCE_ASSERTION_ID,
			"fail",
			command,
			`performance evidence is not valid JSON: ${errorMessage(error)}`,
		);
	}
	if (!isRecord(value)) {
		return gateAssertion(
			PERFORMANCE_ASSERTION_ID,
			"fail",
			command,
			"performance evidence root must be a JSON object",
		);
	}
	let summary: string;
	try {
		const report = inspectPerformance(value as PerformanceInput);
		if (!report.ok) {
			return gateAssertion(
				PERFORMANCE_ASSERTION_ID,
				"fail",
				command,
				formatPerformanceReport(report),
			);
		}
		summary = JSON.stringify(value);
	} catch (error) {
		return gateAssertion(
			PERFORMANCE_ASSERTION_ID,
			"fail",
			command,
			`performance evidence could not be evaluated: ${errorMessage(error)}`,
		);
	}
	return gateAssertion(PERFORMANCE_ASSERTION_ID, "pass", command, summary);
}

interface CommandProbeResult {
	exitCode: number | null;
	stdout: string;
	stderr: string;
	spawnError: string | null;
}

async function runCommandProbe(command: readonly string[]): Promise<CommandProbeResult> {
	try {
		const child = Bun.spawn([...command], { stdout: "pipe", stderr: "pipe" });
		const [stdout, stderr] = await Promise.all([
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
		]);
		const exitCode = await child.exited;
		return {
			exitCode,
			stdout: probeOutput(stdout),
			stderr: probeOutput(stderr),
			spawnError: null,
		};
	} catch (error) {
		return { exitCode: null, stdout: "", stderr: "", spawnError: errorMessage(error) };
	}
}

function binaryProbeDetail(
	path: string,
	probe: string,
	result: CommandProbeResult,
	extra: Readonly<Record<string, unknown>> = {},
): string {
	return JSON.stringify({
		path,
		probe,
		architecture: hostArchitecture(),
		exitCode: result.exitCode,
		stdout: result.stdout,
		stderr: result.stderr,
		...(result.spawnError === null ? {} : { spawnError: result.spawnError }),
		...extra,
	});
}

async function inspectVersionProbe(index: number, path: string): Promise<EvidenceAssertion> {
	const id = `${PACKAGED_BINARY_ASSERTION_PREFIX}-${index}-version`;
	const command = `${path} --version`;
	const result = await runCommandProbe([path, "--version"]);
	const detail = binaryProbeDetail(path, "version", result);
	if (result.spawnError !== null) return gateAssertion(id, "blocked", command, detail);
	return gateAssertion(id, result.exitCode === 0 ? "pass" : "fail", command, detail);
}

async function inspectDoctorProbe(index: number, path: string): Promise<EvidenceAssertion> {
	const id = `${PACKAGED_BINARY_ASSERTION_PREFIX}-${index}-doctor`;
	const command = `${path} doctor`;
	const result = await runCommandProbe([path, "doctor"]);
	if (result.spawnError !== null) {
		return gateAssertion(id, "blocked", command, binaryProbeDetail(path, "doctor", result));
	}
	if (result.exitCode === 0) {
		return gateAssertion(id, "pass", command, binaryProbeDetail(path, "doctor", result));
	}
	if (/unknown command/i.test(`${result.stdout}\n${result.stderr}`)) {
		return gateAssertion(
			id,
			"blocked",
			command,
			binaryProbeDetail(path, "doctor", result, { supported: false }),
		);
	}
	return gateAssertion(id, "fail", command, binaryProbeDetail(path, "doctor", result));
}

function readinessEndpoint(baseUrl: string): string {
	const trimmed = baseUrl.replace(/\/+$/, "");
	return /\/readyz$/.test(trimmed) ? trimmed : `${trimmed}/readyz`;
}

async function inspectReadinessProbe(
	index: number,
	path: string,
	baseUrl: string | undefined,
): Promise<EvidenceAssertion> {
	const id = `${PACKAGED_BINARY_ASSERTION_PREFIX}-${index}-readiness`;
	if (baseUrl === undefined || baseUrl.trim() === "") {
		return gateAssertion(
			id,
			"blocked",
			"test -n <base-url>",
			JSON.stringify({
				path,
				probe: "readiness",
				architecture: hostArchitecture(),
				url: null,
				httpStatus: null,
				skipped: "--base-url was not provided",
			}),
		);
	}
	const url = readinessEndpoint(baseUrl.trim());
	const command = `curl -fsS ${url}`;
	try {
		const response = await fetch(url, {
			redirect: "manual",
			signal: AbortSignal.timeout(READINESS_TIMEOUT_MS),
		});
		const body = probeOutput(await response.text().catch(() => ""));
		const detail = JSON.stringify({
			path,
			probe: "readiness",
			architecture: hostArchitecture(),
			url,
			httpStatus: response.status,
			body,
		});
		return gateAssertion(id, response.ok ? "pass" : "fail", command, detail);
	} catch (error) {
		return gateAssertion(
			id,
			"fail",
			command,
			JSON.stringify({
				path,
				probe: "readiness",
				architecture: hostArchitecture(),
				url,
				httpStatus: null,
				error: errorMessage(error),
			}),
		);
	}
}

async function inspectBinaryProbes(
	paths: readonly string[],
	baseUrl: string | undefined,
): Promise<EvidenceAssertion[]> {
	const unique = [
		...new Set(paths.map((path) => path.trim()).filter((path) => path !== "")),
	].sort();
	if (unique.length === 0) {
		return [
			gateAssertion(
				`${PACKAGED_BINARY_ASSERTION_PREFIX}-UNAVAILABLE`,
				"blocked",
				"test -n <packaged-binary>",
				"no packaged binary path was provided; executable probes stay blocked",
			),
		];
	}
	const assertions: EvidenceAssertion[] = [];
	for (const [index, path] of unique.entries()) {
		assertions.push(await inspectVersionProbe(index, path));
		assertions.push(await inspectDoctorProbe(index, path));
		assertions.push(await inspectReadinessProbe(index, path, baseUrl));
	}
	return assertions;
}

/**
 * Re-key the BIN-PROBE assertions of another bundle so their ids carry the
 * architecture of the host that produced them. The local collector's own ids
 * stay `BIN-PROBE-<index>-<probe>`; qualifying only the merged facts keeps both
 * architectures addressable in one schema-valid bundle.
 */
function mergedProbeAssertions(evidence: EvidenceBundle): EvidenceAssertion[] {
	const merged: EvidenceAssertion[] = [];
	for (const assertion of evidence.assertions) {
		if (assertion.kind !== "GATE") continue;
		if (!assertion.id.startsWith(`${PACKAGED_BINARY_ASSERTION_PREFIX}-`)) continue;
		const detail = decodeProbeDetail(assertion.detail);
		if (detail === null) continue;
		const architecture = detail.architecture ?? evidence.platform.arch;
		const suffix = assertion.id.slice(PACKAGED_BINARY_ASSERTION_PREFIX.length + 1);
		merged.push({
			...assertion,
			id: `${PACKAGED_BINARY_ASSERTION_PREFIX}-${architecture}-${suffix}`,
		});
	}
	return merged;
}

export async function collectEvidence(options: CollectEvidenceOptions): Promise<EvidenceBundle> {
	const artifactsDir = resolve(options.artifactsDir);
	const declaredHashes = await readDeclaredHashes(artifactsDir);
	const assertions: EvidenceAssertion[] = [];
	for (const product of PRODUCTS) {
		for (const architecture of ARCHITECTURES) {
			assertions.push(
				await inspectArchive(
					artifactsDir,
					options.releaseId,
					product,
					architecture,
					declaredHashes,
				),
			);
		}
	}
	const image = await inspectImage(options.imagePath);
	assertions.push(image.assertion);
	assertions.push(
		await finalManifestAssertion(artifactsDir, options.sourceCommit, options.releaseId, assertions),
	);
	assertions.push(await inspectCoverageEvidence(options.coveragePath));
	assertions.push(await inspectPerformanceEvidence(options.performancePath));
	assertions.push(...(await inspectBinaryProbes(options.binaryPaths ?? [], options.baseUrl)));
	if (options.probeEvidencePath !== undefined) {
		const probeEvidence = await readEvidenceBundle(options.probeEvidencePath);
		assertions.push(...mergedProbeAssertions(probeEvidence));
	}
	assertions.sort((left, right) =>
		left.kind === right.kind
			? left.id.localeCompare(right.id)
			: left.kind.localeCompare(right.kind),
	);
	return buildEvidenceBundle({
		sourceCommit: options.sourceCommit,
		releaseId: options.releaseId,
		generatedAt: options.generatedAt,
		platform: {
			os: "linux",
			// The probes were executed by this host, so the bundle platform is the
			// host architecture regardless of the architectures inside the image.
			arch: hostArchitecture(),
		},
		profile: "full-host",
		assertions,
	});
}

interface CollectEvidenceCliOptions extends CollectEvidenceOptions {
	output: string;
}

function parseArgs(args: readonly string[]): CollectEvidenceCliOptions {
	const values: Record<string, string> = {};
	const binaries: string[] = [];
	const read = (index: number, flag: string): [string, number] => {
		const value = args[index + 1];
		if (value === undefined || value.trim() === "") throw new Error(`${flag} requires a value`);
		return [value, index + 1];
	};
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index] ?? "";
		if (argument === "--help" || argument === "-h") {
			console.log(USAGE);
			process.exit(0);
		}
		if (argument === "--binary") {
			const [value, next] = read(index, argument);
			binaries.push(value);
			index = next;
			continue;
		}
		if (
			argument === "--artifacts" ||
			argument === "--image" ||
			argument === "--source-commit" ||
			argument === "--release-id" ||
			argument === "--output" ||
			argument === "--coverage" ||
			argument === "--performance" ||
			argument === "--probe-evidence" ||
			argument === "--base-url"
		) {
			const [value, next] = read(index, argument);
			values[argument.slice(2)] = value;
			index = next;
			continue;
		}
		const separator = argument.indexOf("=");
		if (argument.startsWith("--") && separator > 2) {
			const key = argument.slice(2, separator);
			const value = argument.slice(separator + 1);
			if (value.trim() === "") throw new Error(`--${key} requires a value`);
			if (key === "binary") binaries.push(value);
			else values[key] = value;
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	const artifactsDir = values["artifacts"] ?? process.env.ARTIFACT_DIR;
	const imagePath = values["image"] ?? process.env.IMAGE_DIR;
	const sourceCommit = values["source-commit"] ?? process.env.SOURCE_COMMIT;
	const releaseId = values["release-id"] ?? process.env.RELEASE_ID;
	const output = values["output"] ?? process.env.EVIDENCE_OUTPUT;
	const coveragePath = values["coverage"] ?? process.env.COVERAGE_INPUT;
	const performancePath = values["performance"] ?? process.env.PERFORMANCE_INPUT;
	const probeEvidencePath = values["probe-evidence"] ?? process.env.PROBE_EVIDENCE;
	const baseUrl = values["base-url"] ?? process.env.READINESS_BASE_URL;
	if (artifactsDir === undefined || artifactsDir === "")
		throw new Error("--artifacts <dir> or ARTIFACT_DIR is required");
	if (imagePath === undefined || imagePath === "")
		throw new Error("--image <dir-or-tar> or IMAGE_DIR is required");
	if (sourceCommit === undefined || sourceCommit === "")
		throw new Error("--source-commit <40-hex> or SOURCE_COMMIT is required");
	if (!SOURCE_COMMIT_PATTERN.test(sourceCommit))
		throw new Error("--source-commit must be exactly 40 lowercase hexadecimal characters");
	const expectedReleaseId = `sha-${sourceCommit.slice(0, 12)}`;
	const resolvedReleaseId = releaseId ?? expectedReleaseId;
	if (!RELEASE_ID_PATTERN.test(resolvedReleaseId))
		throw new Error("--release-id must be sha- plus 12 lowercase hexadecimal characters");
	if (resolvedReleaseId !== expectedReleaseId)
		throw new Error(`--release-id must be ${expectedReleaseId} for the selected source commit`);
	if (output === undefined || output === "")
		throw new Error("--output <evidence.json> or EVIDENCE_OUTPUT is required");
	return {
		artifactsDir,
		imagePath,
		sourceCommit,
		releaseId: resolvedReleaseId,
		generatedAt: new Date().toISOString(),
		output,
		...(coveragePath === undefined || coveragePath === "" ? {} : { coveragePath }),
		...(performancePath === undefined || performancePath === "" ? {} : { performancePath }),
		...(probeEvidencePath === undefined || probeEvidencePath === "" ? {} : { probeEvidencePath }),
		...(binaries.length === 0 ? {} : { binaryPaths: binaries }),
		...(baseUrl === undefined || baseUrl === "" ? {} : { baseUrl }),
	};
}

async function main(): Promise<void> {
	const options = parseArgs(Bun.argv.slice(2));
	const bundle = await collectEvidence(options);
	await mkdir(dirname(resolve(options.output)), { recursive: true });
	await writeFile(options.output, `${JSON.stringify(bundle, null, 2)}\n`);
	console.log(
		`collect-evidence: wrote ${options.output} (${bundle.assertions.length} assertions, ${bundle.platform.arch})`,
	);
}

if (import.meta.main) {
	try {
		await main();
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`collect-evidence: ${message}`);
		process.exitCode = 1;
	}
}
