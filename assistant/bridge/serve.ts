/**
 * Opt-in administration bridge sidecar (foundation + FC17).
 *
 * The Go assistant cannot load the selected Pi installation's SDK in-process,
 * so an operator may opt into this sidecar. It resolves exactly one selected
 * installation from an explicit `--package`/`--agent-dir` argument (or the
 * matching environment variable), dynamically imports that installation's
 * public entrypoint, verifies its package identity/version, and then serves a
 * bounded newline-delimited JSON protocol on stdin/stdout:
 *
 *   request  {"id": <id>, "method": <string>, "params": <object>}
 *   response {"id": <id>, "ok": true,  "result": <value>}
 *            {"id": <id>, "ok": false, "error": <string>}
 *
 * Diagnostics are written to stderr only. There is no eval, no shell, and no
 * arbitrary code from the controlling side: methods come from a fixed
 * allowlist and parameters are validated. If the selected installation cannot
 * be resolved or verified the process prints a diagnostic and exits non-zero.
 * It never falls back to a global or bundled SDK.
 */

import { readFile, stat } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

export const PI_CODING_AGENT_PACKAGE = "@earendil-works/pi-coding-agent";
export const BRIDGE_PROTOCOL_VERSION = 1;
export const BRIDGE_MAX_FRAME_BYTES = 1024 * 1024;
export const BRIDGE_MAX_ID_LENGTH = 128;
export const BRIDGE_MAX_METHOD_LENGTH = 128;
export const BRIDGE_MAX_PACKAGE_JSON_BYTES = 64 * 1024;
const PROVIDER_LIST_AVAILABLE_TIMEOUT_MS = 10_000;
const PROVIDER_AUTH_TIMEOUT_MS = 5_000;
const PROVIDER_REFRESH_TIMEOUT_MS = 25_000;
const MODEL_INFO_CURRENCY = "USD";

/** Methods this foundation task implements. Everything else fails closed. */
export const BRIDGE_METHODS = [
	"bridge.hello",
	"pi.providers.list",
	"pi.providers.readiness.check",
	"pi.providers.inventory.refresh",
	"pi.providers.canonical-model-info",
] as const;
export type BridgeMethod = (typeof BRIDGE_METHODS)[number];

export interface BridgeRequest {
	readonly id: number | string;
	readonly method: string;
	readonly params: Record<string, unknown>;
}

export type BridgeResponse =
	| { readonly id: number | string; readonly ok: true; readonly result: unknown }
	| { readonly id: number | string; readonly ok: false; readonly error: string };

export interface PiInstallation {
	readonly packageName: string;
	readonly packageVersion: string;
	readonly packageDir: string;
	/** Absolute filesystem path to the verified public entrypoint. */
	readonly entryPath: string;
	readonly moduleOrigin: string;
}

export interface BridgeHello {
	readonly protocolVersion: number;
	readonly packageName: string;
	readonly packageVersion: string;
	readonly packageDir: string;
	readonly moduleOrigin: string;
	/** Public runtime symbols confirmed present on the selected installation. */
	readonly publicSymbols: readonly string[];
}

function record(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
	if (error instanceof Error && error.message) return error.message;
	return typeof error === "string" && error ? error : "bridge request failed";
}

function encodedBytes(value: string): number {
	return new TextEncoder().encode(value).byteLength;
}

function validEntrypoint(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.length > 0 &&
		!value.includes("\0") &&
		!/^[a-z][a-z0-9+.-]*:\/\//i.test(value)
	);
}

