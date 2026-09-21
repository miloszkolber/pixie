import { createHash } from "node:crypto";
import { chmod, copyFile, lstat, mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, posix, relative, resolve, sep } from "node:path";
import { crc32, inflateRawSync } from "node:zlib";
import type { DeterministicTarEntry } from "./deterministic-tar.ts";

export const BUNDLED_BUN_VERSION = "1.4.2";
/**
 * The official Bun release zip carries only the `bun` executable. Its license
 * ships in the Bun repository at the same release tag, so the pinned license
 * digest below is verified alongside the pinned archive digest.
 */
export const BUNDLED_BUN_LICENSE_PATH = "bun/LICENSE.md";
export const BUNDLED_PI_PACKAGE = "@earendil-works/pi-coding-agent";
export const BUNDLED_PI_VERSION = "0.86.1";
export const RUNTIME_MANIFEST_NAME = "manifest.json";

export const RUNTIME_ARCHITECTURES = ["amd64", "arm64"] as const;
export type RuntimeArchitecture = (typeof RUNTIME_ARCHITECTURES)[number];

interface BunRelease {
	archive: string;
	sha256: string;
	directory: string;
}

const BUN_RELEASES: Record<RuntimeArchitecture, BunRelease> = {
	amd64: {
		archive: "bun-linux-x64.zip",
		sha256: "36368faef7527875d5ffa52e53cd48021741f2a83eb6208a8dd64068d422a913",
		directory: "bun-linux-x64",
	},
	arm64: {
		archive: "bun-linux-aarch64.zip",
		sha256: "54328bbc2d9c8e0c9f892c544d66c57a83b84139e34909e5ee81758f1ac8fda7",
		directory: "bun-linux-aarch64",
	},
};

const BUN_LICENSE_RELEASE = {
	url: `https://raw.githubusercontent.com/oven-sh/bun/bun-v${BUNDLED_BUN_VERSION}/LICENSE.md`,
	sha256: "b9caf52728691b4057e371232c221a132883198be2f3d2ddf92c90404c984b1a",
};

interface PackageManifest {
	name?: unknown;
	version?: unknown;
	license?: unknown;
	dependencies?: unknown;
	optionalDependencies?: unknown;
	os?: unknown;
	cpu?: unknown;
	bin?: unknown;
	exports?: unknown;
}

interface RuntimePackage {
	name: string;
	version: string;
	license: string;
	path: string;
}

interface RuntimeFile {
	path: string;
	sha256: string;
	size: number;
}

export interface RuntimeManifest {
	schemaVersion: 2;
	platform: { os: "linux"; architecture: RuntimeArchitecture };
	bun: { version: typeof BUNDLED_BUN_VERSION; archive: string; sha256: string };
	rootPackage: { name: typeof BUNDLED_PI_PACKAGE; version: typeof BUNDLED_PI_VERSION };
	packages: RuntimePackage[];
	files: RuntimeFile[];
}

export interface StagedRuntime {
	directory: string;
	manifestPath: string;
	manifest: RuntimeManifest;
}

export interface RuntimeArchiveEntry {
	name: string;
	mode: number;
	content: Uint8Array;
}

interface BunArchiveEntry {
	name: string;
	type: "file" | "directory";
	content: Buffer;
}

function sha256(contents: Uint8Array): string {
	return createHash("sha256").update(contents).digest("hex");
}

function platformCpu(architecture: RuntimeArchitecture): string {
	return architecture === "amd64" ? "x64" : "arm64";
}

function elfMachine(architecture: RuntimeArchitecture): number {
	return architecture === "amd64" ? 62 : 183;
}

export function assertLinuxElfArchitecture(
	contents: Uint8Array,
	architecture: RuntimeArchitecture,
	label: string,
): void {
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

export function nativeRuntimeArchitecture(): RuntimeArchitecture {
	if (process.platform !== "linux")
		throw new Error("bundled Pi runtime staging requires a Linux builder");
	if (process.arch === "x64") return "amd64";
	if (process.arch === "arm64") return "arm64";
	throw new Error(`bundled Pi runtime staging does not support host architecture ${process.arch}`);
}

/** Optional Pi dependencies carry native artifacts; never relabel host files. */
export function requireNativeRuntimeArchitecture(architecture: RuntimeArchitecture): void {
	const native = nativeRuntimeArchitecture();
	if (architecture !== native) {
		throw new Error(
			`bundled Pi runtime for linux-${architecture} requires a native linux-${architecture} builder; refusing to copy linux-${native} optional artifacts`,
		);
	}
}

function isContained(root: string, candidate: string): boolean {
	const remainder = relative(root, candidate);
	return (
		remainder === "" ||
		(remainder !== ".." && !remainder.startsWith(`..${sep}`) && !isAbsolute(remainder))
	);
}

function packageParts(name: string): string[] {
	if (name.startsWith("@")) {
		const [scope, packageName, ...rest] = name.split("/");
		if (!scope || !packageName || rest.length > 0) throw new Error(`invalid package name ${name}`);
		return [scope, packageName];
	}
	if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(name)) throw new Error(`invalid package name ${name}`);
	return [name];
}

