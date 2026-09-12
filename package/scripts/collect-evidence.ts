#!/usr/bin/env bun

import { createHash } from "node:crypto";
import { mkdir, readdir, readFile, stat, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import {
	expectedArchiveName,
	type PackageArchitecture,
	type PackageVariant,
} from "./check-package-artifacts.ts";
import {
	buildEvidenceBundle,
	CONTROLLER_IMAGE_ASSERTION_ID,
	type ControllerImageDetail,
	type EvidenceAssertion,
	type EvidenceBundle,
	type EvidencePlatformArchitecture,
	encodeControllerImageDetail,
	encodePackageArchiveDetail,
	packageArchiveAssertionId,
} from "./evidence-bundle.ts";

const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
const SHA256_PATTERN = /^[0-9a-f]{64}$/;
const TAR_BLOCK_SIZE = 512;

const VARIANTS = ["assistant", "host"] as const;
const ARCHITECTURES = ["amd64", "arm64"] as const;

const USAGE = [
	"usage: bun scripts/collect-evidence.ts --artifacts <dir> --image <dir-or-tar>",
	"         --source-commit <40-hex> --release-id sha-<12> --output <evidence.json>",
	"",
	"Inputs may also come from ARTIFACT_DIR, IMAGE_DIR, SOURCE_COMMIT, RELEASE_ID and",
	"EVIDENCE_OUTPUT. The artifact directory must hold the four commit-named",
	"*.tar.gz archives plus checksums.txt, SHA256SUMS or release-manifest.json.",
	"",
	"All inspection is local: archives are decompressed and hashed, and the image",
	"tar is read as docker-save or OCI layout. No docker daemon, registry or",
	"network call is made. Missing expected artifacts are recorded as blocked,",
	"never as pass.",
	"",
	"Known gap: packaged binaries are hashed but not executed, and coverage and",
	"performance producers are owned by their own gates; this bundle does not",
	"fabricate those results.",
].join("\n");

export interface CollectEvidenceOptions {
	artifactsDir: string;
	imagePath: string;
	sourceCommit: string;
	releaseId: string;
	generatedAt: string;
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

function parseTarGz(buffer: Buffer): TarEntry[] {
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

function binaryName(variant: PackageVariant): string {
	return variant === "assistant" ? "pixie-assistant" : "pixie";
}

async function inspectArchive(
	artifactsDir: string,
	releaseId: string,
	variant: PackageVariant,
	architecture: PackageArchitecture,
	declaredHashes: ReadonlyMap<string, string>,
): Promise<EvidenceAssertion> {
	const name = expectedArchiveName(variant, architecture, releaseId);
	const binary = binaryName(variant);
	const id = packageArchiveAssertionId(variant, architecture);
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
	const binarySha256 = sha256(member.content);
	return {
		kind: "GATE",
		id,
		status: "pass",
		command,
		artifact: { name, sha256: archiveSha256 },
		detail: encodePackageArchiveDetail({
			variant,
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
	if (chosen !== undefined && isRecord(chosen.config) && typeof chosen.config.digest === "string") {
		const configEntry = entries.get(blobPath(chosen.config.digest));
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

export async function collectEvidence(options: CollectEvidenceOptions): Promise<EvidenceBundle> {
	const artifactsDir = resolve(options.artifactsDir);
	const declaredHashes = await readDeclaredHashes(artifactsDir);
	const assertions: EvidenceAssertion[] = [];
	for (const variant of VARIANTS) {
		for (const architecture of ARCHITECTURES) {
			assertions.push(
				await inspectArchive(
					artifactsDir,
					options.releaseId,
					variant,
					architecture,
					declaredHashes,
				),
			);
		}
	}
	const image = await inspectImage(options.imagePath);
	assertions.push(image.assertion);
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
			arch: image.detail?.architecture ?? "amd64",
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
		if (
			argument === "--artifacts" ||
			argument === "--image" ||
			argument === "--source-commit" ||
			argument === "--release-id" ||
			argument === "--output"
		) {
			const [value, next] = read(index, argument);
			values[argument.slice(2)] = value;
			index = next;
			continue;
		}
		const separator = argument.indexOf("=");
		if (argument.startsWith("--") && separator > 2) {
			values[argument.slice(2, separator)] = argument.slice(separator + 1);
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	const artifactsDir = values["artifacts"] ?? process.env.ARTIFACT_DIR;
	const imagePath = values["image"] ?? process.env.IMAGE_DIR;
	const sourceCommit = values["source-commit"] ?? process.env.SOURCE_COMMIT;
	const releaseId = values["release-id"] ?? process.env.RELEASE_ID;
	const output = values["output"] ?? process.env.EVIDENCE_OUTPUT;
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
