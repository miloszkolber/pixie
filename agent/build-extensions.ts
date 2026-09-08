// Builds the pinned optional-extension tree into agent/dist/extensions: a
// standalone install root (package.json, lockfile, node_modules) produced
// from the exact pins in the root manifest. The subagent child-launch patch
// is applied during the build, so the produced tree is ready to use as-is:
// point native Pi settings at agent/dist/extensions/node_modules/<package>.
//
// The extension packages are ordinary Pi resources loaded from disk; they
// are never embedded into the pi binary. A target machine needs no bun
// or npm to use the produced tree.
import { copyFile, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const output = join(root, "agent", "dist", "extensions");

const EXTENSION_PINS = [
	"@mjakl/pi-subagent",
	"@juicesharp/rpiv-todo",
	"@juicesharp/rpiv-web-tools",
	"@juicesharp/rpiv-ask-user-question",
	"pi-mcp-adapter",
] as const;

const PATCHED_PACKAGE = "@mjakl/pi-subagent";
const PATCH_MARKER = "rpc-entry";
const PATCHED_RUNNER = join(
	root,
	"node_modules",
	"@mjakl",
	"pi-subagent",
	"runner.ts",
);

const workspace = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
const pins: Record<string, string> = {};
for (const name of EXTENSION_PINS) {
	const pin = workspace.devDependencies?.[name];
	if (!pin) throw new Error(`Extension ${name} is not pinned in the root manifest`);
	pins[name] = pin;
}

await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true, mode: 0o700 });
await writeFile(
	join(output, "package.json"),
	`${JSON.stringify({ name: "pixie-agent-extensions", private: true, dependencies: pins }, null, "\t")}\n`,
);

const install = Bun.spawn(["bun", "install", "--production"], {
	cwd: output,
	stdout: "inherit",
	stderr: "inherit",
});
if ((await install.exited) !== 0) throw new Error("Extension tree install failed");

// The workspace tree carries the same pinned package with the subagent
// child-launch patch already applied (root patchedDependencies); the fresh
// install does not, and applying a patch with git inside this repository
// resolves paths against the repository root. Copy the verified post-image
// instead and confirm the marker afterwards.
const freshRunner = join(output, "node_modules", "@mjakl", "pi-subagent", "runner.ts");
const fresh = await readFile(freshRunner, "utf8");
if (fresh.includes(PATCH_MARKER)) throw new Error("Fresh install unexpectedly carries the patch marker");
const patched = await readFile(PATCHED_RUNNER, "utf8");
if (!patched.includes(PATCH_MARKER))
	throw new Error("Workspace subagent runner is unpatched; run bun install at the repository root");
await copyFile(PATCHED_RUNNER, freshRunner);
if (!(await readFile(freshRunner, "utf8")).includes(PATCH_MARKER))
	throw new Error("Patch marker missing after copy");

for (const name of EXTENSION_PINS) {
	const manifest = JSON.parse(
		await readFile(join(output, "node_modules", ...name.split("/"), "package.json"), "utf8"),
	);
	const expected = pins[name];
	if (manifest.version !== expected)
		throw new Error(`Extension ${name} resolved to ${manifest.version}, expected ${expected}`);
}

console.log(`extension tree ready: ${output} (${EXTENSION_PINS.length} pinned packages, subagent patched)`);
