/**
 * Compiled Bun-host spike for one explicitly selected external Pi SDK.
 *
 * This is intentionally not the production assistant host. It verifies the
 * selected package and loads only its verified public entrypoint so the next
 * host phase can use the SDK in-process without bundling or resolving Pi from
 * the workspace, global installation, or current working directory.
 */

import { createHash, randomBytes } from "node:crypto";
import { chmod, mkdtemp, readFile, realpath, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

export const PI_CODING_AGENT_PACKAGE = "@earendil-works/pi-coding-agent";
export const REQUIRED_PUBLIC_SYMBOLS = [
	"createAgentSession",
	"SessionManager",
	"DefaultResourceLoader",
	"ModelRuntime",
	"AgentSessionRuntime",
	"SettingsManager",
] as const;
export const MAX_PACKAGE_MANIFEST_BYTES = 64 * 1024;
export const MAX_PROBE_REPORT_BYTES = 2048;
const MAX_PUBLIC_ENTRYPOINT_BYTES = 8 * 1024 * 1024;
const PI_SDK_CHECKER_TIMEOUT_MS = 5_000;
const PI_SDK_CHECKER_TERM_GRACE_MS = 250;
const PI_SDK_CHECKER_REAP_TIMEOUT_MS = 1_000;
const PI_SDK_CHECKER_REAP_POLL_MS = 25;
const CHECKER_SUCCESS_TOKEN_BYTES = 32;
const CHECKER_SUCCESS_TOKEN_LENGTH = 43;
const PI_SDK_CHECKER_FAILED_MESSAGE = "selected Pi public entrypoint self-check failed";
const PI_SDK_CHECKER_TIMEOUT_MESSAGE = "selected Pi public entrypoint self-check timed out";
const PI_SDK_CHECKER_MARKER_MESSAGE =
	"selected Pi public entrypoint self-check did not report success";
const PI_SDK_CHECKER_CLEANUP_MESSAGE = "selected Pi public entrypoint self-check cleanup failed";
const PI_SDK_CHECKER_PLATFORM_MESSAGE =
	"selected Pi public entrypoint self-check requires Linux process-group isolation";

export interface VerifiedPiPackage {
	readonly packageName: typeof PI_CODING_AGENT_PACKAGE;
	readonly packageVersion: string;
	/** Canonical package directory, retained for the verified dynamic import only. */
	readonly packageDir: string;
	/** Canonical public entrypoint, retained for the verified dynamic import only. */
	readonly entryPath: string;
	/** Digest of the verified manifest, compared across isolated checker processes. */
	readonly manifestDigest: string;
	/** Digest of the verified public entrypoint, compared across isolated checker processes. */
	readonly entryDigest: string;
}

export interface PiSdkProbeReport {
	readonly packageName: typeof PI_CODING_AGENT_PACKAGE;
	readonly packageVersion: string;
	readonly publicSymbols: readonly (typeof REQUIRED_PUBLIC_SYMBOLS)[number][];
}

class PiSdkProbeError extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function isContainedBy(root: string, candidate: string): boolean {
	const remainder = relative(root, candidate);
	return (
		remainder === "" ||
		(remainder !== ".." && !remainder.startsWith(`..${sep}`) && !isAbsolute(remainder))
	);
}

function validEntrypoint(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length > 0 &&
		!value.includes("\0") &&
		!/^[a-z][a-z0-9+.-]*:/i.test(value)
	);
}

function publicEntrypoint(value: unknown): string | undefined {
	if (validEntrypoint(value)) return value;
	if (!isRecord(value)) return undefined;
	for (const condition of ["bun", "import", "node", "default", "require"]) {
		const entrypoint = publicEntrypoint(value[condition]);
		if (entrypoint) return entrypoint;
	}
	return undefined;
}

function manifestEntrypoint(manifest: Record<string, unknown>): string | undefined {
	if (Object.hasOwn(manifest, "exports")) {
		if (isRecord(manifest.exports)) {
			if (Object.hasOwn(manifest.exports, ".")) return publicEntrypoint(manifest.exports["."]);
			// Conditional root exports omit `.`. A subpath map does not expose
			// the package root and must not fall back to legacy entrypoints.
			if (Object.keys(manifest.exports).some((key) => key.startsWith("."))) return undefined;
		}
		return publicEntrypoint(manifest.exports);
	}
	if (validEntrypoint(manifest.module)) return manifest.module;
	if (validEntrypoint(manifest.main)) return manifest.main;
	return undefined;
}

function validPackageVersion(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length <= 128 &&
		/^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-(?:(?:0|[1-9]\d*)|[0-9a-z-]+)(?:\.(?:(?:0|[1-9]\d*)|[0-9a-z-]+))*)?(?:\+[0-9a-z-]+(?:\.[0-9a-z-]+)*)?$/i.test(
			value,
		)
	);
}

