import { createHash, randomBytes, randomUUID, timingSafeEqual } from "node:crypto";
import {
	chmodSync,
	closeSync,
	fsyncSync,
	linkSync,
	lstatSync,
	mkdirSync,
	openSync,
	readdirSync,
	readFileSync,
	realpathSync,
	renameSync,
	statSync,
	unlinkSync,
	writeSync,
} from "node:fs";
import { isIP } from "node:net";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import {
	HOST_AVAILABLE_OPERATIONS,
	HOST_OPERATION_REASONS,
	HOST_OPERATIONS,
} from "../../shared/src/generated/protocol-catalog.ts";
import { createHostLogger, type HostLogger, redactHostLogText } from "./log.ts";
import { loadVerifiedPiPublicApi, type VerifiedPiPackage } from "./probe.ts";
import { type SteeringBinding, SteeringRegistry } from "./steering.ts";

const MIN_SECRET_LENGTH = 32;
const MAX_OUTBOUND_BYTES = 32 * 1024 * 1024;
const MAX_PENDING_REQUESTS = 128;
const MAX_FRAME_BYTES = 32 * 1024 * 1024;
const MAX_DIALOGS_PER_SESSION = 16;
const MAX_SAFE_REQUEST_ID = 9_007_199_254_740_991;
const DRAIN_TIMEOUT_MS = 25_000;
const RESTART_DRAIN_MS = 250;
const DEFAULT_REQUEST_TIMEOUT_MS = 120_000;
const HOST_SUPPORTED_PROTOCOL_VERSIONS = [2, 1] as const;

const AGENT_DOCUMENT_MAX_BYTES = 65536;
const AGENT_NAME_MAX_BYTES = 80;
const AGENT_REVISION_MAX_BYTES = 128;
const MCP_CONFIG_MAX_BYTES = 4 * 1024 * 1024;
const MCP_SERVER_NAME_MAX_BYTES = 128;
const MCP_SERVER_NAME_PATTERN = /^[A-Za-z0-9._-]{1,128}$/;
const MCP_PROBE_TIMEOUT_MS = 10_000;
const MCP_PROBE_MAX_BODY = 1 << 20;
const MCP_PROBE_MAX_TOOLS = 128;
const MCP_PROBE_MAX_TOOL_NAME_BYTES = 256;
const MCP_PROBE_PROTOCOL_VERSION = "2025-06-18";
const MCP_STATE_MAX_BYTES = 4 * 1024 * 1024;
const MCP_OWNERSHIP_CONFLICT = "MCP ownership conflict";
const MCP_MANAGED_BY_KEY = "_pixieManagedBy";
const MCP_OWNERSHIP_TOKEN_KEY = "_pixieOwnershipToken";
const MCP_MANAGED_BY_VERSION = "browser-mcp/v2";
const THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"] as const;
const PROVIDER_LIST_AVAILABLE_TIMEOUT_MS = 10_000;
const PROVIDER_AUTH_TIMEOUT_MS = 5_000;
const PROVIDER_REFRESH_TIMEOUT_MS = 25_000;
const LOGIN_TIMEOUT_MS = 600_000;

// AUX-13/AUX-12: native session-file coexistence. The lease is advisory: it is
// only consulted by this host, so a native Pi process that never writes one is
// still detectable through a live pid and a fresh lease mtime. SESSION_SCHEMA_VERSION
// tracks the newest Pi session header this host understands; a newer header is
// refused rather than mis-parsed.
const SESSION_LEASE_SUFFIX = ".lease";
const SESSION_LEASE_STALE_MS = 30_000;
const SESSION_SCHEMA_VERSION = 3;

// The host implementation status lives in the shared protocol catalog. Only
// routes the catalog marks available may be advertised; every other catalogued
// route stays in the exhaustive operationSet as false with a stated reason.
const AVAILABLE_OPERATIONS = new Set<string>(HOST_AVAILABLE_OPERATIONS);

function unavailableReason(operation: string): string | undefined {
	return (HOST_OPERATION_REASONS as Record<string, string | undefined>)[operation];
}

export interface PiSession {
	readonly sessionId: string;
	readonly sessionFile?: string;
	readonly messages?: readonly unknown[];
	readonly isStreaming?: boolean;
	readonly model?: unknown;
	readonly thinkingLevel?: unknown;
	readonly modelRuntime?: {
		readonly getModels?: (providerId?: string) => readonly unknown[];
		readonly getModel?: (provider: string, model: string) => unknown;
		readonly getAvailable?: (provider?: unknown, options?: unknown) => Promise<readonly unknown[]>;
	};
	readonly sessionManager?: {
		readonly getEntry?: (id: string) => unknown;
		readonly getLeafId?: () => string | null;
		readonly getSessionName?: () => string | undefined;
	};
	readonly extensionRunner?: {
		readonly getRegisteredCommands?: () => readonly unknown[];
	};
	readonly promptTemplates?: readonly { readonly name?: unknown; readonly description?: unknown }[];
	readonly resourceLoader?: {
		readonly getSkills?: () => { readonly skills?: readonly unknown[] };
	};
	subscribe(listener: (event: Record<string, unknown>) => void): () => void;
	prompt(
		text: string,
		options: {
			images?: unknown[];
			source: "rpc";
			preflightResult: (accepted: boolean) => void;
		},
	): Promise<void>;
	clearQueue(): unknown;
	abort(): Promise<void>;
	waitForIdle(): Promise<void>;
	dispose(): void | Promise<void>;
	bindExtensions(bindings: { mode: "rpc"; uiContext: Record<string, unknown> }): Promise<void>;
	steer?(text: string, images?: unknown[]): Promise<void>;
	followUp?(text: string, images?: unknown[]): Promise<void>;
	compact?(customInstructions?: string): Promise<unknown>;
	setSessionName?(name: string): void | Promise<void>;
	getSessionStats?(): unknown | Promise<unknown>;
	getAvailableThinkingLevels?(): readonly unknown[];
	setModel?(model: unknown, options?: unknown): Promise<void>;
	setThinkingLevel?(level: unknown, options?: unknown): void | Promise<void>;
}

export interface PiSdkApi {
	createAgentSession(options: {
		cwd: string;
		agentDir: string;
		sessionManager?: unknown;
	}): Promise<{ session: PiSession }>;
	getDefaultSessionDir(cwd: string, agentDir: string): string;
	SessionManager: {
		create(cwd: string, sessionDir: string): unknown;
		list(cwd: string, sessionDir: string): Promise<readonly PiSessionInfo[]>;
		open(path: string, sessionDir: string, cwd: string): unknown;
		forkFrom?: (sourcePath: string, targetCwd: string, sessionDir?: string) => unknown;
	};
	readonly publicApi?: PiPublicRootApi;
	readonly ModelRuntime?: unknown;
	readonly SettingsManager?: unknown;
	readonly DefaultPackageManager?: unknown;
	readonly DefaultResourceLoader?: unknown;
	readonly ProjectTrustStore?: unknown;
	readonly hasTrustRequiringProjectResources?: unknown;
}

/** A module namespace loaded from Pi's already verified public package root. */
export type PiPublicRootApi = Readonly<Record<string, unknown>>;

export type BunHostFromPublicApiOptions = Omit<BunHostOptions, "sdk">;

export interface PiSessionInfo {
	readonly id: string;
	readonly path: string;
	readonly cwd?: string;
	readonly name?: string;
	readonly modified?: Date;
}

export interface BunWebSocket {
	send(data: string): number | undefined;
	close(code?: number, reason?: string): void;
}

interface ConnectionData {
	readonly connection: Connection;
}

export interface BunServer {
	readonly port?: number;
	stop(closeActiveConnections?: boolean): void;
	upgrade(request: Request, options: { data: ConnectionData }): boolean;
}

export interface BunServerFactory {
	serve(options: {
		hostname: string;
		port: number;
		fetch(request: Request, server: BunServer): Response | undefined;
		websocket: {
			open(socket: BunWebSocket & { data: ConnectionData }): void;
			message(socket: BunWebSocket & { data: ConnectionData }, message: string | Uint8Array): void;
			close(socket: BunWebSocket & { data: ConnectionData }): void;
		};
	}): BunServer;
}

export interface BunHostOptions {
	readonly host: string;
	readonly port: number;
	readonly secret: string;
	readonly agentDir: string;
	readonly verifiedPi: VerifiedPiPackage;
	readonly sdk: PiSdkApi;
	readonly serverFactory?: BunServerFactory;
	readonly sessionFactory?: (options: {
		cwd: string;
		agentDir: string;
		sessionManager?: unknown;
	}) => Promise<{ session: PiSession }>;
	readonly allowSelfRestart?: boolean;
	readonly onRestart?: () => void;
	/**
	 * Bound on how long one control-plane host request (query, interrupt or
	 * replacement) may stay in flight before the host answers it with a typed
	 * timeout error. `0` disables the deadline. Prompt and other run-extending
	 * work is bounded by Pi, not by this host, and is never deadline-failed.
	 */
	readonly requestTimeoutMs?: number;
	/** Secret-safe lifecycle logger. Defaults to a bounded in-memory logger. */
	readonly logger?: HostLogger;
}

export interface BunHost {
	readonly endpoint: string;
	start(): void;
	close(): Promise<void>;
}

class HostError extends Error {}
class CapabilityError extends HostError {}
// A live foreign writer owns the session file. v2 maps this to the typed
// resource_conflict code so a caller never retries it as fresh work.
class SessionLeaseError extends HostError {}
class SessionSchemaError extends CapabilityError {}
// AUX-05: two registrations that would own one session identity/path. The
// second fails closed instead of replacing or mutating the first.
class SessionInvariantError extends HostError {}
// AUX-05: a late async result whose resident was replaced while it was in
// flight. The result belongs to a dead allocation and is discarded.
class SessionStaleError extends HostError {}

interface SessionFileSummary {
	readonly version: number;
	readonly writtenByNewerRuntime: boolean;
	readonly unknownRecords: number;
	readonly invalidRecords: number;
}

interface ResidentSession {
	readonly id: string;
	readonly cwd: string;
	readonly session: PiSession;
	// AUX-05: a stable allocation generation. Foreground callbacks capture the
	// resident (identity + generation) and are dropped after a replacement.
	unsubscribe: () => void;
	readonly generation: number;
	readonly dialogs: Map<string, Dialog>;
	path?: string;
	stopReason?: string;
	// AUX-13 lease bookkeeping. `leaseDepth` counts concurrent mutating
	// operations so the first acquire publishes the file and the last release
	// removes it; the host event loop makes the check-then-write atomic.
	leaseDepth?: number;
	leasePath?: string;
	// AUX-13 mtime tail: the last observed session-file mtime.
	fileMtime?: number;
	// AUX-12 parsed header/record degradation, reported in the snapshot.
	schema?: SessionFileSummary;
}

interface Dialog {
	readonly primitive: "select" | "confirm" | "input" | "editor";
	readonly options?: readonly string[];
	readonly resolve: (value: string | boolean | undefined) => void;
	readonly abort?: AbortSignal;
	readonly onAbort?: () => void;
}

interface Connection {
	socket?: BunWebSocket;
	handshaken: boolean;
	protocolVersion?: 1 | 2;
	closed: boolean;
	flushing: boolean;
	readonly outbound: string[];
	outboundBytes: number;
	readonly active: Set<number>;
	// AUX-18: per-request deadlines so a hung handler cannot hold an id forever.
	readonly deadlines: Map<number, ReturnType<typeof setTimeout>>;
}

type ProtocolMode = "v1" | "auto" | "v2";
type Lifecycle = "new" | "running" | "draining" | "closed";

interface RequestEnvelope {
	readonly id: number;
	readonly method: string;
	readonly params: Record<string, unknown>;
}

interface PendingLogin {
	readonly id: string;
	readonly providerId: string;
	readonly abort: AbortController;
	frame: Record<string, unknown>;
	resolve?: (value: string) => void;
	reject?: (error: Error) => void;
	begin?: () => void;
	readonly timer: ReturnType<typeof setTimeout>;
}

function operationSet(allowRestart: boolean): Record<string, boolean> {
	return Object.fromEntries(
		HOST_OPERATIONS.map((operation) => [
			operation,
			operation === "runtime.restart" ? allowRestart : AVAILABLE_OPERATIONS.has(operation),
		]),
	);
}

// These are feature groups that the host can establish without inspecting
// private Pi state. Optional operations remain in operationSet so callers can
// distinguish a feature group from one concrete route. Image prompts are a
// capability rather than a route: `session.prompt` accepts image content
// blocks, and there is no separate `session.prompt.image` dispatch.
function hostCapabilities(): Record<string, number> {
	return { sessions: 1, agents: 1, images: 1 };
}

// AUX-18: every host command carries one supervision class. A replacement may
// preempt an in-flight prompt; an in-flight prompt must never delay or block a
// replacement. Interrupts cancel work; unblocks start or extend a run.
type HostCommandClass = "replacement" | "interrupt" | "unblock" | "query";

function hostCommandClass(method: string): HostCommandClass {
	switch (method) {
		case "runtime.restart":
			return "replacement";
		case "session.cancel":
		case "session.clearQueue":
		case "session.release":
		case "runtime.release":
			return "interrupt";
		case "session.prompt":
		case "session.followUp":
		case "session.steer":
		case "session.compact":
		case "session.configure":
			return "unblock";
		default:
			return "query";
	}
}

function loopbackLiteral(host: string): boolean {
	if (isIP(host) === 4) return host.split(".")[0] === "127";
	return host === "::1";
}

