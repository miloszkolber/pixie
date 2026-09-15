import { describe, expect, test } from "bun:test";
import { randomBytes } from "node:crypto";
import {
	chmod,
	mkdir,
	mkdtemp,
	readFile,
	realpath,
	rm,
	symlink,
	writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import {
	encodeProbeReport,
	loadVerifiedPiPublicApi,
	MAX_PROBE_REPORT_BYTES,
	probePiSdk,
	REQUIRED_PUBLIC_SYMBOLS,
	resolvePiPackagePath,
	verifyPiPackage,
} from "../src/probe.ts";

const PACKAGE_NAME = "@earendil-works/pi-coding-agent";
const ASSISTANT_DIRECTORY = resolve(import.meta.dir, "..");
const COMPILED_PROBE_PATH = join(ASSISTANT_DIRECTORY, "pixie-pi-sdk-probe.bun-build");
const FIXTURE_RUNTIME = process.execPath;
const INSTALLED_PI_PACKAGE_PATH = resolve(
	import.meta.dir,
	"../../node_modules/@earendil-works/pi-coding-agent",
);

interface FakePiPackageOptions {
	readonly name?: string;
	readonly version?: string;
	readonly entrypoint?: string;
	readonly exports?: unknown;
	readonly omitExports?: boolean;
	readonly module?: string;
	readonly main?: string;
	readonly symbols?: readonly string[];
	readonly markerPath?: string;
	readonly importPreamble?: string;
}

interface FakePiPackage {
	readonly root: string;
	readonly packageDir: string;
}

function fakePublicApi(
	symbols: readonly string[],
	markerPath?: string,
	importPreamble = "",
): string {
	const marker = markerPath
		? `import { writeFileSync } from "node:fs";\nwriteFileSync(${JSON.stringify(markerPath)}, "external package loaded");\n`
		: "";
	return `${marker}${importPreamble}${symbols.map((symbol) => `export const ${symbol} = () => {};`).join("\n")}\n`;
}

function isContainedBy(root: string, candidate: string): boolean {
	const remainder = relative(root, candidate);
	return (
		remainder === "" ||
		(remainder !== ".." && !remainder.startsWith(`..${sep}`) && !isAbsolute(remainder))
	);
}

async function canExecuteIn(directory: string): Promise<boolean> {
	const executable = join(directory, "pixie-pi-sdk-probe-execute-check");
	try {
		await writeFile(executable, "#!/bin/sh\nexit 0\n", { mode: 0o700 });
		await chmod(executable, 0o700);
		return Bun.spawnSync({ cmd: [executable], stdout: "pipe", stderr: "pipe" }).exitCode === 0;
	} catch {
		return false;
	} finally {
		await rm(executable, { force: true });
	}
}

async function createExternalTemporaryDirectory(prefix: string): Promise<string> {
	const repositoryRoot = await realpath(resolve(import.meta.dir, "../.."));
	const attemptedParents = new Set(["/tmp", "/var/tmp", "/dev/shm", tmpdir()]);
	for (const parent of attemptedParents) {
		const canonicalParent = await realpath(parent).catch(() => undefined);
		if (!canonicalParent || isContainedBy(repositoryRoot, canonicalParent)) continue;
		try {
			const directory = await mkdtemp(join(canonicalParent, prefix));
			const canonicalDirectory = await realpath(directory);
			if (
				!isContainedBy(repositoryRoot, canonicalDirectory) &&
				(await canExecuteIn(canonicalDirectory))
			)
				return canonicalDirectory;
			await rm(directory, { recursive: true, force: true });
		} catch {
			// Try another OS temporary directory rather than using a checkout path.
		}
	}
	throw new Error("no writable executable temporary directory outside the repository is available");
}

async function createFakePiPackage(
	options: FakePiPackageOptions = {},
	temporaryParent = tmpdir(),
	removeParentOnFailure = false,
): Promise<FakePiPackage> {
	const root = await mkdtemp(join(temporaryParent, "pixie-pi-sdk-probe-"));
	try {
		const packageDir = join(root, "external-pi");
		const entrypoint = options.entrypoint ?? "./index.mjs";
		await mkdir(packageDir);
		await writeFile(
			join(packageDir, "package.json"),
			JSON.stringify({
				name: options.name ?? PACKAGE_NAME,
				version: options.version ?? "9.8.7",
				type: "module",
				...(!options.omitExports
					? { exports: options.exports ?? { ".": { import: entrypoint } } }
					: {}),
				...(options.module ? { module: options.module } : {}),
				...(options.main ? { main: options.main } : {}),
			}),
		);
		if (entrypoint.startsWith("./"))
			await writeFile(
				join(packageDir, entrypoint.slice(2)),
				fakePublicApi(
					options.symbols ?? REQUIRED_PUBLIC_SYMBOLS,
					options.markerPath,
					options.importPreamble,
				),
			);
		return { root, packageDir };
	} catch (error) {
		await rm(root, { recursive: true, force: true }).catch(() => undefined);
		if (removeParentOnFailure) await rm(temporaryParent, { recursive: true, force: true });
		throw error;
	}
}

async function createExternalFakePiPackage(
	temporaryParent: string,
	options: FakePiPackageOptions = {},
): Promise<FakePiPackage> {
	return createFakePiPackage(options, temporaryParent, true);
}

async function removeFixture(fixture: FakePiPackage): Promise<void> {
	await rm(fixture.root, { recursive: true, force: true });
}

function outputText(output: Uint8Array): string {
	return new TextDecoder().decode(output);
}

interface FixtureDescendantIdentity {
	readonly pid: number;
	readonly startTime: string;
	readonly token: string;
}

function fixtureDescendantProgram(recordPath: string, token: string): string {
	return `import { readFileSync, writeFileSync } from "node:fs";
const stat = readFileSync("/proc/self/stat", "utf8");
const fields = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\\s+/);
const startTime = fields[19];
if (!startTime) process.exit(2);
writeFileSync(${JSON.stringify(recordPath)}, JSON.stringify({ pid: process.pid, startTime, token: ${JSON.stringify(token)} }), { mode: 0o600 });
setTimeout(() => process.exit(0), 15_000);
setInterval(() => {}, 1_000);
// pixie-fixture-descendant:${token}
`;
}

function descendantImportPreamble(
	recordPath: string,
	token: string,
	completion: string,
	unref = true,
): string {
	return `import { existsSync } from "node:fs";
const descendant = Bun.spawn([${JSON.stringify(FIXTURE_RUNTIME)}, "-e", ${JSON.stringify(fixtureDescendantProgram(recordPath, token))}], { stdin: "ignore", stdout: "ignore", stderr: "ignore" });
for (let attempt = 0; attempt < 100 && !existsSync(${JSON.stringify(recordPath)}); attempt += 1) await Bun.sleep(10);
if (!existsSync(${JSON.stringify(recordPath)})) throw new Error("fixture descendant did not record its identity");
${unref ? "descendant.unref();" : ""}
${completion}
`;
}

async function recordedFixtureDescendant(
	path: string,
	expectedToken: string,
): Promise<FixtureDescendantIdentity | undefined> {
	const contents = await readFile(path, "utf8").catch(() => undefined);
	if (!contents) return undefined;
	let record: unknown;
	try {
		record = JSON.parse(contents);
	} catch {
		return undefined;
	}
	if (
		!record ||
		typeof record !== "object" ||
		!Number.isSafeInteger((record as { pid?: unknown }).pid) ||
		(record as { pid: number }).pid <= 0 ||
		typeof (record as { startTime?: unknown }).startTime !== "string" ||
		!/^\d+$/.test((record as { startTime: string }).startTime) ||
		(record as { token?: unknown }).token !== expectedToken
	)
		return undefined;
	return record as FixtureDescendantIdentity;
}

function processMissing(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return false;
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ESRCH") return true;
		throw error;
	}
}

