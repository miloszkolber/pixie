#!/usr/bin/env bun

/**
 * AUX-35 — Pinned verification environment gate and command wrapper.
 *
 * Source checks are only meaningful under the runtime the repository pins. The
 * workspace requires Bun `1.4.0` (root `package.json` `packageManager`); Pi's
 * bundle cannot run under `1.3.14`. This gate classifies the current process
 * before evidence is trusted:
 *
 *   - `bun-runtime`: the running Bun must match the pinned `packageManager`.
 *   - `temp-workspace`: task-owned temporary work must land in a bounded,
 *     writable workspace on a filesystem with free space. `/tmp` is often a
 *     small tmpfs and can be full; the wrapper exports `TMPDIR`, `TMP`, `TEMP`
 *     and `GOTMPDIR` into that workspace. Override with `PIXIE_RUNTIME_TMPDIR`.
 *   - `go-toolchain`: Go must be on `PATH` for the controller/shared modules.
 *   - `cgo-toolchain`: a C compiler is required when a wrapped command asks for
 *     `-race` or `CGO_ENABLED=1`.
 *
 * Exit status is part of the contract:
 *   0  supported
 *   1  a real failure (unreadable manifest, invalid flag)
 *   3  environment-blocked (unsupported runtime/toolchain or exhausted storage)
 *
 * `--exec -- <command>` runs the command only when the environment is
 * supported, with the bounded temporary workspace exported. An environment
 * block is reported as `environment-blocked` and never converted into success.
 *
 * Usage:
 *   bun scripts/check-runtime.ts
 *   bun scripts/check-runtime.ts --json
 *   bun scripts/check-runtime.ts --exec -- bun test tests
 */

import { existsSync, mkdirSync, readFileSync, rmSync, statfsSync, writeFileSync } from "node:fs";
import { delimiter, resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dir, "../..");
const workspaceRoot = resolve(
	process.env.PIXIE_RUNTIME_TMPDIR?.trim() || resolve(repositoryRoot, ".tmp-work/runtime"),
);
const minimumFreeBytes = 256 * 1024 * 1024;

const EXIT_SUPPORTED = 0;
const EXIT_FAILED = 1;
const EXIT_ENVIRONMENT_BLOCKED = 3;

type CheckStatus = "ok" | "blocked" | "warning";

interface EnvironmentCheck {
	readonly check: string;
	readonly status: CheckStatus;
	readonly detail: string;
	readonly hint?: string;
}

interface EnvironmentClassification {
	readonly status: "supported" | "environment-blocked" | "failed";
	readonly expectedBun: string;
	readonly actualBun: string;
	readonly checks: readonly EnvironmentCheck[];
}

