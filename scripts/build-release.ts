#!/usr/bin/env bun

import { createHash } from "node:crypto";
import {
	chmod,
	copyFile,
	link,
	lstat,
	mkdir,
	mkdtemp,
	readFile,
	rm,
	unlink,
	writeFile,
} from "node:fs/promises";
import { join, resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import { type DeterministicTarEntry, writeDeterministicTarGz } from "./deterministic-tar.ts";
import {
	bundledRuntimeNotices,
	nativeRuntimeArchitecture,
	requireNativeRuntimeArchitecture,
	runtimeArchiveEntries,
	stageBundledPiRuntime,
	verifyBundledPiRuntime,
	verifyBundledPiRuntimeArchive,
} from "./release-runtime.ts";

const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
const ARCHITECTURES = ["amd64", "arm64"] as const;
/** The only public release archives. `pixie_assistant` remains archive-internal. */
export const RELEASE_PRODUCTS = ["pixie_web", "pixie"] as const;

type Architecture = (typeof ARCHITECTURES)[number];
export type ReleaseProduct = (typeof RELEASE_PRODUCTS)[number];

interface ReleaseOptions {
	output: string;
	dryRun: boolean;
	allowDirty: boolean;
	merge: boolean;
	/** Explicit architectures, or `"native"` for the default and `--architecture all`. */
	architectureRequest: Architecture[] | "native";
}

interface ArtifactRecord {
	product: ReleaseProduct;
	architecture: Architecture;
	entrypoint: string;
	archive: string;
	archivePath: string;
	entrypointSha256: string;
	archiveSha256: string;
	entries: string[];
}

interface BuiltArchitecture {
	assistant: string;
	launcher: string;
	web: string;
}

interface TarEntry {
	name: string;
	mode: number;
	type: number;
	content: Buffer;
}

function usage(): never {
	console.error(
		"usage: bun scripts/build-release.ts [--output DIR] [--architecture amd64|arm64|all] [--merge] [--dry-run] [--allow-dirty]",
	);
	console.error(
		"  --architecture defaults to all: it builds this host's native architecture and reports the rest as skipped; a foreign architecture must run on its own native builder",
	);
	process.exit(2);
}

/**
 * `all` selects every architecture this host can stage natively, which is
 * exactly one. The bundled Pi runtime carries native optional dependencies, so
 * a foreign architecture is rejected rather than relabelled; the release
 * workflow stages each architecture on its native runner and merges later.
 */
function parseArchitecture(value: string): Architecture[] | "native" {
	if (value === "all") return "native";
	if ((ARCHITECTURES as readonly string[]).includes(value)) return [value as Architecture];
	usage();
}

function parseArgs(args: readonly string[]): ReleaseOptions {
	let output = resolve(import.meta.dir, "../dist/release");
	let dryRun = false;
	let allowDirty = false;
	let merge = false;
	let architectureRequest: Architecture[] | "native" = "native";
	let architectureSeen = false;
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index];
		switch (argument) {
			case "--dry-run":
				dryRun = true;
				break;
			case "--allow-dirty":
				allowDirty = true;
				break;
			case "--merge":
				merge = true;
				break;
			case "--output": {
				const value = args[++index];
				if (value === undefined || value.trim() === "") usage();
				output = resolve(value);
				break;
			}
			case "--architecture": {
				if (architectureSeen) usage();
				const value = args[++index];
				if (value === undefined) usage();
				architectureRequest = parseArchitecture(value);
				architectureSeen = true;
				break;
			}
			default:
				if (argument?.startsWith("--output=")) {
					const value = argument.slice("--output=".length).trim();
					if (!value) usage();
					output = resolve(value);
					break;
				}
				if (argument?.startsWith("--architecture=")) {
					if (architectureSeen) usage();
					architectureRequest = parseArchitecture(argument.slice("--architecture=".length).trim());
					architectureSeen = true;
					break;
				}
				usage();
		}
	}
	if (merge && architectureSeen) usage();
	return { output, dryRun, allowDirty, merge, architectureRequest };
}

