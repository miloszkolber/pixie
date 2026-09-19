import { lstat, readdir, readFile } from "node:fs/promises";
import { relative, resolve } from "node:path";

export type MewaPackageName = "mewa-ui" | "mewa-svelte" | "mewa-icons";

export interface MewaAsset {
	package: MewaPackageName;
	file: string;
	url: string;
	size: number;
	sha256: string;
	checksumsSha256: string;
}

export interface MewaLock {
	schemaVersion: 1;
	repository: string;
	release: string;
	version: string;
	revision: string;
	assets: MewaAsset[];
	icons: string[];
}

interface PackageManifest {
	name?: string;
	version?: string;
	icons?: unknown;
}

interface Checksums {
	algorithm?: string;
	files?: Record<string, string>;
}

interface SelectedIcons {
	version?: string;
	icons?: unknown;
}

const packageNames = ["mewa-ui", "mewa-svelte", "mewa-icons"] as const;
const hexadecimalDigest = /^[0-9a-f]{64}$/;
const iconName = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function object(value: unknown): Record<string, unknown> | null {
	return typeof value === "object" && value !== null && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

function safeRelativePath(path: string, label: string): void {
	if (
		path.length === 0 ||
		path.startsWith("/") ||
		path.includes("\\") ||
		path.split("/").some((segment) => segment === "" || segment === "." || segment === "..")
	) {
		throw new Error(`${label} contains an unsafe path: ${path}`);
	}
}

function stringArray(value: unknown): value is string[] {
	return Array.isArray(value) && value.every((entry) => typeof entry === "string");
}

export async function sha256(path: string): Promise<string> {
	const hasher = new Bun.CryptoHasher("sha256");
	hasher.update(await Bun.file(path).arrayBuffer());
	return hasher.digest("hex");
}

export async function loadMewaLock(path: string): Promise<MewaLock> {
	const parsed = object(JSON.parse(await readFile(path, "utf8")));
	if (!parsed) throw new Error("Mewa vendor lock must be an object");
	const lock = parsed as unknown as MewaLock;
	if (
		lock.schemaVersion !== 1 ||
		typeof lock.repository !== "string" ||
		!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(lock.repository) ||
		typeof lock.version !== "string" ||
		!/^[0-9]+\.[0-9]+\.[0-9]+$/.test(lock.version) ||
		lock.release !== `v${lock.version}` ||
		typeof lock.revision !== "string" ||
		!/^[0-9a-f]{40}$/.test(lock.revision) ||
		!Array.isArray(lock.assets) ||
		!stringArray(lock.icons)
	) {
		throw new Error("Unsupported Mewa vendor lock");
	}

	const assets = new Map<MewaPackageName, MewaAsset>();
	for (const candidate of lock.assets) {
		const asset = object(candidate) as unknown as MewaAsset | null;
		if (!asset || !packageNames.includes(asset.package) || assets.has(asset.package)) {
			throw new Error("Mewa vendor lock must contain each release package exactly once");
		}
		const expectedFile = `${asset.package}-${lock.version}.tar.gz`;
		const expectedURL = `https://github.com/${lock.repository}/releases/download/${lock.release}/${expectedFile}`;
		if (
			asset.file !== expectedFile ||
			asset.url !== expectedURL ||
			!Number.isSafeInteger(asset.size) ||
			asset.size <= 0 ||
			!hexadecimalDigest.test(asset.sha256) ||
			!hexadecimalDigest.test(asset.checksumsSha256)
		) {
			throw new Error(`Mewa vendor lock has invalid ${asset.package} release metadata`);
		}
		assets.set(asset.package, asset);
	}
	if (assets.size !== packageNames.length) {
		throw new Error("Mewa vendor lock must contain each release package exactly once");
	}
	if (
		new Set(lock.icons).size !== lock.icons.length ||
		lock.icons.length === 0 ||
		lock.icons.some((icon) => !iconName.test(icon))
	) {
		throw new Error("Mewa vendor lock has an invalid selected icon list");
	}
	return lock;
}

export function assetFor(lock: MewaLock, packageName: MewaPackageName): MewaAsset {
	const asset = lock.assets.find((candidate) => candidate.package === packageName);
	if (!asset) throw new Error(`Mewa vendor lock is missing ${packageName}`);
	return asset;
}

async function filesBelow(root: string, directory = root): Promise<string[]> {
	const files: string[] = [];
	for (const entry of await readdir(directory, { withFileTypes: true })) {
		const path = resolve(directory, entry.name);
		if (entry.isDirectory()) {
			files.push(...(await filesBelow(root, path)));
		} else if (entry.isFile()) {
			files.push(relative(root, path).replaceAll("\\", "/"));
		} else {
			throw new Error(`${relative(root, path)} must be a regular file or directory`);
		}
	}
	return files.sort();
}

async function readChecksums(
	root: string,
	file: string,
	asset: MewaAsset,
): Promise<Record<string, string>> {
	const path = resolve(root, file);
	if ((await sha256(path)) !== asset.checksumsSha256) {
		throw new Error(`${asset.package}/${file} does not match the vendor lock`);
	}
	const checksums = JSON.parse(await readFile(path, "utf8")) as Checksums;
	const files = checksums.files;
	if (checksums.algorithm !== "sha256" || !files || !object(files)) {
		throw new Error(`${asset.package} has an invalid checksum manifest`);
	}
	for (const [relativePath, expected] of Object.entries(files)) {
		safeRelativePath(relativePath, `${asset.package} checksum manifest`);
		if (!hexadecimalDigest.test(expected)) {
			throw new Error(`${asset.package} has an invalid checksum for ${relativePath}`);
		}
	}
	return files;
}

async function verifyManifest(
	root: string,
	asset: MewaAsset,
	version: string,
): Promise<PackageManifest> {
	const manifest = JSON.parse(
		await readFile(resolve(root, "manifest.json"), "utf8"),
	) as PackageManifest;
	if (manifest.name !== asset.package || manifest.version !== version) {
		throw new Error(`${asset.package} manifest does not match ${version}`);
	}
	return manifest;
}

async function verifyFiles(
	root: string,
	asset: MewaAsset,
	checksums: Record<string, string>,
	actualFiles: string[],
): Promise<void> {
	for (const file of actualFiles) {
		const expected = checksums[file];
		if (!expected) throw new Error(`${asset.package} contains unexpected file ${file}`);
		if ((await sha256(resolve(root, file))) !== expected) {
			throw new Error(`${asset.package}/${file} failed verification`);
		}
	}
}

export async function verifyReleasePackage(
	root: string,
	asset: MewaAsset,
	version: string,
): Promise<void> {
	if (!(await lstat(root)).isDirectory()) throw new Error(`${asset.package} must be a directory`);
	const checksumFile = "checksums.json";
	const checksums = await readChecksums(root, checksumFile, asset);
	const actual = await filesBelow(root);
	const expected = [...Object.keys(checksums), checksumFile].sort();
	if (actual.length !== expected.length || actual.some((file, index) => file !== expected[index])) {
		throw new Error(`${asset.package} file set does not match its checksum manifest`);
	}
	await verifyFiles(
		root,
		asset,
		checksums,
		actual.filter((file) => file !== checksumFile),
	);
	await verifyManifest(root, asset, version);
}

async function verifySelectedIcons(root: string, lock: MewaLock): Promise<void> {
	const asset = assetFor(lock, "mewa-icons");
	if (!(await lstat(root)).isDirectory()) throw new Error("mewa-icons must be a directory");
	const checksumFile = "upstream-checksums.json";
	const checksums = await readChecksums(root, checksumFile, asset);
	const selected = JSON.parse(
		await readFile(resolve(root, "selected.json"), "utf8"),
	) as SelectedIcons;
	if (
		selected.version !== lock.version ||
		!stringArray(selected.icons) ||
		selected.icons.length !== lock.icons.length ||
		selected.icons.some((icon, index) => icon !== lock.icons[index])
	) {
		throw new Error("mewa-icons selection does not match the vendor lock");
	}

	const actual = await filesBelow(root);
	const packageFiles = [
		"LICENSE",
		"README.md",
		"package.json",
		"manifest.json",
		"licenses/LUCIDE-LICENSE.txt",
	];
	const expected = [
		...packageFiles,
		checksumFile,
		"selected.json",
		...lock.icons.map((icon) => `icons/${icon}.svg`),
	].sort();
	if (actual.length !== expected.length || actual.some((file, index) => file !== expected[index])) {
		throw new Error("mewa-icons file set does not match the selected icon lock");
	}
	await verifyFiles(root, asset, checksums, [
		...packageFiles,
		...lock.icons.map((icon) => `icons/${icon}.svg`),
	]);
	const manifest = await verifyManifest(root, asset, lock.version);
	const manifestIcons = manifest.icons;
	if (!stringArray(manifestIcons) || lock.icons.some((icon) => !manifestIcons.includes(icon))) {
		throw new Error("mewa-icons release manifest is missing a selected icon");
	}
}

export async function verifyVendoredMewa(vendorRoot: string, lock: MewaLock): Promise<void> {
	const rootInfo = await lstat(vendorRoot);
	if (!rootInfo.isDirectory()) throw new Error("Mewa vendor root must be a directory");
	await verifyReleasePackage(
		resolve(vendorRoot, "mewa-ui"),
		assetFor(lock, "mewa-ui"),
		lock.version,
	);
	await verifyReleasePackage(
		resolve(vendorRoot, "mewa-svelte"),
		assetFor(lock, "mewa-svelte"),
		lock.version,
	);
	await verifySelectedIcons(resolve(vendorRoot, "mewa-icons"), lock);

	const rootFiles = await readdir(vendorRoot, { withFileTypes: true });
	const names = rootFiles.map((entry) => entry.name).sort();
	const expected = [...packageNames, "mewa.lock.json"].sort();
	if (names.length !== expected.length || names.some((name, index) => name !== expected[index])) {
		throw new Error("Mewa vendor root contains an unexpected entry");
	}
}