async function packageDirectory(packagePath: string): Promise<string> {
	const selected = typeof packagePath === "string" ? packagePath.trim() : "";
	if (!selected || selected.includes("\0"))
		throw new PiSdkProbeError("Pi SDK probe requires a package path");
	if (!isAbsolute(selected))
		throw new PiSdkProbeError("Pi SDK probe package path must be absolute");

	const selectedPath = resolve(selected);
	const selectedInfo = await stat(selectedPath).catch(() => undefined);
	if (!selectedInfo) throw new PiSdkProbeError("selected Pi package does not exist");
	if (selectedInfo.isDirectory()) return realpath(selectedPath);
	if (selectedInfo.isFile() && basename(selectedPath) === "package.json")
		return realpath(dirname(selectedPath));
	throw new PiSdkProbeError("Pi SDK probe package path must be a directory or package.json");
}

interface ParsedManifest {
	readonly manifest: Record<string, unknown>;
	readonly digest: string;
}

function sha256(contents: Uint8Array): string {
	return createHash("sha256").update(contents).digest("hex");
}

async function readManifest(packageDir: string): Promise<ParsedManifest> {
	const manifestPath = join(packageDir, "package.json");
	const manifestInfo = await stat(manifestPath).catch(() => undefined);
	if (
		!manifestInfo?.isFile() ||
		manifestInfo.size === 0 ||
		manifestInfo.size > MAX_PACKAGE_MANIFEST_BYTES
	)
		throw new PiSdkProbeError("selected Pi package is missing a bounded package.json");

	const canonicalManifestPath = await realpath(manifestPath).catch(() => undefined);
	if (!canonicalManifestPath || !isContainedBy(packageDir, canonicalManifestPath))
		throw new PiSdkProbeError("selected Pi package manifest escapes its package directory");

	const contents = await readFile(manifestPath).catch(() => undefined);
	if (!contents) throw new PiSdkProbeError("selected Pi package package.json could not be read");
	if (contents.byteLength === 0 || contents.byteLength > MAX_PACKAGE_MANIFEST_BYTES)
		throw new PiSdkProbeError("selected Pi package package.json exceeds the size limit");
	try {
		const manifest: unknown = JSON.parse(contents.toString("utf8"));
		if (!isRecord(manifest)) throw new Error();
		return { manifest, digest: sha256(contents) };
	} catch {
		throw new PiSdkProbeError("selected Pi package package.json is not valid JSON");
	}
}

/** Verify an explicitly selected package without importing any of its code. */
export async function verifyPiPackage(packagePath: string): Promise<VerifiedPiPackage> {
	const packageDir = await packageDirectory(packagePath);
	const { manifest, digest: manifestDigest } = await readManifest(packageDir);
	if (manifest.name !== PI_CODING_AGENT_PACKAGE)
		throw new PiSdkProbeError(`selected package is not ${PI_CODING_AGENT_PACKAGE}`);
	if (!validPackageVersion(manifest.version))
		throw new PiSdkProbeError("selected Pi package has an invalid version");
	const entrypoint = manifestEntrypoint(manifest);
	if (!entrypoint) throw new PiSdkProbeError("selected Pi package exposes no public entrypoint");

	const entryPath = resolve(packageDir, entrypoint);
	if (!isContainedBy(packageDir, entryPath))
		throw new PiSdkProbeError("selected Pi package entrypoint escapes its package directory");
	const entryInfo = await stat(entryPath).catch(() => undefined);
	if (!entryInfo?.isFile() || entryInfo.size === 0 || entryInfo.size > MAX_PUBLIC_ENTRYPOINT_BYTES)
		throw new PiSdkProbeError("selected Pi package entrypoint is not a file");
	const canonicalEntryPath = await realpath(entryPath).catch(() => undefined);
	if (!canonicalEntryPath || !isContainedBy(packageDir, canonicalEntryPath))
		throw new PiSdkProbeError("selected Pi package entrypoint escapes its package directory");
	const entryContents = await readFile(canonicalEntryPath).catch(() => undefined);
	if (
		!entryContents ||
		entryContents.byteLength === 0 ||
		entryContents.byteLength > MAX_PUBLIC_ENTRYPOINT_BYTES
	)
		throw new PiSdkProbeError("selected Pi package entrypoint could not be read");

	return {
		packageName: PI_CODING_AGENT_PACKAGE,
		packageVersion: manifest.version,
		packageDir,
		entryPath: canonicalEntryPath,
		manifestDigest,
		entryDigest: sha256(entryContents),
	};
}

type PiSdkImportFailureCategory = "failed" | "invalid-module";

const PI_SDK_IMPORT_FAILURE_MESSAGE: Record<PiSdkImportFailureCategory, string> = {
	failed: "selected Pi public entrypoint failed to import",
	"invalid-module": "selected Pi public entrypoint is not a module",
};

class PiSdkImportFailure extends PiSdkProbeError {
	constructor(category: PiSdkImportFailureCategory) {
		super(PI_SDK_IMPORT_FAILURE_MESSAGE[category]);
	}
}