async function run(
	command: readonly string[],
	cwd: string,
	env: Record<string, string> = {},
): Promise<string> {
	const child = Bun.spawn([...command], {
		cwd,
		env: { ...process.env, ...env },
		stdout: "pipe",
		stderr: "pipe",
	});
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	if (exitCode !== 0) {
		throw new Error(
			`${command[0]} ${command.slice(1).join(" ")} failed (${exitCode}): ${stderr.trim() || stdout.trim()}`,
		);
	}
	return stdout.trim();
}

async function digest(path: string): Promise<string> {
	return createHash("sha256")
		.update(await readFile(path))
		.digest("hex");
}

async function commitIdentity(
	repositoryRoot: string,
): Promise<{ sourceCommit: string; releaseId: string; commitTime: number; clean: boolean }> {
	const sourceCommit = await run(["git", "rev-parse", "--verify", "HEAD^{commit}"], repositoryRoot);
	if (!SOURCE_COMMIT_PATTERN.test(sourceCommit)) {
		throw new Error("selected source revision must be exactly 40 lowercase hexadecimal characters");
	}
	const status = await run(["git", "status", "--porcelain"], repositoryRoot);
	const commitTime = Number(
		await run(["git", "show", "-s", "--format=%ct", sourceCommit], repositoryRoot),
	);
	if (!Number.isSafeInteger(commitTime) || commitTime < 0)
		throw new Error("selected source commit has no valid timestamp");
	return {
		sourceCommit,
		releaseId: `sha-${sourceCommit.slice(0, 12)}`,
		commitTime,
		clean: status === "",
	};
}

function productUsesRuntime(product: ReleaseProduct): boolean {
	return product === "pixie";
}

function productEntrypoint(product: ReleaseProduct): string {
	return product;
}

function productArchiveName(
	product: ReleaseProduct,
	releaseId: string,
	architecture: Architecture,
): string {
	return `${product}-${releaseId}-linux-${architecture}.tar.gz`;
}

/** Public archive paths; the runtime entries are supplied from its verified manifest. */
export function productArchiveLayout(
	product: ReleaseProduct,
	runtimeEntries: readonly string[] = [],
): string[] {
	const base = ["INSTALL.md", "LICENSE", "NOTICE.md"];
	switch (product) {
		case "pixie_web":
			return [...base, "pixie_web"].sort();
		case "pixie":
			return [
				...base,
				"THIRD_PARTY_NOTICES.md",
				"libexec/pixie_assistant.js",
				"pixie",
				...runtimeEntries,
			].sort();
	}
}

/** Reject an archive member set that would not match its public supervisor. */
export function assertProductArchiveLayout(
	product: ReleaseProduct,
	entries: readonly string[],
): void {
	const runtimeEntries = entries.filter((entry) => entry.startsWith("runtime/"));
	const expected = productArchiveLayout(product, runtimeEntries);
	const observed = [...entries].sort();
	if (observed.includes("pixie_assistant") || observed.includes("pixie_assistant.js"))
		throw new Error("release archive must not expose pixie_assistant at its public root");
	if (observed.some((entry) => /(?:^|\/)rpc-entry(?:\.|\/|$)/.test(entry)))
		throw new Error("release archive must not expose a Pi RPC entrypoint");
	if (!equalStrings(observed, expected))
		throw new Error(`release archive layout is invalid for ${product}`);
	if (new Set(observed).size !== observed.length)
		throw new Error("release archive has duplicate entries");
	if (
		productUsesRuntime(product) &&
		(!observed.includes("runtime/manifest.json") ||
			!observed.includes("runtime/bin/bun") ||
			!observed.includes("runtime/node_modules/@earendil-works/pi-coding-agent/package.json"))
	)
		throw new Error(`release archive runtime is incomplete for ${product}`);
}