async function fixtureDescendantHasExited(descendant: FixtureDescendantIdentity): Promise<boolean> {
	if (processMissing(descendant.pid)) return true;
	const [stat, commandLine] = await Promise.all([
		readFile(`/proc/${descendant.pid}/stat`, "utf8").catch(() => undefined),
		readFile(`/proc/${descendant.pid}/cmdline`, "utf8").catch(() => undefined),
	]);
	if (!stat || !commandLine) {
		if (processMissing(descendant.pid)) return true;
		throw new Error("could not safely identify fixture descendant");
	}
	const fields = stat
		.slice(stat.lastIndexOf(")") + 2)
		.trim()
		.split(/\s+/);
	const startTime = fields[19];
	return (
		startTime !== descendant.startTime ||
		!commandLine.includes(`pixie-fixture-descendant:${descendant.token}`)
	);
}

async function waitForFixtureDescendantExit(
	descendant: FixtureDescendantIdentity,
	timeoutMs: number,
): Promise<boolean> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		if (await fixtureDescendantHasExited(descendant)) return true;
		const remaining = deadline - Date.now();
		if (remaining <= 0) return false;
		await Bun.sleep(Math.min(25, remaining));
	}
}

async function expectFixtureDescendantCleaned(path: string, token: string): Promise<void> {
	const descendant = await recordedFixtureDescendant(path, token);
	expect(descendant).toBeDefined();
	if (!descendant) throw new Error("fixture descendant did not record a safe identity");
	expect(await waitForFixtureDescendantExit(descendant, 1_000)).toBe(true);
}

