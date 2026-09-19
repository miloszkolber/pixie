import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { crc32 } from "node:zlib";
import {
	BUNDLED_BUN_LICENSE_PATH,
	BUNDLED_BUN_VERSION,
	bundledRuntimeNotices,
	nativeRuntimeArchitecture,
	parseVerifiedBunArchive,
	runtimeArchiveEntries,
	stageBundledPiRuntime,
	stageVerifiedBunArchive,
	verifyBundledPiRuntime,
} from "../../scripts/release-runtime.ts";

const repositoryRoot = resolve(import.meta.dir, "../../..");

async function writePackage(
	root: string,
	name: string,
	manifest: Record<string, unknown>,
): Promise<void> {
	const directory = join(root, "node_modules", ...name.split("/"));
	await mkdir(directory, { recursive: true });
	await writeFile(
		join(directory, "package.json"),
		JSON.stringify({ name, version: "1.0.0", license: "MIT", ...manifest }),
	);
	await writeFile(
		join(directory, "index.js"),
		`export const packageName = ${JSON.stringify(name)};\n`,
	);
}

function fakeElf(architecture: "amd64" | "arm64"): Buffer {
	const binary = Buffer.alloc(20);
	binary.set([0x7f, 0x45, 0x4c, 0x46, 2, 1], 0);
	const machine = architecture === "amd64" ? 62 : 183;
	binary[18] = machine & 0xff;
	binary[19] = machine >> 8;
	return binary;
}

interface ZipFixtureEntry {
	name: string;
	content: Uint8Array;
	mode: number;
	directory?: boolean;
}

/** Build a stored-entry ZIP fixture so archive parsing needs no network. */
function zipFixture(entries: readonly ZipFixtureEntry[]): Buffer {
	const locals: Buffer[] = [];
	const centrals: Buffer[] = [];
	let offset = 0;
	for (const entry of entries) {
		const name = Buffer.from(entry.name);
		const content = entry.directory ? Buffer.alloc(0) : Buffer.from(entry.content);
		const checksum = crc32(content);
		const local = Buffer.alloc(30);
		local.writeUInt32LE(0x04034b50, 0);
		local.writeUInt16LE(20, 4);
		local.writeUInt32LE(checksum, 14);
		local.writeUInt32LE(content.byteLength, 18);
		local.writeUInt32LE(content.byteLength, 22);
		local.writeUInt16LE(name.byteLength, 26);
		const localBlock = Buffer.concat([local, name, content]);
		const central = Buffer.alloc(46);
		central.writeUInt32LE(0x02014b50, 0);
		central.writeUInt16LE((3 << 8) | 20, 4);
		central.writeUInt16LE(20, 6);
		central.writeUInt32LE(checksum, 16);
		central.writeUInt32LE(content.byteLength, 20);
		central.writeUInt32LE(content.byteLength, 24);
		central.writeUInt16LE(name.byteLength, 28);
		central.writeUInt32LE((entry.mode << 16) >>> 0, 38);
		central.writeUInt32LE(offset, 42);
		centrals.push(Buffer.concat([central, name]));
		locals.push(localBlock);
		offset += localBlock.byteLength;
	}
	const centralBlock = Buffer.concat(centrals);
	const end = Buffer.alloc(22);
	end.writeUInt32LE(0x06054b50, 0);
	end.writeUInt16LE(entries.length, 8);
	end.writeUInt16LE(entries.length, 10);
	end.writeUInt32LE(centralBlock.byteLength, 12);
	end.writeUInt32LE(offset, 16);
	return Buffer.concat([...locals, centralBlock, end]);
}

const FIXTURE_BUN_ROOT = "fixture-bun";

function fixtureBunArchive(architecture: "amd64" | "arm64"): Buffer {
	return zipFixture([
		{ name: `${FIXTURE_BUN_ROOT}/`, content: Buffer.alloc(0), mode: 0o40755, directory: true },
		{ name: `${FIXTURE_BUN_ROOT}/bun`, content: fakeElf(architecture), mode: 0o100755 },
	]);
}

async function stageFixtureBun(directory: string, architecture: "amd64" | "arm64"): Promise<void> {
	const archive = fixtureBunArchive(architecture);
	await stageVerifiedBunArchive({
		directory,
		architecture,
		archive,
		expectedSha256: createHash("sha256").update(archive).digest("hex"),
		releaseDirectory: FIXTURE_BUN_ROOT,
		license: Buffer.from("Bun fixture license\n"),
	});
}

