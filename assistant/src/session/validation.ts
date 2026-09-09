import { isAbsolute } from "node:path";
import type {
	FlowError,
	FlowResult,
	NativeModel,
	NativeResource,
	NativeThinkingLevel,
	PromptBlock,
	PromptReplayBlock,
	PromptRequest,
	SessionCreateRequest,
	SessionResourceInput,
	TrustResolution,
	ValidatedPrompt,
} from "./types.ts";

export const MAX_PROMPT_TEXT_BYTES = 4 * 1024 * 1024;
export const MAX_IMAGES = 8;
export const MAX_IMAGE_BYTES = Math.floor(4.5 * 1024 * 1024);
export const MAX_IMAGE_AGGREGATE_BYTES = 24 * 1024 * 1024;

function error<T>(code: FlowError["code"], message: string): FlowResult<T> {
	return { ok: false, error: { code, message } };
}

function success<T>(value: T): FlowResult<T> {
	return { ok: true, value };
}

function utf8Bytes(value: string): number {
	return new TextEncoder().encode(value).byteLength;
}

function validOpaque(value: unknown, max = 512): value is string {
	return typeof value === "string" && value.length > 0 && value.length <= max && !value.includes("\0");
}

function validAbsolutePath(value: unknown): value is string {
	return typeof value === "string" && value.length > 0 && value.length <= 4096 && !value.includes("\0") && isAbsolute(value);
}

function normalizeResource(resource: NativeResource): NativeResource | undefined {
	if (!validAbsolutePath(resource.path)) return undefined;
	if (resource.resolvedPath !== undefined && !validAbsolutePath(resource.resolvedPath)) return undefined;
	if (resource.scope !== "user" && resource.scope !== "project") return undefined;
	if (resource.kind !== undefined && !["extension", "skill", "prompt", "theme", "unknown"].includes(resource.kind))
		return undefined;
	const requiresTrust = resource.requiresTrust ?? resource.scope === "project";
	return {
		path: resource.path,
		...(resource.resolvedPath ? { resolvedPath: resource.resolvedPath } : {}),
		scope: resource.scope,
		...(resource.kind ? { kind: resource.kind } : {}),
		...(requiresTrust ? { requiresTrust: true } : {}),
	};
}

function normalizeModel(model: NativeModel): NativeModel | undefined {
	if (!validOpaque(model.provider, 256) || !validOpaque(model.id, 512)) return undefined;
	if (model.name !== undefined && !validOpaque(model.name, 1024)) return undefined;
	return {
		provider: model.provider,
		id: model.id,
		...(model.name !== undefined ? { name: model.name } : {}),
	};
}

export function normalizeNativeModels(models: readonly NativeModel[] | undefined): FlowResult<readonly NativeModel[]> {
	if (!models) return success([]);
	const normalized: NativeModel[] = [];
	const seen = new Set<string>();
	for (const model of models) {
		const safe = normalizeModel(model);
		if (!safe) return error("invalid-request", "Native model identity is invalid");
		const key = `${safe.provider}\0${safe.id}`;
		if (seen.has(key)) continue;
		seen.add(key);
		normalized.push(safe);
	}
	return success(normalized);
}

export function validateModelSelection(
	model: NativeModel,
	availableModels: readonly NativeModel[],
): FlowResult<NativeModel> {
	const safe = normalizeModel(model);
	if (!safe) return error("invalid-request", "Native model identity is invalid");
	const catalog = normalizeNativeModels(availableModels);
	if (!catalog.ok) return catalog;
	const match = catalog.value.find((candidate) => candidate.provider === safe.provider && candidate.id === safe.id);
	return match ? success(match) : error("unknown-model", "Model is not available in the native catalog");
}

export function normalizeThinkingLevels(
	levels: readonly NativeThinkingLevel[] | undefined,
): FlowResult<readonly NativeThinkingLevel[]> {
	if (!levels) return success([]);
	const normalized: string[] = [];
	const seen = new Set<string>();
	for (const level of levels) {
		if (!validOpaque(level, 64)) return error("invalid-request", "Native thinking level is invalid");
		if (!seen.has(level)) {
			seen.add(level);
			normalized.push(level);
		}
	}
	return success(normalized);
}

/**
 * Return the strongest known level the native model actually advertises.
 * Unknown future levels are preserved by normalizeThinkingLevels but are not
 * guessed into the known ordering.
 */