/**
 * Dynamically import only a verified public file URL from the selected package.
 *
 * This is not a security sandbox and does not isolate output: in-process code
 * can write to process streams or file descriptors and schedule work after
 * import. The later production host owns any protocol/output boundary. Raw
 * import exceptions are replaced with a fixed diagnostic for its caller.
 */
async function loadPiPublicApiEntrypoint(entryPath: string): Promise<Record<string, unknown>> {
	let loaded: unknown;
	try {
		loaded = await import(pathToFileURL(entryPath).href);
	} catch {
		throw new PiSdkImportFailure("failed");
	}
	if (!isRecord(loaded)) throw new PiSdkImportFailure("invalid-module");
	return loaded;
}

export async function loadVerifiedPiPublicApi(
	verified: VerifiedPiPackage,
): Promise<Record<string, unknown>> {
	return loadPiPublicApiEntrypoint(verified.entryPath);
}

function assertRequiredPublicSymbols(publicApi: Record<string, unknown>): void {
	const missingSymbols = REQUIRED_PUBLIC_SYMBOLS.filter(
		(symbol) => typeof publicApi[symbol] !== "function",
	);
	if (missingSymbols.length)
		throw new PiSdkProbeError(
			`selected Pi package is missing public symbols: ${missingSymbols.join(", ")}`,
		);
}

/** Verify the package and the public SDK symbols needed by the next host phase. */
export async function probePiSdk(packagePath: string): Promise<PiSdkProbeReport> {
	const verified = await verifyPiPackage(packagePath);
	const publicApi = await loadVerifiedPiPublicApi(verified);
	assertRequiredPublicSymbols(publicApi);
	return {
		packageName: verified.packageName,
		packageVersion: verified.packageVersion,
		publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
	};
}

const CHECKER_PACKAGE_ARGUMENT = "--checker-package";
const CHECKER_SUCCESS_MARKER_ARGUMENT = "--checker-success-marker";
const CHECKER_SUCCESS_TOKEN_ARGUMENT = "--checker-success-token";
const CHECKER_RESULT_MARKER_ARGUMENT = "--checker-result-marker";
const CHECKER_RESULT_TOKEN_ARGUMENT = "--checker-result-token";
const CHECKER_TEARDOWN_MARKER_ARGUMENT = "--checker-teardown-marker";
const CHECKER_TEARDOWN_TOKEN_ARGUMENT = "--checker-teardown-token";
const CHECKER_RELEASE_TOKEN_ARGUMENT = "--checker-release-token";
const CHECKER_WORKER_PACKAGE_ARGUMENT = "--checker-worker-package";
const CHECKER_MODE_ENVIRONMENT = "PIXIE_PI_SDK_PROBE_CHECKER";
const CHECKER_WORKER_SUCCESS_MARKER_ENVIRONMENT = "PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_MARKER";
const CHECKER_WORKER_SUCCESS_TOKEN_ENVIRONMENT = "PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_TOKEN";
const CHECKER_GUARDIAN_MODE = "guardian";
const CHECKER_WORKER_MODE = "worker";

interface CheckerMarker {
	readonly path: string;
	readonly token: string;
}

interface CheckerMarkers {
	readonly directory: string;
	readonly workerSuccess: CheckerMarker;
	readonly result: CheckerMarker;
	readonly teardown: CheckerMarker;
	readonly releaseToken: string;
}

interface GuardianInvocation {
	readonly packagePath: string;
	readonly markers: Omit<CheckerMarkers, "directory">;
}

interface WorkerInvocation {
	readonly packagePath: string;
	readonly successMarker: CheckerMarker;
}

interface GuardianProcess {
	readonly child: Bun.Subprocess;
}

type WorkerOutcome =
	| { readonly timedOut: true }
	| { readonly timedOut: false; readonly exitCode: number }
	| { readonly terminated: true };

type GuardianResultKind = "success" | "failed" | "missing-marker" | "timed-out";

interface CheckerPackageIdentity {
	readonly packageVersion: string;
	readonly manifestDigest: string;
	readonly entryDigest: string;
}

interface GuardianResult {
	readonly kind: GuardianResultKind;
	readonly packageIdentity?: CheckerPackageIdentity;
}

type GuardianOutcome =
	| { readonly kind: "result"; readonly result: GuardianResult }
	| { readonly kind: "exited" }
	| { readonly kind: "timed-out" };

function validCheckerPath(value: unknown): value is string {
	return validEntrypoint(value) && isAbsolute(value);
}

function validCheckerToken(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length === CHECKER_SUCCESS_TOKEN_LENGTH &&
		/^[A-Za-z0-9_-]+$/.test(value)
	);
}

function validSha256(value: unknown): value is string {
	return typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
}

function checkerPackageIdentity(verified: VerifiedPiPackage): CheckerPackageIdentity {
	return {
		packageVersion: verified.packageVersion,
		manifestDigest: verified.manifestDigest,
		entryDigest: verified.entryDigest,
	};
}

function samePackageIdentity(left: CheckerPackageIdentity, right: CheckerPackageIdentity): boolean {
	return (
		left.packageVersion === right.packageVersion &&
		left.manifestDigest === right.manifestDigest &&
		left.entryDigest === right.entryDigest
	);
}