export function installInstructions(product: ReleaseProduct): string {
	switch (product) {
		case "pixie_web":
			return `# pixie_web\n\nKeep this extracted directory intact and install its root executable as pixie_web. Supply a private absolute controller configuration when running:\n\n    ./pixie_web serve --config /absolute/path/to/pixie.json\n\nThis controller-only product contains no Node, Bun, Pi, or assistant executable.\n`;
		case "pixie":
			return `# pixie\n\nKeep pixie, libexec/pixie_assistant.js, and runtime/ together in one extracted directory. Run the native Pi TUI with ./pixie (concurrent with the server) and the bundled host with:\n\n    ./pixie serve --config /absolute/path/to/assistant.json\n\nThe archive supplies its own Bun and Pi runtime; external Pi selection is rejected.\n`;
	}
}

function bunEnvironment(workRoot: string): Record<string, string> {
	return { TMPDIR: join(workRoot, "tmp") };
}

/**
 * Bundle the assistant host as portable Bun JavaScript. One pinned runtime
 * serves both the native TUI and this host, so the archive ships a `.js` file
 * rather than a per-architecture compiled executable.
 */
async function buildAssistantBundle(
	entrypoint: string,
	output: string,
	repositoryRoot: string,
	workRoot: string,
	defines: Record<string, string> = {},
): Promise<void> {
	const command = [
		"bun",
		"x",
		"bun@1.4.0",
		"build",
		"--target=bun",
		...Object.entries(defines).flatMap(([name, value]) => [
			"--define",
			`${name}=${JSON.stringify(value)}`,
		]),
		"--outfile",
		output,
		entrypoint,
	];
	await run(command, repositoryRoot, bunEnvironment(workRoot));
	await chmod(output, 0o644);
}

async function buildArchitecture(
	architecture: Architecture,
	repositoryRoot: string,
	workRoot: string,
	releaseId: string,
	sourceCommit: string,
): Promise<BuiltArchitecture> {
	const directory = join(workRoot, "binaries", architecture);
	await mkdir(directory, { recursive: true });
	const assistant = join(directory, "pixie_assistant.js");
	const launcher = join(directory, "pixie");
	const web = join(directory, "pixie_web");
	await buildAssistantBundle(
		join(repositoryRoot, "src/assistant/serve.ts"),
		assistant,
		repositoryRoot,
		workRoot,
		{ PIXIE_ASSISTANT_VERSION: releaseId, PIXIE_ASSISTANT_REVISION: sourceCommit },
	);
	for (const [output, packagePath, stamped] of [
		[launcher, "./cmd/pixie", true],
		[web, "./cmd", true],
	] as const) {
		await run(
			[
				"go",
				"-C",
				repositoryRoot,
				"build",
				"-trimpath",
				"-ldflags",
				stamped ? `-s -w -X main.version=${releaseId} -X main.revision=${sourceCommit}` : "-s -w",
				"-o",
				output,
				packagePath,
			],
			repositoryRoot,
			{ GOOS: "linux", GOARCH: architecture, CGO_ENABLED: "0", ...bunEnvironment(workRoot) },
		);
		await chmod(output, 0o755);
	}
	for (const binary of [launcher, web]) await assertLinuxExecutable(binary, architecture);
	const assistantInfo = await lstat(assistant);
	if (!assistantInfo.isFile() || assistantInfo.isSymbolicLink())
		throw new Error(`assistant bundle is not a regular file: ${assistant}`);
	return { assistant, launcher, web };
}

function elfMachine(architecture: Architecture): number {
	return architecture === "amd64" ? 62 : 183;
}

function assertElfBytes(contents: Uint8Array, architecture: Architecture, label: string): void {
	if (
		contents.byteLength < 20 ||
		contents[0] !== 0x7f ||
		contents[1] !== 0x45 ||
		contents[2] !== 0x4c ||
		contents[3] !== 0x46 ||
		contents[4] !== 2 ||
		contents[5] !== 1
	)
		throw new Error(`${label} is not a 64-bit little-endian ELF executable`);
	const machine = (contents[18] ?? 0) | ((contents[19] ?? 0) << 8);
	if (machine !== elfMachine(architecture))
		throw new Error(`${label} architecture does not match linux-${architecture}`);
}

