/**
 * Compiled pixie_assistant executable (Bun host CLI).
 *
 * Experimental: serve/doctor/uninstall only. There is no Pi RPC child model
 * and no administration bridge sidecar; Pi runs in-process through the
 * operator's verified installation.
 */

import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { isAbsolute, join } from "node:path";
import { type BunHost, startBunHostFromVerifiedPi } from "./host.ts";
import { createHostLogger, redactHostLogText } from "./log.ts";
import { PI_CODING_AGENT_PACKAGE, type VerifiedPiPackage, verifyPiPackage } from "./probe.ts";

// Keep in sync with assistant/package.json version.
const ASSISTANT_VERSION = "0.1.0";
// Release identity baked in by `bun run build:release` via `--define`
// PIXIE_ASSISTANT_VERSION/PIXIE_ASSISTANT_REVISION. A plain
// `bun run src/serve.ts` leaves them undefined and reports the dev fallback,
// which never claims a commit-based release identity.
declare const PIXIE_ASSISTANT_VERSION: string | undefined;
declare const PIXIE_ASSISTANT_REVISION: string | undefined;

function buildVersion(): string {
	const defined = (
		typeof PIXIE_ASSISTANT_VERSION === "string" ? PIXIE_ASSISTANT_VERSION : ""
	).trim();
	return defined === "" ? ASSISTANT_VERSION : defined;
}

function buildRevision(): string {
	const defined = (
		typeof PIXIE_ASSISTANT_REVISION === "string" ? PIXIE_ASSISTANT_REVISION : ""
	).trim();
	return defined === "" ? "unknown" : defined;
}

function versionString(): string {
	return `pixie_assistant ${buildVersion()} (revision ${buildRevision()})`;
}
const REQUIRED_PI_VERSION = "0.85.1";
const DEFAULT_HOST = "127.0.0.1";
const MIN_SECRET_LENGTH = 32;
const DRAIN_TIMEOUT_MS = 25_000;
const RESTART_EXIT_CODE = 75;
const DOCTOR_SCENARIO_TIMEOUT_MS = 10_000;
const DOCTOR_SCENARIO_PREFIX = "pixie_assistant doctor scenario";
export const ASSISTANT_CONFIG_SCHEMA_VERSION = 2;
export const BUNDLED_PI_PACKAGE_ENVIRONMENT = "PIXIE_BUNDLED_PI_PACKAGE";

type Command = "serve" | "doctor" | "uninstall";

interface ParsedArgs {
	readonly command: Command;
	readonly configPath: string;
	readonly scenarioRequested: boolean;
	readonly versionRequested: boolean;
	readonly helpRequested: boolean;
}

export interface ResolvedConfig {
	readonly host: string;
	readonly port: number;
	readonly secret: string;
	readonly agentDir: string;
	readonly piPackage: string;
	readonly allowSelfRestart: boolean;
}

/**
 * The resolved bearer secret. It is recorded as soon as the secret is known so
 * every later fatal path redacts it by default; before resolution the list is
 * empty because no secret can appear yet.
 */
const activeSecrets: string[] = [];