function authorized(request: Request, secret: string): boolean {
	const authorization = request.headers.get("authorization");
	if (!authorization?.startsWith("Bearer ")) return false;
	const candidate = new TextEncoder().encode(authorization.slice("Bearer ".length));
	const expected = new TextEncoder().encode(secret);
	return candidate.byteLength === expected.byteLength && timingSafeEqual(candidate, expected);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

const MAX_REFRESH_FAILURES = 64;

// Project the SDK refresh result onto the wire shape. An SDK error message can
// echo credentials, URLs and absolute paths, so failures cross the boundary as
// a bounded, providerId-sorted {providerId, reason} list without the message.
function refreshOutcome(value: unknown): {
	aborted: boolean;
	failed: Array<{ providerId: string; reason: "refresh_failed" }>;
} {
	const record = isRecord(value) ? value : {};
	const errors = record.errors;
	const providerIds = new Set<string>();
	if (errors && typeof (errors as ReadonlyMap<string, unknown>).keys === "function") {
		for (const providerId of (errors as ReadonlyMap<string, unknown>).keys()) {
			if (typeof providerId === "string" && providerId !== "") providerIds.add(providerId);
		}
	}
	const failed = [...providerIds]
		.sort()
		.slice(0, MAX_REFRESH_FAILURES)
		.map((providerId) => ({ providerId, reason: "refresh_failed" as const }));
	return { aborted: record.aborted === true, failed };
}

interface SessionModelProjection {
	readonly id: string;
	readonly name: string;
	readonly provider: string;
}

function nonEmptyString(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}

// Pi exposes the selected model and thinking level directly on AgentSession.
// Keep the wire projection anchored to those public values rather than to
// defaults or the model catalog, which can describe a different selection.
function projectSessionModel(value: unknown): SessionModelProjection | undefined {
	if (!isRecord(value)) return undefined;
	const provider = nonEmptyString(value.provider) ?? nonEmptyString(value.providerId);
	const id = nonEmptyString(value.id) ?? nonEmptyString(value.modelId);
	if (!provider || !id) return undefined;
	return { provider, id, name: nonEmptyString(value.name) ?? id };
}

function sessionModelChoices(session: PiSession): SessionModelProjection[] {
	const choices: SessionModelProjection[] = [];
	const add = (value: SessionModelProjection | undefined): void => {
		if (!value || choices.some((item) => item.provider === value.provider && item.id === value.id))
			return;
		choices.push(value);
	};
	try {
		for (const model of session.modelRuntime?.getModels?.() ?? []) add(projectSessionModel(model));
	} catch {
		// A model catalog failure does not make an already selected public session
		// model unknown. Return that selected model below without inventing others.
	}
	add(projectSessionModel(session.model));
	return choices;
}

function selectConfigOption(
	id: "provider" | "model" | "thinking",
	name: string,
	category: string,
	currentValue: string,
	choices: readonly { readonly value: string; readonly name: string }[],
): Record<string, unknown> {
	return { id, name, category, type: "select", currentValue, options: choices };
}

function sessionConfiguration(session: PiSession): {
	readonly configOptions: Record<string, unknown>[];
	readonly metadata: Record<string, unknown>;
} {
	const model = projectSessionModel(session.model);
	const thinkingLevel = nonEmptyString(session.thinkingLevel);
	const models = sessionModelChoices(session);
	const configOptions: Record<string, unknown>[] = [];
	if (model) {
		const providers = new Map<string, string>();
		for (const choice of models) providers.set(choice.provider, choice.provider);
		configOptions.push(
			selectConfigOption(
				"provider",
				"Provider",
				"provider",
				model.provider,
				[...providers].map(([value, name]) => ({ value, name })),
			),
		);
		configOptions.push(
			selectConfigOption(
				"model",
				"Model",
				"model",
				model.id,
				models
					.filter((choice) => choice.provider === model.provider)
					.map((choice) => ({ value: choice.id, name: choice.name })),
			),
		);
	}
	if (thinkingLevel) {
		const levels = new Set<string>();
		try {
			for (const level of session.getAvailableThinkingLevels?.() ?? []) {
				const value = nonEmptyString(level);
				if (value) levels.add(value);
			}
		} catch {
			// The selected level is still authoritative when the optional list is
			// unavailable; expose only that real value instead of a guessed list.
		}
		levels.add(thinkingLevel);
		configOptions.push(
			selectConfigOption(
				"thinking",
				"Thinking",
				"thinking",
				thinkingLevel,
				[...levels].map((value) => ({ value, name: value })),
			),
		);
	}
	const metadata: Record<string, unknown> = {};
	if (model) {
		metadata.providerId = model.provider;
		metadata.modelId = model.id;
	}
	if (thinkingLevel) metadata.thinkingLevel = thinkingLevel;
	return { configOptions, metadata };
}

function hasSessionManagerShape(value: unknown): value is PiSdkApi["SessionManager"] {
	if (typeof value !== "function" && !isRecord(value)) return false;
	const manager = value as Record<string, unknown>;
	return (
		typeof manager.create === "function" &&
		typeof manager.list === "function" &&
		typeof manager.open === "function"
	);
}

function compatibleSessionDir0_85_1(cwd: string, agentDir: string): string {
	const resolvedCwd = resolve(cwd);
	const resolvedAgentDir = resolve(agentDir);
	const safePath = `--${resolvedCwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
	const sessionDir = join(resolvedAgentDir, "sessions", safePath);
	mkdirSync(sessionDir, { recursive: true });
	return sessionDir;
}

/**
 * Adapt only the verified public Pi root API required by the session host.
 *
 * Pi 0.85.1 exposes SessionManager but not getDefaultSessionDir from its
 * public root. The local derivation deliberately matches that release's
 * implementation and is unavailable for every other version.
 */
export function createPiSdkApi(
	verifiedPi: VerifiedPiPackage,
	publicApi: PiPublicRootApi,
): PiSdkApi {
	if (verifiedPi.packageVersion !== "0.85.1")
		throw new HostError(
			`Pi public session compatibility requires version 0.85.1, got ${JSON.stringify(verifiedPi.packageVersion)}`,
		);
	if (!isRecord(publicApi)) throw new HostError("loaded Pi public API is not a module record");
	const createAgentSession = publicApi.createAgentSession;
	const sessionManager = publicApi.SessionManager;
	if (typeof createAgentSession !== "function" || !hasSessionManagerShape(sessionManager))
		throw new HostError("loaded Pi public API does not expose the required session API");
	return {
		createAgentSession: createAgentSession as PiSdkApi["createAgentSession"],
		getDefaultSessionDir: compatibleSessionDir0_85_1,
		SessionManager: sessionManager,
		publicApi,
		ModelRuntime: publicApi.ModelRuntime,
		SettingsManager: publicApi.SettingsManager,
		DefaultPackageManager: publicApi.DefaultPackageManager,
		DefaultResourceLoader: publicApi.DefaultResourceLoader,
		ProjectTrustStore: publicApi.ProjectTrustStore,
		hasTrustRequiringProjectResources: publicApi.hasTrustRequiringProjectResources,
	};
}

function validRequestId(id: unknown): id is number {
	return typeof id === "number" && Number.isSafeInteger(id) && id > 0 && id <= MAX_SAFE_REQUEST_ID;
}

function parseV1Request(value: unknown): RequestEnvelope | undefined {
	if (
		!isRecord(value) ||
		!validRequestId(value.id) ||
		typeof value.method !== "string" ||
		value.method.length === 0
	)
		return undefined;
	// v1 historically accepts omitted and null params as no-argument calls. v2
	// deliberately rejects both, so normalization cannot happen before selection.
	const params = value.params === undefined || value.params === null ? {} : value.params;
	return isRecord(params) ? { id: value.id, method: value.method, params } : undefined;
}

function parseV2Request(value: unknown): RequestEnvelope | undefined {
	if (
		!isRecord(value) ||
		!Object.hasOwn(value, "id") ||
		!Object.hasOwn(value, "method") ||
		!Object.hasOwn(value, "params")
	)
		return undefined;
	if (
		!validRequestId(value.id) ||
		typeof value.method !== "string" ||
		value.method.length === 0 ||
		value.method.includes("\0")
	)
		return undefined;
	return isRecord(value.params)
		? { id: value.id, method: value.method, params: value.params }
		: undefined;
}

function parseProtocolMode(raw: string | undefined): ProtocolMode {
	switch (raw?.trim().toLowerCase()) {
		case undefined:
		case "":
		case "v1":
			return "v1";
		case "auto":
			return "auto";
		case "v2":
			return "v2";
		default:
			throw new HostError(
				`PIXIE_PI_PROTOCOL must be one of v1, auto or v2, got ${JSON.stringify(raw)}`,
			);
	}
}

function validHostIdentity(identity: unknown): identity is string {
	return typeof identity === "string" && /^[a-f0-9]{32}$/.test(identity);
}

function loadOrCreateHostIdentity(agentDir: string): string {
	if (!isAbsolute(agentDir)) throw new HostError("agentDir must be absolute");
	const path = join(agentDir, "pixie", "host-identity.json");
	mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
	try {
		const info = lstatSync(path);
		if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600)
			throw new HostError("native host identity must be a regular non-symlink file with mode 0600");
		let record: unknown;
		try {
			record = JSON.parse(readFileSync(path, "utf8"));
		} catch {
			throw new HostError("native host identity file is invalid");
		}
		if (!isRecord(record) || record.version !== 1 || !validHostIdentity(record.identity))
			throw new HostError("native host identity file is invalid");
		return record.identity;
	} catch (error) {
		if (!(error && typeof error === "object" && "code" in error && error.code === "ENOENT"))
			throw error;
	}

	const identity = randomBytes(16).toString("hex");
	const raw = JSON.stringify({ version: 1, identity });
	const temporary = join(dirname(path), `.host-identity-${randomUUID()}`);
	let descriptor: number | undefined;
	try {
		descriptor = openSync(temporary, "wx", 0o600);
		chmodSync(temporary, 0o600);
		writeSync(descriptor, raw);
		fsyncSync(descriptor);
		closeSync(descriptor);
		descriptor = undefined;
		renameSync(temporary, path);
		const published = lstatSync(path);
		if (!published.isFile() || published.isSymbolicLink() || (published.mode & 0o777) !== 0o600)
			throw new HostError("published native host identity is insecure");
		return identity;
	} catch (error) {
		if (descriptor !== undefined) closeSync(descriptor);
		try {
			unlinkSync(temporary);
		} catch {
			/* not published or already removed */
		}
		throw error;
	}
}

function asString(params: Record<string, unknown>, field: string): string {
	const value = params[field];
	if (typeof value !== "string" || value.length === 0) throw new HostError(`${field} is required`);
	return value;
}

function asOptionalString(params: Record<string, unknown>, field: string): string | undefined {
	const value = params[field];
	if (value === undefined || value === null) return undefined;
	if (typeof value !== "string") throw new HostError(`${field} must be a string`);
	return value;
}

function exactExistingDirectory(path: string, field: string): string {
	if (!isAbsolute(path) || resolve(path) !== path)
		throw new HostError(`${field} must be an exact absolute path`);
	let canonical: string;
	try {
		canonical = realpathSync(path);
	} catch {
		throw new HostError(`${field} must be an existing non-symlink directory`);
	}
	if (canonical !== path || !statSync(path).isDirectory())
		throw new HostError(`${field} must be an existing non-symlink directory`);
	return path;
}

function encode(value: unknown): string {
	try {
		return JSON.stringify(value);
	} catch {
		throw new HostError("host response could not be serialized");
	}
}

// AUX-32: a browser reply never carries raw native/SDK error text, which can
// embed absolute paths, URLs or credentials. The message is redacted through
// the same boundary as retained logs and bounded. The typed code/reason still
// identifies the failure class, and the redacted cause is logged separately.
const MAX_HOST_ERROR_MESSAGE_LENGTH = 512;

function safeHostErrorMessage(error: unknown, secrets: readonly string[] = []): string {
	const raw = error instanceof Error ? error.message : "operation failed";
	const redacted = redactHostLogText(raw, secrets);
	if (redacted === "") return "operation failed";
	return redacted.length > MAX_HOST_ERROR_MESSAGE_LENGTH
		? redacted.slice(0, MAX_HOST_ERROR_MESSAGE_LENGTH)
		: redacted;
}

function defaultServerFactory(): BunServerFactory {
	return {
		serve: (options) =>
			Bun.serve(options as Parameters<typeof Bun.serve>[0]) as unknown as BunServer,
	};
}

function sha256Hex(raw: string): string {
	return createHash("sha256").update(raw).digest("hex");
}

// --- Agent sources (filesystem Pi layout, not SDK) ---

function agentProjectRoot(params: Record<string, unknown>): string {
	const projectDir = params.projectDir;
	if (typeof projectDir === "string" && projectDir !== "") return projectDir;
	const cwd = params.cwd;
	if (typeof cwd === "string" && cwd !== "") return cwd;
	return "";
}

function agentDirectories(
	agentDir: string,
	projectRoot: string,
): { path: string; global: boolean }[] {
	const directories = [{ path: join(agentDir, "agents"), global: true }];
	if (projectRoot !== "")
		directories.push({ path: join(projectRoot, ".pi", "agents"), global: false });
	return directories;
}

function readBoundedAgentFile(path: string): string {
	let info: ReturnType<typeof statSync>;
	try {
		info = lstatSync(path);
	} catch {
		throw new HostError(`Cannot load agent: ${path}`);
	}
	if (!info.isFile() || info.isSymbolicLink()) throw new HostError(`Cannot load agent: ${path}`);
	const raw = readFileSync(path, "utf8");
	if (new TextEncoder().encode(raw).byteLength > AGENT_DOCUMENT_MAX_BYTES)
		throw new HostError("Agent exceeds 65536 bytes");
	return raw;
}

function parseAgentFrontmatterValue(value: string): unknown {
	const trimmed = value.trim();
	if (trimmed === "" || trimmed === "~") return undefined;
	if (trimmed === "null" || trimmed === "Null" || trimmed === "NULL") return undefined;
	if (trimmed === "true" || trimmed === "True" || trimmed === "TRUE") return true;
	if (trimmed === "false" || trimmed === "False" || trimmed === "FALSE") return false;
	if (/^[-+]?[0-9]+$/.test(trimmed)) {
		const parsed = Number.parseInt(trimmed, 10);
		if (Number.isSafeInteger(parsed)) return parsed;
	}
	if (/^[-+]?([0-9]+\.[0-9]*|\.[0-9]+|[0-9]+)([eE][-+]?[0-9]+)?$/.test(trimmed)) {
		const parsed = Number.parseFloat(trimmed);
		if (Number.isFinite(parsed)) return parsed;
	}
	if (trimmed.startsWith('"') && trimmed.endsWith('"') && trimmed.length >= 2) {
		try {
			return JSON.parse(trimmed) as unknown;
		} catch {
			return trimmed.slice(1, -1);
		}
	}
	if (trimmed.startsWith("'") && trimmed.endsWith("'") && trimmed.length >= 2)
		return trimmed.slice(1, -1).replaceAll("''", "'");
	if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
		try {
			return JSON.parse(trimmed) as unknown;
		} catch {
			return trimmed;
		}
	}
	return trimmed;
}

function parseAgentFrontmatter(text: string): Record<string, unknown> {
	const trimmed = text.trim();
	if (trimmed.startsWith("{")) {
		try {
			const parsed: unknown = JSON.parse(trimmed);
			if (isRecord(parsed)) return parsed;
			return {};
		} catch {
			throw new HostError("Missing agent frontmatter");
		}
	}
	const result: Record<string, unknown> = {};
	for (const line of text.split("\n")) {
		const stripped = line.trim();
		if (stripped === "" || stripped.startsWith("#")) continue;
		const colon = stripped.indexOf(":");
		if (colon < 0) continue;
		if (
			stripped[colon + 1] !== undefined &&
			stripped[colon + 1] !== " " &&
			stripped[colon + 1] !== "\t"
		)
			continue;
		const key = stripped.slice(0, colon).trim();
		if (key === "") continue;
		const rest = stripped.slice(colon + 1).trim();
		if (rest === "" || rest.startsWith("#")) continue;
		result[key] = parseAgentFrontmatterValue(rest.split("#")[0]?.trim() ?? "");
	}
	return result;
}

function parseAgentDocument(raw: string): { metadata: Record<string, unknown>; content: string } {
	const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(raw);
	if (!match) throw new HostError("Missing agent frontmatter");
	return { metadata: parseAgentFrontmatter(match[1] ?? ""), content: match[2] ?? "" };
}

function yamlScalar(value: unknown): string {
	if (value === undefined || value === null) return "null";
	if (typeof value === "boolean") return value ? "true" : "false";
	if (typeof value === "number") return Number.isFinite(value) ? String(value) : "null";
	if (typeof value === "string") {
		if (
			value !== "" &&
			!/^[\s]|[\s]$/.test(value) &&
			!value.includes("\n") &&
			!value.includes("#") &&
			!value.includes(":") &&
			!/^["'\-?:,[\]{}#&*!|>'"%@`]/.test(value) &&
			!/^(null|Null|NULL|~|true|True|TRUE|false|False|FALSE)$/.test(value) &&
			!/^[-+]?[0-9]+$/.test(value)
		)
			return value;
		return JSON.stringify(value);
	}
	return JSON.stringify(value) ?? "null";
}

function marshalAgentFrontmatter(properties: Record<string, unknown>): string {
	const keys = Object.keys(properties).sort();
	if (keys.length === 0) return "{}\n";
	let out = "";
	for (const key of keys) {
		const encodedKey = /^[A-Za-z0-9_-]+$/.test(key) ? key : (JSON.stringify(key) ?? key);
		const value = properties[key];
		if (Array.isArray(value)) {
			if (value.length === 0) {
				out += `${encodedKey}: []\n`;
				continue;
			}
			out += `${encodedKey}:\n`;
			for (const item of value) out += `  - ${yamlScalar(item)}\n`;
			continue;
		}
		if (isRecord(value)) {
			out += `${encodedKey}:\n`;
			for (const nested of Object.keys(value).sort())
				out += `  ${nested}: ${yamlScalar((value as Record<string, unknown>)[nested])}\n`;
			continue;
		}
		out += `${encodedKey}: ${yamlScalar(value)}\n`;
	}
	return out;
}

function encodeAgentDocument(properties: Record<string, unknown>, content: string): string {
	const encoded = marshalAgentFrontmatter(properties);
	return `---\n${encoded}---\n${content}`;
}

function agentRevision(raw: string): string {
	return `sha256:${sha256Hex(raw)}`;
}

function agentTextOr(value: unknown, fallback: string): string {
	return typeof value === "string" && value !== "" ? value : fallback;
}

function validAgentName(name: string): boolean {
	return /^[\p{L}\p{N} _-]+$/u.test(name);
}

interface AgentDefinition {
	readonly type: string;
	readonly path: string;
	readonly name: string;
	readonly description: string;
	readonly content: string;
	readonly revision: string;
	readonly global: boolean;
	readonly writable: boolean;
	readonly executionEligibility: string;
	readonly properties: Record<string, unknown>;
}

function readAgentDefinition(path: string, global: boolean, root: string): AgentDefinition {
	const raw = readBoundedAgentFile(path);
	let resolved: string;
	try {
		resolved = realpathSync(path);
	} catch {
		throw new HostError(`Cannot load agent: ${path}`);
	}
	if (resolved !== path || dirname(resolved) !== root)
		throw new HostError("agent path changed while reading");
	const { metadata, content } = parseAgentDocument(raw);
	return {
		type: "agent",
		path,
		name: agentTextOr(metadata.name, basename(path).replace(/\.md$/, "")),
		description: agentTextOr(metadata.description, ""),
		content,
		revision: agentRevision(raw),
		global,
		writable: true,
		executionEligibility: "unknown",
		properties: metadata,
	};
}

function listAgentSources(
	agentDir: string,
	projectRoot: string,
): { sources: AgentDefinition[]; warnings: string[] } {
	const sources: AgentDefinition[] = [];
	const warnings: string[] = [];
	for (const directory of agentDirectories(agentDir, projectRoot)) {
		let root: string;
		try {
			root = realpathSync(directory.path);
		} catch (error) {
			if ((error as NodeJS.ErrnoException)?.code === "ENOENT") continue;
			throw error instanceof Error ? error : new HostError(`Cannot list agents: ${directory.path}`);
		}
		if (root !== directory.path) continue;
		let entries: string[];
		try {
			entries = readdirSync(directory.path)
				.filter((name) => name.endsWith(".md"))
				.sort();
		} catch (error) {
			if ((error as NodeJS.ErrnoException)?.code === "ENOENT") continue;
			throw error instanceof Error ? error : new HostError(`Cannot list agents: ${directory.path}`);
		}
		for (const name of entries) {
			const path = join(directory.path, name);
			let resolved: string;
			try {
				resolved = realpathSync(path);
			} catch {
				warnings.push(`Cannot load agent: ${path}`);
				continue;
			}
			if (dirname(resolved) !== root) continue;
			try {
				sources.push(readAgentDefinition(path, directory.global, root));
			} catch {
				warnings.push(`Cannot load agent: ${path}`);
			}
		}
	}
	return { sources, warnings };
}

function findAgentSource(
	agentDir: string,
	projectRoot: string,
	path: string,
): AgentDefinition | undefined {
	if (path === "") return undefined;
	return listAgentSources(agentDir, projectRoot).sources.find((source) => source.path === path);
}

function atomicWriteFile(path: string, data: string): void {
	mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${randomUUID()}.tmp`;
	let descriptor: number | undefined;
	try {
		descriptor = openSync(temporary, "wx", 0o600);
		writeSync(descriptor, data);
		fsyncSync(descriptor);
		closeSync(descriptor);
		descriptor = undefined;
		renameSync(temporary, path);
	} finally {
		if (descriptor !== undefined) {
			try {
				closeSync(descriptor);
			} catch {
				/* already closed */
			}
		}
		try {
			unlinkSync(temporary);
		} catch {
			/* published or never created */
		}
	}
}

function atomicCreateFile(path: string, data: string): void {
	mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${randomUUID()}.tmp`;
	let descriptor: number | undefined;
	try {
		descriptor = openSync(temporary, "wx", 0o600);
		writeSync(descriptor, data);
		fsyncSync(descriptor);
		closeSync(descriptor);
		descriptor = undefined;
		try {
			linkSync(temporary, path);
		} catch (error) {
			if ((error as NodeJS.ErrnoException)?.code === "EEXIST") {
				const exists = new Error("Agent already exists") as Error & { code?: string };
				exists.code = "EEXIST";
				throw exists;
			}
			throw error;
		}
	} finally {
		if (descriptor !== undefined) {
			try {
				closeSync(descriptor);
			} catch {
				/* already closed */
			}
		}
		try {
			unlinkSync(temporary);
		} catch {
			/* published or never created */
		}
	}
}

// --- Native session file coexistence (AUX-13 lease, AUX-12 schema) ---

interface SessionLeaseRecord {
	readonly pid: number;
	readonly acquiredAt: number;
	readonly runtimeId: string;
}

function sessionLeasePath(path: string): string {
	return `${path}${SESSION_LEASE_SUFFIX}`;
}

function processAlive(pid: number): boolean {
	if (!Number.isInteger(pid) || pid <= 0) return false;
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		// EPERM means the process exists but belongs to another user.
		return (error as NodeJS.ErrnoException)?.code === "EPERM";
	}
}

function readSessionLease(path: string): SessionLeaseRecord | undefined {
	let raw: string;
	try {
		raw = readFileSync(path, "utf8");
	} catch {
		return undefined;
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return undefined;
	}
	if (!isRecord(parsed)) return undefined;
	const pid = parsed.pid;
	if (typeof pid !== "number" || !Number.isInteger(pid) || pid <= 0) return undefined;
	return {
		pid,
		acquiredAt: typeof parsed.acquiredAt === "number" ? parsed.acquiredAt : 0,
		runtimeId: typeof parsed.runtimeId === "string" ? parsed.runtimeId : "",
	};
}

// A lease blocks only a live foreign writer. A dead pid or a lease whose mtime
// is older than the staleness window is reclaimed; this host's own lease never
// conflicts with itself.
function sessionLeaseIsLive(record: SessionLeaseRecord, path: string): boolean {
	if (record.pid === process.pid) return false;
	if (!processAlive(record.pid)) return false;
	let mtime = 0;
	try {
		mtime = statSync(path).mtimeMs;
	} catch {
		return false;
	}
	return Date.now() - mtime < SESSION_LEASE_STALE_MS;
}

// "unavailable" means the session has no writable file location yet (for
// example an SDK-only path whose directory does not exist). There is then no
// possible foreign writer, so the mutation proceeds without a lease file.
function claimSessionLease(
	path: string,
	record: SessionLeaseRecord,
): "claimed" | "conflict" | "unavailable" {
	try {
		mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
	} catch {
		return "unavailable";
	}
	let descriptor: number | undefined;
	try {
		descriptor = openSync(path, "wx", 0o600);
	} catch (error) {
		if ((error as NodeJS.ErrnoException)?.code !== "EEXIST") return "unavailable";
		const existing = readSessionLease(path);
		if (existing && sessionLeaseIsLive(existing, path)) return "conflict";
		try {
			unlinkSync(path);
		} catch {
			/* another owner reclaimed it first */
		}
		try {
			descriptor = openSync(path, "wx", 0o600);
		} catch {
			return "conflict";
		}
	}
	try {
		writeSync(descriptor, JSON.stringify(record));
		fsyncSync(descriptor);
	} finally {
		closeSync(descriptor);
	}
	return "claimed";
}