async function assertLinuxExecutable(path: string, architecture: Architecture): Promise<void> {
	const info = await lstat(path);
	if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o111) === 0)
		throw new Error(`release binary is not a regular executable: ${path}`);
	assertElfBytes(await readFile(path), architecture, path);
}

function tarString(buffer: Buffer, offset: number, length: number): string {
	const field = buffer.subarray(offset, offset + length);
	const terminator = field.indexOf(0);
	return new TextDecoder().decode(terminator === -1 ? field : field.subarray(0, terminator));
}

function tarOctal(buffer: Buffer, offset: number, length: number): number {
	const value = tarString(buffer, offset, length).trim();
	if (value === "") return 0;
	const parsed = Number.parseInt(value, 8);
	if (!Number.isSafeInteger(parsed) || parsed < 0)
		throw new Error("release archive has an invalid tar size");
	return parsed;
}

function readTarGz(path: string): Promise<TarEntry[]> {
	return readFile(path).then((compressed) => {
		let archive: Buffer;
		try {
			archive = gunzipSync(compressed);
		} catch {
			throw new Error(`release archive cannot be decompressed: ${path}`);
		}
		const entries: TarEntry[] = [];
		for (let offset = 0; offset < archive.byteLength; ) {
			const header = archive.subarray(offset, offset + 512);
			if (header.byteLength !== 512 || header.every((byte) => byte === 0)) break;
			const size = tarOctal(header, 124, 12);
			const contentStart = offset + 512;
			const contentEnd = contentStart + size;
			if (contentEnd > archive.byteLength)
				throw new Error("release archive tar entry exceeds archive size");
			const prefix = tarString(header, 345, 155);
			const name =
				prefix === "" ? tarString(header, 0, 100) : `${prefix}/${tarString(header, 0, 100)}`;
			entries.push({
				name,
				mode: tarOctal(header, 100, 8),
				type: header[156] ?? 0,
				content: archive.subarray(contentStart, contentEnd),
			});
			offset = contentStart + Math.ceil(size / 512) * 512;
		}
		return entries;
	});
}

function equalStrings(left: readonly string[], right: readonly string[]): boolean {
	return left.length === right.length && left.every((entry, index) => entry === right[index]);
}

async function verifyArchive(
	archivePath: string,
	product: ReleaseProduct,
	architecture: Architecture,
	entries: readonly DeterministicTarEntry[],
): Promise<void> {
	const archiveEntries = await readTarGz(archivePath);
	const observed = archiveEntries.map(({ name }) => name).sort();
	try {
		assertProductArchiveLayout(product, observed);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`${message} for linux-${architecture}`);
	}
	for (const entry of archiveEntries) {
		if (entry.type !== "0".charCodeAt(0) || (entry.mode !== 0o644 && entry.mode !== 0o755))
			throw new Error(`release archive has an unsafe member ${entry.name}`);
		const source = entries.find((candidate) => candidate.name === entry.name);
		if (!source || !Buffer.from(entry.content).equals(await readFile(source.path)))
			throw new Error(`release archive integrity check failed for ${entry.name}`);
	}
	const executableNames: Record<ReleaseProduct, readonly string[]> = {
		pixie_web: ["pixie_web"],
		pixie: ["pixie"],
	};
	for (const name of executableNames[product]) {
		const entry = archiveEntries.find((candidate) => candidate.name === name);
		if (!entry) throw new Error(`release archive lost ${name}`);
		assertElfBytes(entry.content, architecture, `${product}/${name}`);
	}
	if (archiveEntries.some(({ name }) => /(?:^|\/)(?:pi|pi\.exe)$/.test(name)))
		throw new Error("release archive must not expose a Pi executable");
	if (productUsesRuntime(product)) {
		verifyBundledPiRuntimeArchive(
			archiveEntries
				.filter((entry) => entry.name.startsWith("runtime/"))
				.map((entry) => ({ name: entry.name, mode: entry.mode, content: entry.content })),
			architecture,
		);
	}
}