function parseCheckerPackageIdentity(value: unknown): CheckerPackageIdentity | undefined {
	if (!isRecord(value) || !validPackageVersion(value.packageVersion)) return undefined;
	if (!validSha256(value.manifestDigest) || !validSha256(value.entryDigest)) return undefined;
	return {
		packageVersion: value.packageVersion,
		manifestDigest: value.manifestDigest,
		entryDigest: value.entryDigest,
	};
}

function guardianInvocation(argv: readonly string[]): GuardianInvocation {
	const expectedArguments = [
		CHECKER_PACKAGE_ARGUMENT,
		CHECKER_SUCCESS_MARKER_ARGUMENT,
		CHECKER_SUCCESS_TOKEN_ARGUMENT,
		CHECKER_RESULT_MARKER_ARGUMENT,
		CHECKER_RESULT_TOKEN_ARGUMENT,
		CHECKER_TEARDOWN_MARKER_ARGUMENT,
		CHECKER_TEARDOWN_TOKEN_ARGUMENT,
		CHECKER_RELEASE_TOKEN_ARGUMENT,
	];
	if (
		argv.length !== expectedArguments.length * 2 ||
		expectedArguments.some((argument, index) => argv[index * 2] !== argument)
	)
		throw new PiSdkProbeError("Pi SDK probe guardian received invalid arguments");
	const [
		packagePath,
		workerSuccessMarkerPath,
		workerSuccessToken,
		resultMarkerPath,
		resultToken,
		teardownMarkerPath,
		teardownToken,
		releaseToken,
	] = expectedArguments.map((_, index) => argv[index * 2 + 1]);
	if (
		!validCheckerPath(packagePath) ||
		!validCheckerPath(workerSuccessMarkerPath) ||
		!validCheckerPath(resultMarkerPath) ||
		!validCheckerPath(teardownMarkerPath)
	)
		throw new PiSdkProbeError("Pi SDK probe guardian received invalid paths");
	if (
		!validCheckerToken(workerSuccessToken) ||
		!validCheckerToken(resultToken) ||
		!validCheckerToken(teardownToken) ||
		!validCheckerToken(releaseToken)
	)
		throw new PiSdkProbeError("Pi SDK probe guardian received an invalid token");
	if (new Set([workerSuccessMarkerPath, resultMarkerPath, teardownMarkerPath]).size !== 3)
		throw new PiSdkProbeError("Pi SDK probe guardian received duplicate marker paths");
	return {
		packagePath,
		markers: {
			workerSuccess: { path: workerSuccessMarkerPath, token: workerSuccessToken },
			result: { path: resultMarkerPath, token: resultToken },
			teardown: { path: teardownMarkerPath, token: teardownToken },
			releaseToken,
		},
	};
}

function workerInvocation(
	argv: readonly string[],
	environment: Record<string, string | undefined> = process.env,
): WorkerInvocation {
	if (argv.length !== 2 || argv[0] !== CHECKER_WORKER_PACKAGE_ARGUMENT)
		throw new PiSdkProbeError("Pi SDK probe checker received invalid arguments");
	const packagePath = argv[1];
	const successMarkerPath = environment[CHECKER_WORKER_SUCCESS_MARKER_ENVIRONMENT];
	const successToken = environment[CHECKER_WORKER_SUCCESS_TOKEN_ENVIRONMENT];
	if (!validCheckerPath(packagePath) || !validCheckerPath(successMarkerPath))
		throw new PiSdkProbeError("Pi SDK probe checker received invalid paths");
	if (!validCheckerToken(successToken))
		throw new PiSdkProbeError("Pi SDK probe checker received an invalid success token");
	return { packagePath, successMarker: { path: successMarkerPath, token: successToken } };
}

function selfCheckerCommand(arguments_: readonly string[]): string[] {
	if (Bun.main.startsWith("/$bunfs/")) return [process.execPath, ...arguments_];
	return [process.execPath, Bun.main, ...arguments_];
}

function invocationArguments(): readonly string[] {
	return process.argv.slice(2);
}

function privateCheckerMarker(directory: string, name: string): CheckerMarker {
	return {
		path: join(directory, name),
		token: randomBytes(CHECKER_SUCCESS_TOKEN_BYTES).toString("base64url"),
	};
}

async function createCheckerMarkers(): Promise<CheckerMarkers> {
	let directory: string | undefined;
	try {
		directory = await mkdtemp(join(tmpdir(), "pixie-pi-sdk-probe-checker-"));
		await chmod(directory, 0o700);
		return {
			directory,
			workerSuccess: privateCheckerMarker(directory, "worker-success"),
			result: privateCheckerMarker(directory, "result"),
			teardown: privateCheckerMarker(directory, "teardown"),
			releaseToken: randomBytes(CHECKER_SUCCESS_TOKEN_BYTES).toString("base64url"),
		};
	} catch {
		if (directory) await rm(directory, { recursive: true, force: true }).catch(() => undefined);
		throw new PiSdkProbeError(PI_SDK_CHECKER_FAILED_MESSAGE);
	}
}

