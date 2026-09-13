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
 *   event    {"event": <string>, "params": <object>}
 *
 * The request/response direction is strictly one reply per request ID. The
 * event direction is the only unsolicited server-to-client frame: it is
 * emitted while a request is in flight (for example a streaming provider
 * login) and is never correlated by ID. Frames are distinguished by the
 * presence of `id` (response) versus `event` (event); the Go host forwards
 * each event verbatim to the controller's method/params event path.
 *
 * Diagnostics are written to stderr only. There is no eval, no shell, and no
 * arbitrary code from the controlling side: methods come from a fixed
 * allowlist and parameters are validated. If the selected installation cannot
 * be resolved or verified the process prints a diagnostic and exits non-zero.
 * It never falls back to a global or bundled SDK.
 */

import { createHmac, randomBytes, randomUUID } from "node:crypto";
import { mkdirSync, readFileSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

export const PI_CODING_AGENT_PACKAGE = "@earendil-works/pi-coding-agent";
export const BRIDGE_PROTOCOL_VERSION = 1;
export const BRIDGE_MAX_FRAME_BYTES = 1024 * 1024;
export const BRIDGE_MAX_ID_LENGTH = 128;
export const BRIDGE_MAX_METHOD_LENGTH = 128;
export const BRIDGE_MAX_PACKAGE_JSON_BYTES = 64 * 1024;
export const BRIDGE_MAX_EVENT_LENGTH = 128;
const PROVIDER_LIST_AVAILABLE_TIMEOUT_MS = 10_000;
const PROVIDER_AUTH_TIMEOUT_MS = 5_000;
const PROVIDER_REFRESH_TIMEOUT_MS = 25_000;
const MODEL_INFO_CURRENCY = "USD";
const MCP_STATE_MAX_BYTES = 4 * 1024 * 1024;
const PACKAGE_MANIFEST_MAX_BYTES = 64 * 1024;
const INVENTORY_LIMIT = 500;
const THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"] as const;

/** Methods this bridge implements. Everything else fails closed. */
export const BRIDGE_METHODS = [
	"bridge.hello",
	"pi.providers.list",
	"pi.providers.readiness.check",
	"pi.providers.inventory.refresh",
	"pi.providers.canonical-model-info",
	"pi.defaults.read",
	"pi.defaults.save",
	"pi.defaults.clear",
	"pi.preferences.read",
	"pi.preferences.save",
	"pi.preferences.reset",
	"pi.extensions.list",
	"pi.extensions.configure",
	"pi.config.extensions.list",
	"pi.config.extensions.add",
	"pi.config.extensions.set-enabled",
	"pi.config.extensions.remove",
	"pi.session.extensions.list",
	"pi.session.extensions.add",
	"pi.session.extensions.remove",
	"pi.slash-commands.list",
	"provider.loginStart",
	"provider.loginBegin",
	"provider.loginReply",
	"provider.loginCancel",
	"provider.logout",
	"pi.providers.config.delete",
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
		typeof id === "string" &&
		id.length > 0 &&
		id.length <= BRIDGE_MAX_ID_LENGTH &&
		!id.includes("\0");
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

/**
 * One unsolicited server-to-client event frame. The event name is the
 * controller method the Go host must forward (for example `provider.login`);
 * params is that method's payload.
 */
export interface BridgeEvent {
	readonly event: string;
	readonly params: Record<string, unknown>;
}

/** Encode one bounded JSONL event frame. Newline is owned here. */
export function encodeEvent(event: BridgeEvent): string {
	if (
		typeof event.event !== "string" ||
		event.event.length === 0 ||
		event.event.length > BRIDGE_MAX_EVENT_LENGTH ||
		event.event.includes("\0")
	)
		throw new Error("bridge event name is invalid");
	const json = JSON.stringify(event);
	if (encodedBytes(json) + 1 > BRIDGE_MAX_FRAME_BYTES)
		throw new Error(`bridge event exceeds ${BRIDGE_MAX_FRAME_BYTES} bytes`);
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
	else if (!info?.isDirectory()) throw new Error(`selected installation does not exist: ${raw}`);
	const manifest = await readPackageManifest(packageDir);
	if (manifest.name !== PI_CODING_AGENT_PACKAGE)
		throw new Error(`selected installation is not ${PI_CODING_AGENT_PACKAGE}`);
	const version = manifest.version;
	if (typeof version !== "string" || !/^\d+\.\d+\.\d+(?:[-+][a-z0-9.+-]+)?$/i.test(version))
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

function objectValue(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: {};
}

function stringValue(value: unknown): string {
	return typeof value === "string" ? value : "";
}

function requiredString(value: unknown, label: string, max = 4096): string {
	if (typeof value !== "string" || value.length === 0 || value.length > max || value.includes("\0"))
		throw new Error(`Invalid ${label}`);
	return value;
}

/** Resolve one operation's working directory, defaulting to the agent dir. */
function optionalCwd(params: Record<string, unknown>, fallback: string): string {
	const value = params.cwd;
	if (value === undefined || value === null) return fallback;
	if (typeof value !== "string" || !isAbsolute(value) || value.includes("\0"))
		throw new Error("bridge parameter cwd must be an absolute path");
	return value;
}

interface SettingsShape extends Record<string, unknown> {
	defaultProvider?: string;
	defaultModel?: string;
	defaultThinkingLevel?: string;
	compaction?: { reserveTokens?: number; [key: string]: unknown };
	extensions?: unknown[];
	packages?: unknown[];
	lastChangelogVersion?: string;
}

interface SettingsManagerLike {
	reload(): Promise<void>;
	flush(): Promise<void>;
	drainErrors(): unknown[];
	getGlobalSettings(): SettingsShape;
	getProjectSettings(): SettingsShape;
	getDefaultProvider(): string | undefined;
	getDefaultModel(): string | undefined;
	setDefaultProvider(provider: string | undefined): void;
	setDefaultModel(model: string | undefined): void;
	getDefaultThinkingLevel(): string | undefined;
	setDefaultThinkingLevel(level: string | undefined): void;
	getCompactionReserveTokens(): number;
	getLastChangelogVersion(): string | undefined;
	setLastChangelogVersion(version: string | undefined): void;
	setPackages(packages: unknown[]): void;
	setProjectPackages(packages: unknown[]): void;
	setExtensionPaths(paths: unknown[]): void;
	setProjectExtensionPaths(paths: unknown[]): void;
}

interface SettingsManagerApi {
	create(cwd: string, agentDir: string, options?: unknown): SettingsManagerLike;
	fromStorage(storage: unknown, options?: unknown): SettingsManagerLike;
}

interface NativeResource {
	path: string;
	enabled: boolean;
	metadata: { source: string; scope: string; origin: string; baseDir?: string };
}

interface ConfiguredPackage {
	source: string;
	scope: string;
	filtered: boolean;
	installedPath?: string;
}

interface PackageManagerLike {
	listConfiguredPackages(): ConfiguredPackage[];
	resolve(onMissing?: (source: string) => Promise<"skip">): Promise<{
		extensions: NativeResource[];
		skills: NativeResource[];
		prompts: NativeResource[];
		themes: NativeResource[];
	}>;
}

interface PackageManagerApi {
	new (options: {
		cwd: string;
		agentDir: string;
		settingsManager: SettingsManagerLike;
	}): PackageManagerLike;
}

interface TrustStoreLike {
	get(cwd: string): boolean | null;
}

interface BridgeSdk {
	SettingsManager: SettingsManagerApi;
	DefaultPackageManager: PackageManagerApi;
	ProjectTrustStore: new (agentDir: string) => TrustStoreLike;
	hasTrustRequiringProjectResources(cwd: string): boolean;
}

const resourceTokenKey = randomBytes(32);

function resourceToken(value: unknown): string {
	return createHmac("sha256", resourceTokenKey).update(JSON.stringify(value)).digest("hex");
}

function extensionRevision(scope: string, settings: SettingsShape): string {
	return resourceToken([scope, settings.packages ?? [], settings.extensions ?? []]);
}

function extensionResourceKey(resource: NativeResource): string {
	return resourceToken([resource.path, resource.metadata]);
}

interface PendingLogin {
	id: string;
	providerId: string;
	abort: AbortController;
	frame: Record<string, unknown>;
	resolve?: (value: string) => void;
	reject?: (error: Error) => void;
	begin?: () => void;
	timer: ReturnType<typeof setTimeout>;
}

const LOGIN_TIMEOUT_MS = 600_000;

function filterPatterns(value: unknown): string[] {
	if (value === undefined || value === null) return [];
	if (!Array.isArray(value) || value.some((pattern) => typeof pattern !== "string"))
		throw new Error("Invalid native resource filters");
	return value as string[];
}

// Same filter override semantics as the legacy extension-configuration oracle:
// replace any existing +/- rule for the exact path and append the requested
// one, leaving unrelated patterns untouched.
function overrideFilters(
	patterns: string[],
	path: string,
	base: string,
	enabled: boolean,
): string[] {
	if (patterns.some((pattern) => typeof pattern !== "string"))
		throw new Error("Invalid native resource filters");
	return [
		...patterns.filter(
			(pattern) =>
				!(
					["+", "-"].includes(pattern[0] ?? "") &&
					resolve(base, pattern.slice(1)) === resolve(base, path)
				),
		),
		`${enabled ? "+" : "-"}${path}`,
	];
}

// Source references are display metadata, never URLs to fetch. Credentials,
// query strings, fragments and control characters are removed here.
function inventoryReference(value: string): string {
	return value
		.replace(/\p{Cc}/gu, "")
		.replace(/([a-z][a-z0-9+.-]*:\/\/)[^/\s]*@/gi, "$1")
		.split(/[?#]/, 1)[0]
		.slice(0, 1024);
}

async function packageIdentity(
	path?: string,
): Promise<{ name: string | null; version: string | null }> {
	const unknown = { name: null, version: null };
	if (!path) return unknown;
	try {
		const manifestPath = join(path, "package.json");
		const info = await stat(manifestPath);
		if (!info.isFile() || info.size === 0 || info.size > PACKAGE_MANIFEST_MAX_BYTES) return unknown;
		const data = objectValue(JSON.parse(await readFile(manifestPath, "utf8")));
		return {
			name:
				typeof data.name === "string" && /^@?[a-z0-9._/-]{1,200}$/i.test(data.name)
					? data.name
					: null,
			version:
				typeof data.version === "string" &&
				/^\d+\.\d+\.\d+(?:[-+][a-z0-9.+-]+)?$/i.test(data.version)
					? data.version
					: null,
		};
	} catch {
		return unknown;
	}
}

function safeSource(source: { source: string; scope: string; origin: string }) {
	return {
		source: inventoryReference(source.source),
		scope: source.scope,
		origin: source.origin,
	};
}

function readStateFile(path: string, max: number): unknown {
	let raw: string;
	try {
		const info = statSync(path);
		if (!info.isFile() || info.size === 0 || info.size > max)
			throw new Error(`state file is not a bounded regular file: ${path}`);
		raw = readFileSync(path, "utf8");
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
		throw error;
	}
	try {
		return JSON.parse(raw);
	} catch {
		throw new Error(`state file is not valid JSON: ${path}`);
	}
}

// Compatibility for persisted Pixie MCP connection records only. Registration
// and execution belong to the upstream adapter, not a second MCP client here.
function mcpConnection(
	value: unknown,
	agentDir: string,
): { name: string; definition: Record<string, unknown>; source: Record<string, unknown> } {
	const raw = objectValue(value);
	const source = raw.type === "mcp" ? objectValue(raw.server) : raw;
	const name = requiredString(source.name, "MCP name", 128);
	if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.includes("__")) throw new Error("Invalid MCP name");
	const definition: Record<string, unknown> = {};
	if (source.command !== undefined || source.type === "stdio") {
		definition.command = requiredString(source.command, "MCP command");
		if (
			source.args !== undefined &&
			(!Array.isArray(source.args) || source.args.some((item) => typeof item !== "string"))
		)
			throw new Error("MCP arguments must be strings");
		definition.args = source.args ?? [];
		definition.env = Object.fromEntries(
			Object.entries(objectValue(source.env)).map(([key, item]) => {
				if (typeof item !== "string") throw new Error("MCP environment values must be strings");
				return [key, item];
			}),
		);
		if (source.cwd)
			definition.cwd = resolve(
				agentDir,
				requiredString(source.cwd, "MCP working directory").replace(
					/\$\{([A-Z0-9_]+)\}/g,
					(_match, key: string) => {
						const value = process.env[key];
						if (value === undefined) throw new Error(`Missing MCP environment variable: ${key}`);
						return value;
					},
				),
			);
	} else {
		const url = new URL(requiredString(source.uri ?? source.url, "MCP URL"));
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
			throw new Error("MCP requires HTTP(S) without URL credentials");
		if (source.type && !["http", "streamable_http", "sse"].includes(stringValue(source.type)))
			throw new Error("Unsupported MCP transport");
		definition.url = url.href;
		if (source.type === "sse") definition.httpTransport = "sse";
		definition.headers = Array.isArray(source.headers)
			? Object.fromEntries(
					source.headers.map((item) => {
						const header = objectValue(item);
						return [requiredString(header.name, "header"), stringValue(header.value)];
					}),
				)
			: Object.fromEntries(
					Object.entries(objectValue(source.headers)).map(([key, item]) => [
						key,
						stringValue(item),
					]),
				);
	}
	return {
		name,
		definition,
		source: {
			...source,
			name,
			type: definition.command ? "stdio" : source.type === "sse" ? "sse" : "http",
		},
	};
}

function wrapMcp(source: Record<string, unknown>): Record<string, unknown> {
	return {
		type: "mcp",
		server: {
			...source,
			url: source.url ?? source.uri,
			type: source.type === "streamable_http" ? "http" : source.type,
		},
	};
}

function legacyMcp(value: Record<string, unknown>): Record<string, unknown> {
	if (
		!value ||
		Array.isArray(value) ||
		typeof value !== "object" ||
		Object.hasOwn(value, "mcpServers")
	)
		throw new Error(
			"Native MCP configuration is managed by pi-mcp-adapter, not legacy connection administration",
		);
	return value;
}

function slashCommandName(path: string): string {
	const name = basename(path).replace(/\.md$/i, "");
	return name === "SKILL" ? basename(dirname(path)) : name;
}

/**
 * FC20 native resource inventory. The sidecar has no resident `AgentSession`,
 * so loaded-extension evidence is always absent and the reader is reported as
 * `service`, `configured-only` or `not-resident`; the controller accepts those
 * readers and requires empty loaded/errors arrays for them.
 */
async function nativeExtensionInventory(
	agentDir: string,
	cwd: string,
	sessionId: string | null,
	reader: string,
	sdk: BridgeSdk,
): Promise<Record<string, unknown>> {
	const warnings: string[] = [];
	const settings = sdk.SettingsManager.create(cwd, agentDir);
	if (settings.drainErrors().length) warnings.push("settings-read-failed");
	const manager = new sdk.DefaultPackageManager({ cwd, agentDir, settingsManager: settings });
	let packages: ConfiguredPackage[] = [];
	let resources: NativeResource[] = [];
	try {
		packages = manager.listConfiguredPackages();
	} catch {
		warnings.push("package-discovery-failed");
	}
	try {
		// The explicit skip callback resolves manifests and paths only; missing
		// or mismatched packages are never installed during inspection.
		resources = (await manager.resolve(async () => "skip")).extensions;
	} catch {
		warnings.push("resource-discovery-failed");
	}
	const globalSettings = settings.getGlobalSettings();
	const projectSettings = settings.getProjectSettings();
	if (
		packages.length > INVENTORY_LIMIT ||
		resources.length > INVENTORY_LIMIT ||
		(globalSettings.extensions?.length ?? 0) > INVENTORY_LIMIT ||
		(projectSettings.extensions?.length ?? 0) > INVENTORY_LIMIT
	)
		warnings.push("inventory-truncated");
	const configuredPackages = await Promise.all(
		packages.slice(0, INVENTORY_LIMIT).map(async (pkg) => ({
			source: inventoryReference(pkg.source),
			scope: pkg.scope,
			filtered: pkg.filtered,
			installed: Boolean(pkg.installedPath),
			...(await packageIdentity(pkg.installedPath)),
			state: pkg.installedPath ? "not-observed" : "missing",
		})),
	);
	const configurationSupported = (resource: NativeResource) => {
		if (resource.metadata.scope !== "user" && resource.metadata.scope !== "project") return false;
		if (resource.metadata.origin === "top-level") return true;
		const installedPath = packages.find(
			(pkg) => pkg.source === resource.metadata.source && pkg.scope === resource.metadata.scope,
		)?.installedPath;
		try {
			return Boolean(
				installedPath && installedPath !== resource.path && statSync(installedPath).isDirectory(),
			);
		} catch {
			return false;
		}
	};
	const trustStore = new sdk.ProjectTrustStore(agentDir);
	let trustDecision: boolean | null = null;
	try {
		trustDecision = trustStore.get(cwd);
	} catch {
		warnings.push("trust-read-failed");
	}
	const requiresDecision = sdk.hasTrustRequiringProjectResources(cwd);
	return {
		version: 1,
		trust: {
			projectTrusted: !requiresDecision || trustDecision === true,
			decision: trustDecision,
			requiresDecision: requiresDecision && trustDecision === null,
		},
		configurationRevisions: {
			user: extensionRevision("user", globalSettings),
			project: extensionRevision("project", projectSettings),
		},
		context: { cwd: inventoryReference(cwd), sessionId, reader },
		packages: configuredPackages,
		paths: (
			[
				["user", globalSettings],
				["project", projectSettings],
			] as const
		).flatMap(([scope, values]) =>
			(Array.isArray(values.extensions) ? values.extensions : [])
				.filter((path): path is string => typeof path === "string")
				.slice(0, INVENTORY_LIMIT)
				.map((path) => ({ path: inventoryReference(path), scope })),
		),
		resources: resources.slice(0, INVENTORY_LIMIT).map((resource) => ({
			resourceKey: extensionResourceKey(resource),
			configurationSupported: configurationSupported(resource),
			path: inventoryReference(resource.path),
			...safeSource(resource.metadata),
			enabled: resource.enabled,
			state: "not-observed",
		})),
		extensions: [],
		errors: [],
		warnings,
	};
}

function bridgeSdk(module: Record<string, unknown>): BridgeSdk {
	const SettingsManager = module.SettingsManager as SettingsManagerApi | undefined;
	if (
		!SettingsManager ||
		typeof SettingsManager.create !== "function" ||
		typeof SettingsManager.fromStorage !== "function"
	)
		throw new Error("selected installation does not expose public SettingsManager");
	const DefaultPackageManager = module.DefaultPackageManager as PackageManagerApi | undefined;
	if (typeof DefaultPackageManager !== "function")
		throw new Error("selected installation does not expose public DefaultPackageManager");
	const ProjectTrustStore = module.ProjectTrustStore as BridgeSdk["ProjectTrustStore"] | undefined;
	if (typeof ProjectTrustStore !== "function")
		throw new Error("selected installation does not expose public ProjectTrustStore");
	const hasTrustRequiringProjectResources = module.hasTrustRequiringProjectResources;
	if (typeof hasTrustRequiringProjectResources !== "function")
		throw new Error(
			"selected installation does not expose public hasTrustRequiringProjectResources",
		);
	return {
		SettingsManager,
		DefaultPackageManager,
		ProjectTrustStore,
		hasTrustRequiringProjectResources:
			hasTrustRequiringProjectResources as BridgeSdk["hasTrustRequiringProjectResources"],
	};
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
	/** Interactive provider login; streams prompts and notifications. */
	login(providerId: string, type: string, interaction: Record<string, unknown>): Promise<unknown>;
	/** Remove the stored credential for one provider. */
	logout(providerId: string, options?: unknown): Promise<unknown>;
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
	/**
	 * Sink for unsolicited event frames. The process entrypoint writes these to
	 * stdout; tests may collect them. Omitted events are dropped.
	 */
	readonly emit?: (event: BridgeEvent) => void;
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
	const sdk = bridgeSdk(module);
	const emit = options.emit ?? (() => {});
	const emitEvent = (event: string, params: Record<string, unknown>) => emit({ event, params });
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
		publicSymbols: [
			"ModelRuntime",
			"SettingsManager",
			"DefaultPackageManager",
			"ProjectTrustStore",
			"hasTrustRequiringProjectResources",
		],
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

	const canonicalModelInfo = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
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

	const readinessCheck = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
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

	// --- FC19: global defaults and preferences (SettingsManager) ---

	const readDefaults = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const manager = sdk.SettingsManager.create(cwd, agentDir);
		await manager.reload();
		return {
			providerId: manager.getDefaultProvider() || null,
			modelId: manager.getDefaultModel() || null,
		};
	};

	const saveDefaults = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const provider = requiredText(params, "providerId");
		const rawModel = params.modelId;
		if (
			rawModel !== undefined &&
			rawModel !== null &&
			(typeof rawModel !== "string" || rawModel.includes("\0"))
		)
			throw new Error("bridge parameter modelId must be a string");
		const manager = sdk.SettingsManager.create(cwd, agentDir);
		manager.setDefaultProvider(provider);
		manager.setDefaultModel(typeof rawModel === "string" ? rawModel : "");
		await manager.flush();
		return readDefaults(params);
	};

	const clearDefaults = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const manager = sdk.SettingsManager.create(cwd, agentDir);
		manager.setDefaultProvider(undefined);
		manager.setDefaultModel(undefined);
		await manager.flush();
		return readDefaults(params);
	};

	const readPreferences = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const manager = sdk.SettingsManager.create(cwd, agentDir);
		await manager.reload();
		const global = manager.getGlobalSettings();
		const storedReserve = global.compaction?.reserveTokens;
		return {
			values: [
				{ key: "piThinkingEffort", value: global.defaultThinkingLevel ?? null },
				{
					key: "compactionReserveTokens",
					value: storedReserve === undefined ? null : manager.getCompactionReserveTokens(),
				},
			],
		};
	};

	const writePreferences = async (
		params: Record<string, unknown>,
		reset: boolean,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const manager = sdk.SettingsManager.create(cwd, agentDir);
		await manager.reload();
		const rawEntries = reset
			? (Array.isArray(params.keys) ? params.keys : []).map((key) => ({ key, value: null }))
			: Array.isArray(params.values)
				? params.values
				: [];
		for (const raw of rawEntries) {
			const entry = objectValue(raw);
			const key = stringValue(entry.key);
			if (key === "piThinkingEffort") {
				if (entry.value === null || entry.value === undefined) {
					manager.setDefaultThinkingLevel(undefined);
				} else if (
					typeof entry.value === "string" &&
					(THINKING_LEVELS as readonly string[]).includes(entry.value)
				) {
					manager.setDefaultThinkingLevel(entry.value);
				} else {
					throw new Error("Unsupported thinking effort");
				}
			} else if (key === "compactionReserveTokens") {
				// Pi 0.85.1 exposes getCompactionReserveTokens but no setter.
				// Refuse rather than edit settings.json behind the SDK's back.
				throw new Error("compactionReserveTokens has no public setter in the selected Pi");
			} else {
				throw new Error("Unknown preference");
			}
		}
		await manager.flush();
		return readPreferences(params);
	};

	const listExtensions = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const sessionId =
			typeof params.sessionId === "string" && params.sessionId.length ? params.sessionId : null;
		const hasCwd = params.cwd !== undefined && params.cwd !== null;
		const cwd = optionalCwd(params, agentDir);
		const reader = sessionId ? "not-resident" : hasCwd ? "configured-only" : "service";
		return nativeExtensionInventory(agentDir, cwd, sessionId, reader, sdk);
	};

	const listConfiguredMcp = async (): Promise<Record<string, unknown>> => {
		const stored = legacyMcp(
			objectValue(readStateFile(join(agentDir, "mcp.json"), MCP_STATE_MAX_BYTES)),
		);
		const warnings: string[] = [];
		const extensions = Object.entries(stored).map(([configKey, raw]) => {
			const source = objectValue(raw);
			try {
				mcpConnection({ ...source, name: configKey }, agentDir);
			} catch {
				warnings.push(`Invalid MCP configuration: ${configKey}`);
				return {
					configKey,
					enabled: source.enabled !== false,
					invalid: true,
					extension: { type: "mcp", server: { name: configKey, type: "invalid" } },
				};
			}
			return {
				configKey,
				enabled: source.enabled !== false,
				extension: wrapMcp({ ...source, name: configKey }),
			};
		});
		return { extensions, warnings };
	};

	const listSessionMcp = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const sessionId = requiredText(params, "sessionId");
		const stored = objectValue(readStateFile(join(agentDir, "mcp.json"), MCP_STATE_MAX_BYTES));
		const base = Object.hasOwn(stored, "mcpServers") ? {} : legacyMcp(stored);
		const memberships = objectValue(
			readStateFile(join(agentDir, "mcp-sessions.json"), MCP_STATE_MAX_BYTES),
		);
		const membership = objectValue(memberships[sessionId]);
		const all: Record<string, unknown> = { ...base, ...objectValue(membership.add) };
		for (const name of Array.isArray(membership.remove) ? membership.remove : []) {
			if (typeof name === "string") delete all[name];
		}
		const extensions: Array<Record<string, unknown>> = [];
		for (const [name, raw] of Object.entries(all)) {
			let parsed: ReturnType<typeof mcpConnection>;
			try {
				parsed = mcpConnection({ ...objectValue(raw), name }, agentDir);
			} catch {
				continue;
			}
			if (parsed.source.enabled === false) continue;
			extensions.push({ extensionKey: name, extension: wrapMcp(parsed.source) });
		}
		return { extensions, warnings: [] };
	};

	const listSlashCommands = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const availableCommands: Array<Record<string, unknown>> = [
			{ name: "compact", description: "Compact the conversation" },
		];
		const seen = new Set(["compact"]);
		const add = (name: string) => {
			if (!name || seen.has(name)) return;
			seen.add(name);
			availableCommands.push({ name, description: "" });
		};
		try {
			// The sidecar has no resident AgentSession, so this reports the
			// configured prompt and skill commands the package resolver exposes;
			// extension-registered commands require a live session and are absent.
			const settings = sdk.SettingsManager.create(cwd, agentDir);
			const manager = new sdk.DefaultPackageManager({ cwd, agentDir, settingsManager: settings });
			const resolved = await manager.resolve(async () => "skip");
			for (const prompt of resolved.prompts) if (prompt.enabled) add(slashCommandName(prompt.path));
			for (const skill of resolved.skills)
				if (skill.enabled) add(`skill:${slashCommandName(skill.path)}`);
		} catch {
			// Keep the builtin command when native resource discovery is unavailable.
		}
		return { availableCommands };
	};

	// --- FC18: streaming provider login and logout (ModelRuntime.login/logout) ---

	const logins = new Map<string, PendingLogin>();
	const loginTimeout = (login: PendingLogin) => {
		if (logins.get(login.id) === login) logins.delete(login.id);
		login.abort.abort();
	};
	const publishLogin = (login: PendingLogin, frame: Record<string, unknown>) => {
		login.frame = frame;
		emitEvent("provider.login", {
			loginId: login.id,
			providerId: login.providerId,
			frame,
		});
	};
	const startLogin = (params: Record<string, unknown>): Record<string, unknown> => {
		const providerId = requiredText(params, "providerId");
		const type =
			params.type === undefined || params.type === null ? "api_key" : stringValue(params.type);
		if (type !== "api_key" && type !== "oauth") throw new Error("Invalid authentication method");
		const loginId = requiredText(params, "loginId");
		if ([...logins.values()].some((login) => login.providerId === providerId))
			throw new Error("Authentication already in progress");
		if (logins.has(loginId)) throw new Error("Duplicate login ID");
		const abort = new AbortController();
		const frame = { kind: "progress", message: "Starting Pi authentication…" };
		const login: PendingLogin = {
			id: loginId,
			providerId,
			abort,
			frame,
			timer: setTimeout(() => loginTimeout(login), LOGIN_TIMEOUT_MS),
		};
		logins.set(loginId, login);
		// Begin only after the caller can associate the returned ID with its UI.
		login.begin = () => {
			void (async () => {
				const active = await runtimeFor();
				return active.login(providerId, type, {
					signal: abort.signal,
					prompt: (prompt: Record<string, unknown>) =>
						new Promise<string>((resolve, reject) => {
							const kind = stringValue(prompt.type);
							const reply =
								kind === "select"
									? {
											kind: "select",
											message: prompt.message,
											options: prompt.options,
										}
									: {
											kind: "prompt",
											message: prompt.message,
											placeholder: prompt.placeholder,
											secret: kind === "secret",
											allowEmpty: false,
										};
							const promptSignal = prompt.signal as AbortSignal | undefined;
							const signal = promptSignal
								? AbortSignal.any([abort.signal, promptSignal])
								: abort.signal;
							const cancel = () => {
								login.resolve = undefined;
								login.reject = undefined;
								reject(new Error("Authentication cancelled"));
							};
							if (signal.aborted) {
								cancel();
								return;
							}
							signal.addEventListener("abort", cancel, { once: true });
							login.resolve = (value) => {
								signal.removeEventListener("abort", cancel);
								login.resolve = undefined;
								login.reject = undefined;
								resolve(value);
							};
							login.reject = reject;
							publishLogin(login, reply);
						}),
					notify: (event: Record<string, unknown>) => {
						const kind = stringValue(event.type);
						if (kind === "auth_url")
							publishLogin(login, {
								kind: "authUrl",
								url: event.url,
								instructions: event.instructions,
							});
						else if (kind === "device_code")
							publishLogin(login, {
								kind: "deviceCode",
								userCode: event.userCode,
								verificationUri: event.verificationUri,
								expiresInSeconds: event.expiresInSeconds,
							});
						else publishLogin(login, { kind: "progress", message: event.message });
					},
				});
			})()
				.then(
					() => publishLogin(login, { kind: "success" }),
					() =>
						publishLogin(login, {
							kind: "error",
							message: "Pi authentication failed or was cancelled.",
						}),
				)
				.finally(() => {
					clearTimeout(login.timer);
					if (logins.get(login.id) === login) logins.delete(login.id);
				});
		};
		return { loginId, frame };
	};
	const beginLogin = (params: Record<string, unknown>): Record<string, unknown> => {
		const login = logins.get(requiredText(params, "loginId"));
		if (!login?.begin) throw new Error("Login cannot be started");
		const begin = login.begin;
		login.begin = undefined;
		begin();
		return { ok: true };
	};
	const replyLogin = (params: Record<string, unknown>): Record<string, unknown> => {
		const login = logins.get(requiredText(params, "loginId"));
		if (!login?.resolve) throw new Error("No pending authentication question");
		login.resolve(stringValue(params.value));
		return { ok: true };
	};
	const cancelLogin = (params: Record<string, unknown>): Record<string, unknown> => {
		const login = logins.get(requiredText(params, "loginId"));
		if (!login) throw new Error("Unknown or expired login ID");
		login.abort.abort();
		clearTimeout(login.timer);
		logins.delete(login.id);
		return { ok: true };
	};
	const logoutProvider = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const providerId = requiredText(params, "providerId");
		const active = await runtimeFor();
		await active.logout(providerId);
		return { ok: true };
	};

	// --- FC21: scoped native extension enablement through SettingsManager ---

	const configureExtension = async (
		params: Record<string, unknown>,
	): Promise<Record<string, unknown>> => {
		const cwd = optionalCwd(params, agentDir);
		const scope = requiredText(params, "scope");
		if (scope !== "user" && scope !== "project")
			throw new Error("Invalid native configuration scope");
		if (params.confirmed !== true)
			throw new Error("Confirm the scoped native configuration change");
		if (typeof params.enabled !== "boolean")
			throw new Error("Native configuration enabled must be boolean");
		const enabled = params.enabled;
		const resourceKey = requiredText(params, "resourceKey");
		const expectedRevision = requiredText(params, "expectedRevision");
		const source = sdk.SettingsManager.create(cwd, agentDir);
		if (source.drainErrors().length)
			throw new Error("Native settings are unreadable. No configuration saved.");
		const snapshots = {
			global: source.getGlobalSettings(),
			project: source.getProjectSettings(),
		};
		const settingsScope = scope === "user" ? "global" : "project";
		if (extensionRevision(scope, snapshots[settingsScope]) !== expectedRevision)
			throw new Error("Native configuration changed. Refresh inventory before saving.");
		const packages = new sdk.DefaultPackageManager({ cwd, agentDir, settingsManager: source });
		const resources = (await packages.resolve(async () => "skip")).extensions;
		const beforeByKey = new Map(resources.map((item) => [extensionResourceKey(item), item]));
		const resource = beforeByKey.get(resourceKey);
		if (!resource || resource.metadata.scope !== scope)
			throw new Error("Native resource is no longer available. Refresh inventory.");
		const next = structuredClone(snapshots[settingsScope]) as SettingsShape & {
			packages?: unknown[];
			extensions?: unknown[];
		};
		let field: "packages" | "extensions" = "extensions";
		if (resource.metadata.origin === "package") {
			field = "packages";
			const entries = Array.isArray(next.packages) ? next.packages : [];
			const index = entries.findIndex((entry) => {
				const source = typeof entry === "string" ? entry : stringValue(objectValue(entry).source);
				return source === resource.metadata.source;
			});
			if (index < 0 || !resource.metadata.baseDir)
				throw new Error("Manage this resource through native Pi configuration");
			const previous = entries[index];
			const entry = typeof previous === "string" ? { source: previous } : objectValue(previous);
			const rawPatterns =
				entry.extensions === undefined ? undefined : filterPatterns(entry.extensions);
			let patterns = rawPatterns ?? (entry.autoload === false ? [] : ["**"]);
			if (patterns.length === 0 && entry.autoload !== false) patterns = ["!**"];
			entries[index] = {
				...entry,
				extensions: overrideFilters(
					patterns,
					relative(resource.metadata.baseDir, resource.path).split("\\").join("/"),
					resource.metadata.baseDir,
					enabled,
				),
			};
			next.packages = entries;
		} else {
			const patterns = filterPatterns(next.extensions);
			next.extensions = overrideFilters(
				patterns,
				resource.path,
				scope === "user" ? agentDir : join(cwd, ".pi"),
				enabled,
			);
		}
		// Evaluate native filters without loading code or installing packages.
		const previewSettings = sdk.SettingsManager.fromStorage({
			withLock: (target: "global" | "project", fn: (content: string) => void) => {
				fn(JSON.stringify(target === settingsScope ? next : snapshots[target]));
			},
		});
		const preview = (
			await new sdk.DefaultPackageManager({
				cwd,
				agentDir,
				settingsManager: previewSettings,
			}).resolve(async () => "skip")
		).extensions;
		const afterByKey = new Map(preview.map((item) => [extensionResourceKey(item), item]));
		const changed = afterByKey.get(resourceKey);
		if (!changed || changed.enabled !== enabled)
			throw new Error("Native filters cannot apply this change. No configuration saved.");
		if (
			[...beforeByKey].some(
				([key, before]) => key !== resourceKey && afterByKey.get(key)?.enabled !== before.enabled,
			) ||
			[...afterByKey.keys()].some((key) => key !== resourceKey && !beforeByKey.has(key))
		)
			throw new Error("This change would affect other resources. Use native Pi configuration.");
		// Write through the selected installation's own settings storage, which
		// owns the real cross-process lock for its settings file. Re-read and
		// re-check the revision immediately before the locked write; the only
		// residual window is a competing writer landing between this reload and
		// the storage lock, which the post-write confirmation then reports.
		await source.reload();
		const latest = {
			global: source.getGlobalSettings(),
			project: source.getProjectSettings(),
		};
		if (extensionRevision(scope, latest[settingsScope]) !== expectedRevision)
			throw new Error("Concurrent native configuration change");
		if (field === "packages") {
			if (settingsScope === "global") source.setPackages((next.packages ?? []) as never);
			else source.setProjectPackages((next.packages ?? []) as never);
		} else if (settingsScope === "global")
			source.setExtensionPaths((next.extensions ?? []) as never);
		else source.setProjectExtensionPaths((next.extensions ?? []) as never);
		await source.flush();
		const errors = source.drainErrors();
		// Confirm the exact enabled state after the locked write.
		const confirmedSettings = sdk.SettingsManager.create(cwd, agentDir);
		const confirmed = (
			await new sdk.DefaultPackageManager({
				cwd,
				agentDir,
				settingsManager: confirmedSettings,
			}).resolve(async () => "skip")
		).extensions;
		const confirmedResource = new Map(
			confirmed.map((item) => [extensionResourceKey(item), item]),
		).get(resourceKey);
		if (!confirmedResource || confirmedResource.enabled !== enabled)
			throw new Error(
				"Native settings save was not confirmed or conflicted. Refresh inventory before retrying.",
			);
		return {
			saved: true,
			loaded: false,
			reload: "deferred",
			warning: errors.length ? "settings-cleanup-failed" : null,
		};
	};

	// --- FC26: Pixie-local MCP connection stores ---

	const mutateStateFile = (path: string, change: (state: Record<string, unknown>) => void) => {
		const state = objectValue(readStateFile(path, MCP_STATE_MAX_BYTES));
		change(state);
		mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
		const temporary = `${path}.${randomUUID()}.tmp`;
		try {
			writeFileSync(temporary, JSON.stringify(state), { mode: 0o600, flag: "wx" });
			renameSync(temporary, path);
		} finally {
			rmSync(temporary, { force: true });
		}
	};
	const addConfiguredMcp = (params: Record<string, unknown>): Record<string, unknown> => {
		const connection = mcpConnection(params.extension, agentDir);
		mutateStateFile(join(agentDir, "mcp.json"), (state) => {
			legacyMcp(state);
			if (Object.hasOwn(state, connection.name)) throw new Error("MCP connection already exists");
			state[connection.name] = { ...connection.source, enabled: params.enabled !== false };
		});
		return { ok: true };
	};
	const setConfiguredMcpEnabled = (params: Record<string, unknown>): Record<string, unknown> => {
		if (typeof params.enabled !== "boolean") throw new Error("Enabled must be boolean");
		const configKey = requiredText(params, "configKey");
		mutateStateFile(join(agentDir, "mcp.json"), (state) => {
			legacyMcp(state);
			const connection = objectValue(state[configKey]);
			if (!Object.hasOwn(state, configKey) || !connection) throw new Error("Unknown connection");
			connection.enabled = params.enabled;
			state[configKey] = connection;
		});
		return { ok: true };
	};
	const removeConfiguredMcp = (params: Record<string, unknown>): Record<string, unknown> => {
		const configKey = requiredText(params, "configKey");
		mutateStateFile(join(agentDir, "mcp.json"), (state) => {
			legacyMcp(state);
			delete state[configKey];
		});
		return { ok: true };
	};
	const addSessionMcp = (params: Record<string, unknown>): Record<string, unknown> => {
		const sessionId = requiredText(params, "sessionId");
		const connection = mcpConnection(params.extension, agentDir);
		mutateStateFile(join(agentDir, "mcp-sessions.json"), (state) => {
			const membership = objectValue(state[sessionId]);
			const add = objectValue(membership.add);
			add[connection.name] = connection.source;
			membership.add = add;
			const remove = Array.isArray(membership.remove)
				? membership.remove.filter((name): name is string => typeof name === "string")
				: [];
			membership.remove = remove.filter((name) => name !== connection.name);
			state[sessionId] = membership;
		});
		return { ok: true };
	};
	const removeSessionMcp = (params: Record<string, unknown>): Record<string, unknown> => {
		const sessionId = requiredText(params, "sessionId");
		const extensionKey = requiredText(params, "extensionKey");
		mutateStateFile(join(agentDir, "mcp-sessions.json"), (state) => {
			const membership = objectValue(state[sessionId]);
			const add = objectValue(membership.add);
			delete add[extensionKey];
			membership.add = add;
			const remove = Array.isArray(membership.remove)
				? membership.remove.filter((name): name is string => typeof name === "string")
				: [];
			if (!remove.includes(extensionKey)) remove.push(extensionKey);
			membership.remove = remove;
			state[sessionId] = membership;
		});
		return { ok: true };
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
			case "pi.defaults.read":
				return readDefaults(params);
			case "pi.defaults.save":
				return saveDefaults(params);
			case "pi.defaults.clear":
				return clearDefaults(params);
			case "pi.preferences.read":
				return readPreferences(params);
			case "pi.preferences.save":
				return writePreferences(params, false);
			case "pi.preferences.reset":
				return writePreferences(params, true);
			case "pi.extensions.list":
				return listExtensions(params);
			case "pi.extensions.configure":
				return configureExtension(params);
			case "pi.config.extensions.list":
				return listConfiguredMcp();
			case "pi.config.extensions.add":
				return addConfiguredMcp(params);
			case "pi.config.extensions.set-enabled":
				return setConfiguredMcpEnabled(params);
			case "pi.config.extensions.remove":
				return removeConfiguredMcp(params);
			case "pi.session.extensions.list":
				return listSessionMcp(params);
			case "pi.session.extensions.add":
				return addSessionMcp(params);
			case "pi.session.extensions.remove":
				return removeSessionMcp(params);
			case "pi.slash-commands.list":
				return listSlashCommands(params);
			case "provider.loginStart":
				return startLogin(params);
			case "provider.loginBegin":
				return beginLogin(params);
			case "provider.loginReply":
				return replyLogin(params);
			case "provider.loginCancel":
				return cancelLogin(params);
			case "provider.logout":
			case "pi.providers.config.delete":
				return logoutProvider(params);
			default:
				throw new Error(`Unsupported bridge method: ${method}`);
		}
	};

	return {
		installation,
		hello,
		dispatch,
		close() {
			for (const login of [...logins.values()]) {
				login.abort.abort();
				clearTimeout(login.timer);
			}
			logins.clear();
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
	let writeEvent: ((event: BridgeEvent) => void) | undefined;
	try {
		const installation = await resolveInstallation(packagePath);
		const module = await loadPublicApi(installation);
		bridge = createBridge({
			installation,
			module,
			agentDir,
			emit: (event) => writeEvent?.(event),
		});
	} catch (error) {
		console.error(`pixie-admin-bridge: ${errorMessage(error)}`);
		process.exit(2);
	}
	writeEvent = (event) => process.stdout.write(encodeEvent(event));
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