function archiveArtifact(
	product: ReleaseProduct,
	architecture: Architecture,
	archivePath: string,
): ArtifactRecord {
	return {
		product,
		architecture,
		entrypoint: productEntrypoint(product),
		archive: productArchiveName(product, "{releaseId}", architecture),
		archivePath,
		entrypointSha256: "",
		archiveSha256: "",
		entries: [],
	};
}

/** Verify a native-stage archive again before the non-native merge publishes metadata. */
async function verifyPublishedArchive(
	archivePath: string,
	product: ReleaseProduct,
	architecture: Architecture,
	releaseId: string,
): Promise<ArtifactRecord> {
	const archiveEntries = await readTarGz(archivePath);
	const observed = archiveEntries.map(({ name }) => name).sort();
	assertProductArchiveLayout(product, observed);
	if (new Set(observed).size !== observed.length)
		throw new Error(`published archive has duplicate entries: ${archivePath}`);
	for (const entry of archiveEntries) {
		if (entry.type !== "0".charCodeAt(0) || (entry.mode !== 0o644 && entry.mode !== 0o755))
			throw new Error(`published archive has an unsafe member ${entry.name}`);
	}
	const executableNames: Record<ReleaseProduct, readonly string[]> = {
		pixie_web: ["pixie_web"],
		pixie: ["pixie"],
	};
	for (const name of executableNames[product]) {
		const entry = archiveEntries.find((candidate) => candidate.name === name);
		if (!entry) throw new Error(`published archive lost ${name}`);
		assertElfBytes(entry.content, architecture, `${product}/${name}`);
	}
	if (archiveEntries.some(({ name }) => /(?:^|\/)(?:pi|pi\.exe)$/.test(name)))
		throw new Error("published archive must not expose a Pi executable");
	if (productUsesRuntime(product))
		verifyBundledPiRuntimeArchive(
			archiveEntries
				.filter((entry) => entry.name.startsWith("runtime/"))
				.map((entry) => ({ name: entry.name, mode: entry.mode, content: entry.content })),
			architecture,
		);
	const record = archiveArtifact(product, architecture, archivePath);
	record.archive = productArchiveName(product, releaseId, architecture);
	const entrypoint = archiveEntries.find((entry) => entry.name === record.entrypoint);
	if (!entrypoint) throw new Error(`published archive lost ${record.entrypoint}`);
	record.entrypointSha256 = createHash("sha256").update(entrypoint.content).digest("hex");
	record.archiveSha256 = await digest(archivePath);
	record.entries = observed;
	return record;
}

function checksumsFor(artifacts: readonly ArtifactRecord[]): string {
	return `${artifacts
		.map((artifact) => `${artifact.archiveSha256}  ${artifact.archive}`)
		.sort()
		.join("\n")}\n`;
}

function releaseManifest(
	identity: { sourceCommit: string; releaseId: string; clean: boolean },
	artifacts: readonly ArtifactRecord[],
	architectures: readonly Architecture[],
): string {
	return `${JSON.stringify(
		{
			schemaVersion: 2,
			releaseId: identity.releaseId,
			sourceCommit: identity.sourceCommit,
			cleanSourceTree: identity.clean,
			completeSet:
				architectures.length === ARCHITECTURES.length &&
				artifacts.length === ARCHITECTURES.length * RELEASE_PRODUCTS.length,
			architectures,
			products: RELEASE_PRODUCTS,
			artifacts: artifacts.map(
				({
					product,
					architecture,
					entrypoint,
					archive,
					entrypointSha256,
					archiveSha256,
					entries,
				}) => ({
					product,
					architecture,
					entrypoint,
					archive,
					entrypointSha256,
					archiveSha256,
					entries,
				}),
			),
			archiveHashes: Object.fromEntries(
				artifacts.map(({ archive, archiveSha256 }) => [archive, archiveSha256]),
			),
			checksumsPresent: true,
			// OCI attestations are publication evidence, not local archive claims.
			sbomPresent: false,
			provenancePresent: false,
		},
		null,
		2,
	)}\n`;
}

