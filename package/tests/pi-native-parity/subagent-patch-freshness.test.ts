import { expect, test } from "bun:test";
import { cp, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "../../..");
const patchPath = join(root, "agent/extensions/local-patches/@mjakl%2Fpi-subagent@3.0.1.patch");

async function run(args: string[], cwd: string) {
	const child = Bun.spawn(args, { cwd, stdout: "pipe", stderr: "pipe" });
	const [stdout, stderr, code] = await Promise.all([
		new Response(child.stdout).text(),
		new Response(child.stderr).text(),
		child.exited,
	]);
	return { code, output: `${stdout}\n${stderr}` };
}

// The workspace patch for Bun child launches must travel into distributable
// installs: a root patchedDependencies entry does not apply itself inside an
// npm dependency tree. Without network access this test pins the dependency
// version and proves the patch still applies cleanly to a pristine copy of
// that exact installed version, in both directions.
test("subagent child-launch patch applies cleanly to the pinned dependency version", async () => {
	const workspace = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
	const assistant = JSON.parse(await readFile(join(root, "assistant/package.json"), "utf8"));
	expect(workspace.patchedDependencies["@mjakl/pi-subagent@3.0.1"]).toBe(
		"agent/extensions/local-patches/@mjakl%2Fpi-subagent@3.0.1.patch",
	);
	expect(assistant.devDependencies["@mjakl/pi-subagent"]).toBe("3.0.1");
	const patch = await readFile(patchPath, "utf8");
	// Only the runner hunk carries the fix; the .bun-tag marker hunk is a
	// Bun install artifact with asymmetric paths that git cannot apply.
	const marker = "diff --git a/runner.ts";
	const index = patch.indexOf(marker);
	if (index < 0) throw new Error("Child-launch runner hunk missing from the patch");
	const runnerPatchDir = await mkdtemp(join(tmpdir(), "pixie-subagent-patch-"));
	const runnerPatch = join(runnerPatchDir, "runner.patch");
	await Bun.write(runnerPatch, `${patch.slice(index)}\n`);
	try {
		const installed = join(root, "node_modules/@mjakl/pi-subagent/runner.ts");
		const source = await readFile(installed, "utf8");
		expect(source).toContain("process.execPath");
		expect(source).toContain("rpc-entry");
		const installedManifest = JSON.parse(
			await readFile(join(root, "node_modules/@mjakl/pi-subagent/package.json"), "utf8"),
		);
		expect(installedManifest.version).toBe("3.0.1");
		const work = await mkdtemp(join(tmpdir(), "pixie-subagent-pristine-"));
		try {
			await cp(join(root, "node_modules/@mjakl/pi-subagent"), work, { recursive: true });
			// Reverse applies: the installed copy matches the patch post-image.
			expect((await run(["git", "apply", "--check", "--reverse", runnerPatch], work)).code).toBe(0);
			expect((await run(["git", "apply", "--reverse", runnerPatch], work)).code).toBe(0);
			// Forward applies to the pristine copy: a fresh install of the
			// pinned version accepts the patch without conflicts.
			const pristine = await readFile(join(work, "runner.ts"), "utf8");
			expect(pristine).not.toContain("rpc-entry");
			expect((await run(["git", "apply", "--check", runnerPatch], work)).code).toBe(0);
			expect((await run(["git", "apply", runnerPatch], work)).code).toBe(0);
			const patched = await readFile(join(work, "runner.ts"), "utf8");
			expect(patched).toContain("rpc-entry");
		} finally {
			await rm(work, { recursive: true, force: true });
		}
	} finally {
		await rm(runnerPatchDir, { recursive: true, force: true });
	}
});