describe("external Pi SDK probe", () => {
	test("accepts an explicit external package and reports only its verified identity and symbols", async () => {
		const fixture = await createFakePiPackage();
		try {
			const report = await probePiSdk(fixture.packageDir);
			expect(report).toEqual({
				packageName: PACKAGE_NAME,
				packageVersion: "9.8.7",
				publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
			});
			expect(JSON.parse(encodeProbeReport(report))).toEqual(report);
			expect(() =>
				encodeProbeReport({
					packageName: PACKAGE_NAME,
					packageVersion: "x".repeat(MAX_PROBE_REPORT_BYTES),
					publicSymbols: [],
				}),
			).toThrow("size limit");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("records a public-entrypoint identity that changes with its imported source", async () => {
		const fixture = await createFakePiPackage();
		try {
			const before = await verifyPiPackage(fixture.packageDir);
			await writeFile(
				join(fixture.packageDir, "index.mjs"),
				`${fakePublicApi(REQUIRED_PUBLIC_SYMBOLS)}// changed after verification\n`,
			);
			const after = await verifyPiPackage(fixture.packageDir);
			expect(before.packageVersion).toBe(after.packageVersion);
			expect(before.manifestDigest).toBe(after.manifestDigest);
			expect(before.entryDigest).not.toBe(after.entryDigest);
		} finally {
			await removeFixture(fixture);
		}
	});

	test("uses only --pi-package or PIXIE_PI_PACKAGE and never a lookup fallback", () => {
		expect(resolvePiPackagePath(["--pi-package", "/opt/pi-sdk"], {})).toBe("/opt/pi-sdk");
		expect(resolvePiPackagePath([], { PIXIE_PI_PACKAGE: "/opt/external-pi-sdk" })).toBe(
			"/opt/external-pi-sdk",
		);
		expect(() => resolvePiPackagePath([], {})).toThrow("--pi-package or PIXIE_PI_PACKAGE");
		expect(() => resolvePiPackagePath(["--package", "/opt/pi-sdk"], {})).toThrow(
			"unsupported Pi SDK probe argument",
		);
	});

	test("rejects a relative selected package path", async () => {
		await expect(verifyPiPackage("external-pi")).rejects.toThrow("must be absolute");
	});

	test("rejects a package with the wrong identity", async () => {
		const fixture = await createFakePiPackage({ name: "example-not-pi" });
		try {
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow("is not");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects an entrypoint that escapes the selected package", async () => {
		const fixture = await createFakePiPackage({ entrypoint: "../outside.mjs" });
		try {
			await writeFile(join(fixture.root, "outside.mjs"), fakePublicApi(REQUIRED_PUBLIC_SYMBOLS));
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow("entrypoint escapes");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects an entrypoint symlink that resolves outside the selected package", async () => {
		const fixture = await createFakePiPackage();
		try {
			await writeFile(join(fixture.root, "outside.mjs"), fakePublicApi(REQUIRED_PUBLIC_SYMBOLS));
			await rm(join(fixture.packageDir, "index.mjs"));
			await symlink("../outside.mjs", join(fixture.packageDir, "index.mjs"));
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow("entrypoint escapes");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects a package.json symlink that resolves outside the selected package", async () => {
		const fixture = await createFakePiPackage();
		try {
			await writeFile(join(fixture.root, "outside-package.json"), "{}");
			await rm(join(fixture.packageDir, "package.json"));
			await symlink("../outside-package.json", join(fixture.packageDir, "package.json"));
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow("manifest escapes");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects subpath-only exports instead of falling back to main", async () => {
		const fixture = await createFakePiPackage({
			exports: { "./subpath": "./index.mjs" },
			main: "./index.mjs",
		});
		try {
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow(
				"exposes no public entrypoint",
			);
		} finally {
			await removeFixture(fixture);
		}
	});

	test("uses main as the legacy public entrypoint fallback", async () => {
		const fixture = await createFakePiPackage({ omitExports: true, main: "./index.mjs" });
		try {
			await expect(probePiSdk(fixture.packageDir)).resolves.toEqual({
				packageName: PACKAGE_NAME,
				packageVersion: "9.8.7",
				publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
			});
		} finally {
			await removeFixture(fixture);
		}
	});

	test("uses module as the legacy public entrypoint fallback before main", async () => {
		const fixture = await createFakePiPackage({
			omitExports: true,
			module: "./index.mjs",
			main: "./missing-main.mjs",
		});
		try {
			await expect(probePiSdk(fixture.packageDir)).resolves.toEqual({
				packageName: PACKAGE_NAME,
				packageVersion: "9.8.7",
				publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
			});
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects an external package that lacks a required public symbol", async () => {
		const fixture = await createFakePiPackage({
			symbols: REQUIRED_PUBLIC_SYMBOLS.filter((symbol) => symbol !== "AgentSessionRuntime"),
		});
		try {
			await expect(probePiSdk(fixture.packageDir)).rejects.toThrow("AgentSessionRuntime");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("rejects a package with an invalid version", async () => {
		const fixture = await createFakePiPackage({ version: "0.85" });
		try {
			await expect(verifyPiPackage(fixture.packageDir)).rejects.toThrow("invalid version");
		} finally {
			await removeFixture(fixture);
		}
	});

	test("sanitizes direct public-entrypoint import failures", async () => {
		const marker = "UNTRUSTED_DIRECT_IMPORT_MARKER";
		const escapeSequence = "\u001b[31m";
		const fixture = await createFakePiPackage({
			importPreamble: `throw new Error(${JSON.stringify(`${escapeSequence}${marker}`)});\n`,
		});
		try {
			const verified = await verifyPiPackage(fixture.packageDir);
			const error = await loadVerifiedPiPublicApi(verified).catch((failure: unknown) => failure);
			expect(error).toBeInstanceOf(Error);
			const message = (error as Error).message;
			expect(message).toBe("selected Pi public entrypoint failed to import");
			expect(message).not.toContain(marker);
			expect(message).not.toContain(escapeSequence);
		} finally {
			await removeFixture(fixture);
		}
	});

	test("compiled probe dynamically imports the explicit external package from an isolated fixture CWD", async () => {
		if (process.platform !== "linux") return;
		const root = await createExternalTemporaryDirectory("pixie-pi-sdk-compiled-");
		const markerPath = join(root, "external-imported.txt");
		const fixture = await createExternalFakePiPackage(root, { markerPath });
		const consoleStdoutMarker = "UNTRUSTED_PI_CONSOLE_STDOUT_MARKER";
		const consoleStderrMarker = "UNTRUSTED_PI_CONSOLE_STDERR_MARKER";
		const bunStdoutMarker = "UNTRUSTED_PI_BUN_STDOUT_MARKER";
		const bunStderrMarker = "UNTRUSTED_PI_BUN_STDERR_MARKER";
		const escapeSequence = "\u001b[31m";
		const noisyFixture = await createExternalFakePiPackage(root, {
			importPreamble: `console.log(new Error(${JSON.stringify(`${escapeSequence}${consoleStdoutMarker}`)}));
console.error(new Error(${JSON.stringify(`${escapeSequence}${consoleStderrMarker}`)}));
await Bun.write(Bun.stdout, ${JSON.stringify(`${escapeSequence}${bunStdoutMarker}\n`)});
await Bun.write(Bun.stderr, ${JSON.stringify(`${escapeSequence}${bunStderrMarker}\n`)});
`,
		});
		const exitsBeforeExportsFixture = await createExternalFakePiPackage(root, {
			importPreamble: "process.exit(0);\n",
		});
		const nonPiWorkerFixture = await createExternalFakePiPackage(root, { name: "example-not-pi" });
		const hangingDescendantRecordPath = join(root, "guardian-hanging-descendant.json");
		const hangingDescendantToken = randomBytes(16).toString("hex");
		const hangingDescendantFixture =
			process.platform === "linux"
				? await createExternalFakePiPackage(root, {
						importPreamble: descendantImportPreamble(
							hangingDescendantRecordPath,
							hangingDescendantToken,
							"await new Promise(() => {});",
							false,
						),
					})
				: undefined;
		const normalDescendantRecordPath = join(root, "guardian-normal-descendant.json");
		const normalDescendantToken = randomBytes(16).toString("hex");
		const normalDescendantFixture =
			process.platform === "linux"
				? await createExternalFakePiPackage(root, {
						importPreamble: descendantImportPreamble(
							normalDescendantRecordPath,
							normalDescendantToken,
							"",
						),
					})
				: undefined;
		const failingDescendantRecordPath = join(root, "guardian-failing-descendant.json");
		const failingDescendantToken = randomBytes(16).toString("hex");
		const importMarker = "UNTRUSTED_PI_IMPORT_FAILURE_MARKER";
		const failingFixture = await createExternalFakePiPackage(root, {
			importPreamble: descendantImportPreamble(
				failingDescendantRecordPath,
				failingDescendantToken,
				`console.log(new Error(${JSON.stringify(`${escapeSequence}${importMarker}`)}));
console.error(new Error(${JSON.stringify(`${escapeSequence}${importMarker}`)}));
await Bun.write(Bun.stdout, ${JSON.stringify(`${escapeSequence}${importMarker}\n`)});
await Bun.write(Bun.stderr, ${JSON.stringify(`${escapeSequence}${importMarker}\n`)});
throw new Error(${JSON.stringify(`${escapeSequence}${importMarker}`)});`,
			),
		});
		try {
			await rm(COMPILED_PROBE_PATH, { force: true });
			const build = Bun.spawnSync({
				cmd: [process.execPath, "run", "spike:build"],
				cwd: ASSISTANT_DIRECTORY,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(build.exitCode, outputText(build.stderr)).toBe(0);

			const cwd = join(root, "outside-workspace");
			await mkdir(cwd);
			const repositoryRoot = await realpath(resolve(import.meta.dir, "../.."));
			const canonicalFixturePath = await realpath(fixture.packageDir);
			const canonicalCwd = await realpath(cwd);
			expect(isContainedBy(repositoryRoot, canonicalFixturePath)).toBe(false);
			expect(isContainedBy(repositoryRoot, canonicalCwd)).toBe(false);
			const source = resolve(import.meta.dir, "../src/probe.ts");
			const sourceRun = Bun.spawnSync({
				cmd: [process.execPath, source, "--pi-package", fixture.packageDir],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(sourceRun.exitCode, outputText(sourceRun.stderr)).toBe(0);
			expect(outputText(sourceRun.stdout)).toBe(
				encodeProbeReport({
					packageName: PACKAGE_NAME,
					packageVersion: "9.8.7",
					publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
				}),
			);

			// Pi imports bare transitive dependencies such as chalk. This smoke test
			// must use the published build script, otherwise a missing Bun compile
			// autoload flag can leave synthetic fixtures green while real Pi fails.
			const installedManifest = JSON.parse(
				await readFile(join(INSTALLED_PI_PACKAGE_PATH, "package.json"), "utf8"),
			) as { name: string; version: string };
			expect(installedManifest.name).toBe(PACKAGE_NAME);
			const installedRun = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", INSTALLED_PI_PACKAGE_PATH],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(installedRun.exitCode, outputText(installedRun.stderr)).toBe(0);
			expect(outputText(installedRun.stdout)).toBe(
				encodeProbeReport({
					packageName: installedManifest.name as typeof PACKAGE_NAME,
					packageVersion: installedManifest.version,
					publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
				}),
			);

			const run = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", fixture.packageDir],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(run.exitCode, outputText(run.stderr)).toBe(0);
			const expectedReport = {
				packageName: PACKAGE_NAME,
				packageVersion: "9.8.7",
				publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
			};
			expect(outputText(run.stdout)).toBe(encodeProbeReport(expectedReport));
			expect(await readFile(markerPath, "utf8")).toBe("external package loaded");

			const noisyRun = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", noisyFixture.packageDir],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			const noisyStdout = outputText(noisyRun.stdout);
			const noisyStderr = outputText(noisyRun.stderr);
			expect(noisyRun.exitCode, outputText(noisyRun.stderr)).toBe(0);
			expect(noisyStdout).toBe(encodeProbeReport(expectedReport));
			expect(noisyStderr).toBe("");
			for (const marker of [
				consoleStdoutMarker,
				consoleStderrMarker,
				bunStdoutMarker,
				bunStderrMarker,
				escapeSequence,
			]) {
				expect(noisyStdout).not.toContain(marker);
				expect(noisyStderr).not.toContain(marker);
			}

			const failed = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", "relative-pi-package"],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(failed.exitCode).not.toBe(0);
			expect(outputText(failed.stdout)).toBe("");
			expect(outputText(failed.stderr)).toContain("must be absolute");

			const untrustedWorkerMarkerPath = join(root, "untrusted-worker-marker");
			const untrustedWorker = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--checker-worker-package", nonPiWorkerFixture.packageDir],
				cwd,
				env: {
					...process.env,
					PIXIE_PI_SDK_PROBE_CHECKER: "worker",
					PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_MARKER: untrustedWorkerMarkerPath,
					PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_TOKEN: "a".repeat(43),
				},
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(untrustedWorker.exitCode).not.toBe(0);
			expect(outputText(untrustedWorker.stdout)).toBe("");
			expect(outputText(untrustedWorker.stderr)).toBe("");
			expect(
				await readFile(untrustedWorkerMarkerPath, "utf8").catch(() => undefined),
			).toBeUndefined();

			const untrustedGuardianMarkerPaths = [
				join(root, "untrusted-guardian-success-marker"),
				join(root, "untrusted-guardian-result-marker"),
				join(root, "untrusted-guardian-teardown-marker"),
			];
			const untrustedGuardian = Bun.spawnSync({
				cmd: [
					COMPILED_PROBE_PATH,
					"--checker-package",
					nonPiWorkerFixture.packageDir,
					"--checker-success-marker",
					untrustedGuardianMarkerPaths[0],
					"--checker-success-token",
					"a".repeat(43),
					"--checker-result-marker",
					untrustedGuardianMarkerPaths[1],
					"--checker-result-token",
					"b".repeat(43),
					"--checker-teardown-marker",
					untrustedGuardianMarkerPaths[2],
					"--checker-teardown-token",
					"c".repeat(43),
					"--checker-release-token",
					"d".repeat(43),
				],
				cwd,
				env: { ...process.env, PIXIE_PI_SDK_PROBE_CHECKER: "guardian" },
				stdin: "ignore",
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(untrustedGuardian.exitCode).not.toBe(0);
			expect(outputText(untrustedGuardian.stdout)).toBe("");
			expect(outputText(untrustedGuardian.stderr)).toBe("");
			for (const path of untrustedGuardianMarkerPaths)
				expect(await readFile(path, "utf8").catch(() => undefined)).toBeUndefined();

			const importFailure = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", failingFixture.packageDir],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(importFailure.exitCode).not.toBe(0);
			expect(outputText(importFailure.stdout)).toBe("");
			expect(outputText(importFailure.stderr)).toBe(
				"pixie-pi-sdk-probe: selected Pi public entrypoint self-check failed\n",
			);
			expect(outputText(importFailure.stdout)).not.toContain(importMarker);
			expect(outputText(importFailure.stderr)).not.toContain(importMarker);
			expect(outputText(importFailure.stderr)).not.toContain(escapeSequence);
			if (process.platform === "linux")
				await expectFixtureDescendantCleaned(failingDescendantRecordPath, failingDescendantToken);

			const exitsBeforeExports = Bun.spawnSync({
				cmd: [COMPILED_PROBE_PATH, "--pi-package", exitsBeforeExportsFixture.packageDir],
				cwd,
				stdout: "pipe",
				stderr: "pipe",
			});
			expect(exitsBeforeExports.exitCode).not.toBe(0);
			expect(outputText(exitsBeforeExports.stdout)).toBe("");
			expect(outputText(exitsBeforeExports.stderr)).toBe(
				"pixie-pi-sdk-probe: selected Pi public entrypoint self-check did not report success\n",
			);

			if (normalDescendantFixture) {
				const normalCompletion = Bun.spawnSync({
					cmd: [COMPILED_PROBE_PATH, "--pi-package", normalDescendantFixture.packageDir],
					cwd,
					stdout: "pipe",
					stderr: "pipe",
				});
				expect(normalCompletion.exitCode, outputText(normalCompletion.stderr)).toBe(0);
				expect(outputText(normalCompletion.stdout)).toBe(encodeProbeReport(expectedReport));
				await expectFixtureDescendantCleaned(normalDescendantRecordPath, normalDescendantToken);
			}

			if (hangingDescendantFixture) {
				const startedAt = Date.now();
				const timedOut = Bun.spawnSync({
					cmd: [COMPILED_PROBE_PATH, "--pi-package", hangingDescendantFixture.packageDir],
					cwd,
					stdout: "pipe",
					stderr: "pipe",
				});
				const elapsed = Date.now() - startedAt;
				expect(timedOut.exitCode).not.toBe(0);
				expect(outputText(timedOut.stdout)).toBe("");
				expect(outputText(timedOut.stderr)).toBe(
					"pixie-pi-sdk-probe: selected Pi public entrypoint self-check timed out\n",
				);
				expect(elapsed).toBeGreaterThanOrEqual(4_500);
				expect(elapsed).toBeLessThan(8_000);
				await expectFixtureDescendantCleaned(hangingDescendantRecordPath, hangingDescendantToken);
			}
		} finally {
			if (normalDescendantFixture) await removeFixture(normalDescendantFixture);
			if (hangingDescendantFixture) await removeFixture(hangingDescendantFixture);
			await removeFixture(nonPiWorkerFixture);
			await removeFixture(exitsBeforeExportsFixture);
			await removeFixture(failingFixture);
			await removeFixture(noisyFixture);
			await removeFixture(fixture);
			await rm(COMPILED_PROBE_PATH, { force: true });
			await rm(root, { recursive: true, force: true });
		}
	}, 15_000);
});
