/** Browser-safe operations available through the connected Pi agent. */
export interface AgentOperations {
	deleteSession: boolean;
	forkSession: boolean;
	promptImage: boolean;
	promptEmbeddedContext: boolean;
	httpMcp: boolean;
	steer: boolean;
	renameSession: boolean;
	archiveSession: boolean;
	administration: boolean;
}

/** Connection-scoped identity and capabilities; never includes raw Pi metadata. */
export interface AgentProfile {
	name: string;
	version: string;
	pi: boolean;
	compatible: boolean;
	missingRequired: string[];
	operations: AgentOperations;
	capabilities?: Record<string, number>;
}

export type RuntimeAvailability = "ready" | "degraded" | "unavailable";

export interface RuntimeBuild {
	version: string;
	revision?: string;
}

export interface RuntimeRequestMetrics {
	total: number;
	failures: number;
	active: number;
	averageMs: number;
	maxMs: number;
}

export interface RuntimeProcessMetrics {
	uptimeSeconds: number;
	goroutines: number;
	heapBytes: number;
	gcCycles: number;
}

/** Browser-safe local service status; unavailable services omit runtime metrics. */
export interface RuntimeServiceStatus {
	state: RuntimeAvailability;
	build?: RuntimeBuild;
	requests?: RuntimeRequestMetrics;
	process?: RuntimeProcessMetrics;
	detail?: string;
}

/** Browser-safe agent identity and availability without upstream diagnostics. */
export interface RuntimeAgentStatus {
	state: RuntimeAvailability;
	name?: string;
	version?: string;
	detail?: string;
}

export interface RuntimeStatusReport {
	application: RuntimeServiceStatus;
	agent: RuntimeAgentStatus;
	browser: RuntimeServiceStatus;
}

export type McpGatewayState =
	| "ready"
	| "degraded"
	| "not-configured"
	| "unreachable"
	| "incompatible";

export type McpModuleState = "ready" | "unavailable";

export type McpModuleBinding =
	| "not-configured"
	| "disabled"
	| "enabled"
	| "conflict"
	| "unavailable";

export interface McpGatewaySummary {
	state: McpGatewayState;
	detail?: string;
	revision?: string;
}

/** Browser-safe projection of one MCP module published by Pixie MCP. */
export interface McpGatewayModule {
	id: string;
	extensionName: string;
	displayName: string;
	description: string;
	path: string;
	transport: "streamable_http";
	state: McpModuleState;
	detail?: string;
	binding: McpModuleBinding;
	bindingDetail?: string;
}

export interface McpGatewayCatalog {
	schemaVersion: 1;
	gateway: McpGatewaySummary;
	modules: McpGatewayModule[];
}

/** A controller-owned, bounded browser panel. The browser service token is never exposed here. */
export interface BrowserPanel {
	id: string;
}

export type BrowserPanelAction =
	| { type: "open"; url: string }
	| { type: "back" | "forward" | "reload" | "snapshot" | "screenshot" }
	| { type: "click"; ref: string }
	| { type: "fill"; ref: string; text: string }
	| { type: "viewport"; width: number; height: number };

export interface BrowserPanelResult {
	output: string;
	screenshotUrl?: string;
}