/**
 * Native builders publish only their architecture's immutable archives. A
 * separate merge invocation sees both native outputs, validates them again,
 * then writes the single complete checksum and release manifests.
 */
export async function mergeReleaseArtifacts(
	output: string,
	identity: { sourceCommit: string; releaseId: string; clean: boolean },
	repositoryRoot: string,
): Promise<ArtifactRecord[]> {
	const artifacts: ArtifactRecord[] = [];
	for (const architecture of ARCHITECTURES) {
		for (const product of RELEASE_PRODUCTS) {
			const archive = productArchiveName(product, identity.releaseId, architecture);
			const archivePath = join(output, archive);
			const info = await lstat(archivePath).catch(() => undefined);
			if (!info?.isFile() || info.isSymbolicLink())
				throw new Error(`complete release merge requires regular archive ${archive}`);
			artifacts.push(
				await verifyPublishedArchive(archivePath, product, architecture, identity.releaseId),
			);
		}
	}
	const temporaryParent = join(repositoryRoot, ".tmp-work");
	await mkdir(temporaryParent, { recursive: true });
	const workRoot = await mkdtemp(join(temporaryParent, "pixie-release-merge-"));
	try {
		const checksumsPath = join(workRoot, "checksums.txt");
		const manifestPath = join(workRoot, "release-manifest.json");
		await writeFile(checksumsPath, checksumsFor(artifacts), { mode: 0o644 });
		await writeFile(manifestPath, releaseManifest(identity, artifacts, ARCHITECTURES), {
			mode: 0o644,
		});
		await publishImmutable(checksumsPath, join(output, "checksums.txt"));
		await publishImmutable(manifestPath, join(output, "release-manifest.json"));
	} finally {
		await rm(workRoot, { recursive: true, force: true });
	}
	return artifacts;
}

async function copyStageFile(
	stage: string,
	name: string,
	source: string,
	mode: number,
): Promise<DeterministicTarEntry> {
	const destination = join(stage, name);
	await mkdir(resolve(destination, ".."), { recursive: true });
	await copyFile(source, destination);
	await chmod(destination, mode);
	return { name, path: destination, mode };
}

async function writeStageText(
	stage: string,
	name: string,
	contents: string,
	mode = 0o644,
): Promise<DeterministicTarEntry> {
	const path = join(stage, name);
	await mkdir(resolve(path, ".."), { recursive: true });
	await writeFile(path, contents, { mode });
	return { name, path, mode };
}

