import { expect, test } from "bun:test";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { gunzipSync } from "node:zlib";
import { installInstructions } from "../../scripts/build-release.ts";
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
		entries.push({
			name: tarString(header, 0, 100),
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
		variants: string[];
	};
	expect(plan.sourceCommit).toMatch(/^[0-9a-f]{40}$/);
	expect(plan.releaseId).toBe(`sha-${plan.sourceCommit.slice(0, 12)}`);
	expect(plan.mode).toBe("validate-only");
	expect(plan.publication).toBe("disabled");
	expect(plan.architectures).toEqual(["amd64", "arm64"]);
	expect(plan.variants).toEqual(["assistant", "host"]);
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

test("generated install instructions carry required private configuration", () => {
	const assistant = installInstructions("assistant");
	expect(assistant).toContain("~/.config/pixie/pixie.env");
	expect(assistant).toContain("PIXIE_PI_SECRET_KEY");
	expect(assistant).not.toContain("PIXIE_MCP_TOKEN");
	expect(assistant).toContain("agentDir");

	const host = installInstructions("host");
	expect(host).toContain("~/.config/pixie/pixie.env");
	expect(host).toContain("PIXIE_PI_SECRET_KEY");
	expect(host).toContain("PIXIE_MCP_TOKEN");
	expect(host).toContain("piExecutable");
});
