import { expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "../../..");
const patchPath = join(
	root,
	"agent/extensions/local-patches/@earendil-works%2Fpi-coding-agent@0.85.1.patch",
);

async function run(args: string[], cwd: string) {
	const child = Bun.spawn(args, { cwd, stdout: "pipe", stderr: "pipe" });
	const [stdout, stderr, code] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	return { code, output: `${stdout}\n${stderr}` };
}

// The SDK export patch must travel into distributable installs: llama loads
// its built-in factory through the public ./extensions subpath, which only
// exists once the patch is applied. Without it the import degrades instead
// of breaking installs, and a requested --llama profile fails loudly.
test("bundled npm postinstall patch matches the workspace SDK export patch", async () => {
	const assistant = JSON.parse(await readFile(join(root, "assistant/package.json"), "utf8"));
	expect(assistant.scripts.postinstall).toBe("node scripts/apply-patches.mjs");
	expect(assistant.files).toContain("scripts");
	expect(assistant.files).toContain("patches");
	const workspace = await readFile(patchPath, "utf8");
	const bundled = await readFile(
		join(root, "assistant/patches/pi-coding-agent-0.85.1-extensions-export.patch"),
		"utf8",
	);
	// The patch touches only the package export map, so the bundled copy is
	// the workspace patch verbatim.
	expect(bundled.trim()).toBe(workspace.trim());
});

test("SDK export patch applies cleanly to the pinned dependency version", async () => {
	const workspace = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
	const assistant = JSON.parse(await readFile(join(root, "assistant/package.json"), "utf8"));
	expect(workspace.patchedDependencies["@earendil-works/pi-coding-agent@0.85.1"]).toBe(
		"agent/extensions/local-patches/@earendil-works%2Fpi-coding-agent@0.85.1.patch",
	);
	expect(assistant.dependencies["@earendil-works/pi-coding-agent"]).toBe("0.85.1");
	const installedManifest = JSON.parse(
		await readFile(join(root, "node_modules/@earendil-works/pi-coding-agent/package.json"), "utf8"),
	);
	expect(installedManifest.version).toBe("0.85.1");
	expect(installedManifest.exports["./extensions"]?.import).toBe("./dist/extensions/index.js");
	const work = await mkdtemp(join(tmpdir(), "pixie-sdk-patch-"));
	try {
		await cp(
			join(root, "node_modules/@earendil-works/pi-coding-agent/package.json"),
			join(work, "package.json"),
		);
		// Reverse applies: the installed copy matches the patch post-image.
		expect((await run(["git", "apply", "--check", "--reverse", patchPath], work)).code).toBe(0);
		expect((await run(["git", "apply", "--reverse", patchPath], work)).code).toBe(0);
		const pristine = await readFile(join(work, "package.json"), "utf8");
		expect(pristine).not.toContain('"./extensions"');
		// Forward applies to the pristine export map: a fresh install of the
		// pinned version accepts the patch without conflicts.
		expect((await run(["git", "apply", "--check", patchPath], work)).code).toBe(0);
		expect((await run(["git", "apply", patchPath], work)).code).toBe(0);
		const patched = await readFile(join(work, "package.json"), "utf8");
		expect(JSON.parse(patched).exports["./extensions"]?.import).toBe("./dist/extensions/index.js");
	} finally {
		await rm(work, { recursive: true, force: true });
	}
});
