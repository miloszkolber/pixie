import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { productArchiveLayout, RELEASE_PRODUCTS } from "../../scripts/build-release.ts";
import { writeDeterministicTarGz } from "../../scripts/deterministic-tar.ts";
import {
	buildEvidenceBundle,
	type EvidenceAssertion,
	PACKAGED_BINARY_ASSERTION_PREFIX,
} from "../../scripts/evidence-bundle.ts";

/**
 * Shared synthetic exact-commit staging fixtures for the release evidence
 * gates. They create the four commit-named public archives, the merged local manifest
 * and either a docker-save or OCI controller image tar. No registry, tag or
 * lifecycle value is synthesized.
 */

export const FIXTURE_PRODUCTS = RELEASE_PRODUCTS;
export const FIXTURE_ARCHITECTURES = ["amd64", "arm64"] as const;

export function fixtureSha256(value: Uint8Array): string {
	return createHash("sha256").update(value).digest("hex");
}

export function fixtureBinaryName(product: (typeof FIXTURE_PRODUCTS)[number]): string {
	return product;
}

export interface StagedFixture {
	artifactsDir: string;
	imageTar: string;
	archiveSha256: ReadonlyMap<string, string>;
	binarySha256: ReadonlyMap<string, string>;
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

export function writeFixtureTar(entries: readonly { name: string; content: Buffer }[]): Buffer {
	const blocks: Buffer[] = [];
	for (const entry of entries) {
		blocks.push(tarHeader(entry.name, entry.content.byteLength, 0o644), entry.content);
		const padding = (512 - (entry.content.byteLength % 512)) % 512;
		if (padding !== 0) blocks.push(Buffer.alloc(padding));
	}
	blocks.push(Buffer.alloc(1024));
	return Buffer.concat(blocks);
}

/**
 * Stage the four commit-named archives plus the merged local manifest. The
 * manifest carries real archive and entrypoint hashes; publication fields
 * (SBOM, provenance and image digests) are explicitly false/absent.
 */
export async function stageArtifacts(
	root: string,
	includeAll: boolean,
	sourceCommit: string,
	releaseId: string,
): Promise<StagedFixture> {
	const artifactsDir = join(root, "artifacts");
	const staging = join(root, "staging");
	await mkdir(artifactsDir, { recursive: true });
	await mkdir(staging, { recursive: true });
	const archiveSha256 = new Map<string, string>();
	const binarySha256 = new Map<string, string>();
	const artifacts: {
		product: (typeof FIXTURE_PRODUCTS)[number];
		architecture: (typeof FIXTURE_ARCHITECTURES)[number];
		entrypoint: string;
		archive: string;
		entrypointSha256: string;
		archiveSha256: string;
		entries: readonly string[];
	}[] = [];
	for (const product of FIXTURE_PRODUCTS) {
		for (const architecture of FIXTURE_ARCHITECTURES) {
			if (!includeAll && !(product === "pixie_web" && architecture === "amd64")) continue;
			const binary = fixtureBinaryName(product);
			const directory = join(staging, `${product}-${architecture}`);
			await mkdir(directory, { recursive: true });
			const runtime =
				product === "pixie"
					? [
							"runtime/manifest.json",
							"runtime/bin/bun",
							"runtime/bun/LICENSE.md",
							"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
							"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
							"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
						]
					: [];
			const executable = new Set(product === "pixie_web" ? ["pixie_web"] : ["pixie"]);
			const files = productArchiveLayout(product, runtime).map((name) => ({
				name,
				content: executable.has(name)
					? `binary-${product}-${architecture}-${name}`
					: name.endsWith("package.json")
						? '{"name":"@earendil-works/pi-coding-agent","version":"0.85.1"}\n'
						: `${name}\n`,
				mode: executable.has(name) ? 0o755 : 0o644,
			}));
			for (const file of files) {
				const path = join(directory, file.name);
				await mkdir(dirname(path), { recursive: true });
				await writeFile(path, file.content);
			}
			const archiveName = `${product}-${releaseId}-linux-${architecture}.tar.gz`;
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
			archiveSha256.set(archiveName, fixtureSha256(await readFile(archivePath)));
			binarySha256.set(
				archiveName,
				fixtureSha256(Buffer.from(`binary-${product}-${architecture}-${binary}`)),
			);
			artifacts.push({
				product,
				architecture,
				entrypoint: binary,
				archive: archiveName,
				entrypointSha256: binarySha256.get(archiveName) ?? "",
				archiveSha256: archiveSha256.get(archiveName) ?? "",
				entries: files.map((file) => file.name).sort(),
			});
		}
	}
	await writeFile(
		join(artifactsDir, "checksums.txt"),
		`${[...archiveSha256.entries()]
			.map(([name, digest]) => `${digest}  ${name}`)
			.sort()
			.join("\n")}\n`,
	);
	await writeFile(
		join(artifactsDir, "release-manifest.json"),
		`${JSON.stringify(
			{
				schemaVersion: 2,
				releaseId,
				sourceCommit,
				cleanSourceTree: true,
				completeSet: includeAll,
				architectures: includeAll ? [...FIXTURE_ARCHITECTURES] : ["amd64"],
				products: [...FIXTURE_PRODUCTS],
				artifacts,
				archiveHashes: Object.fromEntries(archiveSha256),
				checksumsPresent: true,
				sbomPresent: false,
				provenancePresent: false,
			},
			null,
			2,
		)}\n`,
	);
	return { artifactsDir, imageTar: join(root, "controller.tar"), archiveSha256, binarySha256 };
}

export async function writeDockerSaveTar(
	path: string,
	sourceCommit: string,
	releaseId: string,
): Promise<void> {
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
	const configDigest = fixtureSha256(config);
	const manifest = Buffer.from(
		JSON.stringify([
			{ Config: `${configDigest}.json`, RepoTags: [`pixie:${releaseId}`], Layers: [] },
		]),
	);
	await writeFile(
		path,
		writeFixtureTar([
			{ name: "manifest.json", content: manifest },
			{ name: `${configDigest}.json`, content: config },
		]),
	);
}

export async function writeOciTar(
	path: string,
	sourceCommit: string,
	releaseId: string,
): Promise<{ indexDigest: string; manifestDigest: string }> {
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
	const configDigest = fixtureSha256(config);
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
	const manifestDigest = fixtureSha256(manifest);
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
		writeFixtureTar([
			{ name: "oci-layout", content: Buffer.from('{"imageLayoutVersion":"1.0.0"}') },
			{ name: "index.json", content: index },
			{ name: `blobs/sha256/${manifestDigest}`, content: manifest },
			{ name: `blobs/sha256/${configDigest}`, content: config },
		]),
	);
	return {
		indexDigest: `sha256:${fixtureSha256(index)}`,
		manifestDigest: `sha256:${manifestDigest}`,
	};
}