async function archiveProduct(
	product: ReleaseProduct,
	architecture: Architecture,
	commitTime: number,
	repositoryRoot: string,
	workRoot: string,
	output: string,
	releaseId: string,
	binaries: BuiltArchitecture,
): Promise<ArtifactRecord> {
	const stage = join(workRoot, "stage", `${product}-${architecture}`);
	await mkdir(stage, { recursive: true });
	const entries: DeterministicTarEntry[] = [
		await writeStageText(stage, "INSTALL.md", installInstructions(product)),
		await copyStageFile(stage, "LICENSE", join(repositoryRoot, "LICENSE"), 0o644),
		await copyStageFile(stage, "NOTICE.md", join(repositoryRoot, "NOTICE.md"), 0o644),
	];
	let entrypointPath: string;
	switch (product) {
		case "pixie_web":
			entrypointPath = binaries.web;
			entries.push(await copyStageFile(stage, "pixie_web", binaries.web, 0o755));
			break;
		case "pixie":
			entrypointPath = binaries.launcher;
			entries.push(
				await copyStageFile(stage, "pixie", binaries.launcher, 0o755),
				await copyStageFile(stage, "libexec/pixie_assistant.js", binaries.assistant, 0o644),
			);
			break;
	}
	if (productUsesRuntime(product)) {
		const runtime = join(workRoot, "runtime", architecture);
		await verifyBundledPiRuntime(runtime, architecture);
		entries.push(
			await writeStageText(
				stage,
				"THIRD_PARTY_NOTICES.md",
				await bundledRuntimeNotices(runtime, architecture),
			),
		);
		entries.push(...(await runtimeArchiveEntries(runtime, architecture)));
	}
	const archive = productArchiveName(product, releaseId, architecture);
	const archivePath = join(output, archive);
	await writeDeterministicTarGz(archivePath, entries, commitTime);
	await verifyArchive(archivePath, product, architecture, entries);
	return {
		product,
		architecture,
		entrypoint: productEntrypoint(product),
		archive,
		archivePath,
		entrypointSha256: await digest(entrypointPath),
		archiveSha256: await digest(archivePath),
		entries: entries.map(({ name }) => name).sort(),
	};
}

async function publishImmutable(source: string, destination: string): Promise<void> {
	await mkdir(resolve(destination, ".."), { recursive: true });
	const existing = await lstat(destination).catch(() => undefined);
	if (existing !== undefined) {
		if (!existing.isFile() || existing.isSymbolicLink())
			throw new Error(`refusing to replace non-regular release output ${destination}`);
		if ((await digest(existing ? destination : source)) !== (await digest(source)))
			throw new Error(`release output already exists with different integrity: ${destination}`);
		return;
	}
	const temporary = join(resolve(destination, ".."), `.${crypto.randomUUID()}.pixie-stage`);
	try {
		await copyFile(source, temporary);
		if ((await digest(temporary)) !== (await digest(source)))
			throw new Error(`release output staging integrity check failed: ${destination}`);
		await link(temporary, destination);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "EEXIST") {
			if ((await digest(destination)) !== (await digest(source)))
				throw new Error(`release output already exists with different integrity: ${destination}`);
			return;
		}
		throw error;
	} finally {
		await unlink(temporary).catch(() => undefined);
	}
}

function validateRequestedRuntimeArchitectures(architectures: readonly Architecture[]): void {
	for (const architecture of architectures) requireNativeRuntimeArchitecture(architecture);
}

