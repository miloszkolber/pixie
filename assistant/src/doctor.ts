import { constants } from "node:fs";
import { access, readFile, realpath, stat } from "node:fs/promises";
import {
	assistantFacade,
	describePiInstallation,
	resolveAssistantConfig,
	redactAssistantConfig,
	validateNativeArgv,
	type AssistantConfigRequest,
	type AssistantResolvedConfig,
	type PiInstallationInfo,
} from "./facade.ts";
import { parseAssistantPort, validateAssistantHost, validateAssistantSecret } from "./startup.ts";

export type { AssistantConfigRequest, AssistantResolvedConfig, PiInstallationInfo };

export interface DoctorOptions {
	config?: AssistantResolvedConfig;
	request?: AssistantConfigRequest;
	configPath?: string;
	piExecutable?: string;
	pathEnv?: string;
	allowActiveProbes?: boolean;
}

export interface DoctorCheckResult {
	id: string;
	label: string;
	ok: boolean;
	detail: string;
	remediation?: string;
}

export interface DoctorReport {
	ok: boolean;
	checks: DoctorCheckResult[];
	redactedConfig: Record<string, unknown> | null;
	piInstallation: PiInstallationInfo | null;
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

async function checkConfigFile(configPath: string): Promise<DoctorCheckResult> {
	let raw: string;
	try {
		raw = await readFile(configPath, "utf8");
	} catch (error) {
		return {
			id: "config-file",
			label: "Explicit JSON config is readable",
			ok: false,
			detail: `Cannot read config file: ${errorMessage(error)}`,
			remediation: "Point --config at a readable JSON file or omit it to use CLI/env/defaults.",
		};
	}
	let data: unknown;
	try {
		data = JSON.parse(raw);
	} catch (error) {
		return {
			id: "config-file",
			label: "Explicit JSON config is readable",
			ok: false,
			detail: `Config file is not valid JSON: ${errorMessage(error)}`,
			remediation: "Fix the JSON syntax without adding secrets to URLs or argv.",
		};
	}
	if (!data || typeof data !== "object" || Array.isArray(data))
		return {
			id: "config-file",
			label: "Explicit JSON config is readable",
			ok: false,
			detail: "Config file must contain a JSON object.",
			remediation: "Replace the file content with an object such as {\"port\": 3284}.",
		};
	const record = data as Record<string, unknown>;
	for (const key of ["host", "port", "agentDir", "piExecutable", "piArgv", "llama", "secret"] as const) {
		if (!(key in record)) continue;
		try {
			if (key === "host" && record.host !== undefined) validateAssistantHost(record.host as string);
			if (key === "port" && record.port !== undefined) {
				if (typeof record.port === "number") assistantFacade.validateAssistantRuntimePort(record.port);
				else parseAssistantPort(record.port as string);
			}
			if (key === "secret" && record.secret !== undefined)
				validateAssistantSecret(record.secret as string);
			if (key === "piArgv" && record.piArgv !== undefined) validateNativeArgv(record.piArgv);
			if (key === "agentDir" && record.agentDir !== undefined) {
				const value = record.agentDir;
				if (typeof value !== "string" || value.length === 0 || value.includes("\0"))
					throw new Error("Invalid assistant agent directory");
			}
			if (key === "piExecutable" && record.piExecutable !== undefined) {
				const value = record.piExecutable;
				if (typeof value !== "string" || value.length === 0 || value.includes("\0"))
					throw new Error("Invalid Pi executable path");
			}
			if (key === "llama" && record.llama !== undefined && typeof record.llama !== "boolean")
				throw new Error("Assistant llama flag must be a boolean");
		} catch (error) {
			return {
				id: "config-file",
				label: "Explicit JSON config is readable",
				ok: false,
				detail: `Config field ${key} is invalid: ${errorMessage(error)}`,
				remediation: "Fix the field in the JSON file; CLI still overrides it.",
			};
		}
	}
	const redactedKeys = Object.keys(record).filter((key) => key !== "secret");
	return {
		id: "config-file",
		label: "Explicit JSON config is readable",
		ok: true,
		detail: `Read ${configPath} read-only (keys: ${redactedKeys.join(", ") || "none"}; secret redacted).`,
	};
}

async function checkAgentDir(agentDir: string): Promise<DoctorCheckResult> {
	const base = {
		id: "agent-dir",
		label: "Native agent directory is usable",
	} as const;
	let status;
	try {
		status = await stat(agentDir);
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT")
			return {
				...base,
				ok: true,
				detail: `Fresh native state at ${agentDir}; native first-run will create it. Revalidate after the first run.`,
			};
		return {
			...base,
			ok: false,
			detail: `Cannot inspect agent directory: ${errorMessage(error)}`,
			remediation: "Check the --agent-dir path and directory permissions.",
		};
	}
	if (!status.isDirectory())
		return {
			...base,
			ok: false,
			detail: `Agent directory is not a directory: ${agentDir}`,
			remediation: "Point --agent-dir at a directory; Pi owns its files.",
		};
	try {
		await access(agentDir, constants.R_OK | constants.X_OK);
	} catch (error) {
		return {
			...base,
			ok: false,
			detail: `Agent directory is not readable: ${errorMessage(error)}`,
			remediation: "Grant read/execute permission to the assistant user.",
		};
	}
	try {
		const real = await realpath(agentDir);
		return { ...base, ok: true, detail: `Agent directory is readable at ${real}.` };
	} catch (error) {
		return {
			...base,
			ok: false,
			detail: `Cannot resolve agent directory identity: ${errorMessage(error)}`,
			remediation: "Resolve symlinks and retry; do not move native state.",
		};
	}
}

export async function runAssistantDoctor(options: DoctorOptions = {}): Promise<DoctorReport> {
	const checks: DoctorCheckResult[] = [];
	let resolved: AssistantResolvedConfig | null = null;
	let resolveError: string | null = null;
	if (options.config) {
		resolved = options.config;
	} else {
		try {
			resolved = resolveAssistantConfig(options.request ?? {});
		} catch (error) {
			resolveError = errorMessage(error);
		}
	}
	if (resolved) {
		checks.push({
			id: "cli-config",
			label: "CLI/config/discovery values validate",
			ok: true,
			detail: `Host ${resolved.hostname}:${resolved.port}, agentDir ${resolved.agentDir}, llama ${resolved.llama ? "on" : "off"} (secret redacted).`,
		});
	} else {
		checks.push({
			id: "cli-config",
			label: "CLI/config/discovery values validate",
			ok: false,
			detail: `Invalid CLI/config/discovery: ${resolveError ?? "unknown error"}`,
			remediation: "Fix host (literal loopback), port (1-65535, 0 only for ephemeral runtime), secret (>=16 chars), and agentDir.",
		});
	}

	if (options.configPath !== undefined) {
		checks.push(await checkConfigFile(options.configPath));
	} else {
		checks.push({
			id: "config-file",
			label: "Explicit JSON config is readable",
			ok: true,
			detail: "No explicit config file; using CLI/env/defaults read-only.",
		});
	}

	if (resolved) {
		checks.push(await checkAgentDir(resolved.agentDir));
	} else {
		checks.push({
			id: "agent-dir",
			label: "Native agent directory is usable",
			ok: false,
			detail: "Skipped; resolve CLI/config first.",
			remediation: "Fix the cli-config failure, then rerun the doctor.",
		});
	}

	const piExecutable = options.piExecutable ?? resolved?.piExecutable;
	const pathEnv = options.pathEnv;
	let piInstallation: PiInstallationInfo | null = null;
	try {
		piInstallation = await describePiInstallation({ piExecutable, pathEnv });
	} catch (error) {
		checks.push({
			id: "pi-installation",
			label: "Independent Pi installation resolves",
			ok: false,
			detail: `Pi installation check failed: ${errorMessage(error)}`,
			remediation: "Install Pi for this user or set an explicit executable path.",
		});
	}
	if (piInstallation) {
		if (piInstallation.executable) {
			checks.push({
				id: "pi-installation",
				label: "Independent Pi installation resolves",
				ok: true,
				detail: `${piInstallation.kind} Pi (${piInstallation.sdkVersion ?? "unknown SDK"}) at ${piInstallation.executable.realPath} via ${piInstallation.executable.source}.`,
			});
		} else {
			checks.push({
				id: "pi-installation",
				label: "Independent Pi installation resolves",
				ok: false,
				detail: `No usable Pi installation: ${piInstallation.executableError ?? "unknown"}`,
				remediation:
					"Install the npm Pi package or a standalone Pi binary for this user, check non-login PATH, or set an explicit piExecutable.",
			});
		}
		const sdkDetail =
			piInstallation.sdkVersion !== null
				? `Pi SDK ${piInstallation.sdkVersion} visible (${piInstallation.kind}); binary distributions may expose a different import surface.`
				: "Pi SDK import surface not visible; standalone binaries may still work via the executable.";
		checks.push({
			id: "pi-distribution",
			label: "Pi distribution import surface is explicit",
			ok: piInstallation.sdkVersion !== null || piInstallation.executable !== null,
			detail: sdkDetail,
			...(piInstallation.sdkVersion === null && piInstallation.executable === null
				? { remediation: "Install a supported Pi distribution; version strings alone do not prove API support." }
				: {}),
		});
	}

	checks.push({
		id: "read-only-guarantee",
		label: "Doctor stays read-only",
		ok: true,
		detail:
			"Doctor used only stat/readFile/realpath/access plus config validation; no mkdir/write/install, no extension loading, no provider prompt, no native writes.",
	});

	if (options.allowActiveProbes === true) {
		let detail = "Active probe requested; ";
		if (!resolved) {
			detail += "skipped live check without valid config.";
		} else {
			const controller = new AbortController();
			const timer = setTimeout(() => controller.abort(), 2000);
			try {
				const response = await fetch(`http://${resolved.hostname}:${resolved.port}/livez`, {
					signal: controller.signal,
				}).catch(() => null);
				detail += response
					? `assistant /livez answered ${response.status}.`
					: "assistant /livez unreachable from this host.";
			} finally {
				clearTimeout(timer);
			}
		}
		checks.push({
			id: "active-probes",
			label: "Active probes are explicitly opt-in",
			ok: true,
			detail,
		});
	} else {
		checks.push({
			id: "active-probes",
			label: "Active probes are explicitly opt-in",
			ok: true,
			detail: "Skipped live network/model probes (read-only default; pass allowActiveProbes to enable).",
		});
	}

	const redactedConfig = resolved ? redactAssistantConfig(resolved) : null;
	return { ok: checks.every((check) => check.ok), checks, redactedConfig, piInstallation };
}

export const assistantDoctor = {
	runAssistantDoctor,
};
