import { expect, test } from "bun:test";
import { resolve } from "node:path";

const packageRoot = resolve(import.meta.dir, "../..");

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