const SESSION_RECORD_TYPES = new Set([
	"session",
	"message",
	"model_change",
	"thinking_level_change",
	"compaction",
	"branch_summary",
	"custom",
	"custom_message",
	"label",
	"session_info",
]);

// Parse only the session header and record shapes needed for the version guard.
// Unknown records and unparseable lines are counted, never rewritten or
// rejected: a newer Pi release may add record types this host must ignore.
function readSessionFileSummary(path: string): SessionFileSummary | undefined {
	let raw: string;
	try {
		raw = readFileSync(path, "utf8");
	} catch {
		return undefined;
	}
	let version = 0;
	let unknownRecords = 0;
	let invalidRecords = 0;
	let first = true;
	for (const line of raw.split("\n")) {
		const trimmed = line.trim();
		if (trimmed === "") continue;
		let record: unknown;
		try {
			record = JSON.parse(trimmed);
		} catch {
			invalidRecords++;
			continue;
		}
		if (!isRecord(record)) {
			invalidRecords++;
			continue;
		}
		const type = typeof record.type === "string" ? record.type : "";
		if (first) {
			first = false;
			if (type === "session") {
				version =
					typeof record.version === "number" && Number.isInteger(record.version)
						? record.version
						: 1;
				continue;
			}
			// A file without a header is legacy v1; keep parsing this line.
		}
		if (!SESSION_RECORD_TYPES.has(type)) unknownRecords++;
	}
	return {
		version,
		writtenByNewerRuntime: version > SESSION_SCHEMA_VERSION,
		unknownRecords,
		invalidRecords,
	};
}

// Repair a dangling tool call by appending one synthetic error result. The
// native transcript is never rewritten; the repaired messages are projected.
function repairDanglingToolCalls(messages: readonly unknown[]): {
	messages: unknown[];
	repaired: number;
} {
	const results = new Set<string>();
	for (const value of messages) {
		const message = isRecord(value) ? value : {};
		if (message.role === "toolResult" && typeof message.toolCallId === "string")
			results.add(message.toolCallId);
	}
	const repairedMessages: unknown[] = [];
	let repaired = 0;
	for (const value of messages) {
		repairedMessages.push(value);
		const message = isRecord(value) ? value : {};
		if (message.role !== "assistant" || !Array.isArray(message.content)) continue;
		for (const block of message.content) {
			if (!isRecord(block) || block.type !== "toolCall") continue;
			const id = typeof block.id === "string" ? block.id : "";
			if (id === "" || results.has(id)) continue;
			results.add(id);
			repaired++;
			repairedMessages.push({
				role: "toolResult",
				toolCallId: id,
				toolName: typeof block.name === "string" ? block.name : "tool",
				content: [
					{
						type: "text",
						text: "Tool result unavailable: the session was reopened before this tool call completed.",
					},
				],
				details: { synthetic: true },
				isError: true,
				timestamp: Date.now(),
			});
		}
	}
	return { messages: repairedMessages, repaired };
}

// --- MCP servers (filesystem layers + bounded probe) ---

// In-process mutex serializes file writers so concurrent read-modify-write
// cycles cannot interleave. Bun runs one event loop, but async host calls
// can still overlap across awaits surrounding the synchronous file update.
let fileMutationChain: Promise<void> = Promise.resolve();

async function withFileMutation<T>(work: () => T | Promise<T>): Promise<T> {
	const previous = fileMutationChain;
	let release: () => void = () => {};
	const gate = new Promise<void>((resolve) => {
		release = resolve;
	});
	fileMutationChain = gate;
	await previous;
	try {
		return await work();
	} finally {
		release();
	}
}

interface McpServerState {
	readonly name: string;
	readonly layer: string;
	readonly path: string;
	readonly disabled: boolean;
	readonly definition: Record<string, unknown>;
}

interface McpServerCandidate {
	readonly layer: string;
	readonly path: string;
	readonly disabled: boolean;
	readonly markers: Record<string, string>;
}

interface McpConditionalMutation {
	readonly expectedAgentOwnershipToken: string;
	readonly requireNoOtherLayerCollisions: true;
}

function mcpConfigLayers(agentDir: string, projectDir: string): { name: string; path: string }[] {
	const layers: { name: string; path: string }[] = [];
	const home = homedir();
	if (home !== "") {
		layers.push(
			{ name: "user-config", path: join(home, ".config", "mcp", "mcp.json") },
			{ name: "user-agents", path: join(home, ".agents", "mcp.json") },
			{ name: "user-agents-dir", path: join(home, ".agents", "mcp", "mcp.json") },
		);
	}
	if (agentDir !== "") layers.push({ name: "agent-dir", path: join(agentDir, "mcp.json") });
	if (projectDir !== "" && isAbsolute(projectDir)) {
		layers.push(
			{ name: "project", path: join(projectDir, ".mcp.json") },
			{ name: "project-pi", path: join(projectDir, ".pi", "mcp.json") },
		);
	}
	return layers;
}

function readMCPDocument(path: string): Record<string, unknown> {
	let info: ReturnType<typeof lstatSync>;
	try {
		info = lstatSync(path);
	} catch (error) {
		if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return {};
		throw error instanceof Error ? error : new HostError(`Cannot load MCP configuration ${path}`);
	}
	if (!info.isFile() || info.isSymbolicLink())
		throw new HostError("MCP configuration must be a regular non-symlink file");
	if (info.size > MCP_CONFIG_MAX_BYTES)
		throw new HostError("MCP configuration exceeds the size bound");
	let raw: string;
	try {
		raw = readFileSync(path, "utf8");
	} catch (error) {
		throw error instanceof Error ? error : new HostError(`Cannot load MCP configuration ${path}`);
	}
	try {
		const parsed: unknown = JSON.parse(raw);
		return isRecord(parsed) ? parsed : {};
	} catch {
		throw new HostError("MCP configuration is not valid JSON");
	}
}

function readMCPLayer(path: string): Record<string, Record<string, unknown>> {
	const document = readMCPDocument(path);
	const servers = isRecord(document.mcpServers)
		? (document.mcpServers as Record<string, unknown>)
		: {};
	const result: Record<string, Record<string, unknown>> = {};
	for (const [name, value] of Object.entries(servers)) {
		if (isRecord(value)) result[name] = value;
	}
	return result;
}

function readMCPServers(
	agentDir: string,
	projectDir: string,
): { servers: McpServerState[]; warnings: string[] } {
	if (agentDir !== "" && !isAbsolute(agentDir))
		throw new HostError("Pi agent directory must be absolute");
	const merged = new Map<
		string,
		{
			name: string;
			layer: string;
			path: string;
			disabled: boolean;
			definition: Record<string, unknown>;
		}
	>();
	const warnings: string[] = [];
	for (const layer of mcpConfigLayers(agentDir, projectDir)) {
		let servers: Record<string, Record<string, unknown>>;
		try {
			servers = readMCPLayer(layer.path);
		} catch (error) {
			warnings.push(
				`Cannot load MCP configuration ${layer.path}: ${error instanceof Error ? error.message : "unreadable"}`,
			);
			continue;
		}
		for (const [name, definition] of Object.entries(servers)) {
			const existing = merged.get(name) ?? {
				name,
				layer: layer.name,
				path: layer.path,
				disabled: false,
				definition: {},
			};
			for (const [key, value] of Object.entries(definition)) existing.definition[key] = value;
			if ("disabled" in definition) existing.disabled = definition.disabled === true;
			existing.layer = layer.name;
			existing.path = layer.path;
			merged.set(name, existing);
		}
	}
	return { servers: [...merged.values()].sort((a, b) => (a.name < b.name ? -1 : 1)), warnings };
}

function mcpOwnershipMarkers(definition: Record<string, unknown>): Record<string, string> {
	const markers: Record<string, string> = {};
	for (const key of [MCP_MANAGED_BY_KEY, MCP_OWNERSHIP_TOKEN_KEY]) {
		if (typeof definition[key] === "string") markers[key] = definition[key];
	}
	return markers;
}

// A named read is an ownership inspection rather than an effective-definition
// read. Returning every source entry lets callers detect overlays without
// disclosing endpoint URLs, headers, or arbitrary native definition fields.
function readMCPServerCandidates(
	agentDir: string,
	projectDir: string,
	name: string,
): { servers: McpServerCandidate[]; warnings: string[] } {
	if (agentDir !== "" && !isAbsolute(agentDir))
		throw new HostError("Pi agent directory must be absolute");
	const servers: McpServerCandidate[] = [];
	const warnings: string[] = [];
	for (const layer of mcpConfigLayers(agentDir, projectDir)) {
		let definitions: Record<string, Record<string, unknown>>;
		try {
			definitions = readMCPLayer(layer.path);
		} catch (error) {
			warnings.push(
				`Cannot load MCP configuration ${layer.path}: ${error instanceof Error ? error.message : "unreadable"}`,
			);
			continue;
		}
		const definition = definitions[name];
		if (!definition) continue;
		servers.push({
			layer: layer.name,
			path: layer.path,
			disabled: definition.disabled === true,
			markers: mcpOwnershipMarkers(definition),
		});
	}
	return { servers, warnings };
}

function validMCPServerName(name: string): boolean {
	return name.length <= MCP_SERVER_NAME_MAX_BYTES && MCP_SERVER_NAME_PATTERN.test(name);
}

function conditionalMCPMutation(
	params: Record<string, unknown>,
): McpConditionalMutation | undefined {
	const hasExpectedToken = Object.hasOwn(params, "expectedAgentOwnershipToken");
	const hasCollisionRequirement = Object.hasOwn(params, "requireNoOtherLayerCollisions");
	if (!hasExpectedToken && !hasCollisionRequirement) return undefined;
	if (
		typeof params.expectedAgentOwnershipToken !== "string" ||
		params.expectedAgentOwnershipToken === "" ||
		params.requireNoOtherLayerCollisions !== true
	)
		throw new HostError("invalid conditional MCP mutation");
	return {
		expectedAgentOwnershipToken: params.expectedAgentOwnershipToken,
		requireNoOtherLayerCollisions: true,
	};
}

function mcpAgentEntryOwned(definition: unknown, expectedToken: string): boolean {
	return (
		isRecord(definition) &&
		definition[MCP_MANAGED_BY_KEY] === MCP_MANAGED_BY_VERSION &&
		definition[MCP_OWNERSHIP_TOKEN_KEY] === expectedToken
	);
}

function mcpOwnershipConflict(): never {
	throw new HostError(MCP_OWNERSHIP_CONFLICT);
}

async function mutateMCPServers(
	agentDir: string,
	projectDir: string,
	name: string,
	conditional: McpConditionalMutation | undefined,
	mode: "upsert" | "remove",
	apply: (servers: Record<string, unknown>) => boolean,
): Promise<void> {
	return withFileMutation(() => {
		if (agentDir === "" || !isAbsolute(agentDir))
			throw new HostError("Pi agent directory must be absolute");
		const path = join(agentDir, "mcp.json");
		const document = readMCPDocument(path);
		const servers = isRecord(document.mcpServers)
			? (document.mcpServers as Record<string, unknown>)
			: {};
		if (conditional) {
			const { servers: candidates, warnings } = readMCPServerCandidates(agentDir, projectDir, name);
			// A layer that cannot be inspected is indistinguishable from a
			// collision. Do not replace an entry when ownership cannot be proved.
			if (warnings.length > 0) mcpOwnershipConflict();
			const agentOwned = mcpAgentEntryOwned(servers[name], conditional.expectedAgentOwnershipToken);
			const otherLayerCollision = candidates.some((candidate) => candidate.layer !== "agent-dir");
			if (
				otherLayerCollision ||
				(mode === "upsert" && name in servers && !agentOwned) ||
				(mode === "remove" && !agentOwned)
			)
				mcpOwnershipConflict();
		}
		if (!apply(servers)) return;
		atomicWriteFile(path, JSON.stringify({ ...document, mcpServers: servers }));
	});
}

function mcpProjectDir(params: Record<string, unknown>): string {
	return typeof params.projectDir === "string" ? params.projectDir : "";
}

function mcpHeaderMap(value: unknown): Record<string, string> {
	if (!isRecord(value)) return {};
	const result: Record<string, string> = {};
	for (const [key, raw] of Object.entries(value)) {
		if (typeof raw === "string") result[key] = raw;
	}
	return result;
}

async function mcpReadBody(response: Response): Promise<string> {
	// Stream with a byte limit so a hostile or misbehaving endpoint cannot
	// force unbounded buffering before the 1 MiB cap is enforced.
	const body = response.body;
	if (!body) {
		const text = await response.text();
		if (new TextEncoder().encode(text).byteLength > MCP_PROBE_MAX_BODY)
			throw new Error("MCP response exceeds the size bound");
		return text;
	}
	const reader = body.getReader();
	const chunks: Uint8Array[] = [];
	let total = 0;
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			if (!value) continue;
			total += value.byteLength;
			if (total > MCP_PROBE_MAX_BODY) {
				try {
					await reader.cancel();
				} catch {
					/* already closed */
				}
				throw new Error("MCP response exceeds the size bound");
			}
			chunks.push(value);
		}
	} finally {
		try {
			reader.releaseLock();
		} catch {
			/* already released */
		}
	}
	const combined = new Uint8Array(total);
	let offset = 0;
	for (const chunk of chunks) {
		combined.set(chunk, offset);
		offset += chunk.byteLength;
	}
	return new TextDecoder().decode(combined);
}

function mcpParseSSE(raw: string, wantId: number): string {
	const normalized = raw.replaceAll("\r\n", "\n");
	for (const event of normalized.split("\n\n")) {
		const data: string[] = [];
		for (const line of event.split("\n")) {
			if (!line.startsWith("data:")) continue;
			data.push(line.slice("data:".length).trimStart());
		}
		if (data.length === 0) continue;
		const payload = data.join("\n");
		try {
			const envelope = JSON.parse(payload) as { id?: unknown };
			if (wantId !== 0 && envelope.id !== undefined && String(envelope.id) !== String(wantId))
				continue;
			return payload;
		} catch {}
	}
	throw new Error("MCP event stream did not contain a JSON-RPC response");
}

function mcpDecodeResult(payload: string): Record<string, unknown> | undefined {
	if (payload.trim() === "") return undefined;
	let envelope: { result?: unknown; error?: { code?: unknown; message?: unknown } };
	try {
		envelope = JSON.parse(payload) as typeof envelope;
	} catch {
		throw new Error("MCP response is not valid JSON-RPC");
	}
	if (envelope.error !== undefined && envelope.error !== null) {
		const code = typeof envelope.error.code === "number" ? envelope.error.code : 0;
		const message = typeof envelope.error.message === "string" ? envelope.error.message : "error";
		throw new Error(`MCP error ${code}: ${message}`);
	}
	return isRecord(envelope.result) ? envelope.result : undefined;
}

async function mcpPost(
	endpoint: string,
	headers: Record<string, string>,
	sessionId: string,
	body: Record<string, unknown>,
	wantId: number,
): Promise<{ body: string; sessionId: string }> {
	const controller = new AbortController();
	const timeout = setTimeout(() => controller.abort(), MCP_PROBE_TIMEOUT_MS);
	try {
		const response = await fetch(endpoint, {
			method: "POST",
			headers: {
				"content-type": "application/json",
				accept: "application/json, text/event-stream",
				...(sessionId !== "" ? { "mcp-session-id": sessionId } : {}),
				...headers,
			},
			body: JSON.stringify(body),
			signal: controller.signal,
			redirect: "manual",
		});
		const nextSession = response.headers.get("mcp-session-id") ?? "";
		if (response.status < 200 || response.status >= 300) {
			try {
				await response.body?.cancel();
			} catch {
				/* bounded discard */
			}
			throw new Error(`MCP endpoint returned HTTP ${response.status}`);
		}
		const raw = await mcpReadBody(response);
		if (raw.trim() === "") return { body: "", sessionId: nextSession };
		const contentType = response.headers.get("content-type") ?? "";
		if (contentType.toLowerCase().includes("text/event-stream"))
			return { body: mcpParseSSE(raw, wantId), sessionId: nextSession };
		return { body: raw, sessionId: nextSession };
	} finally {
		clearTimeout(timeout);
	}
}

async function probeMCPServer(
	definition: Record<string, unknown>,
): Promise<Record<string, unknown>> {
	const tools: string[] = [];
	const result: Record<string, unknown> = { reachable: false, tools };
	const urlText =
		typeof definition.url === "string" && definition.url !== ""
			? definition.url
			: typeof definition.uri === "string"
				? definition.uri
				: "";
	if (urlText === "") throw new HostError("MCP server definition is missing a url");
	let parsed: URL;
	try {
		parsed = new URL(urlText);
	} catch {
		throw new HostError("MCP server url must be an http(s) URL without credentials");
	}
	if (
		(parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
		parsed.host === "" ||
		parsed.username !== "" ||
		parsed.password !== ""
	)
		throw new HostError("MCP server url must be an http(s) URL without credentials");
	const headers = mcpHeaderMap(definition.headers);
	const fail = (error: unknown): Record<string, unknown> => ({
		reachable: false,
		tools: [],
		error: error instanceof Error ? error.message : "probe failed",
	});
	try {
		const init = await mcpPost(
			parsed.toString(),
			headers,
			"",
			{
				jsonrpc: "2.0",
				id: 1,
				method: "initialize",
				params: {
					protocolVersion: MCP_PROBE_PROTOCOL_VERSION,
					capabilities: {},
					clientInfo: { name: "pixie", version: "1" },
				},
			},
			1,
		);
		const initResult = mcpDecodeResult(init.body);
		if (initResult) {
			if (typeof initResult.protocolVersion === "string")
				result.protocolVersion = initResult.protocolVersion;
			if (isRecord(initResult.serverInfo)) result.serverInfo = initResult.serverInfo;
		}
		try {
			await mcpPost(
				parsed.toString(),
				headers,
				init.sessionId,
				{ jsonrpc: "2.0", method: "notifications/initialized" },
				0,
			);
		} catch (error) {
			return fail(error);
		}
		const listed = await mcpPost(
			parsed.toString(),
			headers,
			init.sessionId,
			{ jsonrpc: "2.0", id: 2, method: "tools/list", params: {} },
			2,
		);
		const toolsResult = mcpDecodeResult(listed.body);
		if (toolsResult && Array.isArray(toolsResult.tools)) {
			const encoder = new TextEncoder();
			for (const tool of toolsResult.tools) {
				if (tools.length >= MCP_PROBE_MAX_TOOLS) break;
				if (!isRecord(tool) || typeof tool.name !== "string" || tool.name === "") continue;
				if (encoder.encode(tool.name).byteLength > MCP_PROBE_MAX_TOOL_NAME_BYTES) continue;
				tools.push(tool.name);
			}
		}
		return { ...result, reachable: true, tools };
	} catch (error) {
		return fail(error);
	}
}

// --- Legacy Pixie MCP connection stores (bridge parity, no SDK) ---

function readStateFile(path: string, max: number): unknown {
	try {
		const info = statSync(path);
		if (!info.isFile() || info.size === 0 || info.size > max) return undefined;
		return JSON.parse(readFileSync(path, "utf8") as string) as unknown;
	} catch (error) {
		if ((error as NodeJS.ErrnoException)?.code === "ENOENT") return undefined;
		throw error;
	}
}

function requiredBridgeString(value: unknown, label: string, max = 4096): string {
	if (typeof value !== "string" || value.length === 0 || value.length > max || value.includes("\0"))
		throw new HostError(`Invalid ${label}`);
	return value;
}

function mcpConnection(
	value: unknown,
	agentDir: string,
): { name: string; definition: Record<string, unknown>; source: Record<string, unknown> } {
	const raw = isRecord(value) ? value : {};
	const source = raw.type === "mcp" && isRecord(raw.server) ? raw.server : raw;
	const name = requiredBridgeString(source.name, "MCP name", 128);
	if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.includes("__"))
		throw new HostError("Invalid MCP name");
	const definition: Record<string, unknown> = {};
	if (source.command !== undefined || source.type === "stdio") {
		definition.command = requiredBridgeString(source.command, "MCP command");
		if (
			source.args !== undefined &&
			(!Array.isArray(source.args) || source.args.some((item) => typeof item !== "string"))
		)
			throw new HostError("MCP arguments must be strings");
		definition.args = source.args ?? [];
		const env = isRecord(source.env) ? source.env : {};
		for (const item of Object.values(env)) {
			if (typeof item !== "string") throw new HostError("MCP environment values must be strings");
		}
		definition.env = Object.fromEntries(Object.entries(env));
		if (source.cwd !== undefined) {
			const rawCwd = requiredBridgeString(source.cwd, "MCP working directory").replace(
				/\$\{([A-Z0-9_]+)\}/g,
				(_match, key: string) => {
					const resolvedEnv = process.env[key];
					if (resolvedEnv === undefined)
						throw new HostError(`Missing MCP environment variable: ${key}`);
					return resolvedEnv;
				},
			);
			definition.cwd = resolve(agentDir, rawCwd);
		}
	} else {
		const urlRaw = source.uri ?? source.url;
		const urlText = requiredBridgeString(urlRaw, "MCP URL");
		let url: URL;
		try {
			url = new URL(urlText);
		} catch {
			throw new HostError("MCP requires HTTP(S) without URL credentials");
		}
		if (!["http:", "https:"].includes(url.protocol) || url.username !== "" || url.password !== "")
			throw new HostError("MCP requires HTTP(S) without URL credentials");
		if (
			source.type !== undefined &&
			!["http", "streamable_http", "sse"].includes(String(source.type))
		)
			throw new HostError("Unsupported MCP transport");
		definition.url = url.href;
		if (source.type === "sse") definition.httpTransport = "sse";
		const headers = Array.isArray(source.headers)
			? Object.fromEntries(
					source.headers.map((item) => {
						if (!isRecord(item)) throw new HostError("MCP header is invalid");
						return [
							requiredBridgeString(item.name, "header"),
							typeof item.value === "string" ? item.value : "",
						];
					}),
				)
			: Object.fromEntries(
					Object.entries(isRecord(source.headers) ? source.headers : {}).map(([key, item]) => [
						key,
						typeof item === "string" ? item : "",
					]),
				);
		definition.headers = headers;
	}
	return {
		name,
		definition,
		source: {
			...(isRecord(source) ? source : {}),
			name,
			type: definition.command ? "stdio" : source.type === "sse" ? "sse" : "http",
		},
	};
}