/** Parse and validate one JSONL request frame. Throws on malformed input. */
export function parseRequest(line: string): BridgeRequest {
	const text = line.endsWith("\r") ? line.slice(0, -1) : line;
	if (text.trim().length === 0) throw new Error("empty bridge frame");
	if (encodedBytes(text) + 1 > BRIDGE_MAX_FRAME_BYTES)
		throw new Error(`bridge frame exceeds ${BRIDGE_MAX_FRAME_BYTES} bytes`);
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch {
		throw new Error("invalid bridge JSON frame");
	}
	if (!record(parsed)) throw new Error("bridge frame must be an object");
	const { id, method } = parsed;
	const numericID = typeof id === "number" && Number.isSafeInteger(id) && id > 0;
	const stringID =
		typeof id === "string" && id.length > 0 && id.length <= BRIDGE_MAX_ID_LENGTH && !id.includes("\0");
	if (!numericID && !stringID) throw new Error("invalid bridge request id");
	if (
		typeof method !== "string" ||
		method.length === 0 ||
		method.length > BRIDGE_MAX_METHOD_LENGTH ||
		method.includes("\0")
	)
		throw new Error("invalid bridge request method");
	const rawParams = parsed.params === undefined || parsed.params === null ? {} : parsed.params;
	if (!record(rawParams)) throw new Error("bridge request params must be an object");
	return { id: id as number | string, method, params: rawParams };
}

/** Encode one bounded JSONL response frame. Newline is owned here. */
export function encodeResponse(response: BridgeResponse): string {
	const json = JSON.stringify(response);
	if (encodedBytes(json) + 1 > BRIDGE_MAX_FRAME_BYTES)
		throw new Error(`bridge response exceeds ${BRIDGE_MAX_FRAME_BYTES} bytes`);
	return `${json}\n`;
}

function exportEntry(value: unknown): string | undefined {
	if (validEntrypoint(value)) return value;
	if (record(value)) {
		for (const key of ["import", "node", "default", "require"]) {
			const candidate = exportEntry(value[key]);
			if (candidate) return candidate;
		}
	}
	return undefined;
}

function manifestEntry(manifest: Record<string, unknown>): string | undefined {
	const exportsField = manifest.exports;
	if (validEntrypoint(exportsField)) return exportsField;
	if (record(exportsField)) {
		const root = exportEntry(exportsField["."]);
		if (root) return root;
	}
	if (validEntrypoint(manifest.module)) return manifest.module;
	if (validEntrypoint(manifest.main)) return manifest.main;
	return undefined;
}

async function readPackageManifest(packageDir: string): Promise<Record<string, unknown>> {
	const path = join(packageDir, "package.json");
	const info = await stat(path).catch(() => undefined);
	if (!info || !info.isFile() || info.size === 0 || info.size > BRIDGE_MAX_PACKAGE_JSON_BYTES)
		throw new Error("selected installation is missing a bounded package.json");
	let parsed: unknown;
	try {
		parsed = JSON.parse((await readFile(path)).toString("utf8"));
	} catch {
		throw new Error("selected installation package.json is not valid JSON");
	}
	if (!record(parsed)) throw new Error("selected installation package.json is not an object");
	return parsed;
}

/**
 * Resolve and verify exactly one selected installation. `packagePath` must be
 * an absolute directory or the absolute path to its package.json. Relative or
 * missing paths are rejected rather than searched from a global location.
 */
export async function resolveInstallation(packagePath: string): Promise<PiInstallation> {
	const raw = typeof packagePath === "string" ? packagePath.trim() : "";
	if (!raw || raw.includes("\0")) throw new Error("administration bridge requires a package path");
	if (!isAbsolute(raw)) throw new Error("administration bridge package path must be absolute");
	let packageDir = resolve(raw);
	const info = await stat(packageDir).catch(() => undefined);
	if (info?.isFile()) packageDir = dirname(packageDir);
	else if (!info?.isDirectory())
		throw new Error(`selected installation does not exist: ${raw}`);
	const manifest = await readPackageManifest(packageDir);
	if (manifest.name !== PI_CODING_AGENT_PACKAGE)
		throw new Error(`selected installation is not ${PI_CODING_AGENT_PACKAGE}`);
	const version = manifest.version;
	if (
		typeof version !== "string" ||
		!/^\d+\.\d+\.\d+(?:[-+][a-z0-9.+-]+)?$/i.test(version)
	)
		throw new Error("selected installation has no valid package version");
	const entry = manifestEntry(manifest);
	if (!entry) throw new Error("selected installation exposes no public entrypoint");
	const entryPath = resolve(packageDir, entry);
	if (entryPath !== packageDir && !entryPath.startsWith(packageDir + sep))
		throw new Error("selected installation entrypoint escapes its package directory");
	const entryInfo = await stat(entryPath).catch(() => undefined);
	if (!entryInfo?.isFile()) throw new Error("selected installation entrypoint is not a file");
	return {
		packageName: manifest.name,
		packageVersion: version,
		packageDir,
		entryPath,
		moduleOrigin: entryPath,
	};
}