export interface ProbeBinaryOptions {
	name: (typeof FIXTURE_PRODUCTS)[number];
	releaseId: string;
	sourceCommit: string;
	doctor?: boolean;
}

/** Write an executable probe binary that answers --version/doctor for the native target. */
export async function writeProbeBinary(path: string, options: ProbeBinaryOptions): Promise<void> {
	const doctor = options.doctor === false ? 2 : 0;
	const script = `#!/bin/sh
case "$1" in
  --version) echo "${options.name} ${options.releaseId} (revision ${options.sourceCommit})"; exit 0 ;;
  doctor) echo "pixie doctor: configuration is readable ()"; exit ${doctor} ;;
  *) echo "unknown command \\"$1\\"; use serve" >&2; exit 2 ;;
esac
`;
	await writeFile(path, script, { mode: 0o755 });
}

async function runProbeCommand(
	path: string,
	args: readonly string[],
): Promise<{ exitCode: number | null; stdout: string; stderr: string }> {
	const child = Bun.spawn([path, ...args], { stdout: "pipe", stderr: "pipe" });
	const [stdout, stderr] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
	]);
	return { exitCode: await child.exited, stdout, stderr };
}

export interface ProbeEvidenceBundleOptions {
	/** Architecture the simulated host reports on every probe fact. */
	architecture: (typeof FIXTURE_ARCHITECTURES)[number];
	sourceCommit: string;
	releaseId: string;
	generatedAt: string;
	binaries: readonly string[];
}

/**
 * Write a probe-only evidence bundle as a second collector host would emit it.
 * The given binaries are really executed for `--version`/`doctor`; only the
 * reported host architecture is selected by the caller, which lets amd64 tests
 * exercise the merged per-architecture bundle without an arm64 runner.
 */
export async function writeProbeEvidenceBundle(
	path: string,
	options: ProbeEvidenceBundleOptions,
): Promise<void> {
	const unique = [
		...new Set(options.binaries.map((binary) => binary.trim()).filter((binary) => binary !== "")),
	].sort();
	const assertions: EvidenceAssertion[] = [];
	for (const [index, binary] of unique.entries()) {
		for (const probe of ["version", "doctor"] as const) {
			const result = await runProbeCommand(binary, [probe === "version" ? "--version" : "doctor"]);
			assertions.push({
				kind: "GATE",
				id: `${PACKAGED_BINARY_ASSERTION_PREFIX}-${index}-${probe}`,
				status: result.exitCode === 0 ? "pass" : "fail",
				command: `${binary} ${probe === "version" ? "--version" : "doctor"}`,
				detail: JSON.stringify({
					path: binary,
					probe,
					architecture: options.architecture,
					exitCode: result.exitCode,
					stdout: result.stdout,
					stderr: result.stderr,
				}),
			});
		}
		assertions.push({
			kind: "GATE",
			id: `${PACKAGED_BINARY_ASSERTION_PREFIX}-${index}-readiness`,
			status: "blocked",
			command: "test -n <base-url>",
			detail: JSON.stringify({
				path: binary,
				probe: "readiness",
				architecture: options.architecture,
				url: null,
				httpStatus: null,
				skipped: "--base-url was not provided",
			}),
		});
	}
	const bundle = buildEvidenceBundle({
		sourceCommit: options.sourceCommit,
		releaseId: options.releaseId,
		generatedAt: options.generatedAt,
		platform: { os: "linux", arch: options.architecture },
		profile: "full-host",
		assertions,
	});
	await writeFile(path, `${JSON.stringify(bundle, null, 2)}\n`);
}
