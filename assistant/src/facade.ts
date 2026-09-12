import { constants } from "node:fs";
import { access, readFile, realpath, stat } from "node:fs/promises";
import { createRequire } from "node:module";
import { isAbsolute, join, resolve } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";
import manifest from "../package.json" with { type: "json" };
import {
	ADMIN_FEATURES,
	ADMIN_OPERATIONS,
	evaluateAdminProfiles,
	operationSupport,
} from "./admin-profiles/index.ts";
import {
	assessCompatibility,
	CURRENT_PROTOCOL_VERSION,
	CURRENT_STATE_SCHEMA_VERSION,
	checkCompatibility,
	decideCompatibility,
	decideLegacyCompatibility,
	planRollback,
	planSchemaRollback,
	stagedRollback,
} from "./compatibility/index.ts";
import { startHost } from "./server.ts";
import {
	parseAssistantPort,
	validateAssistantHost,
	validateAssistantRuntimePort,
	validateAssistantSecret,
} from "./startup.ts";

export type {
	AdminOperationStatus,
	AdminProfileEvidence,
	AdminProfileReport,
} from "./admin-profiles/types.ts";
export type {
	CompatibilityDecision,
	CompatibilityInput,
	RollbackInput,
	RollbackPlan,
	RollbackRun,
} from "./compatibility/types.ts";
export {
	ADMIN_FEATURES,
	ADMIN_OPERATIONS,
	assessCompatibility,
	CURRENT_PROTOCOL_VERSION,
	CURRENT_STATE_SCHEMA_VERSION,
	checkCompatibility,
	decideCompatibility,
	decideLegacyCompatibility,
	evaluateAdminProfiles,
	operationSupport,
	parseAssistantPort,
	planRollback,
	planSchemaRollback,
	stagedRollback,
	startHost,
	validateAssistantHost,
	validateAssistantRuntimePort,
	validateAssistantSecret,
};

export const ASSISTANT_PROTOCOL_VERSION = 1;

const RESERVED_NATIVE_FLAGS = new Set([
	"--mode",
	"-m",
	"--session",
	"--session-id",
	"-s",
	"--resume",
	"-r",
	"--continue",
	"-c",
	"--print",
	"-p",
	"--no-session",
	"--no_session",
	"--cwd",
	"--working-dir",
	"--workdir",
	"-C",
]);

export interface AssistantCliInput {
	host?: string;
	port?: string;
	agentDir?: string;
	piExecutable?: string;
	piArgv?: string[];
	llama?: boolean;
}

export interface AssistantFileConfig {
	host?: unknown;
	port?: unknown;
	agentDir?: unknown;
	secret?: unknown;
	piExecutable?: unknown;
	piArgv?: unknown;
	llama?: unknown;
}

export interface AssistantEnvInput {
	pixiePiSecretKey?: string;
	piCodingAgentDir?: string;
	assistantHost?: string;
	assistantPort?: string;
}

export interface AssistantConfigRequest {
	cli?: AssistantCliInput;
	env?: AssistantEnvInput;
	file?: AssistantFileConfig;
}

export interface AssistantResolvedConfig {
	hostname: string;
	port: number;
	agentDir: string;
	secret: string;
	llama: boolean;
	piExecutable?: string;
	piArgv: readonly string[];
}

export interface PiExecutableResolution {
	requested: string;
	resolved: string;
	realPath: string;
	source: "explicit" | "path";
}

export type PiDistributionKind = "npm" | "standalone" | "unknown";

export interface PiInstallationInfo {
	kind: PiDistributionKind;
	sdkVersion: string | null;
	sdkPath: string | null;
	executable: PiExecutableResolution | null;
	executableError: string | null;
}

export interface AssistantBuildInfo {
	assistantVersion: string;
	piSdkVersion: string;
	protocolVersion: number;
}

export type AssistantHostHandle = Awaited<ReturnType<typeof startHost>>;

function asOptionalString(value: unknown): string | undefined {
	return typeof value === "string" && value.length > 0 ? value : undefined;
}

function requireAgentDir(value: unknown, fallback: string): string {
	const candidate = typeof value === "string" && value.length > 0 ? value : fallback;
	if (candidate.includes("\0")) throw new Error("Invalid assistant agent directory");
	const resolved = resolve(candidate);
	if (!isAbsolute(resolved)) throw new Error("Invalid assistant agent directory");
	return resolved;
}

export function validateNativeArgv(argv: unknown): readonly string[] {
	if (argv === undefined) return [];
	if (!Array.isArray(argv)) throw new Error("Native argv must be an operator array");
	const checked: string[] = [];
	for (const entry of argv) {
		if (typeof entry !== "string" || entry.length === 0 || entry.includes("\0"))
			throw new Error("Native argv must be an operator array of non-empty strings");
		const flag = entry.split("=", 1)[0] ?? entry;
		if (RESERVED_NATIVE_FLAGS.has(flag))
			throw new Error(`Native argv reserves ${flag}; use the assistant session API instead`);
		checked.push(entry);
	}
	return checked;
}