/** Dynamically import only the verified selected installation entrypoint. */
export async function loadPublicApi(
	installation: PiInstallation,
): Promise<Record<string, unknown>> {
	const loaded = await import(pathToFileURL(installation.entryPath).href);
	if (!record(loaded)) throw new Error("selected installation public entrypoint is not a module");
	return loaded;
}

function requiredText(params: Record<string, unknown>, key: string): string {
	const value = params[key];
	if (typeof value !== "string" || value.length === 0 || value.includes("\0"))
		throw new Error(`bridge parameter ${key} is required`);
	return value;
}

function textArray(params: Record<string, unknown>, key: string): string[] {
	const value = params[key];
	if (value === undefined || value === null) return [];
	if (!Array.isArray(value)) throw new Error(`bridge parameter ${key} must be an array`);
	return value.map((entry) => {
		if (typeof entry !== "string" || entry.length === 0 || entry.includes("\0"))
			throw new Error(`bridge parameter ${key} entries must be non-empty strings`);
		return entry;
	});
}

/**
 * The ModelRuntime surfaced to the bridge methods. It is created lazily so a
 * `bridge.hello` identity check never touches native credential files.
 */
export interface BridgeRuntime {
	getAvailable(provider?: unknown, options?: unknown): Promise<Array<Record<string, unknown>>>;
	getProviders(): Array<Record<string, unknown>>;
	checkAuth(providerId: string, options?: unknown): Promise<unknown>;
	getModels(providerId: string): Array<Record<string, unknown>>;
	getModel(provider: string, model: string): Record<string, unknown> | undefined;
	refresh(options?: unknown): Promise<{ errors: Map<string, unknown> }>;
}

export interface AdminBridge {
	readonly installation: PiInstallation;
	readonly hello: BridgeHello;
	dispatch(method: string, params: Record<string, unknown>): Promise<unknown>;
	close(): void;
}

export interface CreateBridgeOptions {
	readonly installation: PiInstallation;
	readonly module: Record<string, unknown>;
	readonly agentDir: string;
}

