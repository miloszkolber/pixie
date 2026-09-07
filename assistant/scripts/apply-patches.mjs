// Postinstall: apply the bundled Bun child-launch patch to the installed
// @mjakl/pi-subagent copy. The repository workspace applies the same patch
// through root patchedDependencies; standalone npm/tarball installs have no
// workspace, so the published package carries its own copy under patches/.
// Idempotent (skips when already applied), warns instead of failing when the
// dependency is missing, version-drifted, or unpatchable, so installs never
// break: unpatched installs only lose Bun child subagent launches.
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const PATCH_FILE = "pi-subagent-3.0.1-bun-rpc-entry.patch";
const EXPECTED_VERSION = "3.0.1";
const APPLIED_MARKER = "process.execPath,\n      prefixArgs: [fileURLToPath(import.meta.resolve(";

const here = dirname(fileURLToPath(import.meta.url));
const pkgRoot = resolve(here, "..");

function warn(message) {
	console.warn(`[pixie-assistant postinstall] ${message}`);
}

function findTarget() {
	let dir = pkgRoot;
	for (let i = 0; i < 6; i++) {
		const candidate = join(dir, "node_modules", "@mjakl", "pi-subagent");
		if (existsSync(join(candidate, "package.json"))) return candidate;
		const parent = dirname(dir);
		if (parent === dir) return null;
		dir = parent;
	}
	return null;
}

function run(cmd, args, cwd) {
	execFileSync(cmd, args, { cwd, stdio: "pipe", timeout: 30000 });
}

const target = findTarget();
if (!target) {
	warn(
		"no installed @mjakl/pi-subagent found; skipping (Bun child subagent launches need the patched package).",
	);
	process.exit(0);
}

let version = null;
try {
	version = JSON.parse(readFileSync(join(target, "package.json"), "utf8")).version;
} catch {
	warn("cannot read installed @mjakl/pi-subagent version; skipping.");
	process.exit(0);
}
if (version !== EXPECTED_VERSION) {
	warn(`installed @mjakl/pi-subagent is ${version}, patch targets ${EXPECTED_VERSION}; skipping.`);
	process.exit(0);
}

const runner = join(target, "runner.ts");
if (existsSync(runner) && readFileSync(runner, "utf8").includes(APPLIED_MARKER)) {
	process.exit(0); // Already applied (e.g. workspace install via patchedDependencies).
}

const patchPath = join(pkgRoot, "patches", PATCH_FILE);
if (!existsSync(patchPath)) {
	warn("bundled patch file missing; skipping.");
	process.exit(0);
}

try {
	try {
		run("git", ["apply", "--check", patchPath], target);
		run("git", ["apply", patchPath], target);
	} catch {
		run("patch", ["-p1", "--forward", "-i", patchPath], target);
	}
} catch (error) {
	warn(
		`could not apply the Bun child-launch patch (${error.message?.split("\n")[0] ?? error}); Bun child subagent launches may fail.`,
	);
	process.exit(0);
}
