import { expect, test } from "bun:test";
import { lstat, readFile, rm } from "node:fs/promises";
import { resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dir, "../..");
const rootManifestPath = resolve(repositoryRoot, "package.json");
const assistantBundlePath = resolve(repositoryRoot, "dist/pixie_assistant.js");

interface Manifest {
	scripts?: Record<string, string>;
}

async function readManifest(path: string): Promise<Manifest> {
	return JSON.parse(await readFile(path, "utf8")) as Manifest;
}

function script(manifest: Manifest, name: string): string {
	const value = manifest.scripts?.[name];
	if (value === undefined) throw new Error(`missing script ${name}`);
	return value;
}

test("root product commands keep the architecture-appropriate build entrypoints", async () => {
	const root = await readManifest(rootManifestPath);
	expect(script(root, "build:pixie_web")).toBe("bun run build:webui && bun run build:go");
	expect(script(root, "build:pixie")).toBe(
		"CGO_ENABLED=0 go build -trimpath -o dist/pixie ./cmd/pixie",
	);
	expect(script(root, "build:pixie_assistant")).toBe(
		"bun build --target=bun --outfile dist/pixie_assistant.js src/assistant/serve.ts",
	);
});

test("assistant build script emits a portable Bun JavaScript bundle instead of a compiled executable", async () => {
	const root = await readManifest(rootManifestPath);
	const command = script(root, "build:pixie_assistant");

	expect(command).toBe(
		"bun build --target=bun --outfile dist/pixie_assistant.js src/assistant/serve.ts",
	);
	expect(command).not.toContain("--compile");
	expect(command).not.toContain("bun-linux");
	// A missing `.js` would leave a hyphen/extensionless output name.
	expect(command).not.toContain("dist/pixie_assistant ");

	// Execute the root manifest command and confirm the artifact is a
	// JavaScript bundle, not a compiled ELF executable.
	await rm(assistantBundlePath, { force: true });
	const build = Bun.spawn([process.execPath, "run", "build:pixie_assistant"], {
		cwd: repositoryRoot,
		stdout: "pipe",
		stderr: "pipe",
	});
	const [stdout, stderr, exitCode] = await Promise.all([
		new Response(build.stdout).text(),
		new Response(build.stderr).text(),
		build.exited,
	]);
	try {
		if (exitCode !== 0)
			throw new Error(`assistant build failed (${exitCode}): ${stderr.trim() || stdout.trim()}`);
		const info = await lstat(assistantBundlePath);
		expect(info.isFile()).toBe(true);
		const content = await readFile(assistantBundlePath);
		expect([...content.subarray(0, 4)]).not.toEqual([0x7f, 0x45, 0x4c, 0x46]);
		expect(content.toString("utf8")).toContain("pixie_assistant");
	} finally {
		await rm(assistantBundlePath, { force: true });
	}
});
