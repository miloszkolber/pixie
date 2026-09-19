import type { UserMessage } from "@pixie/shared";

export const DOUBLE_CLICK_SETTLE_MS = 250;

export function tupleKey(namespace: string, ...parts: string[]): string {
	return `${namespace}:${parts.map((part) => `${part.length}:${part}`).join("")}`;
}

export function randomId(prefix = "id"): string {
	const bytes = crypto.getRandomValues(new Uint8Array(16));
	const value = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
	return `${prefix}-${value}`;
}

export function userText(content: UserMessage["content"]): string {
	if (typeof content === "string") return content;
	return content
		.filter((c) => c.type === "text")
		.map((c) => c.text)
		.join("");
}

export function isMarkdownPath(path: string): boolean {
	return /\.(md|markdown)$/i.test(path);
}

export function normalizePath(path: string): string {
	return path.replaceAll("\\", "/").replace(/^\.\/+/, "");
}

export function isAbsolutePath(path: string): boolean {
	const normalized = normalizePath(path);
	return normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized);
}

function fileName(path: string): string {
	const parts = normalizePath(path).split("/").filter(Boolean);
	return parts.at(-1) ?? path;
}

function trimTrailingSlashes(path: string): string {
	return path === "/" || /^[A-Za-z]:\/$/.test(path) ? path : path.replace(/\/+$/, "");
}

function canonicalPosixPath(path: string): string {
	const normalized = normalizePath(path);
	const drive = /^[A-Za-z]:\//.exec(normalized)?.[0];
	const absolute = normalized.startsWith("/") || drive !== undefined;
	const body = drive ? normalized.slice(drive.length) : normalized.replace(/^\/+/, "");
	const segments: string[] = [];
	for (const segment of body.split("/")) {
		if (!segment || segment === ".") continue;
		if (segment === "..") {
			const previous = segments.at(-1);
			if (previous && previous !== "..") segments.pop();
			else if (!absolute) segments.push(segment);
			continue;
		}
		segments.push(segment);
	}
	const prefix = drive ?? (absolute ? "/" : "");
	return `${prefix}${segments.join("/")}`;
}

export function projectRelativePath(path: string, projectAreaRoot?: string | undefined): string {
	const canonical = canonicalPosixPath(path);
	if (!canonical || !isAbsolutePath(canonical)) return canonical;

	const root = projectAreaRoot ? trimTrailingSlashes(canonicalPosixPath(projectAreaRoot)) : "";
	const rootPrefix = root.endsWith("/") ? root : `${root}/`;
	if (root && (canonical === root || canonical.startsWith(rootPrefix))) {
		return canonical.slice(root.length).replace(/^\/+/, "") || fileName(canonical);
	}

	return canonical;
}

export function stripFrontmatter(text: string): string {
	const match = /^---[ \t]*\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(text);
	return match ? text.slice(match[0].length) : text;
}

const APPLE_PLATFORM = /Mac|iPhone|iPad|iPod/;

function browserPlatform(): string {
	return typeof navigator === "undefined" ? "" : (navigator.platform ?? "");
}

function isApplePlatform(platform: string): boolean {
	return APPLE_PLATFORM.test(platform);
}

export function hasPlatformModifier(
	event: Pick<KeyboardEvent, "ctrlKey" | "metaKey">,
	platform = browserPlatform(),
): boolean {
	return isApplePlatform(platform)
		? event.metaKey && !event.ctrlKey
		: event.ctrlKey && !event.metaKey;
}

export function relativeTime(ms: number): string {
	const s = Math.floor((Date.now() - ms) / 1000);
	if (s < 60) return "just now";
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ago`;
	const h = Math.floor(m / 60);
	if (h < 24) return `${h}h ago`;
	return `${Math.floor(h / 24)}d ago`;
}

export async function copyText(text: string): Promise<boolean> {
	try {
		await navigator.clipboard.writeText(text);
		return true;
	} catch {
		return false;
	}
}