export function highestSupportedThinkingLevel(
	levels: readonly NativeThinkingLevel[],
): NativeThinkingLevel | undefined {
	const order = ["minimal", "low", "medium", "high", "xhigh", "max"];
	return [...order].reverse().find((level) => levels.includes(level));
}

export function validateThinkingSelection(
	level: NativeThinkingLevel,
	availableLevels: readonly NativeThinkingLevel[],
): FlowResult<NativeThinkingLevel> {
	if (!validOpaque(level, 64)) return error("invalid-request", "Thinking level is invalid");
	if (!availableLevels.includes(level)) return error("unsupported-thinking", "Thinking level is not supported by the native model");
	return success(level);
}

function validBase64(value: string): boolean {
	if (!value || /[^A-Za-z0-9+/=]/.test(value) || value.length % 4 === 1) return false;
	const padding = value.indexOf("=");
	return padding < 0 || padding >= value.length - 2;
}

function imageReplay(mimeType: string, data: string): { type: "image"; mimeType: string; bytes: number } {
	return { type: "image", mimeType, bytes: utf8Bytes(data) };
}

function validatePromptBlock(block: PromptBlock): FlowResult<{ text: string; image?: { type: "image"; mimeType: string; data: string }; replay: ValidatedPrompt["replay"][number] }> {
	if (!block || typeof block !== "object") return error("invalid-request", "Prompt content block is invalid");
	if (block.type === "text") {
		if (typeof block.text !== "string") return error("invalid-request", "Prompt text is invalid");
		return success({ text: block.text, replay: { type: "text", text: block.text } });
	}
	if (block.type === "image") {
		if (typeof block.mimeType !== "string" || !/^image\/[A-Za-z0-9.+-]+$/.test(block.mimeType))
			return error("invalid-request", "Image MIME type is invalid");
		if (typeof block.data !== "string" || !validBase64(block.data))
			return error("invalid-request", "Image data is not valid base64");
		const bytes = utf8Bytes(block.data);
		if (bytes > MAX_IMAGE_BYTES) return error("invalid-request", "Image exceeds the per-image limit");
		return success({
			text: "",
			image: { type: "image", mimeType: block.mimeType, data: block.data },
			replay: imageReplay(block.mimeType, block.data),
		});
	}
	if (block.type === "resource") {
		const resource = block.resource;
		if (!resource || typeof resource !== "object") return error("invalid-request", "Attached resource is invalid");
		const uri = typeof resource.uri === "string" ? resource.uri : "";
		const text = typeof resource.text === "string" ? resource.text : "";
		const mimeType = typeof resource.mimeType === "string" ? resource.mimeType : undefined;
		const name = typeof resource._meta?.name === "string" && resource._meta.name ? resource._meta.name : uri;
		if (!validOpaque(uri, 4096) || !validOpaque(name, 4096))
			return error("invalid-request", "Attached resource identity is invalid");
		const encodedName = JSON.stringify(name);
		return success({
			text: `\n\n<attached-file name=${encodedName}>\n${text}\n</attached-file>`,
			replay: { type: "resource", uri, ...(mimeType ? { mimeType } : {}), name },
		});
	}
	return error("invalid-request", "Unsupported prompt content block");
}

export function validatePromptRequest(request: PromptRequest): FlowResult<ValidatedPrompt> {
	if (!validOpaque(request.sessionKey) || !validOpaque(request.deliveryId) || !validOpaque(request.runId))
		return error("invalid-request", "Prompt identity is invalid");
	if (!Number.isSafeInteger(request.generation) || request.generation < 0)
		return error("invalid-request", "Prompt generation is invalid");
	if (!Array.isArray(request.content) || request.content.length === 0)
		return error("invalid-request", "Prompt content must contain at least one block");
	let text = "";
	let imageBytes = 0;
	let imageCount = 0;
	const images: { type: "image"; mimeType: string; data: string }[] = [];
	const replay: PromptReplayBlock[] = [];
	for (const block of request.content) {
		const result = validatePromptBlock(block);
		if (!result.ok) return result;
		text += result.value.text;
		if (result.value.image) {
			imageCount++;
			imageBytes += utf8Bytes(result.value.image.data);
			images.push(result.value.image);
		}
		replay.push(result.value.replay);
	}
	if (imageCount > MAX_IMAGES) return error("invalid-request", "Prompt contains too many images");
	if (imageBytes > MAX_IMAGE_AGGREGATE_BYTES) return error("invalid-request", "Prompt images exceed the aggregate limit");
	if (utf8Bytes(text) > MAX_PROMPT_TEXT_BYTES) return error("invalid-request", "Prompt text exceeds the limit");
	return success({ text, images, replay });
}