function legacyMcpDocument(value: unknown): Record<string, unknown> {
	if (!isRecord(value) || Array.isArray(value) || Object.hasOwn(value, "mcpServers"))
		throw new HostError(
			"Native MCP configuration is managed by pi-mcp-adapter, not legacy connection administration",
		);
	return value;
}

async function mutateStateFile(
	path: string,
	change: (state: Record<string, unknown>) => void,
): Promise<void> {
	return withFileMutation(() => {
		// Single read, single write: the document is read once so a concurrent
		// writer cannot slip between two reads.
		const current = readStateFile(path, MCP_STATE_MAX_BYTES);
		const state = isRecord(current) ? (current as Record<string, unknown>) : {};
		change(state);
		atomicWriteFile(path, JSON.stringify(state));
	});
}

export function createBunHost(options: BunHostOptions): BunHost {
	const host = options.host.trim();
	const secret = options.secret.trim();
	const protocolMode = parseProtocolMode(process.env.PIXIE_PI_PROTOCOL);
	const allowSelfRestart = options.allowSelfRestart === true;
	if (!loopbackLiteral(host))
		throw new HostError("assistant host must be a literal loopback address");
	if (secret.length < MIN_SECRET_LENGTH)
		throw new HostError(`assistant secret must be at least ${MIN_SECRET_LENGTH} characters`);
	if (!Number.isInteger(options.port) || options.port < 0 || options.port > 65535)
		throw new HostError("assistant port must be between 0 and 65535");
	if (!options.agentDir) throw new HostError("agentDir is required");
	if (
		options.requestTimeoutMs !== undefined &&
		(!Number.isFinite(options.requestTimeoutMs) || options.requestTimeoutMs < 0)
	)
		throw new HostError("requestTimeoutMs must be a finite non-negative number");
	if (!options.verifiedPi?.packageVersion)
		throw new HostError("verified Pi package version is required");
	if (
		typeof options.sdk?.createAgentSession !== "function" ||
		typeof options.sdk.getDefaultSessionDir !== "function" ||
		typeof options.sdk.SessionManager?.create !== "function" ||
		typeof options.sdk.SessionManager.list !== "function" ||
		typeof options.sdk.SessionManager.open !== "function"
	)
		throw new HostError("loaded Pi SDK does not expose the required session API");

	let server: BunServer | undefined;
	let lifecycle: Lifecycle = "new";
	let runtimeId = "";
	const bootId = randomUUID();
	const logger = options.logger ?? createHostLogger({ secrets: [secret] });
	let closePromise: Promise<void> | undefined;
	const residents = new Map<string, ResidentSession>();
	// AUX-05: generation/epoch state. A resident gets one allocation generation;
	// the replacement epoch advances whenever the current resident for an
	// identity is retired, which fences every async result started before it.
	let allocatedGeneration = 0;
	let replacementEpoch = 0;
	const residentPaths = new Map<string, ResidentSession>();
	// AUX-18: set as soon as a replacement is acknowledged. New unblock work is
	// refused while a replacement is pending, but the replacement itself is not
	// blocked by in-flight prompts.
	let replacementPending = false;
	// AUX-03: prototype binding behind the negotiated operation set. The route
	// stays unavailable while Pi exposes no public active-run identity.
	const steering = new SteeringRegistry();
	const requestTimeoutMs = options.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS;
	const connections = new Set<Connection>();
	const inflightCalls = new Set<Promise<void>>();
	const inflightInstalls = new Set<Promise<ResidentSession>>();
	const sessionFactory =
		options.sessionFactory ?? ((input) => options.sdk.createAgentSession(input));
	const logins = new Map<string, PendingLogin>();
	let endpoint = `ws://${host}:${options.port}/pi`;
	let cachedRuntime: unknown | undefined;
	let runtimePromise: Promise<unknown> | undefined;

	const currentOperationSet = (): Record<string, boolean> => operationSet(allowSelfRestart);

	// AUX-05: one allocation generation per resident and one replacement epoch
	// for the identity. Identity alone is enough to detect a swapped object, but
	// the explicit generation comparison states the invariant the callbacks rely
	// on and survives a future in-place reuse.
	const nextGeneration = (): number => {
		allocatedGeneration += 1;
		return allocatedGeneration;
	};

	const isCurrentResident = (resident: ResidentSession): boolean => {
		const current = residents.get(resident.id);
		return current === resident && current.generation === resident.generation;
	};

	// AUX-05: a session file path has at most one resident owner. Registering
	// the same path from a second allocation fails with a typed invariant error
	// rather than silently replacing or shadowing the first owner.
	const registerResidentPath = (resident: ResidentSession, path: string): void => {
		const owner = residentPaths.get(path);
		if (owner && owner !== resident)
			throw new SessionInvariantError(
				`session path ${JSON.stringify(path)} is already registered to another session`,
			);
		const previous = resident.path;
		if (previous && previous !== path && residentPaths.get(previous) === resident)
			residentPaths.delete(previous);
		resident.path = path;
		residentPaths.set(path, resident);
	};

	const releaseResidentPath = (resident: ResidentSession): void => {
		if (resident.path && residentPaths.get(resident.path) === resident)
			residentPaths.delete(resident.path);
	};

	// Retiring the current resident advances the replacement epoch and drops any
	// steering binding, so late callbacks and late results are fenced.
	const retireResident = (resident: ResidentSession): void => {
		if (residents.get(resident.id) === resident) {
			residents.delete(resident.id);
			replacementEpoch += 1;
			steering.retire(resident.id, resident.generation);
		}
		releaseResidentPath(resident);
	};

	// AUX-13: one advisory lease per native session file. It is taken only for
	// mutating operations, blocks only a live foreign writer, and is released
	// with the resident (release/reload/close). Two owners therefore fail closed
	// instead of interleaving writes.
	const acquireSessionLease = (resident: ResidentSession): void => {
		const depth = resident.leaseDepth ?? 0;
		if (depth > 0) {
			resident.leaseDepth = depth + 1;
			return;
		}
		const path = resident.path;
		if (!path) return;
		const leasePath = sessionLeasePath(path);
		const claim = claimSessionLease(leasePath, {
			pid: process.pid,
			acquiredAt: Date.now(),
			runtimeId,
		});
		if (claim === "conflict") {
			throw new SessionLeaseError(
				`session ${JSON.stringify(resident.id)} is owned by a live Pi writer; retry after it exits`,
			);
		}
		if (claim === "unavailable") return;
		resident.leaseDepth = 1;
		resident.leasePath = leasePath;
	};

	const releaseSessionLease = (resident: ResidentSession): void => {
		if (!resident.leaseDepth) return;
		resident.leaseDepth--;
		if (resident.leaseDepth > 0) return;
		const leasePath = resident.leasePath;
		resident.leasePath = undefined;
		if (!leasePath) return;
		try {
			const current = readSessionLease(leasePath);
			if (current && current.pid !== process.pid) return;
			unlinkSync(leasePath);
		} catch {
			/* the lease was already reclaimed or removed */
		}
	};

	const withSessionLease = async <T>(
		resident: ResidentSession,
		work: () => T | Promise<T>,
	): Promise<T> => {
		acquireSessionLease(resident);
		try {
			return await work();
		} finally {
			releaseSessionLease(resident);
		}
	};

	const publish = (resident: ResidentSession, event: Record<string, unknown>): void => {
		if (event.type === "queue_update") return; // Pixie's durable queue is authoritative.
		// AUX-05: a subscribe callback captured before a replacement must not
		// mutate or publish against the replacement resident.
		if (!isCurrentResident(resident)) return;
		if (event.type === "message_end" && isRecord(event.message)) {
			const stopReason = event.message.stopReason;
			if (typeof stopReason === "string") resident.stopReason = stopReason;
		}
		const frame = encode({
			method: "session.event",
			params: { sessionId: resident.id, event },
		});
		for (const connection of connections) if (connection.handshaken) queue(connection, frame);
	};

	const publishMethod = (method: string, params: Record<string, unknown>): void => {
		const frame = encode({ method, params });
		for (const connection of connections) if (connection.handshaken) queue(connection, frame);
	};

	const queue = (connection: Connection, frame: string): void => {
		if (connection.closed) return;
		const frameBytes = new TextEncoder().encode(frame).byteLength;
		if (
			frameBytes > MAX_OUTBOUND_BYTES ||
			connection.outboundBytes + frameBytes > MAX_OUTBOUND_BYTES
		) {
			closeConnection(connection, 1013, "event delivery lost; reload required");
			return;
		}
		connection.outbound.push(frame);
		connection.outboundBytes += frameBytes;
		if (!connection.flushing) {
			connection.flushing = true;
			queueMicrotask(() => flush(connection));
		}
	};

	const flush = (connection: Connection): void => {
		connection.flushing = false;
		if (connection.closed || !connection.socket) return;
		while (connection.outbound.length) {
			const frame = connection.outbound.shift();
			if (!frame) continue;
			connection.outboundBytes -= new TextEncoder().encode(frame).byteLength;
			try {
				if (connection.socket.send(frame) === -1) {
					closeConnection(connection, 1013, "event delivery lost; reload required");
					return;
				}
			} catch {
				closeConnection(connection, 1013, "event delivery lost; reload required");
				return;
			}
		}
	};

	const closeConnection = (
		connection: Connection,
		code = 1000,
		reason = "assistant stopped",
	): void => {
		if (connection.closed) return;
		connection.closed = true;
		connection.outbound.length = 0;
		connection.outboundBytes = 0;
		for (const deadline of connection.deadlines.values()) clearTimeout(deadline);
		connection.deadlines.clear();
		connections.delete(connection);
		try {
			connection.socket?.close(code, reason);
		} catch {
			/* already closed */
		}
	};

	// AUX-18: a graceful stop fails every in-flight request with a typed,
	// delivery-uncertain reply before the socket closes. A caller therefore
	// never sees a prompt vanish without a response.
	const failInFlight = (connection: Connection, message: string): void => {
		if (!connection.closed && connection.socket) {
			for (const id of [...connection.active]) {
				const frame =
					connection.protocolVersion === 2
						? encode({ id, error: { code: -32005, reason: "delivery_uncertain", message } })
						: encode({ id, error: { code: -32000, message } });
				try {
					connection.socket.send(frame);
				} catch {
					/* the id still clears; the socket is already unusable */
				}
			}
		}
		connection.active.clear();
		for (const deadline of connection.deadlines.values()) clearTimeout(deadline);
		connection.deadlines.clear();
	};

	const sessionDir = (cwd: string): string =>
		options.sdk.getDefaultSessionDir(cwd, options.agentDir);

	// AUX-12: inspect the native header before trusting the parsed session. A
	// newer header is refused rather than guessed; unknown records and parse
	// failures are recorded and ignored. The file is never rewritten here.
	const applySessionFileGuard = (resident: ResidentSession): void => {
		if (!resident.path) return;
		const summary = readSessionFileSummary(resident.path);
		if (!summary) return;
		resident.schema = summary;
		if (summary.writtenByNewerRuntime)
			throw new SessionSchemaError(
				`session ${JSON.stringify(resident.id)} was written by a newer Pi runtime (version ${summary.version})`,
			);
		try {
			resident.fileMtime = statSync(resident.path).mtimeMs;
		} catch {
			/* tail remains disabled until the file is readable */
		}
		if (summary.unknownRecords > 0 || summary.invalidRecords > 0)
			logger.info("session.schema.degraded", {
				sessionId: resident.id,
				version: summary.version,
				unknownRecords: summary.unknownRecords,
				invalidRecords: summary.invalidRecords,
			});
	};

	const install = (cwd: string, sessionManager?: unknown): Promise<ResidentSession> => {
		const installation = (async () => {
			const manager = sessionManager ?? options.sdk.SessionManager.create(cwd, sessionDir(cwd));
			const { session } = await sessionFactory({
				cwd,
				agentDir: options.agentDir,
				sessionManager: manager,
			});
			if (!session || typeof session.sessionId !== "string" || session.sessionId.length === 0)
				throw new HostError("Pi session has no native sessionId");
			if (lifecycle !== "running") {
				await session.dispose();
				throw new HostError("assistant host is draining");
			}
			const owner = residents.get(session.sessionId);
			if (owner) {
				// A second registration for one identity must fail closed. Never
				// dispose an object the live resident already owns.
				if (owner.session !== session) await session.dispose();
				throw new SessionInvariantError("native session is already resident");
			}
			const generation = nextGeneration();
			const dialogs = new Map<string, Dialog>();
			const resident: ResidentSession = {
				id: session.sessionId,
				cwd,
				session,
				generation,
				dialogs,
				unsubscribe: () => {},
			};
			try {
				if (typeof session.sessionFile === "string" && session.sessionFile !== "")
					registerResidentPath(resident, session.sessionFile);
				// The callback captures the allocation, so a late event from this
				// session after a replacement is dropped by publish().
				resident.unsubscribe = session.subscribe((event) => publish(resident, event));
				if (lifecycle !== "running") throw new HostError("assistant host is draining");
				residents.set(resident.id, resident);
				await session.bindExtensions({ mode: "rpc", uiContext: extensionUi(resident) });
				if (lifecycle !== "running") throw new HostError("assistant host is draining");
				if (
					typeof session.sessionFile === "string" &&
					session.sessionFile !== "" &&
					session.sessionFile !== resident.path
				)
					registerResidentPath(resident, session.sessionFile);
				applySessionFileGuard(resident);
				logger.info("session.created", { sessionId: resident.id });
				return resident;
			} catch (error) {
				retireResident(resident);
				resident.unsubscribe();
				await session.dispose();
				throw error;
			}
		})();
		inflightInstalls.add(installation);
		void installation.then(
			() => inflightInstalls.delete(installation),
			() => inflightInstalls.delete(installation),
		);
		return installation;
	};

	const extensionUi = (resident: ResidentSession): Record<string, unknown> => {
		const dialog = (
			primitive: Dialog["primitive"],
			title: string,
			values: Record<string, unknown>,
			opts?: { signal?: AbortSignal; timeout?: number },
		): Promise<string | boolean | undefined> =>
			new Promise((resolve, reject) => {
				if (resident.dialogs.size >= MAX_DIALOGS_PER_SESSION) {
					reject(new HostError("too many pending extension dialogs"));
					return;
				}
				const requestId = randomUUID();
				const abort = () => settleDialog(resident, requestId, undefined);
				resident.dialogs.set(requestId, {
					primitive,
					options: values.options as readonly string[] | undefined,
					resolve,
					abort: opts?.signal,
					onAbort: abort,
				});
				opts?.signal?.addEventListener("abort", abort, { once: true });
				publish(resident, {
					type: "pixie:ui:request",
					sessionId: resident.id,
					requestId,
					primitive,
					title,
					...values,
					...(typeof opts?.timeout === "number" ? { timeout: opts.timeout } : {}),
				});
			});
		return {
			select: (
				title: string,
				choices: string[],
				opts?: { signal?: AbortSignal; timeout?: number },
			) => dialog("select", title, { options: choices }, opts),
			confirm: (
				title: string,
				message: string,
				opts?: { signal?: AbortSignal; timeout?: number },
			) => dialog("confirm", title, { message }, opts),
			input: (
				title: string,
				placeholder?: string,
				opts?: { signal?: AbortSignal; timeout?: number },
			) => dialog("input", title, { placeholder }, opts),
			editor: (title: string, prefill?: string) => dialog("editor", title, { prefill }),
			notify: (message: string, level?: string) =>
				publish(resident, { type: "pixie:ui:notify", message, level }),
			setStatus: (key: string, text?: string) =>
				publish(resident, { type: "pixie:ui:status", key, text }),
			setWidget: (key: string, lines: unknown, widgetOptions?: { placement?: string }) => {
				if (Array.isArray(lines) && lines.every((line) => typeof line === "string"))
					publish(resident, {
						type: "pixie:ui:widget",
						key,
						lines,
						placement: widgetOptions?.placement,
					});
			},
			setTitle: (title: string) => publish(resident, { type: "pixie:ui:title", title }),
			setWorkingMessage: (message?: string) =>
				publish(resident, { type: "pixie:ui:working", message }),
			onTerminalInput: () => () => {},
			setWorkingVisible: () => {},
			setWorkingIndicator: () => {},
			setHiddenThinkingLabel: () => {},
			setFooter: () => {},
			setHeader: () => {},
			pasteToEditor: () => {},
			setEditorText: () => {},
			getEditorText: () => "",
			addAutocompleteProvider: () => {},
			setEditorComponent: () => {},
			getEditorComponent: () => undefined,
			getAllThemes: () => [],
			getTheme: () => undefined,
			setTheme: () => ({ success: false, error: "terminal UI is unavailable in RPC mode" }),
			getToolsExpanded: () => false,
			setToolsExpanded: () => {},
			custom: async () => {
				throw new HostError("terminal UI is unavailable in RPC mode");
			},
		};
	};

	const settleDialog = (
		resident: ResidentSession,
		requestId: string,
		value: string | boolean | undefined,
	): boolean => {
		const dialog = resident.dialogs.get(requestId);
		if (!dialog) return false;
		resident.dialogs.delete(requestId);
		if (dialog.abort && dialog.onAbort) dialog.abort.removeEventListener("abort", dialog.onAbort);
		// Pi's confirm primitive requires a boolean: map cancel/timeout to false
		// while select/input/editor keep undefined for cancellation.
		if (value === undefined && dialog.primitive === "confirm") dialog.resolve(false);
		else dialog.resolve(value);
		return true;
	};

	// AUX-12: an idle reopened session may hold a tool call whose result never
	// landed. Project a synthetic result rather than a transcript that would
	// fail to render; a streaming session keeps its real pending calls.
	const projectMessages = (
		resident: ResidentSession,
	): { messages: unknown[]; repaired: number } => {
		const messages = resident.session.messages ? [...resident.session.messages] : [];
		if (resident.session.isStreaming !== false) return { messages, repaired: 0 };
		return repairDanglingToolCalls(messages);
	};

	const snapshot = (resident: ResidentSession): Record<string, unknown> => {
		const configuration = sessionConfiguration(resident.session);
		const projected = projectMessages(resident);
		return {
			capabilities: hostCapabilities(),
			sessionId: resident.id,
			configOptions: configuration.configOptions,
			metadata: resident.schema
				? {
						...configuration.metadata,
						sessionSchema: { ...resident.schema, repairedToolCalls: projected.repaired },
					}
				: configuration.metadata,
			messages: projected.messages,
			commands: [],
			pendingDialogs: [],
		};
	};

	const residentPath = async (
		resident: ResidentSession,
		cwd: string,
	): Promise<string | undefined> => {
		if (typeof resident.path === "string" && resident.path !== "") return resident.path;
		const file = (resident.session as PiSession).sessionFile;
		if (typeof file === "string" && file !== "") {
			registerResidentPath(resident, file);
			return file;
		}
		try {
			const listed = await options.sdk.SessionManager.list(cwd, sessionDir(cwd));
			const found = listed.find((info) => info.id === resident.id);
			if (found) {
				registerResidentPath(resident, found.path);
				return found.path;
			}
		} catch {
			/* listing is best-effort for path recovery */
		}
		return undefined;
	};

	// AUX-13 mtime tail: when a foreign writer advances the session file and the
	// resident is not streaming, re-read it from disk so the projection is not a
	// stale snapshot. A lease holder (our own mutation) is never tailed.
	const tailResidentFromDisk = async (resident: ResidentSession): Promise<ResidentSession> => {
		const path = resident.path;
		if (!path || resident.leaseDepth) return resident;
		let mtime: number;
		try {
			mtime = statSync(path).mtimeMs;
		} catch {
			return resident;
		}
		if (resident.fileMtime === undefined) {
			resident.fileMtime = mtime;
			return resident;
		}
		if (mtime <= resident.fileMtime || resident.session.isStreaming === true) return resident;
		let manager: unknown;
		try {
			manager = options.sdk.SessionManager.open(path, sessionDir(resident.cwd), resident.cwd);
		} catch {
			resident.fileMtime = mtime;
			return resident;
		}
		retireResident(resident);
		resident.unsubscribe();
		try {
			await resident.session.dispose();
		} catch {
			/* replaced by the re-read below */
		}
		const fresh = await install(resident.cwd, manager);
		if (fresh.id !== resident.id) {
			retireResident(fresh);
			fresh.unsubscribe();
			await fresh.session.dispose();
			throw new HostError("native session identity changed while re-reading the session file");
		}
		registerResidentPath(fresh, path);
		fresh.fileMtime = mtime;
		logger.info("session.reloaded", { sessionId: fresh.id });
		return fresh;
	};

	const branchSession = async (
		params: Record<string, unknown>,
		clone: boolean,
	): Promise<unknown> => {
		const parentId = asString(params, "sessionId");
		const parent = residents.get(parentId);
		if (!parent) throw new HostError("session is not loaded");
		const cwd = parent.cwd;
		const parentPath = await residentPath(parent, cwd);
		if (!parentPath) throw new HostError("unknown native session");
		const directory = sessionDir(cwd);
		const entryId = asOptionalString(params, "entryId");
		let manager: unknown;
		let branchedFile: string | undefined;
		if (clone || !entryId) {
			const forkFrom = options.sdk.SessionManager.forkFrom;
			if (typeof forkFrom === "function") {
				manager = (forkFrom as (source: string, target: string, dir?: string) => unknown)(
					parentPath,
					cwd,
					directory,
				);
			} else {
				const opened = options.sdk.SessionManager.open(parentPath, directory, cwd) as {
					getLeafId?: () => string | null;
					createBranchedSession?: (leaf: string) => string | undefined;
				};
				if (typeof opened?.createBranchedSession !== "function")
					throw new CapabilityError("session branching is unavailable in the selected Pi");
				const leaf = typeof opened.getLeafId === "function" ? opened.getLeafId() : undefined;
				if (typeof leaf !== "string" || leaf === "")
					throw new HostError("Pi branch is missing a source entry");
				let created: unknown;
				try {
					created = opened.createBranchedSession(leaf);
				} catch (error) {
					throw new HostError(error instanceof Error ? error.message : "Pi fork failed");
				}
				if (typeof created !== "string" || created === "")
					throw new HostError("Pi branch did not return a session file");
				branchedFile = created;
				manager = opened;
			}
		} else {
			const opened = options.sdk.SessionManager.open(parentPath, directory, cwd) as {
				createBranchedSession?: (leaf: string) => string | undefined;
				getEntry?: (id: string) => unknown;
			};
			if (typeof opened?.createBranchedSession !== "function")
				throw new CapabilityError("session branching is unavailable in the selected Pi");
			if (typeof opened.getEntry === "function" && !opened.getEntry(entryId))
				throw new HostError("unknown branch entry");
			let created: unknown;
			try {
				created = opened.createBranchedSession(entryId);
			} catch (error) {
				throw new HostError(error instanceof Error ? error.message : "Pi fork failed");
			}
			if (typeof created !== "string" || created === "")
				throw new HostError("Pi branch did not return a session file");
			branchedFile = created;
			manager = opened;
		}
		const branched = await install(cwd, manager);
		if (branchedFile !== undefined) registerResidentPath(branched, branchedFile);
		if (branched.id === parentId) {
			retireResident(branched);
			branched.unsubscribe();
			await branched.session.dispose();
			throw new HostError("Pi returned the parent session for a branch");
		}
		return snapshot(branched);
	};

	const switchSession = async (params: Record<string, unknown>): Promise<unknown> => {
		const id = asString(params, "sessionId");
		const requested = asOptionalString(params, "sessionPath");
		let cleanRequested: string | undefined;
		if (requested !== undefined) {
			if (!isAbsolute(requested))
				throw new HostError("session.switch requires an absolute native session path");
			cleanRequested = resolve(requested);
			if (cleanRequested !== requested)
				throw new HostError("session.switch requires an absolute native session path");
		}
		const existing = residents.get(id);
		if (existing) {
			if (cleanRequested !== undefined) {
				const knownPath = await residentPath(existing, existing.cwd);
				if (knownPath !== cleanRequested)
					throw new HostError("session.switch path does not match the verified native registry");
			}
			return snapshot(await tailResidentFromDisk(existing));
		}
		if (cleanRequested !== undefined) {
			for (const resident of residents.values()) {
				const knownPath = await residentPath(resident, resident.cwd).catch(() => undefined);
				if (knownPath === cleanRequested) return snapshot(await tailResidentFromDisk(resident));
			}
			throw new HostError("unknown native session");
		}
		throw new HostError("unknown native session");
	};

	const sdkSettingsManager = (
		cwd: string,
	): {
		reload(): Promise<void>;
		flush(): Promise<void>;
		drainErrors(): unknown[];
		getGlobalSettings(): Record<string, unknown> & {
			defaultThinkingLevel?: string;
			compaction?: { reserveTokens?: number };
			packages?: unknown[];
			extensions?: unknown[];
		};
		getProjectSettings(): Record<string, unknown> & {
			packages?: unknown[];
			extensions?: unknown[];
		};
		getDefaultProvider(): string | undefined;
		getDefaultModel(): string | undefined;
		setDefaultProvider(provider: string | undefined): void;
		setDefaultModel(model: string | undefined): void;
		getDefaultThinkingLevel(): string | undefined;
		setDefaultThinkingLevel(level: string | undefined): void;
		getCompactionReserveTokens(): number;
		setPackages(packages: unknown[]): void;
		setProjectPackages(packages: unknown[]): void;
		setExtensionPaths(paths: unknown[]): void;
		setProjectExtensionPaths(paths: unknown[]): void;
	} => {
		const api = options.sdk.SettingsManager as
			| { create?: (cwd: string, agentDir: string) => unknown; fromStorage?: unknown }
			| undefined;
		if (!api || typeof api.create !== "function")
			throw new CapabilityError("SettingsManager is unavailable in the selected Pi");
		return api.create(cwd, options.agentDir) as ReturnType<typeof sdkSettingsManager>;
	};

	// The focused preference projection crosses one boundary with per-key
	// writability metadata. Keys without a public setter stay visible but are
	// reported read-only so a client never claims a mutation it cannot make.
	type SettingsManager = ReturnType<typeof sdkSettingsManager>;

	const preferenceEntries = (
		manager: SettingsManager,
	): Array<{ key: string; value: unknown; writable: boolean; source: string }> => {
		const global = manager.getGlobalSettings() as {
			defaultThinkingLevel?: string;
			compaction?: { reserveTokens?: number };
		};
		const storedReserve = global.compaction?.reserveTokens;
		return [
			{
				key: "piThinkingEffort",
				value: global.defaultThinkingLevel ?? null,
				writable: true,
				source: "pi",
			},
			{
				key: "compactionReserveTokens",
				value: storedReserve === undefined ? null : manager.getCompactionReserveTokens(),
				writable: false,
				source: "read-only",
			},
		];
	};

	const sdkModelRuntime = async (): Promise<{
		getProviders(): Array<Record<string, unknown>>;
		getModels(providerId: string): Array<Record<string, unknown>>;
		getModel(provider: string, model: string): Record<string, unknown> | undefined;
		getAvailable(provider?: unknown, options?: unknown): Promise<Array<Record<string, unknown>>>;
		checkAuth(providerId: string, options?: unknown): Promise<unknown>;
		getProviderAuthStatus?(
			providerId: string,
		): { configured?: boolean; source?: string } | undefined;
		isUsingOAuth?(providerId: string): boolean;
		listCredentials?(options?: unknown): Promise<readonly unknown[]>;
		refresh(options?: unknown): Promise<{ errors?: Map<string, unknown> }>;
		login(providerId: string, type: string, interaction: Record<string, unknown>): Promise<unknown>;
		logout(providerId: string, options?: unknown): Promise<unknown>;
	}> => {
		if (cachedRuntime) return cachedRuntime as Awaited<ReturnType<typeof sdkModelRuntime>>;
		if (runtimePromise)
			return runtimePromise as Promise<Awaited<ReturnType<typeof sdkModelRuntime>>>;
		runtimePromise = (async () => {
			const api = options.sdk.ModelRuntime as
				| { create?: (config: Record<string, unknown>) => Promise<unknown> }
				| undefined;
			if (!api || typeof api.create !== "function")
				throw new CapabilityError("ModelRuntime is unavailable in the selected Pi");
			cachedRuntime = await api.create({
				authPath: join(options.agentDir, "auth.json"),
				modelsPath: join(options.agentDir, "models.json"),
				allowModelNetwork: false,
			});
			if (!cachedRuntime) throw new HostError("selected installation returned no ModelRuntime");
			return cachedRuntime;
		})();
		try {
			return (await runtimePromise) as Awaited<ReturnType<typeof sdkModelRuntime>>;
		} finally {
			runtimePromise = undefined;
		}
	};

	const sdkPackageManager = (
		cwd: string,
		settings: unknown,
	): {
		listConfiguredPackages(): Array<{
			source: string;
			scope: string;
			filtered: boolean;
			installedPath?: string;
		}>;
		resolve(onMissing: (source: string) => Promise<"skip">): Promise<{
			extensions: Array<{
				path: string;
				enabled: boolean;
				metadata: { source: string; scope: string; origin: string; baseDir?: string };
			}>;
			skills: Array<{ path: string; enabled: boolean }>;
			prompts: Array<{ path: string; enabled: boolean }>;
		}>;
	} => {
		const api = options.sdk.DefaultPackageManager as
			| (new (options: {
					cwd: string;
					agentDir: string;
					settingsManager: unknown;
			  }) => unknown)
			| undefined;
		if (typeof api !== "function")
			throw new CapabilityError("DefaultPackageManager is unavailable in the selected Pi");
		return new api({ cwd, agentDir: options.agentDir, settingsManager: settings }) as ReturnType<
			typeof sdkPackageManager
		>;
	};

	const optionalCwd = (params: Record<string, unknown>): string => {
		const value = params.cwd;
		if (value === undefined || value === null) return options.agentDir;
		if (typeof value !== "string" || !isAbsolute(value) || value.includes("\0"))
			throw new HostError("bridge parameter cwd must be an absolute path");
		return value;
	};

	const providersInventory = async (params: Record<string, unknown>): Promise<unknown> => {
		const runtime = await sdkModelRuntime();
		const filter = Array.isArray(params.providerIds)
			? params.providerIds.filter((id): id is string => typeof id === "string")
			: [];
		const available = await runtime.getAvailable(undefined, {
			signal: AbortSignal.timeout(PROVIDER_LIST_AVAILABLE_TIMEOUT_MS),
		});
		const ready = new Set(
			available.map((model) => String((model as Record<string, unknown>).provider)),
		);
		const entries = await Promise.all(
			runtime
				.getProviders()
				.filter((provider) => !filter.length || filter.includes(String(provider.id)))
				.map(async (provider) => {
					const id = String(provider.id);
					const auth = await runtime.checkAuth(id, {
						signal: AbortSignal.timeout(PROVIDER_AUTH_TIMEOUT_MS),
					});
					const authInfo = isRecord(provider.auth) ? provider.auth : {};
					const apiKeyLogin = Boolean((authInfo as Record<string, unknown>).apiKey);
					const oauth =
						Boolean((authInfo as Record<string, unknown>).oauth) ||
						Boolean((authInfo as Record<string, unknown>).oauthFlow);
					const display = String(provider.name ?? id);
					const models = runtime.getModels(id).map((model) => {
						const record = model as Record<string, unknown>;
						const modelId = typeof record.id === "string" ? record.id : String(record.id ?? "");
						const modelName =
							typeof record.name === "string" && record.name !== "" ? record.name : modelId;
						return {
							id: modelId,
							name: modelName,
							contextLimit: typeof record.contextWindow === "number" ? record.contextWindow : null,
							maxOutputTokens: typeof record.maxTokens === "number" ? record.maxTokens : null,
							reasoning: typeof record.reasoning === "boolean" ? record.reasoning : null,
							modalities: Array.isArray(record.input)
								? (record.input as unknown[]).filter(
										(entry): entry is string => typeof entry === "string",
									)
								: [],
						};
					});
					return {
						providerId: id,
						providerName: display,
						name: display,
						configured: Boolean(auth),
						available: ready.has(id),
						visibleInSetup: true,
						deprecated: false,
						replacement: "",
						readinessCheck: true,
						lastRefreshError: "",
						refreshing: false,
						configKeys: [
							...(apiKeyLogin
								? [
										{
											name: "api_key",
											default: "",
											secret: true,
											required: true,
											oauthFlow: false,
											primary: true,
										},
									]
								: []),
							...(oauth
								? [
										{
											name: "oauth",
											default: "",
											secret: false,
											required: false,
											oauthFlow: true,
											primary: false,
										},
									]
								: []),
						],
						models,
					};
				}),
		);
		return { entries };
	};

	// Pi's AuthStatus.source is a fixed enum and never a credential value. Map
	// explicit sources to field-presence flags so settings can be inspected
	// before mutation without ever returning a key, token, URL or filesystem path.
	const EXPLICIT_AUTH_SOURCES = new Set([
		"stored",
		"runtime",
		"environment",
		"models_json_key",
		"models_json_command",
	]);

	const providerConfigProjection = (
		runtime: Awaited<ReturnType<typeof sdkModelRuntime>>,
		providerId: string,
	): Record<string, unknown> => {
		const provider = runtime
			.getProviders()
			.map((entry) => entry as Record<string, unknown>)
			.find((entry) => String(entry.id) === providerId);
		if (!provider) throw new HostError(`Unknown provider ${JSON.stringify(providerId)}`);
		const auth = isRecord(provider.auth) ? provider.auth : {};
		const status = runtime.getProviderAuthStatus?.(providerId);
		const source = typeof status?.source === "string" ? status.source : "unknown";
		const configured = status?.configured === true;
		const oauth = runtime.isUsingOAuth?.(providerId) === true;
		const explicit = configured && EXPLICIT_AUTH_SOURCES.has(source);
		const fields: Array<Record<string, unknown>> = [];
		if (auth.apiKey || configured) {
			fields.push({ name: "api_key", isSet: explicit && !oauth, source });
		}
		if (oauth || auth.oauth || auth.oauthFlow) {
			fields.push({ name: "oauth", isSet: oauth, source });
		}
		return { providerId, configured, source, fields };
	};

	const slashCommands = async (params: Record<string, unknown>): Promise<unknown> => {
		const availableCommands: Array<Record<string, unknown>> = [
			{ name: "compact", description: "Compact the conversation" },
		];
		const seen = new Set(["compact"]);
		try {
			const cwd = optionalCwd(params);
			const settings = sdkSettingsManager(cwd);
			const manager = sdkPackageManager(cwd, settings);
			const resolved = await manager.resolve(async () => "skip");
			const add = (name: string): void => {
				if (name === "" || seen.has(name)) return;
				seen.add(name);
				availableCommands.push({ name, description: "" });
			};
			const slashName = (path: string): string => {
				const base = basename(path).replace(/\.md$/i, "");
				return base === "SKILL" ? basename(dirname(path)) : base;
			};
			for (const prompt of resolved.prompts) {
				if (prompt.enabled) add(slashName(prompt.path));
			}
			for (const skill of resolved.skills) {
				const record = skill as unknown as { path?: unknown; enabled?: unknown };
				if (record.enabled === true && typeof record.path === "string")
					add(`skill:${slashName(record.path)}`);
			}
		} catch {
			/* keep the builtin command when native discovery is unavailable */
		}
		return { availableCommands };
	};

	const publishLogin = (login: PendingLogin): void => {
		publishMethod("provider.login", {
			loginId: login.id,
			providerId: login.providerId,
			frame: login.frame,
		});
	};

	const startLogin = (params: Record<string, unknown>): Record<string, unknown> => {
		const providerId = asString(params, "providerId");
		const rawType = params.type === undefined || params.type === null ? "api_key" : params.type;
		if (rawType !== "api_key" && rawType !== "oauth")
			throw new HostError("Invalid authentication method");
		const loginId = asString(params, "loginId");
		if ([...logins.values()].some((login) => login.providerId === providerId))
			throw new HostError("Authentication already in progress");
		if (logins.has(loginId)) throw new HostError("Duplicate login ID");
		const abort = new AbortController();
		const login: PendingLogin = {
			id: loginId,
			providerId,
			abort,
			frame: { kind: "progress", message: "Starting Pi authentication…" },
			timer: setTimeout(() => {
				if (logins.get(loginId) === login) logins.delete(loginId);
				abort.abort();
			}, LOGIN_TIMEOUT_MS),
		};
		const begin = (): void => {
			void (async () => {
				const runtime = await sdkModelRuntime();
				return runtime.login(providerId, String(rawType), {
					signal: abort.signal,
					prompt: (prompt: Record<string, unknown>) =>
						new Promise<string>((resolvePrompt, rejectPrompt) => {
							const kind = typeof prompt.type === "string" ? prompt.type : "";
							const reply =
								kind === "select"
									? { kind: "select", message: prompt.message, options: prompt.options }
									: {
											kind: "prompt",
											message: prompt.message,
											placeholder: prompt.placeholder,
											secret: kind === "secret",
											allowEmpty: false,
										};
							const promptSignal = prompt.signal as AbortSignal | undefined;
							const signal =
								promptSignal !== undefined
									? AbortSignal.any([abort.signal, promptSignal])
									: abort.signal;
							const cancel = (): void => {
								login.resolve = undefined;
								login.reject = undefined;
								rejectPrompt(new Error("Authentication cancelled"));
							};
							if (signal.aborted) {
								cancel();
								return;
							}
							signal.addEventListener("abort", cancel, { once: true });
							login.resolve = (value: string) => {
								signal.removeEventListener("abort", cancel);
								login.resolve = undefined;
								login.reject = undefined;
								resolvePrompt(value);
							};
							login.reject = rejectPrompt;
							login.frame = reply;
							publishLogin(login);
						}),
					notify: (event: Record<string, unknown>) => {
						const kind = typeof event.type === "string" ? event.type : "";
						if (kind === "auth_url")
							login.frame = {
								kind: "authUrl",
								url: event.url,
								instructions: event.instructions,
							};
						else if (kind === "device_code")
							login.frame = {
								kind: "deviceCode",
								userCode: event.userCode,
								verificationUri: event.verificationUri,
								expiresInSeconds: event.expiresInSeconds,
							};
						else login.frame = { kind: "progress", message: event.message };
						publishLogin(login);
					},
				});
			})()
				.then(
					() => {
						login.frame = { kind: "success" };
						publishLogin(login);
					},
					() => {
						login.frame = { kind: "error", message: "Pi authentication failed or was cancelled." };
						publishLogin(login);
					},
				)
				.finally(() => {
					clearTimeout(login.timer);
					if (logins.get(login.id) === login) logins.delete(login.id);
				});
		};
		// Store one object reference so mutations made by begin/prompt/notify
		// are visible to loginReply through the same map entry.
		login.begin = begin;
		logins.set(loginId, login);
		return { loginId, frame: login.frame };
	};

	const closeHost = (): Promise<void> => {
		if (closePromise) return closePromise;
		closePromise = (async () => {
			if (lifecycle === "closed") return;
			lifecycle = "draining";
			logger.info("host.draining");
			// Fail every in-flight request before the sockets close so a stop or
			// restart never drops a prompt without a typed reply.
			for (const connection of connections)
				failInFlight(connection, "assistant host is stopping; the request may not have completed");
			server?.stop(true);
			for (const connection of [...connections]) closeConnection(connection);
			for (const login of [...logins.values()]) {
				login.abort.abort();
				clearTimeout(login.timer);
			}
			logins.clear();
			cachedRuntime = undefined;
			const pending = [...inflightCalls, ...inflightInstalls];
			if (pending.length) {
				await Promise.race([
					Promise.allSettled(pending),
					new Promise<void>((resolve) => setTimeout(resolve, DRAIN_TIMEOUT_MS)),
				]);
			}
			for (const resident of [...residents.values()]) {
				releaseSessionLease(resident);
				retireResident(resident);
				resident.unsubscribe();
				for (const requestId of resident.dialogs.keys())
					settleDialog(resident, requestId, undefined);
				try {
					await resident.session.dispose();
				} catch {
					/* close every remaining resident */
				}
				logger.info("session.released", { sessionId: resident.id });
			}
			lifecycle = "closed";
			logger.info("host.closed");
		})();
		return closePromise;
	};

	const dispatch = async (method: string, params: Record<string, unknown>): Promise<unknown> => {
		if (lifecycle !== "running") throw new HostError("assistant host is draining");
		// AUX-18: once a replacement has been acknowledged, new run-extending
		// work is refused. The replacement and its interrupt/query siblings are
		// not blocked by anything already in flight.
		if (replacementPending && hostCommandClass(method) === "unblock")
			throw new CapabilityError("assistant host is restarting; the operation was not started");
		switch (method) {
			case "session.list": {
				const cwd = typeof params.cwd === "string" ? params.cwd : undefined;
				if (!cwd)
					return {
						sessions: [...residents.values()].map((session) => ({
							sessionId: session.id,
							cwd: session.cwd,
						})),
					};
				const cleanCwd = exactExistingDirectory(cwd, "session list cwd");
				const listed = await options.sdk.SessionManager.list(cleanCwd, sessionDir(cleanCwd));
				return {
					sessions: listed.map((info) => ({
						sessionId: info.id,
						cwd: info.cwd || cleanCwd,
						title: info.name,
						updatedAt: info.modified?.toISOString(),
					})),
				};
			}
			case "session.create": {
				for (const key of ["model", "modelId", "provider", "thinkingLevel", "thinking_level"]) {
					if (key in params)
						throw new HostError(
							"Bun assistant does not support create-time model or thinking overrides",
						);
				}
				if (Array.isArray(params.mcpServers) && params.mcpServers.length > 0)
					throw new HostError("Bun assistant does not support create-time MCP servers");
				const cwd = exactExistingDirectory(asString(params, "cwd"), "session create cwd");
				return snapshot(await install(cwd));
			}
			case "session.load": {
				const id = asString(params, "sessionId");
				const cwd = exactExistingDirectory(asString(params, "cwd"), "session load cwd");
				const existing = residents.get(id);
				if (existing) {
					if (existing.cwd !== cwd)
						throw new HostError("session cwd does not match the resident session");
					return snapshot(await tailResidentFromDisk(existing));
				}
				const listed = await options.sdk.SessionManager.list(cwd, sessionDir(cwd));
				const found = listed.find((session) => session.id === id);
				if (!found) throw new HostError("unknown native session");
				return snapshot(
					await install(cwd, options.sdk.SessionManager.open(found.path, sessionDir(cwd), cwd)),
				);
			}
			case "session.prompt": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const generation = resident.generation;
				const epoch = replacementEpoch;
				let steeringBinding: SteeringBinding | undefined;
				// Reset so a prompt without a fresh message_end cannot report the
				// previous prompt's stale stopReason.
				resident.stopReason = undefined;
				try {
					const { text, images } = promptContent(params.content);
					let preflight: boolean | undefined;
					try {
						await resident.session.prompt(text, {
							images,
							source: "rpc",
							preflightResult: (accepted) => {
								preflight = accepted;
								// AUX-03: a preflight-accepted prompt is the run binding
								// the steering spike evaluates. It is only valid while
								// this exact allocation is still current.
								if (
									accepted &&
									residents.get(resident.id) === resident &&
									replacementEpoch === epoch
								)
									steeringBinding = steering.begin(resident.id, generation);
							},
						});
					} catch (error) {
						throw new HostError(
							preflight === false
								? "prompt rejected"
								: error instanceof Error
									? error.message
									: "prompt failed",
						);
					}
					if (preflight === false) throw new HostError("prompt rejected");
					// AUX-05: a replacement epoch change fences the late result.
					if (!isCurrentResident(resident) || replacementEpoch !== epoch)
						throw new SessionStaleError("session was replaced while the prompt was in flight");
					await resident.session.waitForIdle();
					if (!isCurrentResident(resident) || replacementEpoch !== epoch)
						throw new SessionStaleError("session was replaced while the prompt was in flight");
					return { stopReason: resident.stopReason || "stop" };
				} finally {
					if (steeringBinding) steering.end(steeringBinding);
				}
			}
			case "session.cancel": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				resident.session.clearQueue();
				await resident.session.abort();
				return {};
			}
			case "session.configure": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				if ("mcpServers" in params)
					throw new HostError("session.configure does not accept create-time MCP servers");
				const configId = typeof params.configId === "string" ? params.configId : "";
				const value = typeof params.value === "string" ? params.value : "";
				let thinking =
					typeof params.thinkingLevel === "string"
						? params.thinkingLevel
						: typeof params.thinking === "string"
							? params.thinking
							: "";
				let provider = typeof params.provider === "string" ? params.provider : "";
				let modelId = typeof params.modelId === "string" ? params.modelId : "";
				if (isRecord(params.model)) {
					if (modelId === "" && typeof params.model.id === "string") modelId = params.model.id;
					if (provider === "" && typeof params.model.provider === "string")
						provider = params.model.provider;
				}
				let remaining = value;
				switch (configId) {
					case "thinking":
					case "thinking_level":
					case "thinkingLevel":
						if (value === "") throw new HostError("session.configure requires a thinking value");
						thinking = value;
						remaining = "";
						break;
					case "provider": {
						if (value === "") throw new HostError("session.configure requires a provider value");
						const runtime = await sdkModelRuntime().catch(() => undefined);
						let resolved: string | undefined;
						if (runtime) {
							try {
								const available = await runtime.getAvailable(undefined, {
									signal: AbortSignal.timeout(PROVIDER_AUTH_TIMEOUT_MS),
								});
								resolved = available
									.map((model) => model as Record<string, unknown>)
									.find((model) => String(model.provider) === value)?.id as string | undefined;
							} catch {
								resolved = undefined;
							}
							if (!resolved) {
								resolved = runtime
									.getModels(value)
									.map((model) => (model as Record<string, unknown>).id)
									.find((id): id is string => typeof id === "string" && id !== "");
							}
						} else if (typeof resident.session.modelRuntime?.getModels === "function") {
							resolved = resident.session.modelRuntime
								.getModels(value)
								.map((model) => (model as Record<string, unknown>).id)
								.find((id): id is string => typeof id === "string" && id !== "");
						}
						if (!resolved)
							throw new HostError(
								`Pi has no available model for provider ${JSON.stringify(value)}`,
							);
						provider = value;
						modelId = resolved;
						remaining = "";
						break;
					}
					case "model":
					case "modelId":
						if (value === "") throw new HostError("session.configure requires a model value");
						modelId = value;
						remaining = "";
						break;
					case "":
						break;
					default:
						throw new HostError(
							`session.configure does not support configuration ${JSON.stringify(configId)}`,
						);
				}
				if (thinking !== "") {
					if (typeof resident.session.setThinkingLevel !== "function")
						throw new CapabilityError("thinking configuration is unavailable in the selected Pi");
					await resident.session.setThinkingLevel(thinking);
				}
				if (modelId !== "") {
					if (typeof resident.session.setModel !== "function")
						throw new CapabilityError("model configuration is unavailable in the selected Pi");
					if (provider === "") {
						const current = resident.session.model as Record<string, unknown> | undefined;
						if (isRecord(current)) {
							if (typeof current.provider === "string") provider = current.provider;
							else if (typeof current.providerId === "string") provider = current.providerId;
						}
					}
					if (provider === "")
						throw new HostError("session.configure cannot resolve the provider for the model");
					let model: unknown;
					const sessionRuntime = resident.session.modelRuntime;
					if (sessionRuntime?.getModel) model = sessionRuntime.getModel(provider, modelId);
					if (!model) {
						const runtime = await sdkModelRuntime().catch(() => undefined);
						model = runtime?.getModel(provider, modelId);
					}
					if (!model) throw new HostError(`Unknown model ${JSON.stringify(modelId)}`);
					await resident.session.setModel(model);
				}
				if (thinking === "" && modelId === "")
					throw new HostError("session.configure requires a configuration value");
				if (remaining !== "" && thinking === "" && modelId === "")
					throw new HostError("session.configure requires a configuration value");
				return snapshot(resident);
			}
			case "session.fork":
				return branchSession(params, false);
			case "session.clone":
				return branchSession(params, true);
			case "session.getMessages": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const tailed = await tailResidentFromDisk(resident);
				return { messages: projectMessages(tailed).messages };
			}
			case "session.stats": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				if (typeof resident.session.getSessionStats !== "function")
					throw new CapabilityError("session statistics are unavailable in the selected Pi");
				return (await resident.session.getSessionStats()) ?? {};
			}
			case "session.compact": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				if (typeof resident.session.compact !== "function")
					throw new CapabilityError("session compaction is unavailable in the selected Pi");
				const instructions =
					typeof params.customInstructions === "string" && params.customInstructions !== ""
						? params.customInstructions
						: undefined;
				// AUX-03: a compaction rewrites the run queue, so a steering
				// request must not bind to a run that is being compacted away.
				steering.noteCompaction(resident.id, true);
				let result: unknown;
				try {
					result = await resident.session.compact(instructions);
				} finally {
					steering.noteCompaction(resident.id, false);
				}
				return isRecord(result) ? result : { ok: true };
			}
			case "session.rename": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const name =
					(typeof params.name === "string" && params.name !== "" ? params.name : undefined) ??
					(typeof params.title === "string" && params.title !== "" ? params.title : undefined);
				if (!name) throw new HostError("session.rename requires a name");
				if (typeof resident.session.setSessionName !== "function")
					throw new CapabilityError("session rename is unavailable in the selected Pi");
				await resident.session.setSessionName(name);
				return { ok: true };
			}
			case "session.commands": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const commands: unknown[] = [];
				try {
					const runner = resident.session.extensionRunner;
					if (runner && typeof runner.getRegisteredCommands === "function") {
						for (const command of runner.getRegisteredCommands() ?? []) commands.push(command);
					}
					if (Array.isArray(resident.session.promptTemplates)) {
						for (const template of resident.session.promptTemplates) {
							if (isRecord(template) && typeof template.name === "string")
								commands.push({ name: template.name, description: template.description ?? "" });
						}
					}
					const loader = resident.session.resourceLoader;
					if (loader && typeof loader.getSkills === "function") {
						const skills = loader.getSkills()?.skills ?? [];
						for (const skill of skills) {
							if (isRecord(skill) && typeof skill.name === "string")
								commands.push({
									name: `skill:${String(skill.name)}`,
									description: skill.description ?? "",
								});
						}
					}
				} catch {
					/* commands remain best-effort */
				}
				return { commands, availableCommands: commands };
			}
			case "session.followUp": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const action = resident.session.followUp;
				if (typeof action !== "function")
					throw new CapabilityError(`${method} is unavailable in the selected Pi`);
				let text: string;
				let images: unknown[];
				if (typeof params.message === "string" && params.message !== "") {
					text = params.message;
					images = Array.isArray(params.images) ? params.images : [];
				} else {
					const parsed = promptContent(params.content);
					text = parsed.text;
					images = parsed.images;
				}
				if (text === "" && images.length === 0) throw new HostError("prompt content is required");
				await (action as (text: string, images?: unknown[]) => Promise<void>).call(
					resident.session,
					text,
					images,
				);
				return {};
			}
			case "session.steer": {
				// AUX-03 spike: the generation guard plus the prompt preflight
				// acceptance can reject a steering request, but Pi 0.85.1 exposes
				// no public active-run identity and no steering receipt, so the
				// request cannot be proven to target the run the client observed.
				// Record the evaluated reason and keep the negotiated route
				// unavailable; do not accept a request or invent a run ID.
				const resident = residents.get(asString(params, "sessionId"));
				if (resident) {
					const evaluation = steering.evaluate(
						resident.id,
						resident.generation,
						resident.session.isStreaming === true,
					);
					logger.info("session.steer.unavailable", {
						sessionId: resident.id,
						reason: evaluation.ok ? "active-run-binding-unproven" : evaluation.reason,
					});
				}
				throw new CapabilityError("session steering requires a public Pi run identifier");
			}
			case "session.clearQueue": {
				const resident = residents.get(asString(params, "sessionId"));
				if (!resident) throw new HostError("session is not loaded");
				const cleared = resident.session.clearQueue();
				return isRecord(cleared) ? cleared : { ok: true };
			}
			case "session.switch":
				return switchSession(params);
			case "session.release":
			case "runtime.release": {
				const id = asString(params, "sessionId");
				const cwd = exactExistingDirectory(asString(params, "cwd"), "session release cwd");
				const resident = residents.get(id);
				if (!resident) throw new HostError("session is not loaded");
				if (resident.cwd !== cwd)
					throw new HostError("session release identity does not match the resident session");
				releaseSessionLease(resident);
				retireResident(resident);
				resident.unsubscribe();
				for (const requestId of resident.dialogs.keys())
					settleDialog(resident, requestId, undefined);
				await resident.session.dispose();
				logger.info("session.released", { sessionId: id });
				return { ok: true };
			}
			case "session.uiResponse":
				return uiResponse(params, false);
			case "session.uiCancel":
				return uiResponse(params, true);
			case "pi.sources.list": {
				const { sources, warnings } = listAgentSources(options.agentDir, agentProjectRoot(params));
				return { sources, warnings };
			}
			case "pi.sources.create":
			case "pi.sources.update": {
				const projectRoot = agentProjectRoot(params);
				const updating = method === "pi.sources.update" || typeof params.path === "string";
				const rawName = params.name;
				if (typeof rawName !== "string" || rawName.trim() === "" || rawName.includes("\0"))
					throw new HostError("Invalid agent name");
				const name = rawName.trim();
				if (name.length > AGENT_NAME_MAX_BYTES || !validAgentName(name))
					throw new HostError("Invalid agent name");
				const target = isRecord(params.target) ? params.target : {};
				const scopeDir =
					target.scope === "projectDir" && typeof target.projectDir === "string"
						? target.projectDir
						: "";
				let path: string;
				let previous: Record<string, unknown> = {};
				let expectedRevision = "";
				if (updating) {
					const requestPath = typeof params.path === "string" ? params.path : "";
					if (requestPath === "") throw new HostError("Unknown agent source");
					const existing = findAgentSource(options.agentDir, projectRoot, requestPath);
					if (!existing) throw new HostError("Unknown agent source");
					if (
						typeof params.expectedRevision !== "string" ||
						params.expectedRevision === "" ||
						params.expectedRevision.length > AGENT_REVISION_MAX_BYTES
					)
						throw new HostError("Invalid agent revision");
					expectedRevision = params.expectedRevision;
					if (existing.revision !== expectedRevision)
						throw new HostError("Agent changed on disk; reload before editing");
					path = existing.path;
					previous = existing.properties;
				} else {
					if ("expectedRevision" in params)
						throw new HostError("Agent revision is not valid for create");
					let directory = join(options.agentDir, "agents");
					let effectiveRoot = projectRoot;
					if (scopeDir !== "") {
						let resolvedScope: string;
						try {
							resolvedScope = realpathSync(scopeDir);
						} catch {
							throw new HostError("agent directory is unavailable");
						}
						directory = join(resolvedScope, ".pi", "agents");
						effectiveRoot = scopeDir;
					}
					mkdirSync(directory, { recursive: true, mode: 0o700 });
					let resolvedDirectory: string;
					try {
						resolvedDirectory = realpathSync(directory);
					} catch {
						throw new HostError("Agent directory is not rooted");
					}
					if (resolvedDirectory !== directory) throw new HostError("Agent directory is not rooted");
					path = join(directory, `${name}.md`);
					try {
						realpathSync(path);
						throw new HostError("Agent already exists");
					} catch (error) {
						if (error instanceof HostError) throw error;
						if ((error as NodeJS.ErrnoException)?.code !== "ENOENT")
							throw new HostError("Agent already exists");
					}
					if (findAgentSource(options.agentDir, effectiveRoot, path))
						throw new HostError("Agent already exists");
				}
				const current = findAgentSource(options.agentDir, projectRoot, path);
				if (updating && (!current || current.revision !== expectedRevision))
					throw new HostError("Agent changed on disk; reload before editing");
				if (!updating && current) throw new HostError("Agent already exists");
				const properties: Record<string, unknown> = { ...previous };
				if (isRecord(params.properties)) Object.assign(properties, params.properties);
				properties.name = name;
				properties.description = typeof params.description === "string" ? params.description : "";
				for (const key of Object.keys(properties)) {
					if (properties[key] === undefined || properties[key] === null) delete properties[key];
				}
				const document = encodeAgentDocument(
					properties,
					typeof params.content === "string" ? params.content : "",
				);
				if (new TextEncoder().encode(document).byteLength > AGENT_DOCUMENT_MAX_BYTES)
					throw new HostError("Agent must fit within 65536 bytes including frontmatter");
				try {
					if (updating) atomicWriteFile(path, document);
					else atomicCreateFile(path, document);
				} catch (error) {
					if ((error as NodeJS.ErrnoException)?.code === "EEXIST")
						throw new HostError("Agent already exists");
					throw error;
				}
				const saved = findAgentSource(options.agentDir, projectRoot, path);
				if (!saved) throw new HostError("Saved agent could not be loaded");
				return { source: saved };
			}
			case "pi.sources.delete": {
				const projectRoot = agentProjectRoot(params);
				const requestPath = typeof params.path === "string" ? params.path : "";
				const source = findAgentSource(options.agentDir, projectRoot, requestPath);
				if (!source) throw new HostError("Unknown agent source");
				if (
					typeof params.expectedRevision !== "string" ||
					params.expectedRevision === "" ||
					params.expectedRevision.length > AGENT_REVISION_MAX_BYTES
				)
					throw new HostError("Invalid agent revision");
				if (source.revision !== params.expectedRevision)
					throw new HostError("Agent changed on disk; reload before deleting");
				const current = findAgentSource(options.agentDir, projectRoot, source.path);
				if (!current || current.revision !== params.expectedRevision)
					throw new HostError("Agent changed on disk; reload before deleting");
				try {
					unlinkSync(source.path);
				} catch (error) {
					throw new HostError(error instanceof Error ? error.message : "Agent delete failed");
				}
				return { ok: true };
			}
			case "pi.agent-mentions.list": {
				const { sources } = listAgentSources(options.agentDir, agentProjectRoot(params));
				return {
					agents: sources.map((source) => ({
						name: source.name,
						description: source.description,
						sourceType: "agent",
						mention: `@${source.name}`,
					})),
				};
			}
			case "pi.mcp.servers.read": {
				if (typeof params.name === "string" && params.name !== "")
					return readMCPServerCandidates(options.agentDir, mcpProjectDir(params), params.name);
				const { servers, warnings } = readMCPServers(options.agentDir, mcpProjectDir(params));
				return { servers, warnings };
			}
			case "pi.mcp.servers.upsert": {
				const name = typeof params.name === "string" ? params.name : "";
				if (!validMCPServerName(name)) throw new HostError("invalid MCP server name");
				if (!isRecord(params.definition) || Object.keys(params.definition).length === 0)
					throw new HostError("MCP server definition is required");
				await mutateMCPServers(
					options.agentDir,
					mcpProjectDir(params),
					name,
					conditionalMCPMutation(params),
					"upsert",
					(servers) => {
						servers[name] = params.definition as Record<string, unknown>;
						return true;
					},
				);
				const { servers, warnings } = readMCPServers(options.agentDir, mcpProjectDir(params));
				return { servers: servers.filter((server) => server.name === name), warnings };
			}
			case "pi.mcp.servers.remove": {
				const name = typeof params.name === "string" ? params.name : "";
				if (!validMCPServerName(name)) throw new HostError("invalid MCP server name");
				await mutateMCPServers(
					options.agentDir,
					mcpProjectDir(params),
					name,
					conditionalMCPMutation(params),
					"remove",
					(servers) => {
						if (!(name in servers)) return false;
						delete servers[name];
						return true;
					},
				);
				return { ok: true };
			}
			case "pi.mcp.servers.probe": {
				if (!isRecord(params.definition) || Object.keys(params.definition).length === 0)
					throw new HostError("MCP server definition is required");
				return probeMCPServer(params.definition);
			}
			case "pi.providers.list":
				return providersInventory(params);
			case "pi.providers.readiness.check": {
				const runtime = await sdkModelRuntime();
				const providerId = asString(params, "providerId");
				const configured = Boolean(
					await runtime.checkAuth(providerId, {
						signal: AbortSignal.timeout(PROVIDER_AUTH_TIMEOUT_MS),
					}),
				);
				return { providerId, ready: configured, error: null, hasIssue: !configured };
			}
			case "pi.providers.inventory.refresh": {
				const runtime = await sdkModelRuntime();
				const result = await runtime.refresh({
					allowNetwork: true,
					signal: AbortSignal.timeout(PROVIDER_REFRESH_TIMEOUT_MS),
				});
				return { started: [], skipped: [], ...refreshOutcome(result) };
			}
			case "pi.providers.canonical-model-info": {
				const runtime = await sdkModelRuntime();
				const provider = asString(params, "provider");
				const model = asString(params, "model");
				const found = runtime.getModel(provider, model) as Record<string, unknown> | undefined;
				if (!found) throw new HostError(`Unknown model ${JSON.stringify(model)}`);
				const cost = isRecord(found.cost)
					? (found.cost as Record<string, unknown>)
					: ({} as Record<string, unknown>);
				const costOrNull = (value: unknown): number | null =>
					typeof value === "number" && Number.isFinite(value) ? value : null;
				return {
					modelInfo: {
						provider,
						model,
						contextLimit: typeof found.contextWindow === "number" ? found.contextWindow : null,
						maxOutputTokens: typeof found.maxTokens === "number" ? found.maxTokens : null,
						reasoning: typeof found.reasoning === "boolean" ? found.reasoning : null,
						currency: "USD",
						inputTokenCost: costOrNull(cost.input),
						outputTokenCost: costOrNull(cost.output),
						cacheReadTokenCost: costOrNull(cost.cacheRead),
						cacheWriteTokenCost: costOrNull(cost.cacheWrite),
					},
				};
			}
			case "pi.defaults.read": {
				const manager = sdkSettingsManager(optionalCwd(params));
				await manager.reload();
				return {
					providerId: manager.getDefaultProvider() ?? null,
					modelId: manager.getDefaultModel() ?? null,
				};
			}
			case "pi.defaults.save": {
				const cwd = optionalCwd(params);
				const manager = sdkSettingsManager(cwd);
				const provider = asString(params, "providerId");
				const rawModel = params.modelId;
				if (rawModel !== undefined && rawModel !== null) {
					if (typeof rawModel !== "string" || rawModel === "" || rawModel.includes("\0"))
						throw new HostError("bridge parameter modelId must be a string");
				}
				manager.setDefaultProvider(provider);
				manager.setDefaultModel(
					typeof rawModel === "string" && rawModel !== "" ? rawModel : undefined,
				);
				await manager.flush();
				await manager.reload();
				return {
					providerId: manager.getDefaultProvider() ?? null,
					modelId: manager.getDefaultModel() ?? null,
				};
			}
			case "pi.defaults.clear": {
				const cwd = optionalCwd(params);
				const manager = sdkSettingsManager(cwd);
				manager.setDefaultProvider(undefined);
				manager.setDefaultModel(undefined);
				await manager.flush();
				await manager.reload();
				return {
					providerId: manager.getDefaultProvider() ?? null,
					modelId: manager.getDefaultModel() ?? null,
				};
			}
			case "pi.preferences.read": {
				const manager = sdkSettingsManager(optionalCwd(params));
				await manager.reload();
				return { values: preferenceEntries(manager) };
			}
			case "pi.preferences.save":
			case "pi.preferences.reset": {
				const reset = method === "pi.preferences.reset";
				const cwd = optionalCwd(params);
				const manager = sdkSettingsManager(cwd);
				await manager.reload();
				const global = manager.getGlobalSettings() as {
					defaultThinkingLevel?: string;
					compaction?: { reserveTokens?: number };
				};
				const storedReserve = global.compaction?.reserveTokens;
				const currentReserve =
					storedReserve === undefined ? null : manager.getCompactionReserveTokens();
				const rawEntries = reset
					? (Array.isArray(params.keys) ? params.keys : []).map((key) => ({ key, value: null }))
					: Array.isArray(params.values)
						? params.values
						: [];
				for (const raw of rawEntries) {
					const entry = isRecord(raw) ? raw : {};
					const key = typeof entry.key === "string" ? entry.key : "";
					if (key === "piThinkingEffort") {
						if (entry.value === null || entry.value === undefined) {
							manager.setDefaultThinkingLevel(undefined);
						} else if (
							typeof entry.value === "string" &&
							(THINKING_LEVELS as readonly string[]).includes(entry.value)
						) {
							manager.setDefaultThinkingLevel(entry.value);
						} else {
							throw new HostError(
								`Unsupported thinking effort ${JSON.stringify(entry.value)}; expected one of ${THINKING_LEVELS.join(", ")}`,
							);
						}
					} else if (key === "compactionReserveTokens") {
						// The selected Pi exposes no public setter. Tolerate an
						// unchanged value so a read-only key can be echoed back,
						// and fail closed with a clear reason on a real change.
						if (reset) {
							if (storedReserve === undefined) continue;
							throw new CapabilityError(
								"compactionReserveTokens is read-only in the selected Pi; reset it in native Pi configuration",
							);
						}
						if (entry.value === currentReserve) continue;
						throw new CapabilityError(
							"compactionReserveTokens is read-only in the selected Pi; it cannot be changed here",
						);
					} else {
						throw new HostError("Unknown preference");
					}
				}
				await manager.flush();
				await manager.reload();
				return { values: preferenceEntries(manager) };
			}
			case "pi.extensions.list": {
				const cwd = optionalCwd(params);
				const sessionId =
					typeof params.sessionId === "string" && params.sessionId !== "" ? params.sessionId : null;
				const hasCwd = params.cwd !== undefined && params.cwd !== null;
				const reader = sessionId ? "not-resident" : hasCwd ? "configured-only" : "service";
				const settings = sdkSettingsManager(cwd);
				if (settings.drainErrors().length > 0) {
					// Surface settings problems as warnings rather than failing the list.
				}
				let packages: Array<{
					source: string;
					scope: string;
					filtered: boolean;
					installedPath?: string;
				}> = [];
				let resources: Array<{
					path: string;
					enabled: boolean;
					metadata: { source: string; scope: string; origin: string; baseDir?: string };
				}> = [];
				const warnings: string[] = [];
				try {
					const manager = sdkPackageManager(cwd, settings);
					packages = manager.listConfiguredPackages();
					resources = (await manager.resolve(async () => "skip")).extensions;
				} catch {
					warnings.push("resource-discovery-failed");
				}
				const global = settings.getGlobalSettings() as {
					packages?: unknown[];
					extensions?: unknown[];
				};
				const project = settings.getProjectSettings() as {
					packages?: unknown[];
					extensions?: unknown[];
				};
				const token = (value: unknown): string => sha256Hex(JSON.stringify(value));
				const revision = (
					scope: string,
					values: { packages?: unknown[]; extensions?: unknown[] },
				): string => token([scope, values.packages ?? [], values.extensions ?? []]);
				return {
					version: 1,
					trust: { projectTrusted: true, decision: null, requiresDecision: false },
					configurationRevisions: {
						user: revision("user", global),
						project: revision("project", project),
					},
					context: { cwd, sessionId, reader },
					packages: packages.map((pkg) => ({
						source: String(pkg.source).slice(0, 1024),
						scope: pkg.scope,
						filtered: pkg.filtered,
						installed: Boolean(pkg.installedPath),
						state: pkg.installedPath ? "not-observed" : "missing",
					})),
					paths: (
						[
							["user", global],
							["project", project],
						] as const
					).flatMap(([scope, values]) =>
						(Array.isArray(values.extensions) ? values.extensions : [])
							.filter((path): path is string => typeof path === "string")
							.map((path) => ({ path: String(path).slice(0, 1024), scope })),
					),
					resources: resources.map((resource) => ({
						resourceKey: token([resource.path, resource.metadata]),
						configurationSupported: false,
						path: String(resource.path).slice(0, 1024),
						source: String(resource.metadata.source).slice(0, 1024),
						scope: resource.metadata.scope,
						origin: resource.metadata.origin,
						enabled: resource.enabled,
						state: "not-observed",
					})),
					extensions: [],
					errors: [],
					warnings,
				};
			}
			case "pi.extensions.configure": {
				const cwd = optionalCwd(params);
				const scope = typeof params.scope === "string" ? params.scope : "";
				if (scope !== "user" && scope !== "project")
					throw new HostError("Invalid native configuration scope");
				if (params.confirmed !== true)
					throw new HostError("Confirm the scoped native configuration change");
				if (typeof params.enabled !== "boolean")
					throw new HostError("Native configuration enabled must be boolean");
				const enabled = params.enabled;
				const resourceKey = typeof params.resourceKey === "string" ? params.resourceKey : "";
				const expectedRevision =
					typeof params.expectedRevision === "string" ? params.expectedRevision : "";
				if (resourceKey === "" || expectedRevision === "")
					throw new HostError("Invalid native configuration revision");
				const settings = sdkSettingsManager(cwd);
				if (settings.drainErrors().length > 0)
					throw new HostError("Native settings are unreadable. No configuration saved.");
				const token = (value: unknown): string => sha256Hex(JSON.stringify(value));
				const revision = (
					scopeName: string,
					values: { packages?: unknown[]; extensions?: unknown[] },
				): string => token([scopeName, values.packages ?? [], values.extensions ?? []]);
				const snapshots = {
					global: settings.getGlobalSettings() as {
						packages?: unknown[];
						extensions?: unknown[];
					},
					project: settings.getProjectSettings() as {
						packages?: unknown[];
						extensions?: unknown[];
					},
				};
				const settingsScope = scope === "user" ? "global" : "project";
				if (revision(scope, snapshots[settingsScope]) !== expectedRevision)
					throw new HostError("Native configuration changed. Refresh inventory before saving.");
				const manager = sdkPackageManager(cwd, settings);
				const resources = (await manager.resolve(async () => "skip")).extensions;
				const byKey = new Map(resources.map((item) => [token([item.path, item.metadata]), item]));
				const resource = byKey.get(resourceKey);
				if (!resource || resource.metadata.scope !== scope)
					throw new HostError("Native resource is no longer available. Refresh inventory.");
				if (resource.metadata.origin !== "top-level")
					throw new CapabilityError("Manage this resource through native Pi configuration");
				const current = snapshots[settingsScope].extensions;
				const patterns = Array.isArray(current)
					? current.filter((entry): entry is string => typeof entry === "string")
					: [];
				const base = scope === "user" ? options.agentDir : join(cwd, ".pi");
				const nextPatterns = [
					...patterns.filter((pattern) => {
						if (pattern[0] !== "+" && pattern[0] !== "-") return true;
						try {
							return resolve(base, pattern.slice(1)) !== resolve(base, resource.path);
						} catch {
							return true;
						}
					}),
					`${enabled ? "+" : "-"}${resource.path}`,
				];
				await settings.reload();
				const latest = {
					global: settings.getGlobalSettings() as {
						packages?: unknown[];
						extensions?: unknown[];
					},
					project: settings.getProjectSettings() as {
						packages?: unknown[];
						extensions?: unknown[];
					},
				};
				if (revision(scope, latest[settingsScope]) !== expectedRevision)
					throw new HostError("Concurrent native configuration change");
				if (settingsScope === "global") settings.setExtensionPaths(nextPatterns);
				else settings.setProjectExtensionPaths(nextPatterns);
				await settings.flush();
				const confirmedSettings = sdkSettingsManager(cwd);
				const confirmed = (
					await sdkPackageManager(cwd, confirmedSettings).resolve(async () => "skip")
				).extensions;
				const confirmedByKey = new Map(
					confirmed.map((item) => [token([item.path, item.metadata]), item]),
				);
				const changed = confirmedByKey.get(resourceKey);
				if (!changed || changed.enabled !== enabled)
					throw new HostError(
						"Native settings save was not confirmed or conflicted. Refresh inventory before retrying.",
					);
				return { saved: true, loaded: false, reload: "deferred", warning: null };
			}
			case "pi.config.extensions.list": {
				const stored = legacyMcpDocument(
					readStateFile(join(options.agentDir, "mcp.json"), MCP_STATE_MAX_BYTES) ?? {},
				);
				const warnings: string[] = [];
				const extensions = Object.entries(stored).map(([configKey, raw]) => {
					const source = isRecord(raw) ? raw : {};
					try {
						mcpConnection({ ...source, name: configKey }, options.agentDir);
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
						extension: {
							type: "mcp",
							server: { ...source, name: configKey, url: source.url ?? source.uri },
						},
					};
				});
				return { extensions, warnings };
			}
			case "pi.config.extensions.add": {
				const connection = mcpConnection(params.extension, options.agentDir);
				await mutateStateFile(join(options.agentDir, "mcp.json"), (state) => {
					legacyMcpDocument(state);
					if (Object.hasOwn(state, connection.name))
						throw new HostError("MCP connection already exists");
					state[connection.name] = {
						...connection.source,
						enabled: params.enabled !== false,
					};
				});
				return { ok: true };
			}
			case "pi.config.extensions.set-enabled": {
				if (typeof params.enabled !== "boolean") throw new HostError("Enabled must be boolean");
				const configKey = asString(params, "configKey");
				await mutateStateFile(join(options.agentDir, "mcp.json"), (state) => {
					legacyMcpDocument(state);
					const connection = state[configKey];
					if (!Object.hasOwn(state, configKey) || !isRecord(connection))
						throw new HostError("Unknown connection");
					(connection as Record<string, unknown>).enabled = params.enabled;
					state[configKey] = connection;
				});
				return { ok: true };
			}
			case "pi.config.extensions.remove": {
				const configKey = asString(params, "configKey");
				await mutateStateFile(join(options.agentDir, "mcp.json"), (state) => {
					legacyMcpDocument(state);
					delete state[configKey];
				});
				return { ok: true };
			}
			case "pi.session.extensions.list": {
				const sessionId = asString(params, "sessionId");
				const storedRaw = readStateFile(join(options.agentDir, "mcp.json"), MCP_STATE_MAX_BYTES);
				const stored = isRecord(storedRaw) ? (storedRaw as Record<string, unknown>) : {};
				const base = Object.hasOwn(stored, "mcpServers") ? {} : legacyMcpDocument(stored);
				const membershipsRaw = readStateFile(
					join(options.agentDir, "mcp-sessions.json"),
					MCP_STATE_MAX_BYTES,
				);
				const memberships = isRecord(membershipsRaw)
					? (membershipsRaw as Record<string, unknown>)
					: {};
				const membership = isRecord(memberships[sessionId]) ? memberships[sessionId] : {};
				const all: Record<string, unknown> = {
					...base,
					...(isRecord(membership.add) ? membership.add : {}),
				};
				for (const name of Array.isArray(membership.remove) ? membership.remove : []) {
					if (typeof name === "string") delete all[name];
				}
				const extensions: Array<Record<string, unknown>> = [];
				for (const [name, raw] of Object.entries(all)) {
					let parsed: ReturnType<typeof mcpConnection>;
					try {
						parsed = mcpConnection({ ...(isRecord(raw) ? raw : {}), name }, options.agentDir);
					} catch {
						continue;
					}
					if ((parsed.source as Record<string, unknown>).enabled === false) continue;
					extensions.push({
						extensionKey: name,
						extension: { type: "mcp", server: parsed.source },
					});
				}
				return { extensions, warnings: [] };
			}
			case "pi.session.extensions.add": {
				const sessionId = asString(params, "sessionId");
				const connection = mcpConnection(params.extension, options.agentDir);
				await mutateStateFile(join(options.agentDir, "mcp-sessions.json"), (state) => {
					const membership = isRecord(state[sessionId])
						? (state[sessionId] as Record<string, unknown>)
						: {};
					const add = isRecord(membership.add) ? (membership.add as Record<string, unknown>) : {};
					add[connection.name] = connection.source;
					membership.add = add;
					const remove = Array.isArray(membership.remove)
						? (membership.remove as unknown[]).filter(
								(name): name is string => typeof name === "string",
							)
						: [];
					membership.remove = remove.filter((name) => name !== connection.name);
					state[sessionId] = membership;
				});
				return { ok: true };
			}
			case "pi.session.extensions.remove": {
				const sessionId = asString(params, "sessionId");
				const extensionKey = asString(params, "extensionKey");
				await mutateStateFile(join(options.agentDir, "mcp-sessions.json"), (state) => {
					const membership = isRecord(state[sessionId])
						? (state[sessionId] as Record<string, unknown>)
						: {};
					const add = isRecord(membership.add) ? (membership.add as Record<string, unknown>) : {};
					delete add[extensionKey];
					membership.add = add;
					const remove = Array.isArray(membership.remove)
						? (membership.remove as unknown[]).filter(
								(name): name is string => typeof name === "string",
							)
						: [];
					if (!remove.includes(extensionKey)) remove.push(extensionKey);
					membership.remove = remove;
					state[sessionId] = membership;
				});
				return { ok: true };
			}
			case "pi.slash-commands.list":
				return slashCommands(params);
			case "provider.loginStart":
				return startLogin(params);
			case "provider.loginBegin": {
				const login = logins.get(asString(params, "loginId"));
				if (!login || typeof login.begin !== "function")
					throw new HostError("Login cannot be started");
				const begin = login.begin;
				// Clear on the same object reference so the in-flight login
				// keeps its resolve/reject slots for a later loginReply.
				login.begin = undefined;
				begin();
				return { ok: true };
			}
			case "provider.loginReply": {
				const login = logins.get(asString(params, "loginId"));
				if (!login?.resolve) throw new HostError("No pending authentication question");
				login.resolve(typeof params.value === "string" ? params.value : "");
				return { ok: true };
			}
			case "provider.loginCancel": {
				const login = logins.get(asString(params, "loginId"));
				if (!login) throw new HostError("Unknown or expired login ID");
				login.abort.abort();
				clearTimeout(login.timer);
				logins.delete(login.id);
				return { ok: true };
			}
			case "pi.providers.config.read": {
				const runtime = await sdkModelRuntime();
				return providerConfigProjection(runtime, asString(params, "providerId"));
			}
			case "pi.providers.config.delete": {
				const runtime = await sdkModelRuntime();
				const providerId = asString(params, "providerId");
				// Validate the mutation against the same typed provider projection
				// used for reads before clearing credentials.
				providerConfigProjection(runtime, providerId);
				await runtime.logout(providerId);
				return { ok: true };
			}
			case "runtime.restart": {
				if (!allowSelfRestart)
					throw new CapabilityError("runtime.restart is disabled by configuration");
				logger.info("host.restart");
				// The reload preempts new run work immediately. In-flight prompts
				// are failed as delivery-uncertain when closeHost drains.
				replacementPending = true;
				setTimeout(() => {
					void closeHost().finally(() => {
						try {
							options.onRestart?.();
						} catch {
							/* restart hook must not fail the acknowledgement */
						}
					});
				}, RESTART_DRAIN_MS);
				return { ok: true };
			}
			default: {
				// A catalogued route that is unavailable fails closed with the
				// catalog's stated reason; an unknown route is a protocol error.
				const reason = unavailableReason(method);
				if (reason) throw new CapabilityError(reason);
				throw new HostError(
					`operation ${JSON.stringify(method)} is unsupported by the Bun assistant`,
				);
			}
		}
	};

	// AUX-13: a mutating native session operation holds the advisory lease for
	// its whole duration. The wrapper is intentionally the only place that takes
	// the lease, so no read path can accidentally claim ownership.
	const MUTATING_SESSION_METHODS = new Set([
		"session.prompt",
		"session.cancel",
		"session.compact",
		"session.rename",
		"session.configure",
		"session.followUp",
		"session.clearQueue",
		"session.fork",
		"session.clone",
	]);

	const call = async (method: string, params: Record<string, unknown>): Promise<unknown> => {
		if (!MUTATING_SESSION_METHODS.has(method)) return dispatch(method, params);
		const id = typeof params.sessionId === "string" ? params.sessionId : "";
		const resident = id === "" ? undefined : residents.get(id);
		if (!resident) return dispatch(method, params);
		return withSessionLease(resident, () => dispatch(method, params));
	};

	const uiResponse = (
		params: Record<string, unknown>,
		cancelled: boolean,
	): Record<string, unknown> => {
		const resident = residents.get(asString(params, "sessionId"));
		const requestId = asString(params, "requestId");
		const dialog = resident?.dialogs.get(requestId);
		if (!resident || !dialog) throw new HostError("unknown extension dialog request");
		// Controller sends top-level {value, cancelled}; legacy clients nest under result.
		const nested = isRecord(params.result) ? params.result : {};
		const value = Object.hasOwn(params, "value") ? params.value : nested.value;
		const nestedCancelled = nested.cancelled === true;
		const topCancelled = params.cancelled === true;
		const cancel = cancelled || topCancelled || nestedCancelled;
		if (!cancel && !validDialogValue(dialog, value))
			throw new HostError("extension dialog response is invalid");
		settleDialog(resident, requestId, cancel ? undefined : (value as string | boolean));
		return {};
	};

	const v2Error = (
		connection: Connection,
		id: number,
		code: number,
		reason: string,
		message: string,
	): void => {
		queue(connection, encode({ id, error: { code, reason, message } }));
	};

	const negotiate = (connection: Connection, request: RequestEnvelope): void => {
		if (request.method !== "runtime.hello") {
			closeConnection(connection, 1008, "runtime hello required");
			return;
		}
		const version = request.params.protocolVersion;
		const offered = request.params.supportedProtocolVersions;
		const offeredValid =
			offered === undefined ||
			(Array.isArray(offered) &&
				offered.every((candidate) => Number.isInteger(candidate) && (candidate as number) > 0));
		if (!Number.isInteger(version) || (version as number) < 1 || !offeredValid) {
			closeConnection(connection, 1008, "invalid hello params");
			return;
		}
		// AUX-33: the advertisement is the evidence for a negotiated selection.
		// A peer may omit it only when the selection resolves to the explicit
		// legacy v1 path; a v2 selection without an advertisement is refused.
		const advertised = Array.isArray(offered) ? (offered as number[]) : [];
		const peerVersions = advertised.length ? advertised : [version as number];
		const selected = HOST_SUPPORTED_PROTOCOL_VERSIONS.find((candidate) =>
			peerVersions.includes(candidate),
		);
		if (!selected) {
			closeConnection(connection, 1008, "host protocol versions are incompatible");
			return;
		}
		if (selected === 2 && advertised.length === 0) {
			closeConnection(connection, 1008, "protocol version 2 requires supportedProtocolVersions");
			return;
		}
		if (protocolMode === "v2" && selected !== 2) {
			closeConnection(connection, 1008, "host requires protocol version 2");
			return;
		}
		connection.protocolVersion = selected;
		connection.handshaken = true;
		if (selected === 2) {
			queue(
				connection,
				encode({
					id: request.id,
					result: {
						protocolVersion: 2,
						supportedProtocolVersions: HOST_SUPPORTED_PROTOCOL_VERSIONS,
						hostIdentity: runtimeId,
						bootId,
						nativeVersion: options.verifiedPi.packageVersion,
						version: options.verifiedPi.packageVersion,
						ready: true,
						capabilities: hostCapabilities(),
						operationSet: currentOperationSet(),
					},
				}),
			);
			return;
		}
		queue(
			connection,
			encode({
				id: request.id,
				result: {
					protocolVersion: 1,
					runtimeId,
					bootId,
					version: options.verifiedPi.packageVersion,
					ready: true,
					supportedProtocolVersions: HOST_SUPPORTED_PROTOCOL_VERSIONS,
					capabilities: hostCapabilities(),
					operationSet: currentOperationSet(),
				},
			}),
		);
	};

	const handleMessage = (connection: Connection, raw: string | Uint8Array): void => {
		if (lifecycle !== "running") {
			closeConnection(connection, 1001, "assistant stopped");
			return;
		}
		if (raw instanceof Uint8Array) {
			closeConnection(connection, 1003, "text frames required");
			return;
		}
		if (new TextEncoder().encode(raw).byteLength > MAX_FRAME_BYTES) {
			closeConnection(connection, 1009, "frame too large");
			return;
		}
		let decoded: unknown;
		try {
			decoded = JSON.parse(raw);
		} catch {
			closeConnection(connection, 1007, "invalid JSON");
			return;
		}
		const usesNegotiation = protocolMode !== "v1" && !connection.handshaken;
		const v2 = connection.protocolVersion === 2 || usesNegotiation;
		const request = v2 ? parseV2Request(decoded) : parseV1Request(decoded);
		if (!request) {
			closeConnection(connection, 1008, "invalid request envelope");
			return;
		}
		if (!connection.handshaken) {
			if (protocolMode === "v1") {
				if (request.method !== "runtime.hello" || request.params.protocolVersion !== 1) {
					closeConnection(connection, 1008, "runtime hello required");
					return;
				}
				connection.protocolVersion = 1;
				connection.handshaken = true;
				queue(
					connection,
					encode({
						id: request.id,
						result: {
							protocolVersion: 1,
							runtimeId,
							bootId,
							version: options.verifiedPi.packageVersion,
							ready: true,
							capabilities: hostCapabilities(),
							operationSet: currentOperationSet(),
						},
					}),
				);
				return;
			}
			negotiate(connection, request);
			return;
		}
		if (connection.protocolVersion === 2) {
			if (request.method === "runtime.hello") {
				closeConnection(connection, 1008, "runtime hello already completed");
				return;
			}
			const supported = currentOperationSet()[request.method];
			if (supported === undefined) {
				v2Error(
					connection,
					request.id,
					-32601,
					"method_not_found",
					`unknown host method ${JSON.stringify(request.method)}`,
				);
				return;
			}
			if (!supported) {
				v2Error(
					connection,
					request.id,
					-32004,
					"capability_unavailable",
					unavailableReason(request.method) ??
						`operation ${JSON.stringify(request.method)} is unsupported by the Bun assistant`,
				);
				return;
			}
		}
		if (connection.active.has(request.id)) {
			if (connection.protocolVersion === 2) {
				closeConnection(connection, 1008, "duplicate in-flight host id");
				return;
			}
			queue(
				connection,
				encode({
					id: request.id,
					error: { code: -32000, message: "request id is already in flight" },
				}),
			);
			return;
		}
		if (connection.active.size >= MAX_PENDING_REQUESTS) {
			if (connection.protocolVersion === 2)
				v2Error(connection, request.id, -32000, "internal", "too many pending requests");
			else
				queue(
					connection,
					encode({
						id: request.id,
						error: { code: -32000, message: "too many pending requests" },
					}),
				);
			return;
		}
		connection.active.add(request.id);
		const finish = (): void => {
			connection.active.delete(request.id);
			const deadline = connection.deadlines.get(request.id);
			if (deadline) {
				clearTimeout(deadline);
				connection.deadlines.delete(request.id);
			}
		};
		// AUX-18: a hung control-plane handler must not hold its id forever. The
		// deadline answers the correlation with a typed timeout and releases the
		// id. Run-extending work (prompt, follow-up, steer, compaction) is
		// bounded by Pi, not by a fixed host deadline.
		const deadlineApplies = requestTimeoutMs > 0 && hostCommandClass(request.method) !== "unblock";
		if (deadlineApplies) {
			const deadline = setTimeout(() => {
				if (!connection.active.has(request.id)) return;
				finish();
				if (connection.protocolVersion === 2)
					v2Error(connection, request.id, -32000, "internal", "host request timed out");
				else
					queue(
						connection,
						encode({
							id: request.id,
							error: { code: -32000, message: "host request timed out" },
						}),
					);
			}, requestTimeoutMs);
			(deadline as { unref?: () => void }).unref?.();
			connection.deadlines.set(request.id, deadline);
		}
		const work = call(request.method, request.params)
			.then(
				(result) => {
					// A timeout or graceful stop already answered this id.
					if (!connection.active.has(request.id)) return;
					let frame: string;
					try {
						frame = encode({ id: request.id, result });
					} catch {
						finish();
						logger.error("host.request.result.invalid", "host response could not be serialized", {
							method: request.method,
						});
						if (connection.protocolVersion === 2)
							v2Error(
								connection,
								request.id,
								-32000,
								"internal",
								"host response could not be serialized",
							);
						else
							queue(
								connection,
								encode({
									id: request.id,
									error: {
										code: -32000,
										message: "host response could not be serialized",
									},
								}),
							);
						return;
					}
					finish();
					queue(connection, frame);
				},
				(error: unknown) => {
					if (!connection.active.has(request.id)) return;
					// The redacted cause is retained in the bounded log; only the
					// bounded, secret-free message crosses to the browser.
					logger.error("host.request.failed", error, { method: request.method });
					finish();
					const message = safeHostErrorMessage(error, [secret]);
					if (connection.protocolVersion === 2) {
						if (error instanceof CapabilityError)
							v2Error(connection, request.id, -32004, "capability_unavailable", message);
						else if (
							error instanceof SessionLeaseError ||
							error instanceof SessionInvariantError ||
							error instanceof SessionStaleError
						)
							v2Error(connection, request.id, -32003, "resource_conflict", message);
						else v2Error(connection, request.id, -32000, "internal", message);
					} else queue(connection, encode({ id: request.id, error: { code: -32000, message } }));
				},
			)
			.finally(finish);
		inflightCalls.add(work);
		void work.then(
			() => inflightCalls.delete(work),
			() => inflightCalls.delete(work),
		);
	};

	return {
		get endpoint() {
			return endpoint;
		},
		start(): void {
			if (lifecycle !== "new")
				throw new HostError(
					lifecycle === "closed" ? "Bun host is closed" : "Bun host is already started",
				);
			logger.info("host.starting", { port: options.port });
			try {
				runtimeId = loadOrCreateHostIdentity(options.agentDir);
			} catch (error) {
				logger.error("host.identity.failed", error);
				throw error;
			}
			lifecycle = "running";
			try {
				server = (options.serverFactory ?? defaultServerFactory()).serve({
					hostname: host,
					port: options.port,
					fetch(request, activeServer) {
						if (lifecycle !== "running") return new Response(null, { status: 503 });
						const pathname = new URL(request.url).pathname;
						if (pathname === "/livez")
							return request.method === "GET"
								? new Response("ok")
								: new Response(null, { status: 405 });
						if (pathname === "/readyz") {
							if (!authorized(request, secret)) return new Response(null, { status: 401 });
							return request.method === "GET"
								? Response.json({
										protocolVersion: 1,
										runtimeId,
										bootId,
										version: options.verifiedPi.packageVersion,
										capabilities: hostCapabilities(),
										operationSet: currentOperationSet(),
										ready: lifecycle === "running",
									})
								: new Response(null, { status: 405 });
						}
						if (pathname !== "/pi") return new Response(null, { status: 404 });
						if (!authorized(request, secret)) return new Response(null, { status: 401 });
						if (request.headers.has("origin")) return new Response(null, { status: 403 });
						const connection: Connection = {
							handshaken: false,
							closed: false,
							flushing: false,
							outbound: [],
							outboundBytes: 0,
							active: new Set(),
							deadlines: new Map(),
						};
						return activeServer.upgrade(request, { data: { connection } })
							? undefined
							: new Response(null, { status: 400 });
					},
					websocket: {
						open(socket) {
							socket.data.connection.socket = socket;
							connections.add(socket.data.connection);
						},
						message(socket, message) {
							handleMessage(socket.data.connection, message);
						},
						close(socket) {
							closeConnection(socket.data.connection);
						},
					},
				});
				if (server.port !== undefined) endpoint = `ws://${host}:${server.port}/pi`;
				logger.info("host.ready", { port: server.port ?? options.port });
			} catch (error) {
				logger.error("host.start.failed", error);
				throw error;
			}
		},
		close: closeHost,
	};
}