test("runtime staging copies the installed exact closure, bundled Bun, and Pi TUI bundle", async () => {
	await mkdir(join(repositoryRoot, ".tmp-work"), { recursive: true });
	const temporary = await mkdtemp(join(repositoryRoot, ".tmp-work", "release-runtime-"));
	try {
		await writePackage(temporary, "@earendil-works/pi-coding-agent", {
			version: "0.85.1",
			dependencies: { "required-dependency": "1.0.0" },
			optionalDependencies: { "native-optional": "1.0.0" },
			bin: { pi: "dist/bundle/cli.js" },
			exports: {
				".": { import: "./index.js" },
				"./rpc-entry": { import: "./dist/bun/rpc-entry.js" },
			},
		});
		const piDirectory = join(temporary, "node_modules", "@earendil-works", "pi-coding-agent");
		await mkdir(join(piDirectory, "dist", "bun", "chunks"), { recursive: true });
		await writeFile(join(piDirectory, "dist", "bun", "cli.js"), 'import "./chunks/tui.js";\n');
		await writeFile(join(piDirectory, "dist", "bun", "chunks", "tui.js"), "export {};\n");
		await writeFile(join(piDirectory, "dist", "bun", "rpc-entry.js"), "export {};\n");
		await writePackage(temporary, "required-dependency", {});
		await writePackage(temporary, "native-optional", {
			os: ["linux"],
			cpu: [nativeRuntimeArchitecture() === "amd64" ? "x64" : "arm64"],
		});

		const runtime = await stageBundledPiRuntime({
			repositoryRoot: temporary,
			directory: join(temporary, "runtime"),
			architecture: nativeRuntimeArchitecture(),
			stageBunRuntime: stageFixtureBun,
		});

		expect(runtime.manifest.bun.version).toBe(BUNDLED_BUN_VERSION);
		expect(runtime.manifest.bun.archive).toMatch(/^bun-linux-(?:x64|aarch64)\.zip$/);
		expect(runtime.manifest.bun.sha256).toMatch(/^[0-9a-f]{64}$/);
		expect(runtime.manifest.rootPackage).toEqual({
			name: "@earendil-works/pi-coding-agent",
			version: "0.85.1",
		});
		expect(runtime.manifest.packages.map((entry) => entry.name)).toEqual(
			expect.arrayContaining([
				"@earendil-works/pi-coding-agent",
				"required-dependency",
				"native-optional",
			]),
		);
		const entries = await runtimeArchiveEntries(runtime.directory, nativeRuntimeArchitecture());
		const archiveNames = entries.map((entry) => entry.name);
		expect(archiveNames).toEqual(
			expect.arrayContaining([
				"runtime/manifest.json",
				"runtime/bin/bun",
				`runtime/${BUNDLED_BUN_LICENSE_PATH}`,
				"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
				"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
			]),
		);
		expect(archiveNames).not.toContain("runtime/node_modules/.bin/pi");
		expect(archiveNames).not.toContain(
			"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/rpc-entry.js",
		);
		expect(archiveNames.some((name) => name.includes("node/bin/node"))).toBe(false);
		expect(entries.find((entry) => entry.name === "runtime/bin/bun")?.mode).toBe(0o755);
		const stagedManifest = JSON.parse(
			await readFile(
				join(
					runtime.directory,
					"node_modules",
					"@earendil-works",
					"pi-coding-agent",
					"package.json",
				),
				"utf8",
			),
		) as Record<string, unknown>;
		expect(stagedManifest).not.toHaveProperty("bin");
		expect(stagedManifest.exports).not.toHaveProperty("./rpc-entry");

		const notices = await bundledRuntimeNotices(runtime.directory, nativeRuntimeArchitecture());
		expect(notices).toContain(`## Bun ${BUNDLED_BUN_VERSION}`);
		expect(notices).toContain("## Pi 0.85.1 (MIT)");
		expect(notices).not.toMatch(/Node\.js|Bundled npm/);

		await writeFile(
			join(runtime.directory, "node_modules", "required-dependency", "index.js"),
			"tampered\n",
		);
		await expect(
			verifyBundledPiRuntime(runtime.directory, nativeRuntimeArchitecture()),
		).rejects.toThrow("integrity check failed");
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});

test("runtime verification refuses a Node manifest in place of the pinned Bun runtime", async () => {
	await mkdir(join(repositoryRoot, ".tmp-work"), { recursive: true });
	const temporary = await mkdtemp(join(repositoryRoot, ".tmp-work", "release-runtime-node-"));
	try {
		await writePackage(temporary, "@earendil-works/pi-coding-agent", {
			version: "0.85.1",
			exports: { ".": { import: "./index.js" } },
		});
		const piDirectory = join(temporary, "node_modules", "@earendil-works", "pi-coding-agent");
		await mkdir(join(piDirectory, "dist", "bun"), { recursive: true });
		await writeFile(join(piDirectory, "dist", "bun", "cli.js"), "export {};\n");
		const runtime = await stageBundledPiRuntime({
			repositoryRoot: temporary,
			directory: join(temporary, "runtime"),
			architecture: nativeRuntimeArchitecture(),
			stageBunRuntime: stageFixtureBun,
		});
		const manifestPath = join(runtime.directory, "manifest.json");
		const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as Record<string, unknown>;
		manifest.node = manifest.bun;
		delete manifest.bun;
		await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
		await expect(
			verifyBundledPiRuntime(runtime.directory, nativeRuntimeArchitecture()),
		).rejects.toThrow(`must pin Bun ${BUNDLED_BUN_VERSION}`);
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});

test("verified Bun staging rejects bad hashes, unsafe paths, symlinks, and wrong architectures", async () => {
	const architecture = nativeRuntimeArchitecture();
	const other = architecture === "amd64" ? "arm64" : "amd64";
	const archive = fixtureBunArchive(architecture);
	const license = Buffer.from("Bun fixture license\n");
	const shared = {
		directory: join(repositoryRoot, ".tmp-work", "bad-bun"),
		architecture,
		archive,
		releaseDirectory: FIXTURE_BUN_ROOT,
		license,
	} as const;
	await expect(
		stageVerifiedBunArchive({ ...shared, expectedSha256: "0".repeat(64) }),
	).rejects.toThrow("SHA-256");
	await expect(
		stageVerifiedBunArchive({
			...shared,
			expectedSha256: createHash("sha256").update(archive).digest("hex"),
			expectedLicenseSha256: "1".repeat(64),
		}),
	).rejects.toThrow("license SHA-256");
	await expect(
		stageVerifiedBunArchive({
			...shared,
			expectedSha256: createHash("sha256").update(archive).digest("hex"),
			architecture: other,
		}),
	).rejects.toThrow("architecture does not match");
	const unsafe = zipFixture([
		{ name: "../escape", content: Buffer.from("x"), mode: 0o100644 },
		{ name: `${FIXTURE_BUN_ROOT}/bun`, content: fakeElf(architecture), mode: 0o100755 },
	]);
	expect(() => parseVerifiedBunArchive(unsafe)).toThrow("unsafe path");
	const symlink = zipFixture([
		{ name: FIXTURE_BUN_ROOT, content: Buffer.alloc(0), mode: 0o40755, directory: true },
		{ name: `${FIXTURE_BUN_ROOT}/bun`, content: Buffer.from("../outside"), mode: 0o120777 },
	]);
	expect(() => parseVerifiedBunArchive(symlink)).toThrow("symlink");
	const corrupted = Buffer.from(archive);
	const bunContentOffset =
		30 +
		Buffer.byteLength(`${FIXTURE_BUN_ROOT}/`) +
		30 +
		Buffer.byteLength(`${FIXTURE_BUN_ROOT}/bun`);
	corrupted[bunContentOffset] = (corrupted[bunContentOffset] ?? 0) ^ 0xff;
	expect(() => parseVerifiedBunArchive(corrupted)).toThrow("CRC");
});

test("runtime staging fails closed when a copied dependency omits license metadata", async () => {
	const architecture = nativeRuntimeArchitecture();
	await mkdir(join(repositoryRoot, ".tmp-work"), { recursive: true });
	const temporary = await mkdtemp(join(repositoryRoot, ".tmp-work", "release-runtime-license-"));
	try {
		await writePackage(temporary, "@earendil-works/pi-coding-agent", {
			version: "0.85.1",
			dependencies: { "unlicensed-dependency": "1.0.0" },
		});
		const unlicensed = join(temporary, "node_modules", "unlicensed-dependency");
		await mkdir(unlicensed, { recursive: true });
		await writeFile(
			join(unlicensed, "package.json"),
			JSON.stringify({ name: "unlicensed-dependency", version: "1.0.0" }),
		);
		await expect(
			stageBundledPiRuntime({
				repositoryRoot: temporary,
				directory: join(temporary, "runtime"),
				architecture,
				stageBunRuntime: stageFixtureBun,
			}),
		).rejects.toThrow("unlicensed-dependency has no license metadata");
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});

test("runtime staging refuses a cross-architecture optional dependency before copying it", async () => {
	const native = nativeRuntimeArchitecture();
	const other = native === "amd64" ? "arm64" : "amd64";
	await mkdir(join(repositoryRoot, ".tmp-work"), { recursive: true });
	const temporary = await mkdtemp(join(repositoryRoot, ".tmp-work", "release-runtime-cross-"));
	try {
		await writePackage(temporary, "@earendil-works/pi-coding-agent", { version: "0.85.1" });
		const target = join(temporary, "runtime");
		await expect(
			stageBundledPiRuntime({ repositoryRoot: temporary, directory: target, architecture: other }),
		).rejects.toThrow(`requires a native linux-${other} builder`);
		await expect(Bun.file(join(target, "manifest.json")).exists()).resolves.toBe(false);
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});
