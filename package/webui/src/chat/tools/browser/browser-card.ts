import { strArg, toolContent } from "../tool-helpers";

const IMAGE_MIME_TYPES = new Set(["image/png", "image/jpeg", "image/webp"]);
const BASE64_PATTERN = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
const MAX_IMAGE_BASE64_LENGTH = Math.ceil((64 * 1024 * 1024) / 3) * 4;
const ARTIFACT_PATH_PATTERN =
	/^\/v1\/artifacts\/[A-Za-z0-9_-]{1,38}\/[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.(?:png|jpe?g|webp)$/i;

export interface BrowserImageBlock {
	type: "image";
	data: string;
	mimeType: string;
}

export interface BrowserArtifact {
	name: string;
	url?: string;
}

export interface BrowserDetails {
	session?: string;
	command?: string;
	code?: string | number;
	artifact?: BrowserArtifact;
}

function responseRecord(value: unknown): Record<string, unknown> | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const outcome = Reflect.get(value, "outcome");
	return outcome === "completed" || outcome === "failed" || outcome === "rejected"
		? (value as Record<string, unknown>)
		: undefined;
}

export function browserResponse(result: unknown): Record<string, unknown> | undefined {
	const direct = responseRecord(result);
	if (direct) return direct;
	if (result && typeof result === "object") {
		const structured = responseRecord(Reflect.get(result, "structuredContent"));
		if (structured) return structured;
	}
	for (const block of toolContent(result)) {
		const text =
			typeof block === "string"
				? block
				: block && typeof block === "object" && Reflect.get(block, "type") === "text"
					? Reflect.get(block, "text")
					: undefined;
		if (typeof text !== "string" || text.length > 1024 * 1024) continue;
		try {
			const parsed = responseRecord(JSON.parse(text));
			if (parsed) return parsed;
		} catch {
			// Ordinary browser output remains plain text.
		}
	}
	return undefined;
}

export function browserDetails(result: unknown): BrowserDetails {
	const raw =
		(result && typeof result === "object" ? Reflect.get(result, "details") : undefined) ??
		browserResponse(result);
	if (!raw || typeof raw !== "object") return {};
	const session = Reflect.get(raw, "session");
	const command = Reflect.get(raw, "command");
	const code = Reflect.get(raw, "code");
	const rawArtifact = Reflect.get(raw, "artifact");
	const artifact =
		typeof rawArtifact === "string"
			? { name: rawArtifact }
			: rawArtifact &&
					typeof rawArtifact === "object" &&
					typeof Reflect.get(rawArtifact, "name") === "string"
				? {
						name: Reflect.get(rawArtifact, "name") as string,
						...(typeof Reflect.get(rawArtifact, "url") === "string"
							? { url: Reflect.get(rawArtifact, "url") as string }
							: {}),
					}
				: undefined;
	return {
		...(typeof session === "string" ? { session } : {}),
		...(typeof command === "string" ? { command } : {}),
		...(typeof code === "string" || typeof code === "number" ? { code } : {}),
		...(artifact ? { artifact } : {}),
	};
}

function isBrowserImageBlock(value: unknown): value is BrowserImageBlock {
	if (!value || typeof value !== "object") return false;
	const data = Reflect.get(value, "data");
	const mimeType = Reflect.get(value, "mimeType");
	return (
		Reflect.get(value, "type") === "image" &&
		typeof data === "string" &&
		typeof mimeType === "string" &&
		data.length > 0 &&
		data.length <= MAX_IMAGE_BASE64_LENGTH &&
		IMAGE_MIME_TYPES.has(mimeType.toLowerCase()) &&
		BASE64_PATTERN.test(data)
	);
}

export function browserImages(result: unknown): BrowserImageBlock[] {
	return toolContent(result).filter(isBrowserImageBlock);
}

export function browserArtifactUrl(url: string | undefined): string | undefined {
	if (!url || url.includes("\\") || url.includes("?") || url.includes("#")) return undefined;
	return ARTIFACT_PATH_PATTERN.test(url) ? url : undefined;
}

export function browserSummary(args: Record<string, unknown>): string {
	const command = strArg(args, "command");
	const session = strArg(args, "session");
	return [command || "browser", session ? `in ${session}` : ""].filter(Boolean).join(" ");
}