export function resolveNativeTrust(input: SessionResourceInput): FlowResult<{
	readonly resources: readonly NativeResource[];
	readonly loadedResources: readonly NativeResource[];
	readonly trust: TrustResolution;
}> {
	const resources: NativeResource[] = [];
	for (const resource of input.resources ?? []) {
		const normalized = normalizeResource(resource);
		if (!normalized) return error("invalid-resource", "Native resource identity is invalid");
		resources.push(normalized);
	}
	const hasProjectResources = resources.some(
		(resource) => resource.scope === "project" && (resource.requiresTrust ?? true),
	);
	let trust: TrustResolution;
	if (!hasProjectResources) {
		trust = {
			state: "not-required",
			allowed: true,
			decision: input.projectTrust ?? null,
			reason: "no-project-resources",
		};
	} else if (input.projectTrust === true || input.projectTrust === false) {
		trust = {
			state: input.projectTrust ? "trusted" : "untrusted",
			allowed: input.projectTrust,
			decision: input.projectTrust,
			reason: "stored-decision",
		};
	} else if (input.defaultProjectTrust === "always") {
		trust = { state: "trusted", allowed: true, decision: "always", reason: "default-always" };
	} else {
		// Native "ask" and "never" both fail closed for a headless flow. The
		// decision is still reported so a caller can present the native trust UI.
		trust = {
			state: "untrusted",
			allowed: false,
			decision: input.defaultProjectTrust ?? "ask",
			reason: "trust-required",
		};
	}
	const loadedResources = resources.filter(
		(resource) => resource.scope === "user" || !resource.requiresTrust || trust.allowed,
	);
	return success({ resources, loadedResources, trust });
}

export function validateCreateRequest(request: SessionCreateRequest): FlowResult<SessionCreateRequest> {
	const identity = request.identity;
	if (
		!validOpaque(identity.sessionKey) ||
		!validOpaque(identity.sessionId) ||
		!validOpaque(identity.nativeSessionId) ||
		!validOpaque(identity.bootId) ||
		!Number.isSafeInteger(identity.childGeneration) ||
		identity.childGeneration < 0 ||
		!validAbsolutePath(identity.cwd) ||
		!validAbsolutePath(identity.agentDir)
	)
		return error("invalid-request", "Native session identity is invalid");
	const resources = resolveNativeTrust(request);
	if (!resources.ok) return resources;
	const models = normalizeNativeModels(request.availableModels);
	if (!models.ok) return models;
	const levels = normalizeThinkingLevels(request.availableThinkingLevels);
	if (!levels.ok) return levels;
	if (request.model) {
		const selected = validateModelSelection(request.model, models.value);
		if (!selected.ok) return selected;
	}
	if (request.thinkingLevel !== undefined) {
		const selected = validateThinkingSelection(request.thinkingLevel, levels.value);
		if (!selected.ok) return selected;
	}
	return success({
		...request,
		identity: { ...identity },
		resources: resources.value.resources,
		projectTrust: request.projectTrust ?? null,
		defaultProjectTrust: request.defaultProjectTrust ?? "ask",
		availableModels: models.value,
		availableThinkingLevels: levels.value,
		...(request.model ? { model: normalizeModel(request.model)! } : {}),
	});
}

/**
 * Remove credential-shaped keys before a native event or adapter-owned value
 * can enter a replay record or diagnostic. Prompt text is not inspected or
 * rewritten: it is user content, not a credential channel.
 */
export function redactSecrets(value: unknown): unknown {
	const secretKey = /(?:secret|token|password|passwd|api[-_]?key|authorization|credential|private[-_]?key|access[-_]?token|refresh[-_]?token)/i;
	const visit = (current: unknown, seen: WeakSet<object>): unknown => {
		if (current === null || typeof current !== "object") return current;
		if (seen.has(current)) return "[Circular]";
		seen.add(current);
		if (Array.isArray(current)) return current.map((item) => visit(item, seen));
		const result: Record<string, unknown> = {};
		for (const [key, item] of Object.entries(current)) {
			if (secretKey.test(key)) continue;
			result[key] = visit(item, seen);
		}
		return result;
	};
	return visit(value, new WeakSet<object>());
}