/** Accepts only navigable http(s) URLs without embedded credentials. */
export function safeBrowserURL(value: string): string | null {
	if (
		typeof value !== "string" ||
		value.length === 0 ||
		value.length > 2048 ||
		hasUnsafeBrowserURLCharacter(value)
	) {
		return null;
	}
	try {
		const parsed = new URL(value);
		if (
			(parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
			!parsed.hostname ||
			parsed.username ||
			parsed.password
		) {
			return null;
		}
		return parsed.href;
	} catch {
		return null;
	}
}

function hasUnsafeBrowserURLCharacter(value: string): boolean {
	for (const character of value) {
		const code = character.codePointAt(0) ?? 0;
		if (code <= 0x20 || code === 0x7f) return true;
	}
	return false;
}

/** Deliberately excludes Pi extension configuration, raw objects, and MCP credentials. */
export interface PiExtensionSummary {
	name: string;
	type: "builtin" | "platform" | "mcp";
	displayName?: string;
	description?: string;
	bundled?: boolean;
	availableTools?: string[];
}

/** Config state appears only in the configured catalog collection. */
export interface PiConfiguredExtensionSummary extends PiExtensionSummary {
	enabled: boolean;
	configKey?: string;
}

/** Session state includes Pi's authoritative key for safe removal. */
export interface PiSessionExtensionSummary extends PiExtensionSummary {
	extensionKey: string;
}

export interface PiExtensionCatalog {
	configured: PiConfiguredExtensionSummary[];
	available: PiExtensionSummary[];
	warningCount: number;
}

/** Deliberately excludes input/output schemas and any extension implementation metadata. */
export interface PiToolSummary {
	name: string;
	description: string;
	parameters: string[];
}

/** Pixie-owned schedules run ordinary Pi sessions in an admitted project. */
export interface Schedule {
	id: string;
	projectId: string;
	root: string;
	prompt: string;
	cron: string;
	timezone: string;
	model?: { provider: string; id: string };
	paused: boolean;
	nextRun: string;
	runs: ScheduleRun[];
}
export interface ScheduleRun {
	id: string;
	sessionId?: string;
	startedAt: string;
	finishedAt?: string;
	status: "running" | "completed" | "failed" | "interrupted";
	error?: string;
}

export const PROJECT_ICONS = ["folder", "code", "book", "flask", "rocket", "sparkles"] as const;
export type ProjectIcon = (typeof PROJECT_ICONS)[number];
export const PROJECT_NAME_MAX_LENGTH = 100;

export function normalizeProjectName(value: unknown): string {
	if (typeof value !== "string") throw new Error("Project name must be text");
	const name = value.trim();
	if (!name) throw new Error("Project name cannot be empty");
	if (name.includes("\0")) throw new Error("Project name contains an invalid character");
	if (name.length > PROJECT_NAME_MAX_LENGTH) {
		throw new Error(`Project name must be ${PROJECT_NAME_MAX_LENGTH} characters or fewer`);
	}
	return name;
}

export function normalizeProjectIcon(value: unknown): ProjectIcon {
	if (typeof value !== "string" || !(PROJECT_ICONS as readonly string[]).includes(value)) {
		throw new Error("Unknown project icon");
	}
	return value as ProjectIcon;
}

export interface Project {
	id: string;
	name: string;
	/** Singleton for wire and persisted-state compatibility. */
	roots: string[];
	slug: string;
	lastOpened: number;
	icon?: ProjectIcon;
	closed?: true;
}

export interface ProjectFsChange {
	root: string;
	path: string;
}

export interface ProjectFsChangedPayload {
	projectId: string;
	changes: ProjectFsChange[];
	truncated: boolean;
}

export interface SessionGoal {
	projectId: string;
	sessionId: string;
	goal: string | null;
	tasks: SessionTask[];
	updatedAt: number | null;
}

export type SessionTaskStatus = "pending" | "active" | "done";

export interface SessionTask {
	id: string;
	text: string;
	status: SessionTaskStatus;
}

export const SESSION_GOAL_MAX_LENGTH = 2_000;

export function normalizeSessionGoal(value: unknown): string {
	if (typeof value !== "string") throw new Error("Session goal must be text");
	const goal = value.trim();
	if (!goal) throw new Error("Session goal cannot be empty");
	if (goal.includes("\0")) throw new Error("Session goal contains an invalid character");
	if (goal.length > SESSION_GOAL_MAX_LENGTH) {
		throw new Error(`Session goal must be ${SESSION_GOAL_MAX_LENGTH} characters or fewer`);
	}
	return goal;
}

export type FileKind = "file" | "dir";

export interface FileNode {
	path: string;
	name: string;
	kind: FileKind;
	gitignored?: boolean;
	children?: FileNode[];
}

export interface FileListing {
	nodes: FileNode[];
	complete: boolean;
	warnings: string[];
}

/** A directory admitted for selection by the browser directory picker. */
export interface DirectoryEntry {
	name: string;
	path: string;
}

export interface DirectoryListing {
	/** Null when choosing one of the configured mount roots. */
	path: string | null;
	roots: string[];
	directories: DirectoryEntry[];
	page: number;
	pageSize: number;
	hasMore: boolean;
	complete: boolean;
	warnings: string[];
	/** Opaque continuation marker for clients that do not want to derive a page number. */
	cursor: string | null;
}

export type GitFileStatus = "added" | "modified" | "deleted" | "renamed" | "untracked";

export interface GitFileChange {
	path: string;
	originalPath?: string;
	status: GitFileStatus;
	added?: number;
	removed?: number;
}

export type GitHead =
	| { kind: "branch"; name: string }
	| { kind: "detached"; oid: string }
	| { kind: "unborn" };

export interface GitRepository {
	id: string;
	root: string;
	relativePath: string;
	name: string;
	head: GitHead;
	clean: boolean;
	changes: GitFileChange[];
	comparisonId?: string;
}

export interface GitRepositoryList {
	repositories: GitRepository[];
	complete: boolean;
	warnings: string[];
}

export interface GitBranchRef {
	ref: string;
	name: string;
}

export type GitDiffScope =
	| { kind: "branch"; baseRef: string }
	| { kind: "uncommitted" }
	| { kind: "commit"; sha: string }
	| { kind: "pinned"; baseRef: string };

/** A bounded Git file preview. Unavailable previews never include file content. */
export interface GitDiffFile {
	original: string;
	modified: string;
	originalPath?: string;
	comparisonId?: string;
	unavailable?: true;
	binary?: true;
	tooLarge?: true;
	message?: string;
}

export interface GitCommit {
	sha: string;
	shortSha: string;
	subject: string;
	author: string;
	committedAt: string;
}

export type ProviderAuthKind = "oauth" | "api-key" | "env" | "other";

export interface ProviderStatus {
	id: string;
	name: string;
	configured: boolean;
	/** Upstream inventory availability; not an authentication/readiness check. */
	available?: boolean;
	configuration?: "reported" | "explicit" | "defaults" | "unknown";
	deprecated?: boolean;
	replacement?: string;
	kind?: ProviderAuthKind;
	detail?: string;
	canOAuth?: boolean;
	canApiKey?: boolean;
	canConfigure?: boolean;
	canLogout?: boolean;
	modelCount: number;
	availableModelCount: number;
	/** Pi inventory marks providers that support Pi readiness checks. */
	readinessCheck: boolean;
}

export interface ProviderStatusReport {
	providers: ProviderStatus[];
}

/** Browser-safe agent completion data. Source paths and raw Pi values stay server-side. */
export interface AgentMentionInfo {
	name: string;
	description: string;
	sourceType: "skill" | "builtinSkill" | "agent" | "project";
	mention: string;
}

/** The focused, allowlisted Pi preference projection. */
export interface PiPreferences {
	compactionReserveTokens?: number;
	piThinkingEffort?: "off" | "minimal" | "low" | "medium" | "high" | "xhigh";
}

/** Pi's global provider/model default, never persisted by Pixie. */
export interface PiProviderDefaults {
	providerId: string | null;
	modelId: string | null;
}

/** Opaque catalog identity. Paths and arbitrary source properties never cross this boundary. */
export interface PiAgentCatalogEntry {
	id: string;
	name: string;
	description: string;
	instructions: string;
	scope: "global" | "project";
	writable: boolean;
	/** Pi supports model ID preference only. The agent inherits its provider. */
	modelId?: string;
}

export type LoginFrame =
	| { kind: "authUrl"; url: string; instructions?: string }
	| { kind: "deviceCode"; userCode: string; verificationUri: string; expiresInSeconds?: number }
	| { kind: "select"; message: string; options: { id: string; label: string }[] }
	| {
			kind: "prompt";
			message: string;
			placeholder?: string;
			allowEmpty?: boolean;
			secret?: boolean;
	  }
	| { kind: "progress"; message: string }
	| { kind: "success" }
	| { kind: "error"; message: string };

export interface LoginPush {
	loginId: string;
	providerId: string;
	frame: LoginFrame;
}

export interface LoginReply {
	loginId: string;
	value: string;
}

export interface ModelReference {
	provider: string;
	id: string;
}

export function modelReferenceKey(ref: Pick<ModelReference, "provider" | "id">): string {
	return JSON.stringify([ref.provider, ref.id]);
}

export function normalizeModelReferences(value: unknown): ModelReference[] {
	if (!Array.isArray(value)) return [];
	const result: ModelReference[] = [];
	const seen = new Set<string>();
	for (const candidate of value) {
		if (typeof candidate !== "object" || candidate === null || Array.isArray(candidate)) continue;
		const provider = Reflect.get(candidate, "provider");
		const id = Reflect.get(candidate, "id");
		if (typeof provider !== "string" || typeof id !== "string") continue;
		if (!provider || !id || provider.includes("\0") || id.includes("\0")) continue;
		const ref = { provider, id };
		const key = modelReferenceKey(ref);
		if (seen.has(key)) continue;
		seen.add(key);
		result.push(ref);
	}
	return result;
}

export interface AppConfig {
	signet: SignetSettings;
	/** Models hidden from pixie catalog and selection surfaces. */
	hiddenModels?: ModelReference[];
}

export interface SignetSettings {
	enabled: boolean;
	address: string;
	port: number;
}

export interface SignetStatus {
	enabled: boolean;
	endpoint: string;
	reachable: boolean;
}

export type AppConfigPatch = Omit<Partial<AppConfig>, "signet"> & {
	signet?: Partial<SignetSettings>;
};

export const DEFAULT_SIGNET_SETTINGS: SignetSettings = {
	enabled: false,
	address: "127.0.0.1",
	port: 3850,
};

export const DEFAULT_CONFIG = {
	signet: DEFAULT_SIGNET_SETTINGS,
	hiddenModels: [],
} satisfies AppConfig;

export const IMAGE_MAX_BASE64_BYTES = 4.5 * 1024 * 1024;

export function base64EncodedLength(byteLength: number): number {
	return Math.ceil(byteLength / 3) * 4;
}

export const ACCEPTED_IMAGE_TYPES: readonly string[] = [
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
];

export const REQUEST_IMAGE_BASE64_BUDGET = 24 * 1024 * 1024;

/** Browser-selected text attachments are embedded Pi resources, never files on disk. */
export const TEXT_ATTACHMENT_MAX_BYTES = 1024 * 1024;
export const REQUEST_TEXT_ATTACHMENT_MAX_BYTES = 2 * 1024 * 1024;
export const REQUEST_TEXT_ATTACHMENT_MAX_COUNT = 4;
export const TEXT_ATTACHMENT_FILENAME_MAX_BYTES = 255;
export const TEXT_ATTACHMENT_FILENAME_MAX_RUNES = 128;

export const TEXT_ATTACHMENT_MEDIA_TYPES = [
	"text/plain",
	"text/markdown",
	"text/css",
	"text/html",
	"text/javascript",
	"text/x-c",
	"text/x-c++src",
	"text/x-csharp",
	"text/x-go",
	"text/x-java-source",
	"text/x-python",
	"text/x-rust",
	"text/x-shellscript",
	"text/x-typescript",
	"text/x-yaml",
	"application/json",
	"application/toml",
	"application/xml",
] as const;

const TEXT_ATTACHMENT_MEDIA_TYPE_BY_EXTENSION: Readonly<
	Record<string, (typeof TEXT_ATTACHMENT_MEDIA_TYPES)[number]>
> = {
	c: "text/x-c",
	cc: "text/x-c++src",
	cpp: "text/x-c++src",
	cs: "text/x-csharp",
	css: "text/css",
	csv: "text/plain",
	go: "text/x-go",
	h: "text/x-c",
	hpp: "text/x-c++src",
	html: "text/html",
	java: "text/x-java-source",
	js: "text/javascript",
	json: "application/json",
	jsx: "text/javascript",
	md: "text/markdown",
	mdx: "text/markdown",
	mjs: "text/javascript",
	py: "text/x-python",
	rb: "text/plain",
	rs: "text/x-rust",
	sh: "text/x-shellscript",
	sql: "text/plain",
	toml: "application/toml",
	ts: "text/x-typescript",
	tsx: "text/x-typescript",
	txt: "text/plain",
	xml: "application/xml",
	yaml: "text/x-yaml",
	yml: "text/x-yaml",
};

export const ACCEPTED_TEXT_ATTACHMENT_EXTENSIONS = Object.keys(
	TEXT_ATTACHMENT_MEDIA_TYPE_BY_EXTENSION,
);

export interface TextResourceAttachment {
	type: "text";
	name: string;
	mimeType: (typeof TEXT_ATTACHMENT_MEDIA_TYPES)[number];
	text: string;
}

export function textAttachmentMediaType(name: string): TextResourceAttachment["mimeType"] | null {
	const extension = name.trim().toLowerCase().split(".").at(-1);
	return extension && extension !== name.toLowerCase()
		? (TEXT_ATTACHMENT_MEDIA_TYPE_BY_EXTENSION[extension] ?? null)
		: null;
}

export function utf8ByteLength(value: string): number {
	return new TextEncoder().encode(value).byteLength;
}

export function validateTextResourceAttachments(
	value: unknown,
): asserts value is TextResourceAttachment[] {
	if (!Array.isArray(value)) throw new Error("Session text attachments must be an array");
	if (value.length > REQUEST_TEXT_ATTACHMENT_MAX_COUNT) {
		throw new Error(
			`Session text attachments are limited to ${REQUEST_TEXT_ATTACHMENT_MAX_COUNT} files`,
		);
	}
	let totalBytes = 0;
	for (const attachment of value) {
		if (
			typeof attachment !== "object" ||
			attachment === null ||
			Array.isArray(attachment) ||
			Reflect.get(attachment, "type") !== "text" ||
			typeof Reflect.get(attachment, "name") !== "string" ||
			typeof Reflect.get(attachment, "mimeType") !== "string" ||
			typeof Reflect.get(attachment, "text") !== "string"
		) {
			throw new Error("Malformed session text attachment");
		}
		const name = Reflect.get(attachment, "name") as string;
		const mimeType = Reflect.get(attachment, "mimeType") as string;
		const text = Reflect.get(attachment, "text") as string;
		if (
			!name ||
			name !== name.trim() ||
			name.includes("/") ||
			name.includes("\\") ||
			name.includes("\0") ||
			name.length > TEXT_ATTACHMENT_FILENAME_MAX_RUNES ||
			utf8ByteLength(name) > TEXT_ATTACHMENT_FILENAME_MAX_BYTES
		) {
			throw new Error("Session text attachment filename is invalid");
		}
		if (!(TEXT_ATTACHMENT_MEDIA_TYPES as readonly string[]).includes(mimeType)) {
			throw new Error(`Unsupported text attachment media type: ${mimeType}`);
		}
		if (!isSafeTextAttachmentText(text))
			throw new Error("Session text attachment is not valid text");
		const byteLength = utf8ByteLength(text);
		if (byteLength > TEXT_ATTACHMENT_MAX_BYTES) {
			throw new Error("Session text attachment exceeds the 1 MiB size limit");
		}
		totalBytes += byteLength;
		if (totalBytes > REQUEST_TEXT_ATTACHMENT_MAX_BYTES) {
			throw new Error("Session text attachments exceed the 2 MiB aggregate size limit");
		}
	}
}

function isSafeTextAttachmentText(value: string): boolean {
	for (const character of value) {
		const code = character.codePointAt(0) ?? 0;
		if (
			code === 0 ||
			(code < 0x20 && character !== "\t" && character !== "\n" && character !== "\r")
		) {
			return false;
		}
	}
	return true;
}

/**
 * Validate image blocks accepted by the browser session prompt and steer
 * protocol. The check is deliberately structural and does not decode image
 * bytes, which keeps it suitable for both browser and controller callers.
 */
export function validateRequestImages(value: unknown): asserts value is {
	type: "image";
	data: string;
	mimeType: string;
}[] {
	if (!Array.isArray(value)) throw new Error("Session images must be an array");
	let totalLength = 0;
	for (const image of value) {
		if (
			typeof image !== "object" ||
			image === null ||
			Array.isArray(image) ||
			Reflect.get(image, "type") !== "image" ||
			typeof Reflect.get(image, "data") !== "string" ||
			typeof Reflect.get(image, "mimeType") !== "string"
		) {
			throw new Error("Malformed session image");
		}
		const data = Reflect.get(image, "data") as string;
		const mimeType = Reflect.get(image, "mimeType") as string;
		if (!ACCEPTED_IMAGE_TYPES.includes(mimeType))
			throw new Error(`Unsupported image media type: ${mimeType}`);
		if (!isCanonicalBase64(data)) throw new Error("Session image data must be canonical base64");
		if (data.length > IMAGE_MAX_BASE64_BYTES)
			throw new Error("Session image exceeds the 4.5 MiB encoded size limit");
		totalLength += data.length;
		if (totalLength > REQUEST_IMAGE_BASE64_BUDGET)
			throw new Error("Session images exceed the 24 MiB aggregate encoded size limit");
	}
}

function isCanonicalBase64(value: string): boolean {
	if (!value || value.length % 4 !== 0) return false;
	if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) return false;
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	if (value.endsWith("==")) return (alphabet.indexOf(value.at(-3) ?? "") & 15) === 0;
	if (value.endsWith("=")) return (alphabet.indexOf(value.at(-2) ?? "") & 3) === 0;
	return true;
}

export function isRetriedAttempt(
	messages: readonly { role: string; stopReason?: string }[],
	index: number,
): boolean {
	const message = messages[index];
	if (message?.role !== "assistant" || message.stopReason !== "error") return false;
	return messages[index + 1]?.role === "assistant";
}

export type HistoryScope =
	| { kind: "chat"; sessionId: string }
	| { kind: "project"; projectId: string }
	| { kind: "all" };

export interface PromptHit {
	text: string;
	timestamp: number;
	sessionId: string;
	sessionTitle?: string;
	projectId?: string;
	cwd: string;
	messageIndex?: number;
	anchorText?: string;
}

export interface MessageHit extends PromptHit {
	role: "user" | "assistant";
	snippet: string;
	messageIndex: number;
	anchorText: string;
}

export const MAX_HISTORY_LIMIT = 200;

export const MAX_HISTORY_QUERY_LENGTH = 200;

export interface HistorySearchResult {
	prompts: PromptHit[];
	messages: MessageHit[];
	promptTotal: number;
	messageTotal: number;
	indexing: boolean;
	incomplete: boolean;
}