async function removeCheckerMarkers(markers: CheckerMarkers): Promise<void> {
	try {
		await rm(markers.directory, { recursive: true, force: true });
	} catch {
		throw new PiSdkProbeError(PI_SDK_CHECKER_CLEANUP_MESSAGE);
	}
}

async function checkerReportedToken(marker: CheckerMarker): Promise<boolean> {
	const contents = await readFile(marker.path, "utf8").catch(() => undefined);
	return contents === marker.token;
}

async function writeCheckerToken(marker: CheckerMarker): Promise<void> {
	await writeFile(marker.path, marker.token, {
		encoding: "utf8",
		flag: "wx",
		mode: 0o600,
	});
}

async function writeCheckerPackageIdentity(
	marker: CheckerMarker,
	identity: CheckerPackageIdentity,
): Promise<void> {
	await writeFile(marker.path, `${marker.token}:${JSON.stringify(identity)}`, {
		encoding: "utf8",
		flag: "wx",
		mode: 0o600,
	});
}

async function checkerReportedPackageIdentity(
	marker: CheckerMarker,
): Promise<CheckerPackageIdentity | undefined> {
	const contents = await readFile(marker.path, "utf8").catch(() => undefined);
	if (!contents?.startsWith(`${marker.token}:`)) return undefined;
	try {
		return parseCheckerPackageIdentity(JSON.parse(contents.slice(marker.token.length + 1)));
	} catch {
		return undefined;
	}
}

async function writeGuardianResult(marker: CheckerMarker, result: GuardianResult): Promise<void> {
	await writeFile(marker.path, `${marker.token}:${JSON.stringify(result)}`, {
		encoding: "utf8",
		flag: "wx",
		mode: 0o600,
	});
}

async function guardianReportedResult(marker: CheckerMarker): Promise<GuardianResult | undefined> {
	const contents = await readFile(marker.path, "utf8").catch(() => undefined);
	if (!contents?.startsWith(`${marker.token}:`)) return undefined;
	try {
		const result = JSON.parse(contents.slice(marker.token.length + 1));
		if (!isRecord(result)) return undefined;
		if (
			result.kind !== "success" &&
			result.kind !== "failed" &&
			result.kind !== "missing-marker" &&
			result.kind !== "timed-out"
		)
			return undefined;
		const packageIdentity =
			result.kind === "success" ? parseCheckerPackageIdentity(result.packageIdentity) : undefined;
		if (result.kind === "success" && !packageIdentity) return undefined;
		return packageIdentity ? { kind: result.kind, packageIdentity } : { kind: result.kind };
	} catch {
		return undefined;
	}
}

function guardianArguments(invocation: GuardianInvocation): string[] {
	return [
		CHECKER_PACKAGE_ARGUMENT,
		invocation.packagePath,
		CHECKER_SUCCESS_MARKER_ARGUMENT,
		invocation.markers.workerSuccess.path,
		CHECKER_SUCCESS_TOKEN_ARGUMENT,
		invocation.markers.workerSuccess.token,
		CHECKER_RESULT_MARKER_ARGUMENT,
		invocation.markers.result.path,
		CHECKER_RESULT_TOKEN_ARGUMENT,
		invocation.markers.result.token,
		CHECKER_TEARDOWN_MARKER_ARGUMENT,
		invocation.markers.teardown.path,
		CHECKER_TEARDOWN_TOKEN_ARGUMENT,
		invocation.markers.teardown.token,
		CHECKER_RELEASE_TOKEN_ARGUMENT,
		invocation.markers.releaseToken,
	];
}

function startGuardian(invocation: GuardianInvocation): GuardianProcess {
	const child = Bun.spawn(selfCheckerCommand(guardianArguments(invocation)), {
		stdin: "pipe",
		stdout: "ignore",
		stderr: "ignore",
		// Bun gives a detached POSIX child its own session and process group. The
		// guardian later signals that self-owned group with process.kill(0, ...).
		detached: true,
		env: { ...process.env, [CHECKER_MODE_ENVIRONMENT]: CHECKER_GUARDIAN_MODE },
	});
	return { child };
}

function startCheckerWorker(invocation: GuardianInvocation): Bun.Subprocess {
	return Bun.spawn(selfCheckerCommand([CHECKER_WORKER_PACKAGE_ARGUMENT, invocation.packagePath]), {
		stdin: "ignore",
		stdout: "ignore",
		stderr: "ignore",
		detached: false,
		env: {
			...process.env,
			[CHECKER_MODE_ENVIRONMENT]: CHECKER_WORKER_MODE,
			[CHECKER_WORKER_SUCCESS_MARKER_ENVIRONMENT]: invocation.markers.workerSuccess.path,
			[CHECKER_WORKER_SUCCESS_TOKEN_ENVIRONMENT]: invocation.markers.workerSuccess.token,
		},
	});
}