/** Build a bridge over a verified installation's public `ModelRuntime`. */
export function createBridge(options: CreateBridgeOptions): AdminBridge {
	const { installation, module, agentDir } = options;
	if (typeof agentDir !== "string" || !isAbsolute(agentDir) || agentDir.includes("\0"))
		throw new Error("administration bridge requires an absolute agent directory");
	const ModelRuntime = module.ModelRuntime as
		| { create?: (config: Record<string, unknown>) => Promise<BridgeRuntime> }
		| undefined;
	if (!ModelRuntime || typeof ModelRuntime.create !== "function")
		throw new Error("selected installation does not expose public ModelRuntime");
	let runtime: BridgeRuntime | undefined;
	const runtimeFor = async (): Promise<BridgeRuntime> => {
		if (!runtime) {
			runtime = await ModelRuntime.create!({
				authPath: join(agentDir, "auth.json"),
				modelsPath: join(agentDir, "models.json"),
				allowModelNetwork: false,
			});
		}
		if (!runtime) throw new Error("selected installation returned no ModelRuntime");
		return runtime;
	};
	const hello: BridgeHello = {
		protocolVersion: BRIDGE_PROTOCOL_VERSION,
		packageName: installation.packageName,
		packageVersion: installation.packageVersion,
		packageDir: installation.packageDir,
		moduleOrigin: installation.moduleOrigin,
		publicSymbols: ["ModelRuntime"],
	};

	const inventory = async (providerIds: string[]): Promise<Record<string, unknown>> => {
		const active = await runtimeFor();
		const available = await active.getAvailable(undefined, {
			signal: AbortSignal.timeout(PROVIDER_LIST_AVAILABLE_TIMEOUT_MS),
		});
		const ready = new Set(available.map((model) => model.provider));
		const entries = await Promise.all(
			active
				.getProviders()
				.filter((provider) => !providerIds.length || providerIds.includes(String(provider.id)))
				.map(async (provider) => {
					const id = String(provider.id);
					const auth = await active.checkAuth(id, {
						signal: AbortSignal.timeout(PROVIDER_AUTH_TIMEOUT_MS),
					});
					const authInfo = (provider.auth ?? {}) as Record<string, any>;
					const apiKeyLogin = Boolean(authInfo.apiKey?.login);
					const oauth = Boolean(authInfo.oauth);
					return {
						providerId: id,
						providerName: String(provider.name ?? id),
						configured: Boolean(auth),
						readinessCheck: true,
						available: ready.has(id),
						canOAuth: oauth,
						canApiKey: apiKeyLogin,
						configKeys: [
							...(apiKeyLogin
								? [{ name: "api_key", secret: true, required: true, primary: true }]
								: []),
							...(oauth ? [{ name: "oauth", oauthFlow: true }] : []),
						],
						models: active.getModels(id).map((model) => ({
							id: model.id,
							name: model.name,
							contextLimit: model.contextWindow,
							maxOutputTokens: model.maxTokens,
							reasoning: model.reasoning,
							modalities: model.input,
						})),
					};
				}),
		);
		return { entries };
	};

	const canonicalModelInfo = async (params: Record<string, unknown>): Promise<Record<string, unknown>> => {
		const active = await runtimeFor();
		const provider = requiredText(params, "provider");
		const model = requiredText(params, "model");
		const found = active.getModel(provider, model);
		if (!found) throw new Error("Unknown model");
		const cost = (found.cost ?? {}) as Record<string, unknown>;
		return {
			modelInfo: {
				provider,
				model,
				contextLimit: found.contextWindow,
				maxOutputTokens: found.maxTokens,
				reasoning: found.reasoning,
				currency: MODEL_INFO_CURRENCY,
				inputTokenCost: cost.input,
				outputTokenCost: cost.output,
				cacheReadTokenCost: cost.cacheRead,
				cacheWriteTokenCost: cost.cacheWrite,
			},
		};
	};

	const readinessCheck = async (params: Record<string, unknown>): Promise<Record<string, unknown>> => {
		const active = await runtimeFor();
		const providerId = requiredText(params, "providerId");
		const configured = Boolean(
			await active.checkAuth(providerId, { signal: AbortSignal.timeout(PROVIDER_AUTH_TIMEOUT_MS) }),
		);
		return { providerId, ready: configured, hasIssue: !configured, verified: false };
	};

	const inventoryRefresh = async (): Promise<Record<string, unknown>> => {
		const active = await runtimeFor();
		const result = await active.refresh({
			allowNetwork: true,
			signal: AbortSignal.timeout(PROVIDER_REFRESH_TIMEOUT_MS),
		});
		const errors = result && result.errors instanceof Map ? [...result.errors.keys()] : [];
		return { started: [], skipped: [], errors };
	};

	const dispatch = async (method: string, params: Record<string, unknown>): Promise<unknown> => {
		switch (method) {
			case "bridge.hello":
				return hello;
			case "pi.providers.list":
				return inventory(textArray(params, "providerIds"));
			case "pi.providers.canonical-model-info":
				return canonicalModelInfo(params);
			case "pi.providers.readiness.check":
				return readinessCheck(params);
			case "pi.providers.inventory.refresh":
				return inventoryRefresh();
			default:
				throw new Error(`Unsupported bridge method: ${method}`);
		}
	};

	return {
		installation,
		hello,
		dispatch,
		close() {
			runtime = undefined;
		},
	};
}