function fail(message: string, secrets: readonly string[] = activeSecrets): never {
	// Host diagnostics are redacted so an error path can never export a bearer
	// token, credential value, endpoint URL or absolute filesystem path.
	console.error(`pixie_assistant: ${redactHostLogText(message, secrets)}`);
	process.exit(1);
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

/** Redact a top-level serve failure before it can reach stderr. */
export function fatalServeMessage(
	error: unknown,
	secrets: readonly string[] = activeSecrets,
): string {
	return redactHostLogText(errorMessage(error), secrets);
}

function usage(): string {
	return [
		"usage: pixie_assistant [serve|doctor|uninstall] --config ABS [--version]",
		"doctor --scenario verifies a disposable direct host without creating a Pi session or using the configured agent directory or port",
		"ports must match: config port (assistant listen, required) and PIXIE_PI_PORT (controller dial); localhost is an alias for 127.0.0.1",
	].join("\n");
}

export { usage };

export function normalizeAssistantHostValue(value: string): string {
	const trimmed = value.trim();
	if (trimmed === "") return DEFAULT_HOST;
	if (trimmed.toLowerCase() === "localhost") return DEFAULT_HOST;
	return trimmed;
}

export function pairedAssistantPortNote(port: number): string {
	return (
		`assistant config port is ${port}; ` +
		`controller PIXIE_PI_PORT must dial the same port over loopback`
	);
}

function legacyCliError(argument: string): never {
	fail(
		`legacy option ${JSON.stringify(argument)} is no longer supported; ` +
			"start the internal assistant only through the archive's pixie_cli launcher",
	);
}

function parseArgs(argv: readonly string[]): ParsedArgs {
	let command: Command = "serve";
	let commandSeen = false;
	let configPath = "";
	let scenarioRequested = false;
	let versionRequested = false;
	let helpRequested = false;
	for (let index = 0; index < argv.length; index += 1) {
		const argument = argv[index] ?? "";
		if (argument === "serve" || argument === "doctor" || argument === "uninstall") {
			if (commandSeen) fail("multiple commands supplied");
			command = argument;
			commandSeen = true;
		} else if (argument === "version" && !commandSeen) {
			versionRequested = true;
		} else if (argument === "--version") {
			versionRequested = true;
		} else if (
			argument === "--help" ||
			argument === "-h" ||
			(argument === "help" && !commandSeen)
		) {
			helpRequested = true;
		} else if (argument === "--config") {
			const value = argv[index + 1];
			if (value === undefined || value.trim() === "") fail("--config requires a value");
			index += 1;
			configPath = value;
		} else if (argument.startsWith("--config=")) {
			const value = argument.slice("--config=".length);
			if (value.trim() === "") fail("--config requires a value");
			configPath = value;
		} else if (argument === "--pi-package" || argument.startsWith("--pi-package=")) {
			fail("--pi-package is not accepted; pixie_cli supplies the bundled Pi archive");
		} else if (argument === "--scenario") {
			if (scenarioRequested) fail("multiple --scenario values supplied");
			scenarioRequested = true;
		} else if (
			argument === "--pi-executable" ||
			argument.startsWith("--pi-executable=") ||
			argument === "--pi-args" ||
			argument.startsWith("--pi-args=") ||
			argument === "--admin-bridge" ||
			argument.startsWith("--admin-bridge=") ||
			argument.startsWith("--admin-bridge-") ||
			argument.startsWith("--pi_executable") ||
			argument.startsWith("--admin_bridge")
		) {
			legacyCliError(argument);
		} else {
			fail(`unknown argument ${JSON.stringify(argument)}`);
		}
	}
	if (scenarioRequested && command !== "doctor") fail("--scenario is only supported with doctor");
	return { command, configPath, scenarioRequested, versionRequested, helpRequested };
}

function rejectLegacyEnv(): void {
	if ((process.env.PIXIE_ASSISTANT_PORT ?? "").trim() !== "") {
		fail(
			"PIXIE_ASSISTANT_PORT is not supported; set config port and PIXIE_PI_PORT to the same value",
		);
	}
	const hits: string[] = [];
	for (const name of ["PIXIE_PI_EXECUTABLE", "PIXIE_PI_ARGS"]) {
		if ((process.env[name] ?? "").trim() !== "") hits.push(name);
	}
	for (const [name, value] of Object.entries(process.env)) {
		if (name.startsWith("PIXIE_ADMIN_BRIDGE") && (value ?? "").trim() !== "") hits.push(name);
	}
	if (hits.length > 0) {
		fail(
			`legacy environment ${hits.join(", ")} is no longer supported; ` +
				"start the internal assistant only through the archive's pixie_cli launcher",
		);
	}
}

function rejectLegacyConfig(config: Record<string, unknown>): void {
	const hits: string[] = [];
	const legacyKeys = [
		"piExecutable",
		"piArgs",
		"adminBridge",
		"adminBridgeBun",
		"adminBridgeScript",
	];
	for (const key of legacyKeys) {
		if (!Object.hasOwn(config, key)) continue;
		const value = config[key];
		if (typeof value === "string" && value.trim() === "") continue;
		if (typeof value === "boolean" && value === false) continue;
		if (value === undefined || value === null) continue;
		hits.push(key);
	}
	for (const key of Object.keys(config)) {
		if (key.startsWith("adminBridge") && !legacyKeys.includes(key) && !hits.includes(key)) {
			const value = config[key];
			if (value === undefined || value === null) continue;
			if (typeof value === "string" && value.trim() === "") continue;
			if (typeof value === "boolean" && value === false) continue;
			hits.push(key);
		}
	}
	if (hits.length > 0) {
		fail(
			`legacy config ${hits.join(", ")} is no longer supported; ` +
				"start the internal assistant only through the archive's pixie_cli launcher",
		);
	}
}

async function readConfig(configPath: string): Promise<Record<string, unknown>> {
	if (configPath.trim() === "") return {};
	if (!isAbsolute(configPath)) fail("--config must be an absolute path");
	let raw: string;
	try {
		raw = await Bun.file(configPath).text();
	} catch {
		// The configured path itself may contain spaces, so it is never
		// interpolated into the fatal message.
		fail("read config: the selected configuration file is unreadable");
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw as string);
	} catch {
		fail("decode config: not valid JSON");
	}
	if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
		fail("decode config: must be a JSON object");
	}
	return parsed as Record<string, unknown>;
}