function pause(milliseconds: number): Promise<void> {
	return new Promise((resolvePause) => setTimeout(resolvePause, milliseconds));
}

async function checkerExitedWithin(child: Bun.Subprocess, timeoutMs: number): Promise<boolean> {
	if (child.exitCode !== null) return true;
	let timeout: ReturnType<typeof setTimeout> | undefined;
	try {
		return await Promise.race([
			child.exited.then(
				() => true,
				() => true,
			),
			new Promise<boolean>((resolveTimeout) => {
				timeout = setTimeout(() => resolveTimeout(false), timeoutMs);
			}),
		]);
	} finally {
		if (timeout) clearTimeout(timeout);
	}
}

function signalCheckerProcess(child: Bun.Subprocess, signal: NodeJS.Signals): void {
	try {
		child.kill(signal);
	} catch {
		// The child may already have exited; its final reaping check is authoritative.
	}
}

async function waitForWorkerOutcome(
	child: Bun.Subprocess,
	terminated: Promise<void>,
): Promise<WorkerOutcome> {
	let timeout: ReturnType<typeof setTimeout> | undefined;
	try {
		return await Promise.race([
			child.exited.then(
				(exitCode) => ({ timedOut: false as const, exitCode }),
				() => ({ timedOut: false as const, exitCode: -1 }),
			),
			terminated.then(() => ({ terminated: true as const })),
			new Promise<{ readonly timedOut: true }>((resolveTimeout) => {
				timeout = setTimeout(() => resolveTimeout({ timedOut: true }), PI_SDK_CHECKER_TIMEOUT_MS);
			}),
		]);
	} finally {
		if (timeout) clearTimeout(timeout);
	}
}

interface GuardianTermination {
	readonly terminated: Promise<void>;
	readonly remove: () => void;
}

function installGuardianTerminationHandler(): GuardianTermination {
	let resolveTermination: (() => void) | undefined;
	let terminationRequested = false;
	const terminated = new Promise<void>((resolve) => {
		resolveTermination = resolve;
	});
	const handler = () => {
		if (terminationRequested) return;
		terminationRequested = true;
		resolveTermination?.();
	};
	process.on("SIGTERM", handler);
	return {
		terminated,
		remove: () => process.off("SIGTERM", handler),
	};
}

async function stopTimedOutWorker(worker: Bun.Subprocess): Promise<void> {
	signalCheckerProcess(worker, "SIGTERM");
	if (await checkerExitedWithin(worker, PI_SDK_CHECKER_TERM_GRACE_MS)) return;
	signalCheckerProcess(worker, "SIGKILL");
	await checkerExitedWithin(worker, PI_SDK_CHECKER_REAP_TIMEOUT_MS);
}

async function waitForGuardianRelease(releaseToken: string): Promise<void> {
	process.stdin.setEncoding("utf8");
	let release = "";
	for await (const chunk of process.stdin) {
		if (release.length > releaseToken.length + 1) continue;
		release += chunk;
		if (release === `${releaseToken}\n`) return;
	}
}

/**
 * The guardian cleanup path is Linux-only because it must prove that Bun's
 * detached launch made this process its own session and process-group leader.
 * An externally invoked internal mode therefore fails before signalling a
 * caller-owned group instead of assuming a launch invariant it does not have.
 */
async function guardianOwnsLinuxProcessGroup(): Promise<boolean> {
	if (process.platform !== "linux") return false;
	const statContents = await readFile("/proc/self/stat", "utf8").catch(() => undefined);
	if (!statContents) return false;
	const fields = statContents
		.slice(statContents.lastIndexOf(")") + 2)
		.trim()
		.split(/\s+/);
	const processGroupId = Number.parseInt(fields[2] ?? "", 10);
	const sessionId = Number.parseInt(fields[3] ?? "", 10);
	return processGroupId === process.pid && sessionId === process.pid;
}

async function terminateGuardianGroup(teardown: CheckerMarker): Promise<void> {
	if (!(await guardianOwnsLinuxProcessGroup()))
		throw new PiSdkProbeError(PI_SDK_CHECKER_CLEANUP_MESSAGE);
	try {
		process.kill(0, "SIGTERM");
	} catch {
		throw new PiSdkProbeError(PI_SDK_CHECKER_CLEANUP_MESSAGE);
	}
	await pause(PI_SDK_CHECKER_TERM_GRACE_MS);
	await writeCheckerToken(teardown).catch(() => undefined);
	process.kill(0, "SIGKILL");
}