export interface BridgeServer {
	handle(line: string): Promise<BridgeResponse | undefined>;
}

/**
 * Frame handler shared by the process entrypoint and its tests. A malformed
 * frame throws so the caller fails closed; a method failure is returned as a
 * bounded error response and keeps the channel open.
 */
export function createBridgeServer(
	bridge: AdminBridge,
	diagnostic: (message: string) => void = () => {},
): BridgeServer {
	return {
		async handle(line: string) {
			let request: BridgeRequest;
			try {
				request = parseRequest(line);
			} catch (error) {
				diagnostic(`bridge: ${errorMessage(error)}`);
				throw error;
			}
			try {
				const result = await bridge.dispatch(request.method, request.params);
				return { id: request.id, ok: true, result };
			} catch (error) {
				return { id: request.id, ok: false, error: errorMessage(error) };
			}
		},
	};
}

/** Read one `--flag value` or `--flag=value` argument from argv. */
export function argumentValue(argv: readonly string[], flag: string): string {
	for (let index = 0; index < argv.length; index++) {
		const argument = argv[index];
		if (argument === flag) {
			const next = argv[index + 1];
			if (next !== undefined && !next.startsWith("--")) return next;
		} else if (argument.startsWith(`${flag}=`)) {
			return argument.slice(flag.length + 1);
		}
	}
	return "";
}

/** Resolve the selected installation path, then the documented environment. */
export function resolvePackagePath(argv: readonly string[]): string {
	return argumentValue(argv, "--package") || process.env.PIXIE_PI_PACKAGE || "";
}

/** Resolve the selected agent directory, then the documented environment. */
export function resolveAgentDir(argv: readonly string[]): string {
	return argumentValue(argv, "--agent-dir") || process.env.PI_CODING_AGENT_DIR || "";
}

async function main(): Promise<void> {
	const argv = process.argv.slice(2);
	const packagePath = resolvePackagePath(argv);
	const agentDir = resolveAgentDir(argv);
	if (!packagePath) {
		console.error("pixie-admin-bridge: no selected Pi package path was provided");
		process.exit(2);
	}
	if (!agentDir) {
		console.error("pixie-admin-bridge: no Pi agent directory was provided");
		process.exit(2);
	}
	let bridge: AdminBridge;
	try {
		const installation = await resolveInstallation(packagePath);
		const module = await loadPublicApi(installation);
		bridge = createBridge({ installation, module, agentDir });
	} catch (error) {
		console.error(`pixie-admin-bridge: ${errorMessage(error)}`);
		process.exit(2);
	}
	const server = createBridgeServer(bridge, (message) => console.error(message));
	const decoder = new TextDecoder();
	let buffer = "";
	const processLine = async (line: string) => {
		const response = await server.handle(line);
		if (response) process.stdout.write(encodeResponse(response));
	};
	try {
		for await (const chunk of process.stdin) {
			const text = typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
			buffer += text;
			let newline = buffer.indexOf("\n");
			while (newline >= 0) {
				const line = buffer.slice(0, newline);
				buffer = buffer.slice(newline + 1);
				if (line.trim().length > 0) await processLine(line);
				newline = buffer.indexOf("\n");
			}
			if (encodedBytes(buffer) > BRIDGE_MAX_FRAME_BYTES) {
				throw new Error(`bridge input frame exceeds ${BRIDGE_MAX_FRAME_BYTES} bytes`);
			}
		}
		if (buffer.trim().length > 0) await processLine(buffer);
	} catch (error) {
		console.error(`pixie-admin-bridge: ${errorMessage(error)}`);
		process.exit(2);
	}
}

// Bun runs the module directly; importing it from a test does not start the loop.
if (import.meta.main) {
	void main();
}
