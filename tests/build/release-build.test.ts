import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import {
	assertProductArchiveLayout,
	installInstructions,
	mergeReleaseArtifacts,
	productArchiveLayout,
	RELEASE_PRODUCTS,
} from "../../scripts/build-release.ts";
import { writeDeterministicTarGz } from "../../scripts/deterministic-tar.ts";

const packageRoot = resolve(import.meta.dir, "../..");
const sourceCommitTime = 1_735_689_600;

interface TarEntry {
	name: string;
	mode: number;
	uid: number;
	gid: number;
	mtime: number;
	type: number;
}

function text(value: Uint8Array): string {
	return new TextDecoder().decode(value);
}

function tarString(archive: Uint8Array, offset: number, length: number): string {
	const field = archive.subarray(offset, offset + length);
	const terminator = field.indexOf(0);
	return text(terminator === -1 ? field : field.subarray(0, terminator));
}

function tarOctal(archive: Uint8Array, offset: number, length: number): number {
	const value = tarString(archive, offset, length).trim();
	if (!value) return 0;
	return Number.parseInt(value, 8);
}

function tarEntries(archive: Uint8Array): TarEntry[] {
	const entries: TarEntry[] = [];
	for (let offset = 0; offset < archive.byteLength; ) {
		const header = archive.subarray(offset, offset + 512);
		if (header.every((byte) => byte === 0)) break;
		const size = tarOctal(header, 124, 12);
		const prefix = tarString(header, 345, 155);
		const name = tarString(header, 0, 100);
		entries.push({
			name: prefix === "" ? name : `${prefix}/${name}`,
			mode: tarOctal(header, 100, 8),
			uid: tarOctal(header, 108, 8),
			gid: tarOctal(header, 116, 8),
			mtime: tarOctal(header, 136, 12),
			type: header[156] ?? 0,
		});
		offset += 512 + Math.ceil(size / 512) * 512;
	}
	return entries;
}

function runTar(cwd: string, args: readonly string[]) {
	const child = Bun.spawnSync(["tar", ...args], { cwd, stdout: "pipe", stderr: "pipe" });
	return { exitCode: child.exitCode, stdout: text(child.stdout), stderr: text(child.stderr) };
}

function fakeElf(architecture: "amd64" | "arm64"): Buffer {
	const binary = Buffer.alloc(64);
	binary.set([0x7f, 0x45, 0x4c, 0x46, 2, 1]);
	binary[18] = architecture === "amd64" ? 62 : 183;
	return binary;
}

function sha256(contents: Uint8Array): string {
	return createHash("sha256").update(contents).digest("hex");
}

function fixtureRuntime(architecture: "amd64" | "arm64"): Map<string, Buffer> {
	const piManifest = Buffer.from(
		JSON.stringify({ name: "@earendil-works/pi-coding-agent", version: "0.85.1", license: "MIT" }),
	);
	const files = new Map<string, Buffer>([
		["runtime/bin/bun", fakeElf(architecture)],
		["runtime/bun/LICENSE.md", Buffer.from("Bun fixture license\n")],
		["runtime/node_modules/@earendil-works/pi-coding-agent/package.json", piManifest],
		[
			"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
			Buffer.from('import "./chunks/tui.js";\n'),
		],
		[
			"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
			Buffer.from("export {};\n"),
		],
	]);
	const bun =
		architecture === "amd64"
			? {
					archive: "bun-linux-x64.zip",
					sha256: "2d03fb5fb83ac8b567aca0a281b2ce1a1a19d488f56c2968d88c3f25e92fe452",
				}
			: {
					archive: "bun-linux-aarch64.zip",
					sha256: "4b1a332ee861983eb93bcfe6f770fff94e3e31b2c388bdaea3c8ed35e58eed0e",
				};
	const manifest = Buffer.from(
		JSON.stringify({
			schemaVersion: 2,
			platform: { os: "linux", architecture },
			bun: { version: "1.4.0", ...bun },
			rootPackage: { name: "@earendil-works/pi-coding-agent", version: "0.85.1" },
			packages: [
				{
					name: "@earendil-works/pi-coding-agent",
					version: "0.85.1",
					license: "MIT",
					path: "node_modules/@earendil-works/pi-coding-agent",
				},
			],
			files: [...files.entries()].map(([path, content]) => ({
				path: path.slice("runtime/".length),
				sha256: sha256(content),
				size: content.byteLength,
			})),
		}),
	);
	files.set("runtime/manifest.json", manifest);
	return files;
}