async function runGuardian(invocation: GuardianInvocation): Promise<void> {
	// This must precede worker creation: the detached guardian owns its process
	// group and stays alive long enough to terminate it after SIGTERM.
	if (!(await guardianOwnsLinuxProcessGroup()))
		throw new PiSdkProbeError(PI_SDK_CHECKER_CLEANUP_MESSAGE);
	const termination = installGuardianTerminationHandler();
	let result: GuardianResult = { kind: "failed" };
	try {
		const worker = startCheckerWorker(invocation);
		const outcome = await waitForWorkerOutcome(worker, termination.terminated);
		if ("timedOut" in outcome && outcome.timedOut) {
			await stopTimedOutWorker(worker);
			result = { kind: "timed-out" };
		} else if ("terminated" in outcome) {
			result = { kind: "failed" };
		} else if (outcome.exitCode !== 0) {
			result = { kind: "failed" };
		} else {
			const packageIdentity = await checkerReportedPackageIdentity(
				invocation.markers.workerSuccess,
			);
			result = packageIdentity ? { kind: "success", packageIdentity } : { kind: "missing-marker" };
		}
	} catch {
		result = { kind: "failed" };
	}
	try {
		await writeGuardianResult(invocation.markers.result, result);
	} catch {
		// The parent treats an absent or invalid result marker as a fixed failure.
	}
	try {
		await Promise.race([
			waitForGuardianRelease(invocation.markers.releaseToken),
			termination.terminated,
		]);
		await terminateGuardianGroup(invocation.markers.teardown);
	} finally {
		termination.remove();
	}
}

async function waitForGuardianOutcome(
	guardian: GuardianProcess,
	markers: CheckerMarkers,
): Promise<GuardianOutcome> {
	const deadline = Date.now() + PI_SDK_CHECKER_TIMEOUT_MS + PI_SDK_CHECKER_REAP_TIMEOUT_MS;
	for (;;) {
		const result = await guardianReportedResult(markers.result);
		if (result) return { kind: "result", result };
		if (guardian.child.exitCode !== null) return { kind: "exited" };
		const remaining = deadline - Date.now();
		if (remaining <= 0) return { kind: "timed-out" };
		await pause(Math.min(PI_SDK_CHECKER_REAP_POLL_MS, remaining));
	}
}

async function guardianTeardownObserved(
	guardian: GuardianProcess,
	markers: CheckerMarkers,
): Promise<boolean> {
	const deadline = Date.now() + PI_SDK_CHECKER_REAP_TIMEOUT_MS;
	let teardownReported = false;
	for (;;) {
		if (await checkerReportedToken(markers.teardown)) teardownReported = true;
		if (guardian.child.exitCode !== null || guardianProcessIsMissing(guardian.child))
			return teardownReported;
		const remaining = deadline - Date.now();
		if (remaining <= 0) return false;
		await pause(Math.min(PI_SDK_CHECKER_REAP_POLL_MS, remaining));
	}
}

/**
 * Bun 1.4 can leave `exitCode` null after a detached guardian self-SIGKILLs.
 * This is a non-signalling observation of the direct child only. A live or
 * recycled PID is treated as still running, so the authenticated teardown
 * marker remains necessary but cannot by itself hide a surviving guardian.
 */
function guardianProcessIsMissing(guardian: Bun.Subprocess): boolean {
	try {
		process.kill(guardian.pid, 0);
		return false;
	} catch (error) {
		return isRecord(error) && error.code === "ESRCH";
	}
}

async function releaseGuardian(
	guardian: GuardianProcess,
	markers: CheckerMarkers,
): Promise<boolean> {
	const stdin = guardian.child.stdin;
	if (!stdin || typeof stdin === "number") return guardianTeardownObserved(guardian, markers);
	try {
		stdin.write(`${markers.releaseToken}\n`);
		stdin.end();
	} catch {
		try {
			stdin.end();
		} catch {
			// A direct guardian exit is also a valid teardown observation.
		}
	}
	return guardianTeardownObserved(guardian, markers);
}

async function confirmPublicSymbolsInChecker(verified: VerifiedPiPackage): Promise<void> {
	const markers = await createCheckerMarkers();
	let guardian: GuardianProcess | undefined;
	let outcome: GuardianOutcome | undefined;
	let guardianFailed = false;
	let cleanupFailed = false;
	try {
		guardian = startGuardian({ packagePath: verified.packageDir, markers });
		outcome = await waitForGuardianOutcome(guardian, markers);
	} catch {
		guardianFailed = true;
	} finally {
		try {
			if (guardian && !(await releaseGuardian(guardian, markers))) cleanupFailed = true;
		} catch {
			cleanupFailed = true;
		}
		try {
			await removeCheckerMarkers(markers);
		} catch {
			cleanupFailed = true;
		}
	}
	if (cleanupFailed) throw new PiSdkProbeError(PI_SDK_CHECKER_CLEANUP_MESSAGE);
	if (guardianFailed || !outcome || outcome.kind === "exited")
		throw new PiSdkProbeError(PI_SDK_CHECKER_FAILED_MESSAGE);
	if (outcome.kind === "timed-out") throw new PiSdkProbeError(PI_SDK_CHECKER_TIMEOUT_MESSAGE);
	if (outcome.result.kind === "failed") throw new PiSdkProbeError(PI_SDK_CHECKER_FAILED_MESSAGE);
	if (outcome.result.kind === "missing-marker")
		throw new PiSdkProbeError(PI_SDK_CHECKER_MARKER_MESSAGE);
	if (outcome.result.kind === "timed-out")
		throw new PiSdkProbeError(PI_SDK_CHECKER_TIMEOUT_MESSAGE);
	const workerPackageIdentity = outcome.result.packageIdentity;
	if (
		outcome.result.kind !== "success" ||
		!workerPackageIdentity ||
		!samePackageIdentity(checkerPackageIdentity(verified), workerPackageIdentity)
	)
		throw new PiSdkProbeError(PI_SDK_CHECKER_FAILED_MESSAGE);
}