function fail(message: string): never {
	throw new Error(`check-runtime: ${message}`);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function expectedBunVersion(): string {
	const parsed: unknown = JSON.parse(readFileSync(resolve(repositoryRoot, "package.json"), "utf8"));
	if (!isRecord(parsed) || typeof parsed.packageManager !== "string") {
		fail("root package.json has no packageManager field");
	}
	const match = /^bun@(\d+\.\d+\.\d+(?:[-+][\w.]+)?)$/.exec(parsed.packageManager);
	if (match?.[1] === undefined) {
		fail(`packageManager ${JSON.stringify(parsed.packageManager)} is not an exact bun@X.Y.Z pin`);
	}
	return match[1];
}

function findExecutable(name: string): string | null {
	for (const directory of (process.env.PATH ?? "").split(delimiter)) {
		if (directory.trim() === "") continue;
		const candidate = resolve(directory, name);
		if (existsSync(candidate)) return candidate;
	}
	return null;
}

function probeTool(command: string, args: readonly string[]): { ok: boolean; detail: string } {
	const executable = findExecutable(command);
	if (executable === null) return { ok: false, detail: `${command} not found on PATH` };
	const result = Bun.spawnSync([executable, ...args], { stdout: "pipe", stderr: "pipe" });
	if (result.exitCode !== 0) {
		return { ok: false, detail: `${command} ${args.join(" ")} exited ${result.exitCode}` };
	}
	const firstLine = result.stdout.toString().trim().split("\n")[0] ?? "";
	return { ok: true, detail: firstLine };
}

function checkBunRuntime(): EnvironmentCheck {
	const expected = expectedBunVersion();
	const actual = Bun.version;
	if (actual === expected) {
		return { check: "bun-runtime", status: "ok", detail: `Bun ${actual} matches the pin` };
	}
	return {
		check: "bun-runtime",
		status: "blocked",
		detail: `running Bun ${actual}, repository pins ${expected}`,
		hint: `Run under the pinned runtime, e.g. \`bun x bun@${expected} run <script>\` or install Bun ${expected}.`,
	};
}

function checkTempWorkspace(): EnvironmentCheck {
	try {
		mkdirSync(workspaceRoot, { recursive: true });
	} catch (error) {
		return {
			check: "temp-workspace",
			status: "blocked",
			detail: `cannot create ${workspaceRoot}: ${errorMessage(error)}`,
			hint: "Set PIXIE_RUNTIME_TMPDIR to a writable directory on a filesystem with free space.",
		};
	}
	const probe = resolve(workspaceRoot, ".probe");
	try {
		writeFileSync(probe, "probe");
		rmSync(probe, { force: true });
	} catch (error) {
		return {
			check: "temp-workspace",
			status: "blocked",
			detail: `workspace is not writable: ${errorMessage(error)}`,
			hint: "Set PIXIE_RUNTIME_TMPDIR to a writable directory.",
		};
	}
	let freeBytes: number;
	try {
		const stats = statfsSync(workspaceRoot);
		freeBytes = Number(stats.bavail) * Number(stats.bsize);
	} catch (error) {
		return {
			check: "temp-workspace",
			status: "warning",
			detail: `workspace is writable; free space unknown: ${errorMessage(error)}`,
		};
	}
	if (freeBytes < minimumFreeBytes) {
		return {
			check: "temp-workspace",
			status: "blocked",
			detail: `${workspaceRoot} has ${Math.floor(freeBytes / 1024 / 1024)} MiB free`,
			hint: "Point PIXIE_RUNTIME_TMPDIR at a filesystem with at least 256 MiB free.",
		};
	}
	return {
		check: "temp-workspace",
		status: "ok",
		detail: `${workspaceRoot} writable with ${Math.floor(freeBytes / 1024 / 1024)} MiB free`,
	};
}

function checkGoToolchain(): EnvironmentCheck {
	const result = probeTool("go", ["version"]);
	if (!result.ok) {
		return {
			check: "go-toolchain",
			status: "blocked",
			detail: result.detail,
			hint: "Install the Go toolchain pinned by web/go.mod before running controller or shared checks.",
		};
	}
	return { check: "go-toolchain", status: "ok", detail: result.detail };
}

function needsCgo(args: readonly string[]): boolean {
	if (args.includes("-race")) return true;
	const cgo = (process.env.CGO_ENABLED ?? "").toLowerCase();
	return cgo === "1" || cgo === "true";
}

function checkCgoToolchain(args: readonly string[]): EnvironmentCheck {
	if (!needsCgo(args)) {
		return { check: "cgo-toolchain", status: "ok", detail: "CGO not required for this invocation" };
	}
	for (const compiler of ["cc", "gcc", "clang"]) {
		const result = probeTool(compiler, ["--version"]);
		if (result.ok) return { check: "cgo-toolchain", status: "ok", detail: result.detail };
	}
	return {
		check: "cgo-toolchain",
		status: "blocked",
		detail: "no C compiler (cc/gcc/clang) found on PATH",
		hint: "Install a C toolchain or run without -race with CGO_ENABLED=0.",
	};
}

function classify(args: readonly string[]): EnvironmentClassification {
	const expectedBun = expectedBunVersion();
	const checks = [
		checkBunRuntime(),
		checkTempWorkspace(),
		checkGoToolchain(),
		checkCgoToolchain(args),
	];
	const blocked = checks.some((check) => check.status === "blocked");
	return {
		status: blocked ? "environment-blocked" : "supported",
		expectedBun,
		actualBun: Bun.version,
		checks,
	};
}

function formatClassification(classification: EnvironmentClassification): string {
	const lines = [`check-runtime: ${classification.status} (Bun ${classification.actualBun})`];
	for (const check of classification.checks) {
		lines.push(`  [${check.status}] ${check.check}: ${check.detail}`);
		if (check.hint !== undefined) lines.push(`      ${check.hint}`);
	}
	return lines.join("\n");
}

function classifyExitCode(classification: EnvironmentClassification): number {
	return classification.status === "supported" ? EXIT_SUPPORTED : EXIT_ENVIRONMENT_BLOCKED;
}

async function execWrapper(command: readonly string[]): Promise<number> {
	if (command.length === 0) {
		console.error("check-runtime: --exec requires a command after `--`");
		return EXIT_FAILED;
	}
	const classification = classify(command);
	if (classification.status !== "supported") {
		console.error(formatClassification(classification));
		console.error(
			"check-runtime: refusing to run; evidence would be environment-blocked, not a pass.",
		);
		return EXIT_ENVIRONMENT_BLOCKED;
	}
	const environment = {
		...process.env,
		TMPDIR: workspaceRoot,
		TMP: workspaceRoot,
		TEMP: workspaceRoot,
		GOTMPDIR: workspaceRoot,
	};
	const processHandle = Bun.spawn([...command], {
		cwd: process.cwd(),
		env: environment,
		stdin: "inherit",
		stdout: "inherit",
		stderr: "inherit",
	});
	return await processHandle.exited;
}

async function main(): Promise<number> {
	const args = process.argv.slice(2);
	if (args[0] === "--exec") {
		if (args[1] !== "--") {
			console.error("check-runtime: expected `--exec -- <command>`");
			return EXIT_FAILED;
		}
		return await execWrapper(args.slice(2));
	}
	const classification = classify(args);
	if (args.includes("--json")) {
		console.log(JSON.stringify(classification, null, "\t"));
	} else {
		const output = formatClassification(classification);
		if (classification.status === "supported") console.log(output);
		else console.error(output);
	}
	return classifyExitCode(classification);
}

if (import.meta.main) {
	try {
		process.exit(await main());
	} catch (error) {
		console.error(`check-runtime: FAILED — ${errorMessage(error)}`);
		process.exit(EXIT_FAILED);
	}
}