async function writeMergeArchive(
	directory: string,
	product: (typeof RELEASE_PRODUCTS)[number],
	architecture: "amd64" | "arm64",
	releaseId: string,
): Promise<void> {
	const stage = join(directory, `${product}-${architecture}`);
	await mkdir(stage, { recursive: true });
	const regular = join(stage, "regular");
	const executable = join(stage, "executable");
	await writeFile(regular, "release entry\n");
	await writeFile(executable, fakeElf(architecture));
	const executableNames: Record<(typeof RELEASE_PRODUCTS)[number], readonly string[]> = {
		pixie_web: ["pixie_web"],
		pixie: ["pixie"],
	};
	const runtime = product === "pixie" ? fixtureRuntime(architecture) : new Map();
	const entries = productArchiveLayout(product, [...runtime.keys()]).map((name) => ({
		name,
		path: runtime.has(name)
			? join(stage, name)
			: executableNames[product].includes(name)
				? executable
				: regular,
		mode: executableNames[product].includes(name) || name === "runtime/bin/bun" ? 0o755 : 0o644,
	}));
	for (const [name, content] of runtime) {
		const path = join(stage, name);
		await mkdir(resolve(path, ".."), { recursive: true });
		await writeFile(path, content);
	}
	await writeDeterministicTarGz(
		join(directory, `${product}-${releaseId}-linux-${architecture}.tar.gz`),
		entries,
		sourceCommitTime,
	);
}

test("release builder derives one commit identity without publishing or accepting a dirty tree", async () => {
	const child = Bun.spawn(["bun", "scripts/build-release.ts", "--dry-run"], {
		cwd: packageRoot,
		stdout: "pipe",
		stderr: "pipe",
	});
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	expect(exitCode).toBe(0);
	expect(stderr).toBe("");
	const plan = JSON.parse(stdout) as {
		releaseId: string;
		sourceCommit: string;
		mode: string;
		publication: string;
		architectures: string[];
		nativeRuntimeArchitecture: string;
		skippedArchitectures: string[];
		products: string[];
	};
	expect(plan.sourceCommit).toMatch(/^[0-9a-f]{40}$/);
	expect(plan.releaseId).toBe(`sha-${plan.sourceCommit.slice(0, 12)}`);
	expect(plan.mode).toBe("validate-only");
	expect(plan.publication).toBe("disabled");
	expect(plan.architectures).toEqual([plan.nativeRuntimeArchitecture]);
	expect(plan.products).toEqual([...RELEASE_PRODUCTS]);
	expect(plan.skippedArchitectures).toEqual(
		["amd64", "arm64"].filter((architecture) => architecture !== plan.nativeRuntimeArchitecture),
	);
});

test("--architecture all builds only the native architecture and reports the skipped one", async () => {
	const child = Bun.spawn(
		["bun", "scripts/build-release.ts", "--architecture", "all", "--dry-run"],
		{ cwd: packageRoot, stdout: "pipe", stderr: "pipe" },
	);
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	expect(exitCode).toBe(0);
	expect(stderr).toBe("");
	const plan = JSON.parse(stdout) as {
		architectures: string[];
		nativeRuntimeArchitecture: string;
		skippedArchitectures: string[];
	};
	expect(plan.architectures).toEqual([plan.nativeRuntimeArchitecture]);
	expect(plan.skippedArchitectures).toHaveLength(1);
	expect(plan.skippedArchitectures).not.toContain(plan.nativeRuntimeArchitecture);
});