function resolvePort(
	cliPort: string | undefined,
	envPort: string | undefined,
	filePort: unknown,
): number {
	if (cliPort !== undefined) return parseAssistantPort(cliPort);
	if (envPort !== undefined && envPort !== "") {
		if (envPort === "0") return validateAssistantRuntimePort(0);
		return parseAssistantPort(envPort);
	}
	if (typeof filePort === "number") return validateAssistantRuntimePort(filePort);
	if (typeof filePort === "string") {
		if (filePort === "0") return validateAssistantRuntimePort(0);
		return parseAssistantPort(filePort);
	}
	if (filePort !== undefined) throw new Error("Assistant port must be an integer from 0 to 65535");
	return parseAssistantPort(undefined);
}

function resolveLlama(cli: boolean | undefined, file: unknown): boolean {
	if (cli !== undefined) return cli;
	if (typeof file === "boolean") return file;
	if (file !== undefined) throw new Error("Assistant llama flag must be a boolean");
	return false;
}

export function resolveAssistantConfig(
	request: AssistantConfigRequest = {},
): AssistantResolvedConfig {
	const cli = request.cli ?? {};
	const env = request.env ?? {};
	const file = request.file ?? {};
	const hostname = validateAssistantHost(
		cli.host ?? env.assistantHost ?? asOptionalString(file.host),
	);
	const port = resolvePort(cli.port, env.assistantPort, file.port);
	const secretSource =
		env.pixiePiSecretKey ?? (typeof file.secret === "string" ? file.secret : undefined);
	const secret = validateAssistantSecret(secretSource);
	const agentDir = requireAgentDir(
		cli.agentDir ?? env.piCodingAgentDir ?? asOptionalString(file.agentDir),
		getAgentDir(),
	);
	const piExecutable = cli.piExecutable ?? asOptionalString(file.piExecutable) ?? undefined;
	if (piExecutable !== undefined && piExecutable.includes("\0"))
		throw new Error("Invalid Pi executable path");
	const piArgv = validateNativeArgv(cli.piArgv ?? (file.piArgv as unknown) ?? undefined);
	const llama = resolveLlama(cli.llama, file.llama);
	return {
		hostname,
		port,
		agentDir,
		secret,
		llama,
		...(piExecutable !== undefined ? { piExecutable } : {}),
		piArgv,
	};
}

export function defaultAssistantEnv(): AssistantEnvInput {
	return {
		pixiePiSecretKey: process.env.PIXIE_PI_SECRET_KEY,
		piCodingAgentDir: process.env.PI_CODING_AGENT_DIR,
		assistantHost: process.env.PIXIE_ASSISTANT_HOST,
		assistantPort: process.env.PIXIE_ASSISTANT_PORT,
	};
}

async function checkExecutableFile(resolved: string): Promise<void> {
	const status = await stat(resolved);
	if (!status.isFile()) throw new Error(`Pi executable is not a file: ${resolved}`);
	await access(resolved, constants.X_OK).catch(() => {
		throw new Error(`Pi executable is not executable: ${resolved}`);
	});
}

async function checkInterpreter(resolved: string): Promise<void> {
	let prefix: string;
	try {
		const handle = await readFile(resolved, "utf8").then(
			(text) => text.slice(0, 1024),
			() => "",
		);
		prefix = handle;
	} catch {
		return;
	}
	if (!prefix.startsWith("#!")) return;
	const firstLine = prefix.split("\n", 1)[0]?.slice(2).trim() ?? "";
	const interpreter = firstLine.split(/\s+/)[0];
	if (!interpreter) return;
	const candidate =
		interpreter === "/usr/bin/env"
			? (prefix.split("\n", 1)[0]?.trim().split(/\s+/)[1] ?? "")
			: interpreter;
	if (!candidate) return;
	const resolvedInterpreter = candidate.startsWith("/") ? candidate : `/usr/bin/${candidate}`;
	try {
		await access(resolvedInterpreter, constants.X_OK);
	} catch {
		throw new Error(`Pi interpreter missing: ${resolvedInterpreter} (for ${resolved})`);
	}
}

export async function resolvePiExecutable(
	explicit?: string,
	pathEnv?: string,
): Promise<PiExecutableResolution> {
	if (explicit !== undefined) {
		if (explicit.length === 0 || explicit.includes("\0"))
			throw new Error("Invalid Pi executable path");
		const resolved = resolve(explicit);
		try {
			await checkExecutableFile(resolved);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT")
				throw new Error(`Pi executable not found: ${resolved} (explicit piExecutable)`);
			throw error instanceof Error ? error : new Error(`Pi executable not found: ${resolved}`);
		}
		await checkInterpreter(resolved);
		const realPath = await realpath(resolved).catch(() => resolved);
		return { requested: explicit, resolved, realPath, source: "explicit" };
	}
	const pathValue = pathEnv ?? process.env.PATH ?? "";
	const directories = pathValue.split(":");
	const searched = directories.filter((dir) => dir.length > 0);
	let lastError: string | null = null;
	for (const dir of searched) {
		const candidate = join(dir, "pi");
		try {
			await checkExecutableFile(candidate);
			await checkInterpreter(candidate);
			const realPath = await realpath(candidate).catch(() => candidate);
			return { requested: "pi", resolved: candidate, realPath, source: "path" };
		} catch (error) {
			if (error instanceof Error && error.message.startsWith("Pi interpreter missing")) throw error;
			lastError = error instanceof Error ? error.message : String(error);
		}
	}
	throw new Error(
		`Pi executable not found on PATH (searched ${searched.length} directories${lastError ? `; last: ${lastError}` : ""})`,
	);
}