async function regularDirectory(path: string, label: string): Promise<void> {
	const info = await lstat(path).catch(() => undefined);
	if (!info?.isDirectory() || info.isSymbolicLink())
		throw new Error(`${label} must be a non-symlink directory`);
}

async function readPackageManifest(packageDirectory: string): Promise<PackageManifest> {
	const manifestPath = join(packageDirectory, "package.json");
	const info = await lstat(manifestPath).catch(() => undefined);
	if (!info?.isFile() || info.isSymbolicLink())
		throw new Error(`runtime package ${packageDirectory} has no regular package.json`);
	let parsed: unknown;
	try {
		parsed = JSON.parse(await readFile(manifestPath, "utf8"));
	} catch {
		throw new Error(`runtime package ${packageDirectory} has invalid package.json`);
	}
	if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
		throw new Error(`runtime package ${packageDirectory} package.json must be an object`);
	return parsed as PackageManifest;
}

function requiredLicense(manifest: PackageManifest, packageName: string): string {
	if (typeof manifest.license !== "string" || manifest.license.trim() === "") {
		throw new Error(`runtime package ${packageName} has no license metadata`);
	}
	return manifest.license.trim();
}

function stringMap(value: unknown, label: string): Record<string, string> {
	if (value === undefined) return {};
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error(`${label} must be an object`);
	const result: Record<string, string> = {};
	for (const [name, version] of Object.entries(value)) {
		if (typeof version !== "string" || version.trim() === "")
			throw new Error(`${label} dependency ${name} must use a non-empty version`);
		result[name] = version;
	}
	return result;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** Keep the normal TUI bundle. Only Pi's RPC entrypoints and npm bin alias are removed. */
function excludedPiRuntimePath(path: string): boolean {
	return /(?:^|\/)rpc-entry(?:\.|\/|$)/.test(path);
}

function strippedPiRuntimeManifest(manifest: PackageManifest): Record<string, unknown> {
	if (!isRecord(manifest)) throw new Error("bundled Pi package manifest must be an object");
	const staged = { ...manifest };
	// The native root `pixie` launcher is the only public Pi launch surface.
	delete staged.bin;
	if (isRecord(staged.exports)) {
		const exports = { ...staged.exports };
		delete exports["./rpc-entry"];
		staged.exports = exports;
	}
	return staged;
}

function allowsPlatform(value: unknown, expected: string, label: string): boolean {
	if (value === undefined) return true;
	if (!Array.isArray(value) || value.some((entry) => typeof entry !== "string"))
		throw new Error(`${label} must be an array of strings`);
	const values = value as string[];
	const denied = values.some((entry) => entry === `!${expected}`);
	const allowed = values.filter((entry) => !entry.startsWith("!"));
	return !denied && (allowed.length === 0 || allowed.includes(expected));
}

function assertPackagePlatform(
	manifest: PackageManifest,
	architecture: RuntimeArchitecture,
	packagePath: string,
): void {
	if (!allowsPlatform(manifest.os, "linux", `runtime package ${packagePath} os`))
		throw new Error(`runtime package ${packagePath} is not compatible with linux`);
	if (
		!allowsPlatform(manifest.cpu, platformCpu(architecture), `runtime package ${packagePath} cpu`)
	)
		throw new Error(`runtime package ${packagePath} is not compatible with linux-${architecture}`);
}

async function findInstalledDependency(
	fromPackage: string,
	sourceNodeModules: string,
	name: string,
): Promise<string | undefined> {
	const sourceRoot = dirname(sourceNodeModules);
	let current = fromPackage;
	for (;;) {
		const candidate = join(current, "node_modules", ...packageParts(name));
		const info = await lstat(candidate).catch(() => undefined);
		if (info !== undefined) {
			if (!info.isDirectory() || info.isSymbolicLink())
				throw new Error(`runtime dependency ${name} is not a non-symlink directory`);
			const resolved = resolve(candidate);
			if (!isContained(sourceNodeModules, resolved))
				throw new Error(`runtime dependency ${name} escapes node_modules`);
			return resolved;
		}
		if (current === sourceRoot) return undefined;
		const parent = dirname(current);
		if (!isContained(sourceRoot, parent)) return undefined;
		current = parent;
	}
}

async function copyPackageFiles(
	source: string,
	destination: string,
	root: string,
	packagePath: string,
	path = "",
): Promise<void> {
	await regularDirectory(source, "runtime package source");
	await mkdir(destination, { recursive: true, mode: 0o755 });
	for (const entry of (await readdir(source, { withFileTypes: true })).sort((left, right) =>
		left.name.localeCompare(right.name),
	)) {
		if (entry.name === "node_modules") continue;
		const childPath = path === "" ? entry.name : `${path}/${entry.name}`;
		if (packagePath === BUNDLED_PI_PACKAGE && excludedPiRuntimePath(childPath)) continue;
		const sourcePath = join(source, entry.name);
		const destinationPath = join(destination, entry.name);
		const info = await lstat(sourcePath);
		if (info.isSymbolicLink())
			throw new Error(`runtime package contains a symlink: ${relative(root, sourcePath)}`);
		if (info.isDirectory()) {
			await copyPackageFiles(sourcePath, destinationPath, root, packagePath, childPath);
			continue;
		}
		if (!info.isFile())
			throw new Error(`runtime package contains a non-regular file: ${relative(root, sourcePath)}`);
		if (packagePath === BUNDLED_PI_PACKAGE && childPath === "package.json") {
			const manifest = strippedPiRuntimeManifest(await readPackageManifest(source));
			await writeFile(destinationPath, `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o644 });
			continue;
		}
		await copyFile(sourcePath, destinationPath);
		await chmod(destinationPath, 0o644);
	}
}

async function collectRuntimeFiles(directory: string): Promise<RuntimeFile[]> {
	const files: RuntimeFile[] = [];
	async function visit(current: string): Promise<void> {
		for (const entry of (await readdir(current, { withFileTypes: true })).sort((left, right) =>
			left.name.localeCompare(right.name),
		)) {
			const path = join(current, entry.name);
			const info = await lstat(path);
			if (info.isSymbolicLink()) throw new Error(`runtime staging contains a symlink: ${path}`);
			if (info.isDirectory()) {
				await visit(path);
				continue;
			}
			if (!info.isFile()) throw new Error(`runtime staging contains a non-regular file: ${path}`);
			const contents = await readFile(path);
			files.push({
				path: relative(directory, path).replaceAll("\\", "/"),
				sha256: sha256(contents),
				size: contents.byteLength,
			});
		}
	}
	await visit(directory);
	return files.sort((left, right) => left.path.localeCompare(right.path));
}

function runtimePath(path: string): string {
	if (
		path === "" ||
		path.startsWith("/") ||
		path.includes("\\") ||
		path.split("/").some((part) => part === "" || part === "." || part === "..")
	)
		throw new Error(`runtime manifest contains an unsafe path ${JSON.stringify(path)}`);
	return path;
}

function asManifest(value: unknown): RuntimeManifest {
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error("runtime manifest must be an object");
	const manifest = value as Partial<RuntimeManifest>;
	if (manifest.schemaVersion !== 2) throw new Error("runtime manifest schemaVersion must be 2");
	if (
		manifest.platform?.os !== "linux" ||
		!RUNTIME_ARCHITECTURES.includes(manifest.platform.architecture)
	)
		throw new Error("runtime manifest has an invalid platform");
	if (
		manifest.bun?.version !== BUNDLED_BUN_VERSION ||
		typeof manifest.bun.archive !== "string" ||
		manifest.bun.archive !== BUN_RELEASES[manifest.platform.architecture].archive ||
		typeof manifest.bun.sha256 !== "string" ||
		manifest.bun.sha256 !== BUN_RELEASES[manifest.platform.architecture].sha256
	)
		throw new Error(`runtime manifest must pin Bun ${BUNDLED_BUN_VERSION}`);
	if (
		manifest.rootPackage?.name !== BUNDLED_PI_PACKAGE ||
		manifest.rootPackage.version !== BUNDLED_PI_VERSION
	)
		throw new Error(`runtime manifest must pin ${BUNDLED_PI_PACKAGE}@${BUNDLED_PI_VERSION}`);
	if (!Array.isArray(manifest.packages) || !Array.isArray(manifest.files))
		throw new Error("runtime manifest must list packages and files");
	return manifest as RuntimeManifest;
}

const ZIP_LOCAL_SIGNATURE = 0x04034b50;
const ZIP_CENTRAL_SIGNATURE = 0x02014b50;
const ZIP_END_SIGNATURE = 0x06054b50;

function zipUint16(buffer: Uint8Array, offset: number): number {
	return (buffer[offset] ?? 0) | ((buffer[offset + 1] ?? 0) << 8);
}

function zipUint32(buffer: Uint8Array, offset: number): number {
	return (
		((buffer[offset] ?? 0) |
			((buffer[offset + 1] ?? 0) << 8) |
			((buffer[offset + 2] ?? 0) << 16) |
			((buffer[offset + 3] ?? 0) << 24)) >>>
		0
	);
}

function safeBunArchivePath(path: string): string {
	// ZIP directory members conventionally end in one slash. The slash is
	// metadata, not a second empty path component; reject every other empty part.
	const normalized = path.endsWith("/") ? path.slice(0, -1) : path;
	if (
		normalized === "" ||
		normalized.startsWith("/") ||
		normalized.includes("\\") ||
		normalized.includes("\0") ||
		normalized.split("/").some((part) => part === "" || part === "." || part === "..")
	)
		throw new Error(`Bun archive has an unsafe path ${JSON.stringify(path)}`);
	return normalized;
}

function zipEntryData(
	buffer: Buffer,
	localOffset: number,
	compressedSize: number,
	name: string,
): Buffer {
	if (
		localOffset + 30 > buffer.byteLength ||
		zipUint32(buffer, localOffset) !== ZIP_LOCAL_SIGNATURE
	)
		throw new Error(`Bun archive has an invalid local header for ${name}`);
	const nameLength = zipUint16(buffer, localOffset + 26);
	const extraLength = zipUint16(buffer, localOffset + 28);
	const start = localOffset + 30 + nameLength + extraLength;
	const end = start + compressedSize;
	if (end > buffer.byteLength) throw new Error(`Bun archive entry exceeds archive size: ${name}`);
	return buffer.subarray(start, end);
}

/** Parse and validate an official Bun zip release before any member is staged. */
export function parseVerifiedBunArchive(archive: Uint8Array): BunArchiveEntry[] {
	const buffer = Buffer.from(archive.buffer, archive.byteOffset, archive.byteLength);
	const minimumEnd = 22;
	if (buffer.byteLength < minimumEnd) throw new Error("Bun archive is too small");
	let end = -1;
	const scanFloor = Math.max(0, buffer.byteLength - (minimumEnd + 0xffff));
	for (let offset = buffer.byteLength - minimumEnd; offset >= scanFloor; offset -= 1) {
		if (zipUint32(buffer, offset) === ZIP_END_SIGNATURE) {
			end = offset;
			break;
		}
	}
	if (end < 0) throw new Error("Bun archive has no end-of-central-directory record");
	const entryCount = zipUint16(buffer, end + 10);
	const centralSize = zipUint32(buffer, end + 12);
	const centralOffset = zipUint32(buffer, end + 16);
	if (entryCount === 0xffff || centralSize === 0xffffffff || centralOffset === 0xffffffff)
		throw new Error("Bun archive uses an unsupported ZIP64 layout");
	if (centralOffset + centralSize > buffer.byteLength)
		throw new Error("Bun archive central directory exceeds archive size");
	const entries: BunArchiveEntry[] = [];
	const seen = new Set<string>();
	let cursor = centralOffset;
	for (let index = 0; index < entryCount; index += 1) {
		if (cursor + 46 > buffer.byteLength || zipUint32(buffer, cursor) !== ZIP_CENTRAL_SIGNATURE)
			throw new Error("Bun archive has an invalid central directory entry");
		const flags = zipUint16(buffer, cursor + 8);
		const method = zipUint16(buffer, cursor + 10);
		const checksum = zipUint32(buffer, cursor + 16);
		const compressedSize = zipUint32(buffer, cursor + 20);
		const uncompressedSize = zipUint32(buffer, cursor + 24);
		const nameLength = zipUint16(buffer, cursor + 28);
		const extraLength = zipUint16(buffer, cursor + 30);
		const commentLength = zipUint16(buffer, cursor + 32);
		const externalAttributes = zipUint32(buffer, cursor + 38);
		const rawName = new TextDecoder().decode(
			buffer.subarray(cursor + 46, cursor + 46 + nameLength),
		);
		if ((flags & 0x1) !== 0) throw new Error(`Bun archive entry is encrypted: ${rawName}`);
		const name = safeBunArchivePath(rawName);
		const unixMode = externalAttributes >>> 16;
		if (unixMode !== 0 && (unixMode & 0xf000) === 0xa000)
			throw new Error(`Bun archive contains a symlink: ${name}`);
		if (seen.has(name)) throw new Error(`Bun archive duplicates ${name}`);
		seen.add(name);
		if (rawName.endsWith("/")) {
			entries.push({ name, type: "directory", content: Buffer.alloc(0) });
		} else {
			if (method !== 0 && method !== 8)
				throw new Error(`Bun archive entry uses an unsupported compression method: ${name}`);
			const compressed = zipEntryData(buffer, zipUint32(buffer, cursor + 42), compressedSize, name);
			let content: Buffer;
			if (method === 0) {
				content = Buffer.from(compressed);
			} else {
				try {
					content = inflateRawSync(compressed);
				} catch {
					throw new Error(`Bun archive entry cannot be decompressed: ${name}`);
				}
			}
			if (content.byteLength !== uncompressedSize)
				throw new Error(`Bun archive entry has an invalid size: ${name}`);
			if (crc32(content) !== checksum)
				throw new Error(`Bun archive entry failed its CRC check: ${name}`);
			entries.push({ name, type: "file", content });
		}
		cursor += 46 + nameLength + extraLength + commentLength;
	}
	if (entries.length === 0) throw new Error("Bun archive contains no entries");
	return entries;
}

async function downloadedBunArchive(architecture: RuntimeArchitecture): Promise<Buffer> {
	const release = BUN_RELEASES[architecture];
	const url = `https://github.com/oven-sh/bun/releases/download/bun-v${BUNDLED_BUN_VERSION}/${release.archive}`;
	const response = await fetch(url);
	if (!response.ok) throw new Error(`could not download pinned Bun runtime (${response.status})`);
	return Buffer.from(await response.arrayBuffer());
}

async function downloadedBunLicense(): Promise<Buffer> {
	const response = await fetch(BUN_LICENSE_RELEASE.url);
	if (!response.ok) throw new Error(`could not download the Bun license (${response.status})`);
	return Buffer.from(await response.arrayBuffer());
}

export async function stageBundledBunRuntime(options: {
	directory: string;
	architecture: RuntimeArchitecture;
}): Promise<void> {
	const release = BUN_RELEASES[options.architecture];
	const [archive, license] = await Promise.all([
		downloadedBunArchive(options.architecture),
		downloadedBunLicense(),
	]);
	return stageVerifiedBunArchive({
		directory: options.directory,
		architecture: options.architecture,
		archive,
		expectedSha256: release.sha256,
		releaseDirectory: release.directory,
		archiveName: release.archive,
		license,
		expectedLicenseSha256: BUN_LICENSE_RELEASE.sha256,
	});
}

/**
 * Stage a caller-verified Bun zip archive. Production staging supplies only the
 * pinned Bun release above; this lower-level helper permits deterministic
 * offline fixtures to exercise zip, ELF and license validation.
 */
export async function stageVerifiedBunArchive(options: {
	directory: string;
	architecture: RuntimeArchitecture;
	archive: Uint8Array;
	expectedSha256: string;
	releaseDirectory: string;
	archiveName?: string;
	/** Bun zips omit a license, so the verified license text is staged separately. */
	license: Uint8Array;
	expectedLicenseSha256?: string;
}): Promise<void> {
	if (!/^[0-9a-f]{64}$/.test(options.expectedSha256))
		throw new Error("Bun runtime expected SHA-256 must be a lowercase SHA-256 digest");
	if (sha256(options.archive) !== options.expectedSha256)
		throw new Error(
			`Bun runtime SHA-256 does not match ${options.archiveName ?? "verified archive"}`,
		);
	if (options.license.byteLength === 0)
		throw new Error("Bun runtime license notice must not be empty");
	if (
		options.expectedLicenseSha256 !== undefined &&
		!/^[0-9a-f]{64}$/.test(options.expectedLicenseSha256)
	)
		throw new Error("Bun license expected SHA-256 must be a lowercase SHA-256 digest");
	if (
		options.expectedLicenseSha256 !== undefined &&
		sha256(options.license) !== options.expectedLicenseSha256
	)
		throw new Error("Bun license SHA-256 does not match the pinned Bun release");
	const members = parseVerifiedBunArchive(options.archive);
	const byName = new Map<string, BunArchiveEntry>();
	for (const member of members) {
		if (byName.has(member.name)) throw new Error(`Bun archive duplicates ${member.name}`);
		byName.set(member.name, member);
	}
	const bun = byName.get(`${options.releaseDirectory}/bun`);
	if (bun === undefined || bun.type === "directory")
		throw new Error("Bun archive is missing its regular bun executable");
	assertLinuxElfArchitecture(bun.content, options.architecture, "bundled Bun runtime");
	const bunDirectory = join(resolve(options.directory), "bin");
	await mkdir(bunDirectory, { recursive: true, mode: 0o755 });
	await writeFile(join(bunDirectory, "bun"), bun.content, { mode: 0o755 });
	await chmod(join(bunDirectory, "bun"), 0o755);
	const licensePath = join(resolve(options.directory), ...BUNDLED_BUN_LICENSE_PATH.split("/"));
	await mkdir(dirname(licensePath), { recursive: true, mode: 0o755 });
	await writeFile(licensePath, options.license, { mode: 0o644 });
}

export async function stageBundledPiRuntime(options: {
	repositoryRoot: string;
	directory: string;
	architecture: RuntimeArchitecture;
	/** Offline test seam. Production callers must not supply it. */
	stageBunRuntime?: (directory: string, architecture: RuntimeArchitecture) => Promise<void>;
}): Promise<StagedRuntime> {
	requireNativeRuntimeArchitecture(options.architecture);
	const repositoryRoot = resolve(options.repositoryRoot);
	const sourceNodeModules = join(repositoryRoot, "node_modules");
	const runtimeDirectory = resolve(options.directory);
	await regularDirectory(sourceNodeModules, "workspace node_modules");
	await mkdir(runtimeDirectory, { recursive: true, mode: 0o755 });
	if (options.stageBunRuntime)
		await options.stageBunRuntime(runtimeDirectory, options.architecture);
	else
		await stageBundledBunRuntime({
			directory: runtimeDirectory,
			architecture: options.architecture,
		});
	const destinationNodeModules = join(runtimeDirectory, "node_modules");
	await mkdir(destinationNodeModules, { recursive: true, mode: 0o755 });

	const root = join(sourceNodeModules, ...packageParts(BUNDLED_PI_PACKAGE));
	await regularDirectory(root, `runtime root package ${BUNDLED_PI_PACKAGE}`);
	const packages = new Map<string, RuntimePackage>();
	const visited = new Set<string>();

	async function visit(packageDirectory: string, expectedName?: string): Promise<void> {
		const canonical = resolve(packageDirectory);
		if (visited.has(canonical)) return;
		if (!isContained(sourceNodeModules, canonical))
			throw new Error(`runtime package escapes workspace node_modules: ${canonical}`);
		const manifest = await readPackageManifest(canonical);
		if (typeof manifest.name !== "string" || manifest.name.trim() === "")
			throw new Error(`runtime package ${canonical} has no name`);
		if (expectedName !== undefined && manifest.name !== expectedName)
			throw new Error(`runtime dependency ${expectedName} resolved to ${manifest.name}`);
		if (typeof manifest.version !== "string" || manifest.version.trim() === "")
			throw new Error(`runtime package ${manifest.name} has no version`);
		assertPackagePlatform(manifest, options.architecture, manifest.name);
		const license = requiredLicense(manifest, manifest.name);
		visited.add(canonical);
		const path = relative(sourceNodeModules, canonical).replaceAll("\\", "/");
		if (path === "" || path.startsWith("../"))
			throw new Error("runtime package path escapes node_modules");
		packages.set(path, {
			name: manifest.name,
			version: manifest.version,
			license,
			path: `node_modules/${path}`,
		});
		await copyPackageFiles(
			canonical,
			join(destinationNodeModules, path),
			sourceNodeModules,
			manifest.name,
		);

		const dependencies = stringMap(
			manifest.dependencies,
			`runtime package ${manifest.name} dependencies`,
		);
		const optionalDependencies = stringMap(
			manifest.optionalDependencies,
			`runtime package ${manifest.name} optionalDependencies`,
		);
		for (const name of Object.keys(dependencies).sort()) {
			const dependency = await findInstalledDependency(canonical, sourceNodeModules, name);
			if (!dependency)
				throw new Error(`runtime package ${manifest.name} is missing dependency ${name}`);
			await visit(dependency, name);
		}
		// Preserve every native optional package installed for this native builder.
		for (const name of Object.keys(optionalDependencies).sort()) {
			if (dependencies[name] !== undefined) continue;
			const dependency = await findInstalledDependency(canonical, sourceNodeModules, name);
			if (dependency) await visit(dependency, name);
		}
	}

	await visit(root, BUNDLED_PI_PACKAGE);
	const rootPackage = packages.get(BUNDLED_PI_PACKAGE);
	if (!rootPackage || rootPackage.version !== BUNDLED_PI_VERSION)
		throw new Error(`runtime must contain ${BUNDLED_PI_PACKAGE}@${BUNDLED_PI_VERSION}`);
	const manifest: RuntimeManifest = {
		schemaVersion: 2,
		platform: { os: "linux", architecture: options.architecture },
		bun: {
			version: BUNDLED_BUN_VERSION,
			archive: BUN_RELEASES[options.architecture].archive,
			sha256: BUN_RELEASES[options.architecture].sha256,
		},
		rootPackage: { name: BUNDLED_PI_PACKAGE, version: BUNDLED_PI_VERSION },
		packages: [...packages.values()].sort((left, right) => left.path.localeCompare(right.path)),
		files: await collectRuntimeFiles(runtimeDirectory),
	};
	const manifestPath = join(runtimeDirectory, RUNTIME_MANIFEST_NAME);
	await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o644 });
	await verifyBundledPiRuntime(runtimeDirectory, options.architecture);
	return { directory: runtimeDirectory, manifestPath, manifest };
}

function relativeImportTargets(path: string, source: string): string[] {
	const targets: string[] = [];
	const pattern = /(?:from\s*|import\s*)["'](\.[^"']+)["']/g;
	for (const match of source.matchAll(pattern)) {
		const raw = match[1];
		if (raw === undefined) continue;
		targets.push(posix.normalize(posix.join(posix.dirname(path), raw)));
	}
	return targets;
}

function assertPiRuntimeFiles(
	files: ReadonlyMap<string, { content: Uint8Array }>,
	architecture: RuntimeArchitecture,
): void {
	const packagePrefix = `node_modules/${BUNDLED_PI_PACKAGE}/`;
	const packageManifest = files.get(`${packagePrefix}package.json`);
	if (!packageManifest)
		throw new Error("runtime staging is missing the bundled Pi package manifest");
	let parsed: PackageManifest;
	try {
		parsed = JSON.parse(new TextDecoder().decode(packageManifest.content)) as PackageManifest;
	} catch {
		throw new Error("runtime staging has an unreadable bundled Pi package manifest");
	}
	if (parsed.name !== BUNDLED_PI_PACKAGE || parsed.version !== BUNDLED_PI_VERSION)
		throw new Error(`runtime must contain ${BUNDLED_PI_PACKAGE}@${BUNDLED_PI_VERSION}`);
	if (parsed.bin !== undefined)
		throw new Error("runtime staging must not expose a Pi package bin entrypoint");
	if (isRecord(parsed.exports) && parsed.exports["./rpc-entry"] !== undefined)
		throw new Error("runtime staging must not expose a Pi RPC entrypoint");
	for (const path of files.keys()) {
		if (path.startsWith("node_modules/.bin/"))
			throw new Error("runtime staging must not expose a package-manager executable");
		if (
			path.startsWith(packagePrefix) &&
			/(?:^|\/)rpc-entry(?:\.|\/|$)/.test(path.slice(packagePrefix.length))
		)
			throw new Error("runtime staging must not include a Pi RPC surface");
	}
	const cliPath = `${packagePrefix}dist/bun/cli.js`;
	const cli = files.get(cliPath);
	if (!cli) throw new Error("runtime staging is missing Pi's public bun TUI entrypoint cli.js");
	const toVisit = [cliPath];
	const visited = new Set<string>();
	while (toVisit.length > 0) {
		const path = toVisit.pop();
		if (path === undefined || visited.has(path)) continue;
		visited.add(path);
		const file = files.get(path);
		if (!file) throw new Error(`runtime staging is missing retained Pi TUI chunk ${path}`);
		const source = new TextDecoder().decode(file.content);
		for (const target of relativeImportTargets(path, source)) {
			if (!target.startsWith(packagePrefix)) continue;
			if (!files.has(target))
				throw new Error(`runtime staging is missing retained Pi TUI chunk ${target}`);
			toVisit.push(target);
		}
	}
	const bun = files.get("bin/bun");
	if (!bun) throw new Error("runtime staging is missing bundled Bun");
	assertLinuxElfArchitecture(bun.content, architecture, "bundled Bun runtime");
	const license = files.get(BUNDLED_BUN_LICENSE_PATH);
	if (!license || license.content.byteLength === 0)
		throw new Error("runtime staging is missing the bundled Bun license notice");
}

function verifyRuntimeManifestFiles(
	manifest: RuntimeManifest,
	files: ReadonlyMap<string, { content: Uint8Array }>,
): void {
	const declaredFiles = new Map<string, RuntimeFile>();
	for (const file of manifest.files) {
		const path = runtimePath(file.path);
		if (!/^[0-9a-f]{64}$/.test(file.sha256) || !Number.isSafeInteger(file.size) || file.size < 0)
			throw new Error(`runtime manifest has an invalid digest for ${path}`);
		if (declaredFiles.has(path)) throw new Error(`runtime manifest duplicates ${path}`);
		declaredFiles.set(path, file);
	}
	if (files.size !== declaredFiles.size)
		throw new Error("runtime manifest file set does not match staging");
	for (const [path, declared] of declaredFiles) {
		const actual = files.get(path)?.content;
		if (!actual || sha256(actual) !== declared.sha256 || actual.byteLength !== declared.size)
			throw new Error(`runtime staging integrity check failed for ${path}`);
	}
}

function verifyRuntimePackages(
	manifest: RuntimeManifest,
	files: ReadonlyMap<string, { content: Uint8Array }>,
	architecture: RuntimeArchitecture,
): void {
	const packagePaths = new Set<string>();
	for (const packageRecord of manifest.packages) {
		const path = runtimePath(packageRecord.path);
		if (!path.startsWith("node_modules/"))
			throw new Error(`runtime package path is outside node_modules: ${path}`);
		if (packagePaths.has(path)) throw new Error(`runtime manifest duplicates package ${path}`);
		packagePaths.add(path);
		const packageFile = files.get(`${path}/package.json`);
		if (!packageFile) throw new Error(`runtime manifest package is missing ${path}/package.json`);
		let packageManifest: PackageManifest;
		try {
			packageManifest = JSON.parse(
				new TextDecoder().decode(packageFile.content),
			) as PackageManifest;
		} catch {
			throw new Error(`runtime manifest package has invalid package.json at ${path}`);
		}
		if (
			packageManifest.name !== packageRecord.name ||
			packageManifest.version !== packageRecord.version ||
			requiredLicense(packageManifest, packageRecord.name) !== packageRecord.license
		)
			throw new Error(`runtime manifest package identity changed for ${path}`);
		assertPackagePlatform(packageManifest, architecture, packageRecord.name);
	}
	if (!packagePaths.has(`node_modules/${BUNDLED_PI_PACKAGE}`))
		throw new Error(`runtime manifest is missing ${BUNDLED_PI_PACKAGE}`);
}

/** Verify files on disk after staging, including Bun architecture and Pi TUI closure. */
export async function verifyBundledPiRuntime(
	directory: string,
	architecture: RuntimeArchitecture,
): Promise<RuntimeManifest> {
	const runtimeDirectory = resolve(directory);
	await regularDirectory(runtimeDirectory, "runtime directory");
	let manifest: RuntimeManifest;
	try {
		manifest = asManifest(
			JSON.parse(await readFile(join(runtimeDirectory, RUNTIME_MANIFEST_NAME), "utf8")),
		);
	} catch (error) {
		if (error instanceof Error && error.message.startsWith("runtime manifest")) throw error;
		throw new Error("runtime manifest is unreadable or invalid JSON");
	}
	if (manifest.platform.architecture !== architecture)
		throw new Error(
			`runtime manifest is for linux-${manifest.platform.architecture}, not linux-${architecture}`,
		);
	const actualFiles = await collectRuntimeFiles(runtimeDirectory);
	const files = new Map(
		actualFiles
			.filter((file) => file.path !== RUNTIME_MANIFEST_NAME)
			.map((file) => [file.path, { content: readFile(join(runtimeDirectory, file.path)) }]),
	);
	const loaded = new Map<string, { content: Uint8Array }>();
	for (const [path, pending] of files) loaded.set(path, { content: await pending.content });
	verifyRuntimeManifestFiles(manifest, loaded);
	verifyRuntimePackages(manifest, loaded, architecture);
	assertPiRuntimeFiles(loaded, architecture);
	const bunInfo = await lstat(join(runtimeDirectory, "bin", "bun"));
	if (!bunInfo.isFile() || bunInfo.isSymbolicLink() || (bunInfo.mode & 0o111) === 0)
		throw new Error("bundled Bun runtime is not a regular executable");
	return manifest;
}

/** Verify a release archive's runtime entries without extracting untrusted paths. */
export function verifyBundledPiRuntimeArchive(
	entries: readonly RuntimeArchiveEntry[],
	architecture: RuntimeArchitecture,
): RuntimeManifest {
	const runtime = entries.filter((entry) => entry.name.startsWith("runtime/"));
	const files = new Map<string, { content: Uint8Array; mode: number }>();
	for (const entry of runtime) {
		const path = runtimePath(entry.name.slice("runtime/".length));
		if (files.has(path)) throw new Error(`release archive duplicates runtime entry ${path}`);
		files.set(path, { content: entry.content, mode: entry.mode });
	}
	const manifestFile = files.get(RUNTIME_MANIFEST_NAME);
	if (!manifestFile) throw new Error("release archive is missing runtime manifest");
	let manifest: RuntimeManifest;
	try {
		manifest = asManifest(JSON.parse(new TextDecoder().decode(manifestFile.content)));
	} catch (error) {
		if (error instanceof Error && error.message.startsWith("runtime manifest")) throw error;
		throw new Error("release archive runtime manifest is unreadable or invalid JSON");
	}
	if (manifest.platform.architecture !== architecture)
		throw new Error(`release archive runtime is for linux-${manifest.platform.architecture}`);
	files.delete(RUNTIME_MANIFEST_NAME);
	verifyRuntimeManifestFiles(manifest, files);
	verifyRuntimePackages(manifest, files, architecture);
	assertPiRuntimeFiles(files, architecture);
	if (files.get("bin/bun")?.mode !== 0o755)
		throw new Error("release archive Bun runtime must be executable");
	return manifest;
}

/** Build a retained-notice file only after every copied package supplied license metadata. */
export async function bundledRuntimeNotices(
	directory: string,
	architecture: RuntimeArchitecture,
): Promise<string> {
	const manifest = await verifyBundledPiRuntime(directory, architecture);
	const bunLicense = await readFile(
		join(directory, ...BUNDLED_BUN_LICENSE_PATH.split("/")),
		"utf8",
	).catch(() => "");
	if (bunLicense.trim() === "")
		throw new Error("bundled Bun runtime is missing its license notice");
	const pi = manifest.packages.find((entry) => entry.name === BUNDLED_PI_PACKAGE);
	if (pi?.license !== "MIT") throw new Error("bundled Pi package must retain MIT license metadata");
	const closure = manifest.packages
		.map((entry) => `- ${entry.name}@${entry.version} — ${entry.license}`)
		.sort()
		.join("\n");
	return [
		"# Third-party notices",
		"",
		`## Bun ${BUNDLED_BUN_VERSION}`,
		"",
		`Source: ${manifest.bun.archive} (SHA-256 ${manifest.bun.sha256})`,
		"",
		bunLicense.trim(),
		"",
		`## Pi ${BUNDLED_PI_VERSION} (${pi.license})`,
		"",
		`${BUNDLED_PI_PACKAGE} is distributed under the ${pi.license} license declared in its bundled package metadata.`,
		"",
		"## Bundled Pi dependency closure",
		"",
		"Every package below supplied non-empty license metadata while the archive was staged.",
		closure,
		"",
	].join("\n");
}

export async function runtimeArchiveEntries(
	runtimeDirectory: string,
	architecture: RuntimeArchitecture,
): Promise<DeterministicTarEntry[]> {
	const manifest = await verifyBundledPiRuntime(runtimeDirectory, architecture);
	return [
		{
			name: `runtime/${RUNTIME_MANIFEST_NAME}`,
			path: join(runtimeDirectory, RUNTIME_MANIFEST_NAME),
			mode: 0o644,
		},
		...manifest.files.map((file) => ({
			name: `runtime/${file.path}`,
			path: join(runtimeDirectory, file.path),
			mode: file.path === "bin/bun" ? 0o755 : 0o644,
		})),
	];
}
