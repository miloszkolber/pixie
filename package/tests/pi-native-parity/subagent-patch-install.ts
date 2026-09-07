// Exercise real child processes from a clean production-only frozen install.
import { copyFile, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const root = resolve(import.meta.dir, "../../..");
const temporary = await mkdtemp(join(tmpdir(), "pixie-subagent-patch-install-"));
try {
	const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
	for (const path of [
		"package.json",
		"bun.lock",
		...manifest.workspaces.packages.map((path: string) => `${path}/package.json`),
		...Object.values(manifest.patchedDependencies),
		"package/tests/pi-native-parity/subagent-child.test.ts",
		"package/tests/pi-native-parity/subagent-child-provider.ts",
	] as string[]) {
		await mkdir(dirname(join(temporary, path)), { recursive: true });
		await copyFile(join(root, path), join(temporary, path));
	}
	async function run(args: string[]) {
		const child = Bun.spawn(args, { cwd: temporary, stdout: "pipe", stderr: "pipe" });
		const [stdout, stderr, code] = await Promise.all([
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
			child.exited,
		]);
		return { code, output: `${stdout}\n${stderr}` };
	}
	const installed = await run([
		process.execPath,
		"install",
		"--frozen-lockfile",
		"--ignore-scripts",
		"--production",
		"--filter",
		"@pixie_ai/pixie-assistant",
	]);
	if (installed.code !== 0) throw new Error(installed.output);
	const testArgs = [
		process.execPath,
		"test",
		"package/tests/pi-native-parity/subagent-child.test.ts",
	];
	const patched = await run(testArgs);
	if (patched.code !== 0) throw new Error(patched.output);
	console.log(patched.output.trim());
	console.log(JSON.stringify({ bun: Bun.version, freshFrozenInstall: true, realChildren: true }));
} finally {
	await rm(temporary, { recursive: true, force: true });
}