async function runCheckerWorker(invocation: WorkerInvocation): Promise<void> {
	const verified = await verifyPiPackage(invocation.packagePath);
	const publicApi = await loadPiPublicApiEntrypoint(verified.entryPath);
	assertRequiredPublicSymbols(publicApi);
	const verifiedAfterImport = await verifyPiPackage(invocation.packagePath);
	if (
		!samePackageIdentity(
			checkerPackageIdentity(verified),
			checkerPackageIdentity(verifiedAfterImport),
		)
	)
		throw new PiSdkProbeError(PI_SDK_CHECKER_FAILED_MESSAGE);
	await writeCheckerPackageIdentity(
		invocation.successMarker,
		checkerPackageIdentity(verifiedAfterImport),
	);
}

/**
 * Probe the selected package through a disposable diagnostic checker.
 *
 * The parent verifies package identity and canonical entrypoint before it
 * starts a detached guardian, discards its output, and builds the report only
 * after a matching private result marker and observed guardian teardown.
 * The selected Pi package remains operator-trusted: this catches accidental
 * early process exit, not malicious in-process package behavior. This
 * CLI-only diagnostic isolation is not part of the future in-process Pi host
 * execution path. It is not a sandbox for intentionally daemonizing code that
 * leaves the guardian's process group.
 */
async function probePiSdkInChecker(packagePath: string): Promise<PiSdkProbeReport> {
	if (process.platform !== "linux") throw new PiSdkProbeError(PI_SDK_CHECKER_PLATFORM_MESSAGE);
	const verified = await verifyPiPackage(packagePath);
	await confirmPublicSymbolsInChecker(verified);
	return {
		packageName: verified.packageName,
		packageVersion: verified.packageVersion,
		publicSymbols: REQUIRED_PUBLIC_SYMBOLS,
	};
}

/** Resolve the only accepted selection inputs; no package lookup fallback exists. */
export function resolvePiPackagePath(
	argv: readonly string[],
	environment: Record<string, string | undefined> = process.env,
): string {
	let explicitPath: string | undefined;
	for (let index = 0; index < argv.length; index += 1) {
		const argument = argv[index];
		let candidate: string | undefined;
		if (argument === "--pi-package") {
			candidate = argv[index + 1];
			index += 1;
		} else if (argument.startsWith("--pi-package=")) {
			candidate = argument.slice("--pi-package=".length);
		} else {
			throw new PiSdkProbeError("unsupported Pi SDK probe argument");
		}
		if (!candidate || explicitPath !== undefined)
			throw new PiSdkProbeError("Pi SDK probe requires exactly one --pi-package value");
		explicitPath = candidate;
	}
	const selected = explicitPath ?? environment.PIXIE_PI_PACKAGE;
	if (!selected)
		throw new PiSdkProbeError("Pi SDK probe requires --pi-package or PIXIE_PI_PACKAGE");
	return selected;
}

/** JSONL output contains only identity, version, and verified symbol names. */
export function encodeProbeReport(report: PiSdkProbeReport): string {
	const encoded = JSON.stringify(report);
	if (new TextEncoder().encode(encoded).byteLength > MAX_PROBE_REPORT_BYTES)
		throw new PiSdkProbeError("Pi SDK probe report exceeds the size limit");
	return `${encoded}\n`;
}

function diagnostic(error: unknown): string {
	return error instanceof PiSdkProbeError ? error.message : "verification failed";
}

async function main(): Promise<void> {
	const arguments_ = invocationArguments();
	const checkerMode = process.env[CHECKER_MODE_ENVIRONMENT];
	if (arguments_[0] === CHECKER_PACKAGE_ARGUMENT && checkerMode === CHECKER_GUARDIAN_MODE) {
		try {
			await runGuardian(guardianInvocation(arguments_));
		} catch {
			process.exitCode = 1;
		}
		return;
	}
	if (arguments_[0] === CHECKER_WORKER_PACKAGE_ARGUMENT && checkerMode === CHECKER_WORKER_MODE) {
		try {
			await runCheckerWorker(workerInvocation(arguments_));
		} catch {
			process.exitCode = 1;
		}
		return;
	}
	try {
		const report = await probePiSdkInChecker(resolvePiPackagePath(arguments_));
		process.stdout.write(encodeProbeReport(report));
	} catch (error) {
		console.error(`pixie-pi-sdk-probe: ${diagnostic(error)}`);
		process.exitCode = 1;
	}
}

if (import.meta.main) await main();
