// Prove the checked-in patch applies to a fresh locked install, not a manually
// modified node_modules. Run using the pinned Bun from the repository root.
import { copyFile, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const root = resolve(import.meta.dir, "../../..");
const temporary = await mkdtemp(join(tmpdir(), "pixie-mcp-patch-install-"));
try {
	const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
	const files = [
		"package.json",
		"bun.lock",
		...manifest.workspaces.packages.map((path: string) => `${path}/package.json`),
		...(Object.values(manifest.patchedDependencies) as string[]),
	];
	for (const path of files) {
		await mkdir(dirname(join(temporary, path)), { recursive: true });
		await copyFile(join(root, path), join(temporary, path));
	}
	const install = Bun.spawn(
		[
			process.execPath,
			"install",
			"--frozen-lockfile",
			"--ignore-scripts",
			"--production",
			"--filter",
			"@pixie/pi-host",
		],
		{ cwd: temporary, stdout: "pipe", stderr: "pipe" },
	);
	const [stdout, stderr, code] = await Promise.all([
		new Response(install.stdout).text(),
		new Response(install.stderr).text(),
		install.exited,
	]);
	if (code !== 0) throw new Error(`Fresh frozen install failed (${code}): ${stdout}\n${stderr}`);
	const probe = Bun.spawn(
		[
			process.execPath,
			"-e",
			`import {createRequire} from 'node:module'; const r=createRequire(${JSON.stringify(join(temporary, "pi/host/package.json"))}); const m=r('pi-mcp-adapter'); const apis=['readMcpResourceV1','callMcpAppToolV1']; if(apis.some(api=>typeof m[api]!=='function')) throw new Error('missing host App v1 API'); console.log(JSON.stringify({bun:Bun.version,apis,freshFrozenInstall:true}));`,
		],
		{ cwd: temporary, stdout: "pipe", stderr: "pipe" },
	);
	const [probeOut, probeErr, probeCode] = await Promise.all([
		new Response(probe.stdout).text(),
		new Response(probe.stderr).text(),
		probe.exited,
	]);
	if (probeCode !== 0) throw new Error(`Patched API probe failed: ${probeErr}`);
	console.log(probeOut.trim());
} finally {
	await rm(temporary, { recursive: true, force: true });
}