export function startBunHost(options: BunHostOptions): BunHost {
	const host = createBunHost(options);
	host.start();
	return host;
}

/** Start a host from the selected package's already loaded public root API. */
export function startBunHostFromPublicApi(
	options: BunHostFromPublicApiOptions,
	publicApi: PiPublicRootApi,
): BunHost {
	return startBunHost({ ...options, sdk: createPiSdkApi(options.verifiedPi, publicApi) });
}

/** Start the host from the package entrypoint that the caller already verified. */
export async function startBunHostFromVerifiedPi(
	options: BunHostFromPublicApiOptions,
): Promise<BunHost> {
	return startBunHostFromPublicApi(options, await loadVerifiedPiPublicApi(options.verifiedPi));
}

function validDialogValue(dialog: Dialog, value: unknown): boolean {
	if (dialog.primitive === "confirm") return typeof value === "boolean";
	if (typeof value !== "string") return false;
	return dialog.primitive !== "select" || dialog.options?.includes(value) === true;
}

function promptContent(raw: unknown): { text: string; images: unknown[] } {
	if (!Array.isArray(raw) || raw.length === 0) throw new HostError("prompt content is required");
	const text: string[] = [];
	const images: unknown[] = [];
	for (const entry of raw) {
		if (!isRecord(entry) || typeof entry.type !== "string")
			throw new HostError("prompt content block must be an object");
		if (entry.type === "text") {
			if (typeof entry.text !== "string") throw new HostError("text prompt block is invalid");
			text.push(entry.text);
		} else if (entry.type === "image") {
			if (
				typeof entry.data !== "string" ||
				!entry.data ||
				typeof entry.mimeType !== "string" ||
				!entry.mimeType
			)
				throw new HostError("image prompt block is invalid");
			images.push({ type: "image", data: entry.data, mimeType: entry.mimeType });
		} else if (entry.type === "resource") {
			throw new HostError("Bun assistant does not support text resource prompts");
		} else throw new HostError(`unsupported prompt content type ${JSON.stringify(entry.type)}`);
	}
	if (text.length === 0 && images.length === 0) throw new HostError("prompt content is required");
	return { text: text.join("\n"), images };
}