const ASSISTANT_CONFIG_FIELDS = new Set([
	"schemaVersion",
	"host",
	"port",
	"agentDir",
	"allowSelfRestart",
]);

/**
 * Validate the archive service configuration independently from environment
 * resolution. Package selection is intentionally not a config field: only an
 * archive wrapper may supply the private bundled path environment.
 */
export function validateAssistantConfig(config: Record<string, unknown>): void {
	if (Object.hasOwn(config, "piPackage")) {
		throw new Error("config piPackage is not accepted; pixie_cli supplies the bundled Pi archive");
	}
	for (const key of Object.keys(config)) {
		if (!ASSISTANT_CONFIG_FIELDS.has(key))
			throw new Error(`config field ${JSON.stringify(key)} is not supported by schemaVersion 2`);
	}
	if (config.schemaVersion !== ASSISTANT_CONFIG_SCHEMA_VERSION)
		throw new Error(`config schemaVersion must be ${ASSISTANT_CONFIG_SCHEMA_VERSION}`);
	if (typeof config.host !== "string" || config.host.trim() === "")
		throw new Error("config host must be a non-empty string");
	if (
		typeof config.port !== "number" ||
		!Number.isInteger(config.port) ||
		config.port < 1 ||
		config.port > 65535
	)
		throw new Error("config port must be an integer port 1-65535");
	if (typeof config.agentDir !== "string" || !isAbsolute(config.agentDir.trim()))
		throw new Error("config agentDir must be an absolute path");
	if (typeof config.allowSelfRestart !== "boolean")
		throw new Error("config allowSelfRestart must be a boolean");
}

export function rejectPublicPiPackageEnvironment(
	environment: Readonly<Record<string, string | undefined>> = process.env,
): void {
	if (Object.hasOwn(environment, "PIXIE_PI_PACKAGE")) {
		throw new Error("PIXIE_PI_PACKAGE is not accepted; pixie_cli supplies the bundled Pi archive");
	}
}

function firstNonEmpty(...values: readonly unknown[]): string {
	for (const value of values) {
		if (typeof value === "string" && value.trim() !== "") return value.trim();
	}
	return "";
}

function expandHomePath(value: string): string {
	const trimmed = value.trim();
	if (trimmed !== "~" && !trimmed.startsWith("~/")) return trimmed;
	const home = homedir().trim();
	if (home === "") return trimmed;
	if (trimmed === "~") return home;
	return `${home}${trimmed.slice(1)}`;
}

function resolveHost(config: Record<string, unknown>): string {
	const value = firstNonEmpty(process.env.PIXIE_ASSISTANT_HOST, config.host, DEFAULT_HOST);
	if (value === "") return DEFAULT_HOST;
	if (
		typeof config.host !== "undefined" &&
		config.host !== null &&
		typeof config.host !== "string"
	) {
		fail("config host must be a string");
	}
	return normalizeAssistantHostValue(value);
}

function resolvePort(config: Record<string, unknown>): number {
	const configured = config.port;
	if (typeof configured !== "number" || !Number.isInteger(configured)) {
		fail("config port must be an integer port 1-65535");
	}
	if (configured < 1 || configured > 65535) {
		fail(`config port must be a port 1-65535, got ${JSON.stringify(configured)}`);
	}
	return configured;
}