function sdkPackagePath(): string | null {
	try {
		return createRequire(import.meta.url).resolve("@earendil-works/pi-coding-agent/package.json");
	} catch {
		return null;
	}
}

export async function describeAssistantBuild(): Promise<AssistantBuildInfo> {
	const assistantVersion =
		typeof manifest.version === "string" && manifest.version.length > 0
			? manifest.version
			: "0.0.0-dev";
	const sdkPath = sdkPackagePath();
	let piSdkVersion = "unknown";
	if (sdkPath) {
		try {
			const raw = await readFile(sdkPath, "utf8");
			const data = JSON.parse(raw) as { version?: unknown };
			if (typeof data.version === "string" && data.version.length > 0) piSdkVersion = data.version;
		} catch {
			piSdkVersion = "unknown";
		}
	}
	return { assistantVersion, piSdkVersion, protocolVersion: ASSISTANT_PROTOCOL_VERSION };
}

export async function describePiInstallation(
	options: { piExecutable?: string; pathEnv?: string } = {},
): Promise<PiInstallationInfo> {
	const sdkPath = sdkPackagePath();
	let sdkVersion: string | null = null;
	if (sdkPath) {
		try {
			const raw = await readFile(sdkPath, "utf8");
			const data = JSON.parse(raw) as { version?: unknown };
			sdkVersion = typeof data.version === "string" ? data.version : null;
		} catch {
			sdkVersion = null;
		}
	}
	let executable: PiExecutableResolution | null = null;
	let executableError: string | null = null;
	try {
		executable = await resolvePiExecutable(options.piExecutable, options.pathEnv);
	} catch (error) {
		executableError = error instanceof Error ? error.message : String(error);
	}
	let kind: PiDistributionKind = "unknown";
	if (sdkVersion && executable) {
		kind =
			executable.realPath.includes("node_modules") || executable.resolved.endsWith(".js")
				? "npm"
				: executable.realPath.includes("node_modules")
					? "npm"
					: "standalone";
		if (executable.realPath.includes("node_modules")) kind = "npm";
		else if (sdkPath && executable.realPath.startsWith(resolve(sdkPath, "..", ".."))) kind = "npm";
		else kind = executable.resolved.endsWith(".js") ? "npm" : "standalone";
	} else if (sdkVersion) {
		kind = "npm";
	} else if (executable) {
		kind =
			executable.resolved.endsWith(".js") || executable.realPath.includes("node_modules")
				? "npm"
				: "standalone";
	}
	return { kind, sdkVersion, sdkPath, executable, executableError };
}

export function redactAssistantConfig(resolved: AssistantResolvedConfig): Record<string, unknown> {
	return {
		hostname: resolved.hostname,
		port: resolved.port,
		agentDir: resolved.agentDir,
		llama: resolved.llama,
		...(resolved.piExecutable !== undefined ? { piExecutable: resolved.piExecutable } : {}),
		piArgv: [...resolved.piArgv],
		secret: { present: true, length: resolved.secret.length, redacted: "***" },
	};
}

export async function startAssistantHost(
	resolved: AssistantResolvedConfig & {
		allowSelfRestart?: boolean;
		onRestart?: () => void;
		startupDeadlineMs?: number;
		adminDeadlineMs?: number;
		drainDeadlineMs?: number;
	},
): Promise<AssistantHostHandle> {
	return startHost({
		agentDir: resolved.agentDir,
		secret: resolved.secret,
		hostname: resolved.hostname,
		port: resolved.port,
		llama: resolved.llama,
		allowSelfRestart: resolved.allowSelfRestart,
		onRestart: resolved.onRestart,
		startupDeadlineMs: resolved.startupDeadlineMs,
		adminDeadlineMs: resolved.adminDeadlineMs,
		drainDeadlineMs: resolved.drainDeadlineMs,
	});
}

export const assistantFacade = {
	protocolVersion: ASSISTANT_PROTOCOL_VERSION,
	resolveAssistantConfig,
	defaultAssistantEnv,
	validateNativeArgv,
	resolvePiExecutable,
	describeAssistantBuild,
	describePiInstallation,
	redactAssistantConfig,
	ADMIN_FEATURES,
	ADMIN_OPERATIONS,
	evaluateAdminProfiles,
	operationSupport,
	assessCompatibility,
	checkCompatibility,
	decideCompatibility,
	decideLegacyCompatibility,
	planRollback,
	planSchemaRollback,
	stagedRollback,
	startAssistantHost,
	startHost,
	parseAssistantPort,
	validateAssistantHost,
	validateAssistantRuntimePort,
	validateAssistantSecret,
};
