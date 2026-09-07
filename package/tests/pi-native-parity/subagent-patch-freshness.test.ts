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

// The subagent child-launch patch is workspace and operator-setup layer, not
// assistant surface: the published assistant stays extension-agnostic. The
// workspace still needs the patch for real-child verification, so this test
// pins the dependency version and proves the patch applies cleanly to a
// pristine copy of that exact installed version, in both directions.
test("subagent child-launch patch applies cleanly to the pinned dependency version", async () => {
	const workspace = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
	expect(workspace.devDependencies["@mjakl/pi-subagent"]).toBe("3.0.1");
	expect(workspace.patchedDependencies["@mjakl/pi-subagent@3.0.1"]).toBe(
		"agent/extensions/local-patches/@mjakl%2Fpi-subagent@3.0.1.patch",
	);
	const assistant = JSON.parse(await readFile(join(root, "assistant/package.json"), "utf8"));
	expect(Object.keys(assistant.dependencies ?? {})).not.toContain("@mjakl/pi-subagent");
	expect(Object.keys(assistant.devDependencies ?? {})).not.toContain("@mjakl/pi-subagent");
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
		await cp(installed, join(runnerPatchDir, "runner.ts"));
		// Reverse applies: the installed copy matches the patch post-image.
		expect((await run(["git", "apply", "--check", "--reverse", runnerPatch], runnerPatchDir)).code).toBe(0);
		expect((await run(["git", "apply", "--reverse", runnerPatch], runnerPatchDir)).code).toBe(0);
		const pristine = await readFile(join(runnerPatchDir, "runner.ts"), "utf8");
		expect(pristine).not.toContain("rpc-entry");
		// Forward applies to the pristine runner: a fresh install of the
		// pinned version accepts the patch without conflicts.
		expect((await run(["git", "apply", "--check", runnerPatch], runnerPatchDir)).code).toBe(0);
		expect((await run(["git", "apply", runnerPatch], runnerPatchDir)).code).toBe(0);
		expect(await readFile(join(runnerPatchDir, "runner.ts"), "utf8")).toContain("rpc-entry");
	} finally {
		await rm(runnerPatchDir, { recursive: true, force: true });
	}
});