function resolveSecret(config: Record<string, unknown>): string {
	if (
		typeof config.secret !== "undefined" &&
		config.secret !== null &&
		typeof config.secret !== "string"
	) {
		fail("config secret must be a string");
	}
	const value = firstNonEmpty(process.env.PIXIE_PI_SECRET_KEY, config.secret);
	if (value.length < MIN_SECRET_LENGTH) {
		fail(`assistant secret must be at least ${MIN_SECRET_LENGTH} characters`);
	}
	activeSecrets.length = 0;
	activeSecrets.push(value);
	return value;
}

function resolveAgentDir(config: Record<string, unknown>): string {
	if (
		typeof config.agentDir !== "undefined" &&
		config.agentDir !== null &&
		typeof config.agentDir !== "string"
	) {
		fail("config agentDir must be a string");
	}
	const raw = firstNonEmpty(process.env.PI_CODING_AGENT_DIR, config.agentDir);
	if (raw === "") fail("agentDir is required via PI_CODING_AGENT_DIR or the agentDir config field");
	const expanded = expandHomePath(raw);
	if (!isAbsolute(expanded)) fail("agentDir must be an absolute path");
	return expanded;
}

function resolveBundledPiPackage(environment: NodeJS.ProcessEnv = process.env): string {
	rejectPublicPiPackageEnvironment(environment);
	const raw = (environment[BUNDLED_PI_PACKAGE_ENVIRONMENT] ?? "").trim();
	if (raw === "") {
		fail("bundled Pi archive path is unavailable; start through pixie_cli or pixie_full");
	}
	if (!isAbsolute(raw)) fail("bundled Pi archive path must be absolute");
	return raw;
}

function resolveAllowSelfRestart(config: Record<string, unknown>): boolean {
	if (
		typeof config.allowSelfRestart !== "undefined" &&
		config.allowSelfRestart !== null &&
		typeof config.allowSelfRestart !== "boolean"
	) {
		fail("config allowSelfRestart must be a boolean");
	}
	if (config.allowSelfRestart === true) return true;
	switch ((process.env.PIXIE_ALLOW_SELF_RESTART ?? "").trim().toLowerCase()) {
		case "1":
		case "true":
		case "yes":
			return true;
		default:
			return false;
	}
}

function resolveConfig(config: Record<string, unknown>): ResolvedConfig {
	try {
		validateAssistantConfig(config);
	} catch (error) {
		fail(errorMessage(error));
	}
	return {
		host: resolveHost(config),
		port: resolvePort(config),
		secret: resolveSecret(config),
		agentDir: resolveAgentDir(config),
		piPackage: resolveBundledPiPackage(),
		allowSelfRestart: resolveAllowSelfRestart(config),
	};
}

function requirePiVersion(verified: VerifiedPiPackage): void {
	const message = piVersionError(verified);
	if (message) fail(message);
}

export function piVersionError(verified: VerifiedPiPackage): string | undefined {
	if (
		verified.packageName !== PI_CODING_AGENT_PACKAGE ||
		verified.packageVersion !== REQUIRED_PI_VERSION
	)
		return (
			`selected Pi package must be ${PI_CODING_AGENT_PACKAGE}@${REQUIRED_PI_VERSION}, ` +
			`got ${verified.packageName}@${verified.packageVersion}`
		);
	return undefined;
}

async function verifiedPiOrFail(piPackage: string): Promise<VerifiedPiPackage> {
	try {
		return await verifyPiPackage(piPackage);
	} catch (error) {
		fail(`selected Pi package verification failed: ${errorMessage(error)}`);
	}
}

/**
 * The scenario must prove a direct host without sharing any mutable Pi state
 * with the configured service. Its caller owns the temporary directory.
 */
export function deriveDoctorScenarioConfig(
	configured: ResolvedConfig,
	agentDir: string,
	secret: string,
): ResolvedConfig {
	if (!isAbsolute(agentDir)) throw new Error("scenario agentDir must be absolute");
	if (secret.length < MIN_SECRET_LENGTH)
		throw new Error(`scenario secret must be at least ${MIN_SECRET_LENGTH} characters`);
	return {
		host: DEFAULT_HOST,
		port: 0,
		secret,
		agentDir,
		piPackage: configured.piPackage,
		allowSelfRestart: false,
	};
}

