// Postinstall: apply the bundled upstreamable SDK patch to installed copies.
// The repository workspace applies it through root patchedDependencies;
// standalone npm/tarball installs have no workspace, so the published
// package carries its own copy under patches/. Idempotent (skips when
// already applied), warns instead of failing when a dependency is missing,
// version-drifted, or unpatchable, so installs never break: unpatched
// installs only lose the public path to the SDK's built-in extension
// barrel. Extension-specific fixes stay out of the assistant entirely; they
// live with the extension setup (the workspace agent layer, or the
// standalone agent distribution).
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const JOBS = [
	{
		dependency: "@earendil-works/pi-coding-agent",
		expectedVersion: "0.85.1",
		patchFile: "pi-coding-agent-0.85.1-extensions-export.patch",
		// The patch only adds the ./extensions subpath to the export map.
		appliedMarker: {
			file: "package.json",
			text: '"./extensions"',
		},
		missingNote: "--llama needs the patched SDK export for its built-in factory",
	},
];

const here = dirname(fileURLToPath(import.meta.url));
const pkgRoot = resolve(here, "..");

function warn(message) {
	console.warn(`[pixie-assistant postinstall] ${message}`);
}

function findTarget(parts) {
	let dir = pkgRoot;
	for (let i = 0; i < 6; i++) {
		const candidate = join(dir, "node_modules", ...parts);
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

function applied(job, target) {
	const file = join(target, job.appliedMarker.file);
	try {
		return readFileSync(file, "utf8").includes(job.appliedMarker.text);
	} catch {
		return false;
	}
}

for (const job of JOBS) {
	const parts = job.dependency.split("/");
	const target = findTarget(parts);
	if (!target) {
		warn(`no installed ${job.dependency} found; skipping (${job.missingNote}).`);
		continue;
	}

	let version = null;
	try {
		version = JSON.parse(readFileSync(join(target, "package.json"), "utf8")).version;
	} catch {
		warn(`cannot read installed ${job.dependency} version; skipping.`);
		continue;
	}
	if (version !== job.expectedVersion) {
		warn(`installed ${job.dependency} is ${version}, patch targets ${job.expectedVersion}; skipping.`);
		continue;
	}

	if (applied(job, target)) continue; // Already applied (e.g. workspace install via patchedDependencies).

	const patchPath = join(pkgRoot, "patches", job.patchFile);
	if (!existsSync(patchPath)) {
		warn(`bundled patch file missing (${job.patchFile}); skipping.`);
		continue;
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
			`could not apply ${job.patchFile} (${error.message?.split("\n")[0] ?? error}); ${job.missingNote}.`,
		);
		continue;
	}
	// git apply can exit 0 while skipping a patch (for example under a
	// git-repository home directory, where patch paths resolve against the
	// repository root), so verify the applied marker after the fact.
	if (!applied(job, target))
		warn(`${job.patchFile} exited cleanly but the marker is absent; ${job.missingNote}.`);
}