test("release archives are deterministic, regular-file-only, and readable by the installed tar", async () => {
	const temporary = await mkdtemp(join(tmpdir(), "pixie-release-archive-"));
	try {
		const stage = join(temporary, "stage");
		await mkdir(stage);
		const names = [
			"pixie-assistant.service",
			"NOTICE.md",
			"pixie-assistant",
			"LICENSE",
			"assistant.json",
			"INSTALL.md",
		];
		for (const name of names) {
			await writeFile(join(stage, name), `${name}\n`);
			await chmod(join(stage, name), 0o700);
		}
		const entries = names.map((name) => ({
			name,
			path: join(stage, name),
			mode: name === "pixie-assistant" ? 0o755 : 0o644,
		}));
		const first = join(temporary, "first.tar.gz");
		const second = join(temporary, "second.tar.gz");
		await writeDeterministicTarGz(first, entries, sourceCommitTime);
		await writeDeterministicTarGz(second, entries, sourceCommitTime);

		const firstBytes = await readFile(first);
		expect(firstBytes.equals(await readFile(second))).toBe(true);
		expect([...firstBytes.subarray(0, 10)]).toEqual([
			0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff,
		]);

		const headers = tarEntries(gunzipSync(firstBytes));
		expect(headers.map(({ name }) => name)).toEqual([
			"INSTALL.md",
			"LICENSE",
			"NOTICE.md",
			"assistant.json",
			"pixie-assistant",
			"pixie-assistant.service",
		]);
		for (const header of headers) {
			expect(header.uid).toBe(0);
			expect(header.gid).toBe(0);
			expect(header.mtime).toBe(sourceCommitTime);
			expect(header.type).toBe("0".charCodeAt(0));
			expect(header.mode).toBe(header.name === "pixie-assistant" ? 0o755 : 0o644);
		}

		const list = runTar(temporary, ["-tzf", first]);
		expect(list.exitCode).toBe(0);
		expect(list.stderr).toBe("");
		expect(list.stdout.trim().split("\n")).toEqual(headers.map(({ name }) => name));
		const extracted = join(temporary, "extracted");
		await mkdir(extracted);
		const extract = runTar(temporary, ["-xzf", first, "-C", extracted]);
		expect(extract.exitCode).toBe(0);
		for (const header of headers) {
			const mode = (await lstat(join(extracted, header.name))).mode;
			expect(mode & 0o777).toBe(header.mode);
		}

		const unsafe = join(stage, "unsafe-link");
		await symlink(join(stage, "LICENSE"), unsafe);
		await expect(
			writeDeterministicTarGz(
				join(temporary, "unsafe.tar.gz"),
				[{ name: "unsafe-link", path: unsafe, mode: 0o644 }],
				sourceCommitTime,
			),
		).rejects.toThrow("not a regular file");
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});

test("deterministic release archives preserve long runtime member paths through ustar prefixes", async () => {
	const temporary = await mkdtemp(join(tmpdir(), "pixie-release-ustar-"));
	try {
		const source = join(temporary, "runtime-file");
		const archive = join(temporary, "runtime.tar.gz");
		const name =
			"runtime/node_modules/@anthropic-ai/sdk/resources/beta/organization/federation/rules/workspaces.d.mts.map";
		await writeFile(source, "runtime\n");
		await writeDeterministicTarGz(archive, [{ name, path: source, mode: 0o644 }], sourceCommitTime);
		expect(tarEntries(gunzipSync(await readFile(archive))).map((entry) => entry.name)).toEqual([
			name,
		]);
		const listed = runTar(temporary, ["-tzf", archive]);
		expect(listed.exitCode).toBe(0);
		expect(listed.stdout.trim()).toBe(name);
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});

test("two public product layouts retain the native Pi TUI only in the Pi-bearing archive", () => {
	const runtime = [
		"runtime/manifest.json",
		"runtime/bin/bun",
		"runtime/bun/LICENSE.md",
		"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
		"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
		"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
	];
	expect(productArchiveLayout("pixie_web")).toEqual([
		"INSTALL.md",
		"LICENSE",
		"NOTICE.md",
		"pixie_web",
	]);
	expect(productArchiveLayout("pixie", runtime)).toEqual(
		expect.arrayContaining([
			"pixie",
			"libexec/pixie_assistant.js",
			"THIRD_PARTY_NOTICES.md",
			...runtime,
		]),
	);
	expect(() =>
		assertProductArchiveLayout(
			"pixie",
			productArchiveLayout("pixie", runtime).filter((entry) => entry !== "pixie"),
		),
	).toThrow("layout is invalid");
	expect(() =>
		assertProductArchiveLayout("pixie", [
			...productArchiveLayout("pixie", runtime),
			"pixie_assistant",
		]),
	).toThrow("public root");
});

test("release archives reject Node, public assistant, RPC, and incomplete Bun layouts", () => {
	const runtime = [
		"runtime/manifest.json",
		"runtime/bin/bun",
		"runtime/bun/LICENSE.md",
		"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
		"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
		"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
	];
	const layout = productArchiveLayout("pixie", runtime);
	expect(() =>
		assertProductArchiveLayout(
			"pixie",
			layout.filter((entry) => entry !== "runtime/bin/bun"),
		),
	).toThrow("runtime is incomplete");
	expect(() =>
		assertProductArchiveLayout("pixie", [
			...layout.filter((entry) => entry !== "runtime/bin/bun"),
			"runtime/node/bin/node",
		]),
	).toThrow("runtime is incomplete");
	expect(() => assertProductArchiveLayout("pixie", [...layout, "pixie_assistant.js"])).toThrow(
		"public root",
	);
	expect(() =>
		assertProductArchiveLayout("pixie", [
			...layout,
			"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/rpc-entry.js",
		]),
	).toThrow("RPC entrypoint");
});

test("generated install instructions preserve native Pi ownership and exclusive installation", () => {
	const host = installInstructions("pixie");
	expect(host).toContain("./pixie");
	expect(host).toContain("./pixie serve --config");
	expect(host).toContain("libexec/pixie_assistant.js");
});

test("release merge requires native archives for every product before publishing complete metadata", async () => {
	const temporary = await mkdtemp(join(tmpdir(), "pixie-release-merge-"));
	const output = join(temporary, "output");
	const sourceCommit = "a".repeat(40);
	const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
	const identity = { sourceCommit, releaseId, clean: true };
	try {
		await mkdir(output);
		await expect(mergeReleaseArtifacts(output, identity, temporary)).rejects.toThrow(
			"complete release merge requires regular archive",
		);
		expect(await Bun.file(join(output, "checksums.txt")).exists()).toBe(false);
		for (const architecture of ["amd64", "arm64"] as const)
			for (const product of RELEASE_PRODUCTS)
				await writeMergeArchive(output, product, architecture, releaseId);

		const artifacts = await mergeReleaseArtifacts(output, identity, temporary);
		expect(artifacts).toHaveLength(4);
		expect((await readFile(join(output, "checksums.txt"), "utf8")).trim().split("\n")).toHaveLength(
			4,
		);
		const manifest = JSON.parse(await readFile(join(output, "release-manifest.json"), "utf8")) as {
			completeSet: boolean;
			architectures: string[];
			artifacts: { entrypointSha256: string }[];
		};
		expect(manifest.completeSet).toBe(true);
		expect(manifest.architectures).toEqual(["amd64", "arm64"]);
		expect(manifest.artifacts).toHaveLength(4);
		expect(
			manifest.artifacts.every((artifact) => /^[0-9a-f]{64}$/.test(artifact.entrypointSha256)),
		).toBe(true);
	} finally {
		await rm(temporary, { recursive: true, force: true });
	}
});