class DoctorScenarioFailure extends Error {
	constructor(stage: string) {
		super(`doctor scenario failed at ${stage}`);
	}
}

function scenarioRecord(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

async function doctorScenarioStage<T>(stage: string, action: () => Promise<T>): Promise<T> {
	try {
		const result = await action();
		console.log(`${DOCTOR_SCENARIO_PREFIX}: ${stage}: pass`);
		return result;
	} catch {
		// The endpoint and bearer secret must never escape diagnostics, including
		// through a transport-library error message.
		console.error(`${DOCTOR_SCENARIO_PREFIX}: ${stage}: fail`);
		throw new DoctorScenarioFailure(stage);
	}
}

function doctorScenarioHttpUrl(endpoint: string, pathname: string): string {
	const url = new URL(endpoint);
	url.protocol = "http:";
	url.pathname = pathname;
	url.search = "";
	url.hash = "";
	return url.toString();
}

async function doctorScenarioRequest<T>(
	url: string,
	headers: Record<string, string> | undefined,
	inspect: (response: Response) => Promise<T> | T,
): Promise<T> {
	const controller = new AbortController();
	let timedOut = false;
	const timer = setTimeout(() => {
		timedOut = true;
		controller.abort();
	}, DOCTOR_SCENARIO_TIMEOUT_MS);
	try {
		const response = await fetch(url, { headers, signal: controller.signal });
		try {
			return await inspect(response);
		} finally {
			try {
				await response.body?.cancel();
			} catch {
				/* response is already consumed or closed */
			}
		}
	} catch (error) {
		if (timedOut) throw new Error("scenario request timed out");
		throw error;
	} finally {
		clearTimeout(timer);
		controller.abort();
	}
}

async function waitForDoctorScenarioSocket(socket: WebSocket): Promise<void> {
	await new Promise<void>((resolve, reject) => {
		let settled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const finish = (settle: () => void): void => {
			if (settled) return;
			settled = true;
			if (timer !== undefined) clearTimeout(timer);
			settle();
		};
		timer = setTimeout(
			() => finish(() => reject(new Error("websocket open timed out"))),
			DOCTOR_SCENARIO_TIMEOUT_MS,
		);
		socket.onopen = () => finish(resolve);
		socket.onerror = () => finish(() => reject(new Error("websocket open failed")));
		socket.onclose = () => finish(() => reject(new Error("websocket closed before open")));
	});
}

function assertDoctorScenarioReady(value: unknown, packageVersion: string): void {
	if (!scenarioRecord(value)) throw new Error("ready response is not an object");
	if (value.ready !== true || value.protocolVersion !== 1 || value.version !== packageVersion)
		throw new Error("ready response has an incompatible identity");
	if (typeof value.runtimeId !== "string" || value.runtimeId === "")
		throw new Error("ready response has no runtime ID");
	if (typeof value.bootId !== "string" || value.bootId === "")
		throw new Error("ready response has no boot ID");
	const capabilities = value.capabilities;
	if (!scenarioRecord(capabilities) || capabilities.sessions !== 1)
		throw new Error("ready response does not advertise session capability");
	if (!scenarioRecord(value.operationSet)) throw new Error("ready response has no operation set");
}

function assertDoctorScenarioHello(value: unknown): void {
	if (!scenarioRecord(value) || value.id !== 1 || !scenarioRecord(value.result))
		throw new Error("runtime hello response is invalid");
	const capabilities = value.result.capabilities;
	if (!scenarioRecord(capabilities) || capabilities.sessions !== 1)
		throw new Error("runtime hello does not advertise session capability");
	const operationSet = value.result.operationSet;
	if (!scenarioRecord(operationSet)) throw new Error("runtime hello has no operation set");
	for (const operation of ["session.create", "session.load", "session.prompt", "session.cancel"]) {
		if (operationSet[operation] !== true)
			throw new Error(`runtime hello does not enable ${operation}`);
	}
}

async function doctorScenarioRuntimeHello(socket: WebSocket): Promise<void> {
	const response = await new Promise<unknown>((resolve, reject) => {
		let settled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const finish = (settle: () => void): void => {
			if (settled) return;
			settled = true;
			if (timer !== undefined) clearTimeout(timer);
			settle();
		};
		timer = setTimeout(
			() => finish(() => reject(new Error("runtime hello timed out"))),
			DOCTOR_SCENARIO_TIMEOUT_MS,
		);
		socket.onmessage = (event) => {
			try {
				finish(() =>
					resolve(JSON.parse(typeof event.data === "string" ? event.data : String(event.data))),
				);
			} catch {
				finish(() => reject(new Error("runtime hello response is not JSON")));
			}
		};
		socket.onerror = () => finish(() => reject(new Error("runtime hello websocket failed")));
		socket.onclose = () => finish(() => reject(new Error("runtime hello websocket closed")));
		try {
			socket.send(
				JSON.stringify({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } }),
			);
		} catch {
			finish(() => reject(new Error("runtime hello could not be sent")));
		}
	});
	assertDoctorScenarioHello(response);
}

