const CANVAS_ID = "[a-z2-7]{16,128}";
const RENDER_KEY = "[a-f0-9]{32}";
const CANVAS_REFERENCE = new RegExp(
	`^pixie://canvas/artifact/(${CANVAS_ID})/(${RENDER_KEY})\\.png$`,
);
const CANVAS_PATH = new RegExp(`^/mcp/canvas/artifact/(${CANVAS_ID})/(${RENDER_KEY})\\.png$`);
const CANVAS_MANAGEMENT_PATH = new RegExp(
	`^/api/canvas/artifacts?/([^/]+)/([^/]+)/(${CANVAS_ID})/(${RENDER_KEY})\\.png$`,
);

/** Controller-verified scope used to turn an opaque Canvas reference into a
 * same-origin, cookie-authenticated management URL. */
export interface CanvasArtifactScope {
	projectId: string;
	sessionId: string;
}

function safeBaseUrl(baseUrl: string | URL | undefined): string | null {
	if (baseUrl === undefined) return "";
	const value = String(baseUrl);
	if (!value || value.includes("?") || value.includes("#") || value.includes("\\")) return null;
	if (value.startsWith("/") && !value.startsWith("//"))
		return value.endsWith("/") ? value.slice(0, -1) : value;
	try {
		const parsed = new URL(value);
		if (
			(parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
			parsed.username ||
			parsed.password ||
			parsed.search ||
			parsed.hash
		) {
			return null;
		}
		return parsed.origin + parsed.pathname.replace(/\/$/, "");
	} catch {
		return null;
	}
}

function joinArtifactPath(path: string, baseUrl: string | URL | undefined): string | null {
	const base = safeBaseUrl(baseUrl);
	if (base === null) return null;
	return `${base}${path}`;
}

function safePathSegment(value: string): string | null {
	if (
		!value ||
		value.includes("?") ||
		value.includes("#") ||
		value.includes("/") ||
		value.includes("\\")
	)
		return null;
	return encodeURIComponent(value);
}

function safeScope(scope: CanvasArtifactScope | undefined): string | null {
	if (!scope) return null;
	const projectId = safePathSegment(scope.projectId);
	const sessionId = safePathSegment(scope.sessionId);
	return projectId === null || sessionId === null ? null : `${projectId}/${sessionId}`;
}

function isScope(value: unknown): value is CanvasArtifactScope {
	return (
		value !== null &&
		typeof value === "object" &&
		typeof (value as CanvasArtifactScope).projectId === "string" &&
		typeof (value as CanvasArtifactScope).sessionId === "string"
	);
}

/**
 * Turn the server's opaque pixie:// reference into a same-origin image URL.
 * Authentication is supplied by the browser session; no bearer/query token
 * is accepted or generated here.
 */
export function canvasArtifactUrl(
	reference: string | null | undefined,
	baseUrl?: string | URL,
): string | null;
export function canvasArtifactUrl(
	reference: string | null | undefined,
	scope: CanvasArtifactScope,
	baseUrl?: string | URL,
): string | null;
export function canvasArtifactUrl(
	canvasId: string,
	renderKey: string,
	baseUrl?: string | URL,
): string | null;
export function canvasArtifactUrl(
	canvasId: string,
	renderKey: string,
	scope: CanvasArtifactScope,
	baseUrl?: string | URL,
): string | null;
export function canvasArtifactUrl(
	first: string | null | undefined,
	second?: string | URL | CanvasArtifactScope,
	third?: string | URL | CanvasArtifactScope,
	fourth?: string | URL,
): string | null {
	const canvasParts =
		typeof first === "string" &&
		!first.startsWith("pixie://") &&
		!first.startsWith("/mcp/canvas/") &&
		!first.startsWith("/api/canvas/");
	const scope = canvasParts
		? isScope(third)
			? third
			: undefined
		: isScope(second)
			? second
			: isScope(third)
				? third
				: undefined;
	const baseUrl = canvasParts
		? scope
			? fourth
			: typeof third === "string" || third instanceof URL
				? third
				: undefined
		: isScope(second)
			? typeof third === "string" || third instanceof URL
				? third
				: undefined
			: typeof second === "string" || second instanceof URL
				? second
				: fourth;
	const scopedPath = safeScope(scope);
	if (scope && scopedPath === null) return null;
	if (canvasParts) {
		if (!new RegExp(`^${CANVAS_ID}$`).test(first)) return null;
		const renderKey =
			typeof second === "string" ? second : typeof third === "string" ? third : null;
		if (renderKey === null || !new RegExp(`^${RENDER_KEY}$`).test(renderKey)) return null;
		const path = scopedPath
			? `/api/canvas/artifact/${scopedPath}/${first}/${renderKey}.png`
			: `/mcp/canvas/artifact/${first}/${renderKey}.png`;
		return joinArtifactPath(path, baseUrl);
	}
	const reference = first;
	if (
		typeof reference !== "string" ||
		reference.includes("?") ||
		reference.includes("#") ||
		reference.includes("\\")
	) {
		return null;
	}
	const managementMatch = CANVAS_MANAGEMENT_PATH.exec(reference);
	if (managementMatch) {
		const projectID = managementMatch[1];
		const sessionID = managementMatch[2];
		if (projectID === undefined || sessionID === undefined) return null;
		if (
			scopedPath &&
			`${encodeURIComponent(projectID)}/${encodeURIComponent(sessionID)}` !== scopedPath
		)
			return null;
		return joinArtifactPath(reference, baseUrl);
	}
	const match = CANVAS_REFERENCE.exec(reference) ?? CANVAS_PATH.exec(reference);
	if (!match) return null;
	const path = scopedPath
		? `/api/canvas/artifact/${scopedPath}/${match[1]}/${match[2]}.png`
		: `/mcp/canvas/artifact/${match[1]}/${match[2]}.png`;
	return joinArtifactPath(path, baseUrl);
}

export function buildCanvasArtifactUrl(
	canvasId: string,
	renderKey: string,
	baseUrl?: string | URL,
): string | null {
	return canvasArtifactUrl(`pixie://canvas/artifact/${canvasId}/${renderKey}.png`, baseUrl);
}

export function buildCanvasArtifactUrlForSession(
	canvasId: string,
	renderKey: string,
	scope: CanvasArtifactScope,
	baseUrl?: string | URL,
): string | null {
	return canvasArtifactUrl(`pixie://canvas/artifact/${canvasId}/${renderKey}.png`, scope, baseUrl);
}

/** Build the session-scoped management status route without adding credentials. */
export function canvasManagementStatusUrl(scope: CanvasArtifactScope): string | null;
export function canvasManagementStatusUrl(projectId: string, sessionId: string): string | null;
export function canvasManagementStatusUrl(
	first: CanvasArtifactScope | string,
	second?: string,
): string | null {
	const scope = typeof first === "string" ? { projectId: first, sessionId: second ?? "" } : first;
	const scopedPath = safeScope(scope);
	return scopedPath === null ? null : `/api/canvas/status/${scopedPath}`;
}

/** Build the session-scoped management removal route without adding credentials. */
export function canvasManagementRemoveUrl(scope: CanvasArtifactScope): string | null;
export function canvasManagementRemoveUrl(projectId: string, sessionId: string): string | null;
export function canvasManagementRemoveUrl(
	first: CanvasArtifactScope | string,
	second?: string,
): string | null {
	const scope = typeof first === "string" ? { projectId: first, sessionId: second ?? "" } : first;
	const scopedPath = safeScope(scope);
	return scopedPath === null ? null : `/api/canvas/remove/${scopedPath}`;
}

export const authenticatedCanvasArtifactUrl = canvasArtifactUrl;