async function main(): Promise<void> {
	const options = parseArgs(Bun.argv.slice(2));
	const repositoryRoot = resolve(import.meta.dir, "..");
	const identity = await commitIdentity(repositoryRoot);
	if (!identity.clean && !options.allowDirty && !options.dryRun) {
		throw new Error(
			"release builds require a clean source tree (use --allow-dirty only for local inspection)",
		);
	}
	if (options.merge) {
		const plan = {
			releaseId: identity.releaseId,
			sourceCommit: identity.sourceCommit,
			cleanSourceTree: identity.clean,
			architectures: [...ARCHITECTURES],
			products: [...RELEASE_PRODUCTS],
			output: options.output,
			operation: "merge",
		};
		if (options.dryRun) {
			console.log(
				JSON.stringify({ ...plan, mode: "validate-only", publication: "disabled" }, null, 2),
			);
			return;
		}
		const artifacts = await mergeReleaseArtifacts(options.output, identity, repositoryRoot);
		console.log(
			JSON.stringify(
				{ ...plan, mode: "merged", archives: artifacts.map(({ archive }) => archive).sort() },
				null,
				2,
			),
		);
		return;
	}
	const nativeArchitecture = nativeRuntimeArchitecture();
	const architectures: Architecture[] =
		options.architectureRequest === "native" ? [nativeArchitecture] : options.architectureRequest;
	const plan = {
		releaseId: identity.releaseId,
		sourceCommit: identity.sourceCommit,
		cleanSourceTree: identity.clean,
		architectures,
		products: [...RELEASE_PRODUCTS],
		output: options.output,
		operation: "build",
		nativeRuntimeArchitecture: nativeArchitecture,
		// A single native host cannot stage the bundled Pi runtime for a foreign
		// architecture; that architecture is built on its own native runner and
		// merged later, so it is reported here rather than requested.
		skippedArchitectures: ARCHITECTURES.filter(
			(architecture) => !architectures.includes(architecture),
		),
	};
	if (options.dryRun) {
		console.log(
			JSON.stringify({ ...plan, mode: "validate-only", publication: "disabled" }, null, 2),
		);
		return;
	}
	// Do this before creating an output directory or compiling binaries. An amd64
	// builder must not leave a partial arm64 release beside old artifacts.
	validateRequestedRuntimeArchitectures(architectures);
	const temporaryParent = join(repositoryRoot, ".tmp-work");
	await mkdir(temporaryParent, { recursive: true });
	const workRoot = await mkdtemp(join(temporaryParent, "pixie-release-"));
	try {
		await mkdir(join(workRoot, "tmp"), { recursive: true });
		await run(
			["bun", "x", "bun@1.4.0", "run", "build:webui"],
			repositoryRoot,
			bunEnvironment(workRoot),
		);
		const stagedOutput = join(workRoot, "output");
		await mkdir(stagedOutput, { recursive: true });
		const artifacts: ArtifactRecord[] = [];
		for (const architecture of architectures) {
			const binaries = await buildArchitecture(
				architecture,
				repositoryRoot,
				workRoot,
				identity.releaseId,
				identity.sourceCommit,
			);
			await stageBundledPiRuntime({
				repositoryRoot,
				directory: join(workRoot, "runtime", architecture),
				architecture,
			});
			for (const product of RELEASE_PRODUCTS) {
				artifacts.push(
					await archiveProduct(
						product,
						architecture,
						identity.commitTime,
						repositoryRoot,
						workRoot,
						stagedOutput,
						identity.releaseId,
						binaries,
					),
				);
			}
		}
		const completeSet =
			architectures.length === ARCHITECTURES.length &&
			artifacts.length === ARCHITECTURES.length * RELEASE_PRODUCTS.length;
		if (!completeSet && architectures.length !== 1)
			throw new Error("incomplete native release staging must contain exactly one architecture");
		const architecture = architectures[0] as Architecture;
		const checksumsName = completeSet ? "checksums.txt" : `checksums-linux-${architecture}.txt`;
		const manifestName = completeSet
			? "release-manifest.json"
			: `release-manifest-linux-${architecture}.json`;
		const checksumsPath = join(stagedOutput, checksumsName);
		await writeFile(checksumsPath, checksumsFor(artifacts), { mode: 0o644 });
		const manifestPath = join(stagedOutput, manifestName);
		await writeFile(manifestPath, releaseManifest(identity, artifacts, architectures), {
			mode: 0o644,
		});
		for (const path of [
			...artifacts.map(({ archivePath }) => archivePath),
			checksumsPath,
			manifestPath,
		])
			if ((await lstat(path)).isSymbolicLink())
				throw new Error(`release staging output is a symlink: ${path}`);
		for (const artifact of artifacts)
			await publishImmutable(artifact.archivePath, join(options.output, artifact.archive));
		await publishImmutable(checksumsPath, join(options.output, checksumsName));
		await publishImmutable(manifestPath, join(options.output, manifestName));
		console.log(
			JSON.stringify(
				{
					...plan,
					mode: "staged",
					completeSet,
					archives: artifacts.map(({ archive }) => archive).sort(),
				},
				null,
				2,
			),
		);
	} finally {
		await rm(workRoot, { recursive: true, force: true });
	}
}

if (import.meta.main) {
	try {
		await main();
	} catch (error) {
		console.error(`build-release: ${error instanceof Error ? error.message : String(error)}`);
		process.exitCode = 1;
	}
}