async function cleanupDoctorScenario(
	socket: WebSocket | undefined,
	host: BunHost | undefined,
	agentDir: string | undefined,
): Promise<DoctorScenarioFailure | undefined> {
	let failed = false;
	try {
		socket?.close();
	} catch {
		failed = true;
	}
	try {
		await host?.close();
	} catch {
		failed = true;
	}
	try {
		if (agentDir !== undefined) await rm(agentDir, { recursive: true, force: true });
	} catch {
		failed = true;
	}
	if (failed) {
		console.error(`${DOCTOR_SCENARIO_PREFIX}: cleanup: fail`);
		return new DoctorScenarioFailure("cleanup");
	}
	console.log(`${DOCTOR_SCENARIO_PREFIX}: cleanup: pass`);
	return undefined;
}

async function runDoctorScenario(resolved: ResolvedConfig): Promise<void> {
	console.log(`${DOCTOR_SCENARIO_PREFIX}: configuration validation: pass`);
	let agentDir: string | undefined;
	let host: BunHost | undefined;
	let socket: WebSocket | undefined;
	let cleanupFailure: DoctorScenarioFailure | undefined;
	try {
		const verified = await doctorScenarioStage("Pi package verification", async () => {
			const selected = await verifyPiPackage(resolved.piPackage);
			const versionError = piVersionError(selected);
			if (versionError) throw new Error(versionError);
			return selected;
		});
		agentDir = await doctorScenarioStage("temporary agent directory", () =>
			mkdtemp(join(tmpdir(), "pixie_assistant-doctor-")),
		);
		const scenario = await doctorScenarioStage("disposable configuration", async () =>
			deriveDoctorScenarioConfig(
				resolved,
				agentDir as string,
				randomBytes(MIN_SECRET_LENGTH).toString("hex"),
			),
		);
		const startedHost = await doctorScenarioStage("direct host start", () =>
			startBunHostFromVerifiedPi({
				host: scenario.host,
				port: scenario.port,
				secret: scenario.secret,
				agentDir: scenario.agentDir,
				verifiedPi: verified,
				allowSelfRestart: scenario.allowSelfRestart,
			}),
		);
		host = startedHost;
		await doctorScenarioStage("GET /livez", async () =>
			doctorScenarioRequest(
				doctorScenarioHttpUrl(startedHost.endpoint, "/livez"),
				undefined,
				(response) => {
					if (response.status !== 200) throw new Error("livez did not return HTTP 200");
				},
			),
		);
		await doctorScenarioStage("bearer GET /readyz", async () =>
			doctorScenarioRequest(
				doctorScenarioHttpUrl(startedHost.endpoint, "/readyz"),
				{ Authorization: `Bearer ${scenario.secret}` },
				async (response) => {
					if (response.status !== 200) throw new Error("readyz did not return HTTP 200");
					assertDoctorScenarioReady(await response.json(), verified.packageVersion);
				},
			),
		);
		await doctorScenarioStage("authenticated WebSocket runtime.hello", async () => {
			socket = new WebSocket(startedHost.endpoint, {
				headers: { Authorization: `Bearer ${scenario.secret}` },
			} as unknown as string[]);
			await waitForDoctorScenarioSocket(socket);
			await doctorScenarioRuntimeHello(socket);
		});
	} finally {
		cleanupFailure = await cleanupDoctorScenario(socket, host, agentDir);
	}
	if (cleanupFailure) throw cleanupFailure;
	console.log(`${DOCTOR_SCENARIO_PREFIX}: complete: pass`);
}

async function runDoctor(
	resolved: ResolvedConfig,
	configPath: string,
	scenarioRequested = false,
): Promise<void> {
	if (scenarioRequested) {
		await runDoctorScenario(resolved);
		return;
	}
	const verified = await verifiedPiOrFail(resolved.piPackage);
	requirePiVersion(verified);
	console.log(
		redactHostLogText(
			`pixie_assistant doctor: config=${configPath.trim() === "" ? "(none)" : configPath}`,
			activeSecrets,
		),
	);
	console.log(
		redactHostLogText(
			`host=${resolved.host} port=${resolved.port} agentDir=${resolved.agentDir} ` +
				`piPackage=${resolved.piPackage} allowSelfRestart=${resolved.allowSelfRestart}`,
			activeSecrets,
		),
	);
	console.log(pairedAssistantPortNote(resolved.port));
	console.log(`pi=${verified.packageName}@${verified.packageVersion}`);
}

async function runServe(resolved: ResolvedConfig): Promise<void> {
	const verified = await verifiedPiOrFail(resolved.piPackage);
	requirePiVersion(verified);
	const hostLogger = createHostLogger({
		secrets: [resolved.secret],
		sink: (entry) => {
			// Entries are already redacted by the logger; retain the emit path
			// only for service stderr so an operator can inspect lifecycle.
			console.error(JSON.stringify(entry));
		},
	});
	let host: BunHost;
	try {
		host = await startBunHostFromVerifiedPi({
			host: resolved.host,
			port: resolved.port,
			secret: resolved.secret,
			agentDir: resolved.agentDir,
			verifiedPi: verified,
			allowSelfRestart: resolved.allowSelfRestart,
			logger: hostLogger,
			onRestart: () => {
				// The host drains before invoking this hook; exit with the
				// restart status honored by the packaged systemd unit.
				process.exit(RESTART_EXIT_CODE);
			},
		});
	} catch (error) {
		fail(`assistant host failed to start: ${errorMessage(error)}`);
	}
	console.error(
		`pixie_assistant serving on ${resolved.host}:${resolved.port} ` +
			`with ${verified.packageName}@${verified.packageVersion}`,
	);
	let draining = false;
	const shutdown = (): void => {
		if (draining) return;
		draining = true;
		void (async () => {
			try {
				await Promise.race([
					host.close(),
					new Promise<never>((_resolve, reject) => {
						setTimeout(() => reject(new Error("drain timed out")), DRAIN_TIMEOUT_MS);
					}),
				]);
				process.exit(0);
			} catch (error) {
				console.error(
					`pixie_assistant: shutdown failed: ${redactHostLogText(errorMessage(error), activeSecrets)}`,
				);
				process.exit(1);
			}
		})();
	};
	process.on("SIGINT", shutdown);
	process.on("SIGTERM", shutdown);
}

async function main(): Promise<void> {
	// An inherited secret is known before any argument or config is read, so it
	// is redacted from every early fatal path, not only after resolution.
	const inheritedSecret = (process.env.PIXIE_PI_SECRET_KEY ?? "").trim();
	if (inheritedSecret !== "") {
		activeSecrets.length = 0;
		activeSecrets.push(inheritedSecret);
	}
	const parsed = parseArgs(process.argv.slice(2));
	if (parsed.versionRequested) {
		console.log(versionString());
		return;
	}
	if (parsed.helpRequested) {
		console.log(usage());
		return;
	}
	if (parsed.command === "uninstall") {
		console.log("pixie_assistant uninstall: stop and remove the selected user unit and binary");
		console.log("plan-only: no files, units, or native Pi state were changed");
		return;
	}
	rejectLegacyEnv();
	try {
		rejectPublicPiPackageEnvironment();
	} catch (error) {
		fail(errorMessage(error));
	}
	const config = await readConfig(parsed.configPath);
	rejectLegacyConfig(config);
	const resolved = resolveConfig(config);
	if (parsed.command === "doctor") {
		await runDoctor(resolved, parsed.configPath, parsed.scenarioRequested);
		return;
	}
	await runServe(resolved);
}

if (import.meta.main) {
	try {
		await main();
	} catch (error) {
		console.error(`pixie_assistant: ${fatalServeMessage(error)}`);
		process.exitCode = 1;
	}
}
