import { type CanvasArtifactScope, canvasArtifactUrl } from "./canvas-artifact-url";

/**
 * Browser-side Canvas projections.
 *
 * Canvas HTML is never a UI value. The browser receives metadata and a
 * controller-authenticated raster artifact instead, so these models do not
 * contain an executable preview or a URL credential.
 */

export type CanvasAvailability =
	| "empty"
	| "ready"
	| "rendering"
	| "disabled"
	| "unavailable"
	| "removed"
	| "corrupt"
	| "stale";

export type CanvasPreviewStatus = "idle" | "pending" | "ready" | "stale" | "unavailable" | "error";

export interface CanvasScope {
	sessionId: string;
	/** Project association needed by controller management/artifact routes. */
	projectId?: string;
}

export interface CanvasDocument {
	id: string;
	generation: number;
	version: number;
	availability: CanvasAvailability;
	createdAt?: string;
	updatedAt?: string;
	removed?: boolean;
}

export interface CanvasWarning {
	code?: string;
	message: string;
}

/** A raster-only preview. An artifact URL is same-origin and cookie-authenticated. */
export interface CanvasPreviewMetadata {
	kind: "raster";
	status: CanvasPreviewStatus;
	canvasId: string | null;
	generation: number | null;
	version: number | null;
	url: string | null;
	/** Alias used by API adapters; identical to url and never contains a token. */
	artifactUrl?: string | null;
	mime: "image/png" | null;
	width: number | null;
	height: number | null;
	bytes: number | null;
	alt: string;
	reason: string | null;
}

export interface CanvasState {
	scope: CanvasScope | null;
	status: CanvasAvailability;
	document: CanvasDocument | null;
	currentVersion: number | null;
	viewedVersion: number | null;
	preview: CanvasPreviewMetadata;
	warnings: CanvasWarning[];
	error: CanvasWarning | null;
}

export interface CanvasStatus {
	scope: CanvasScope | null;
	availability: CanvasAvailability;
	canvasId: string | null;
	generation: number | null;
	currentVersion: number | null;
	viewedVersion: number | null;
	preview: CanvasPreviewMetadata;
	warnings: CanvasWarning[];
}

export interface CanvasRemovalRequest {
	canvasId: string;
	expectedGeneration: number;
	expectedVersion: number;
}

export interface CanvasStatusResponse {
	outcome?: string;
	canvas?: Partial<CanvasDocument> & {
		canvasId?: string;
		available?: boolean;
		availability?: string;
	};
	canvasId?: string;
	generation?: number;
	version?: number;
	updatedAt?: string;
	createdAt?: string;
	availability?: string;
	available?: boolean;
	removed?: boolean;
	warnings?: Array<string | CanvasWarning>;
}

export interface CanvasWriteResult {
	outcome?: string;
	canvasId: string;
	generation: number;
	version: number;
	updatedAt?: string;
	mutationId?: string;
	idempotent?: boolean;
}

export interface CanvasScreenshotResult {
	outcome?: string;
	canvasId: string;
	generation: number;
	version: number;
	mime?: string;
	width?: number;
	height?: number;
	bytes?: number;
	artifact?: string;
	cached?: boolean;
}

export interface CanvasVersionEvent {
	sessionId: string;
	canvasId: string;
	generation: number;
	version: number;
	updatedAt?: string;
}

export const EMPTY_CANVAS_PREVIEW: CanvasPreviewMetadata = {
	kind: "raster",
	status: "idle",
	canvasId: null,
	generation: null,
	version: null,
	url: null,
	artifactUrl: null,
	mime: null,
	width: null,
	height: null,
	bytes: null,
	alt: "Canvas raster preview",
	reason: null,
};

export function emptyCanvasState(sessionId: string | null = null, projectId?: string): CanvasState {
	return {
		scope: sessionId === null ? null : { sessionId, ...(projectId ? { projectId } : {}) },
		status: "empty",
		document: null,
		currentVersion: null,
		viewedVersion: null,
		preview: { ...EMPTY_CANVAS_PREVIEW },
		warnings: [],
		error: null,
	};
}

export function createCanvasState(sessionId: string | null = null): CanvasState {
	return emptyCanvasState(sessionId);
}

export function canvasStatusFromState(state: CanvasState): CanvasStatus {
	return {
		scope: state.scope,
		availability: state.status,
		canvasId: state.document?.id ?? null,
		generation: state.document?.generation ?? null,
		currentVersion: state.currentVersion,
		viewedVersion: state.viewedVersion,
		preview: state.preview,
		warnings: state.warnings,
	};
}

export function canvasRemovalRequest(state: CanvasState): CanvasRemovalRequest | null {
	if (!state.document || state.status === "removed" || state.status === "empty") return null;
	return {
		canvasId: state.document.id,
		expectedGeneration: state.document.generation,
		expectedVersion: state.document.version,
	};
}

function statusFromResponse(
	value: unknown,
	available: boolean | undefined,
	removed: boolean | undefined,
): CanvasAvailability {
	if (removed === true) return "removed";
	if (
		value === "empty" ||
		value === "ready" ||
		value === "rendering" ||
		value === "disabled" ||
		value === "unavailable" ||
		value === "removed" ||
		value === "corrupt" ||
		value === "stale"
	) {
		return value;
	}
	if (available === false) return "unavailable";
	return "ready";
}

function warningList(values: CanvasStatusResponse["warnings"]): CanvasWarning[] {
	return (values ?? []).flatMap((warning) => {
		if (typeof warning === "string" && warning.trim()) return [{ message: warning }];
		if (
			warning &&
			typeof warning === "object" &&
			typeof warning.message === "string" &&
			warning.message.trim()
		) {
			return [
				{
					message: warning.message,
					...(typeof warning.code === "string" ? { code: warning.code } : {}),
				},
			];
		}
		return [];
	});
}

/** Convert a controller status response without accepting untrusted HTML. */
export function canvasStateFromStatus(
	state: CanvasState,
	response: CanvasStatusResponse,
): CanvasState {
	const source = response.canvas ?? response;
	const nestedId = "id" in source && typeof source.id === "string" ? source.id : undefined;
	const id =
		typeof source.canvasId === "string" ? source.canvasId : (nestedId ?? response.canvasId);
	const generation =
		typeof source.generation === "number" ? source.generation : response.generation;
	const version = typeof source.version === "number" ? source.version : response.version;
	const availability = statusFromResponse(
		response.availability ?? source.availability,
		response.available ?? source.available,
		source.removed,
	);
	const sameDocument = id !== undefined && state.document?.id === id;
	const document =
		id && typeof generation === "number" && typeof version === "number"
			? {
					id,
					generation,
					version,
					availability,
					...(typeof source.createdAt === "string" ? { createdAt: source.createdAt } : {}),
					...(typeof source.updatedAt === "string"
						? { updatedAt: source.updatedAt }
						: typeof response.updatedAt === "string"
							? { updatedAt: response.updatedAt }
							: {}),
				}
			: null;

	// A status for a replaced canvas invalidates the old raster. Never display
	// the old artifact as if it belonged to the new generation.
	const replaced =
		!sameDocument ||
		(state.document !== null &&
			document !== null &&
			state.document.generation !== document.generation);
	const unavailable =
		availability === "disabled" || availability === "unavailable" || availability === "removed";
	const newerVersion =
		!replaced &&
		!unavailable &&
		document !== null &&
		state.currentVersion !== null &&
		document.version > state.currentVersion;
	return {
		...state,
		status: availability,
		document,
		currentVersion: document?.version ?? null,
		viewedVersion: replaced ? null : state.viewedVersion,
		preview:
			replaced || unavailable
				? {
						...EMPTY_CANVAS_PREVIEW,
						status: unavailable ? "unavailable" : "idle",
						reason: unavailable ? "Canvas preview is currently unavailable." : null,
					}
				: newerVersion
					? staleCanvasPreview(state.preview)
					: state.preview,
		warnings: warningList(response.warnings),
		error: null,
	};
}

export function canvasPreviewFromScreenshot(
	result: CanvasScreenshotResult,
	artifactUrl: string | null,
	scope?: CanvasArtifactScope,
): CanvasPreviewMetadata {
	const safeArtifactUrl =
		artifactUrl === null
			? null
			: scope
				? canvasArtifactUrl(artifactUrl, scope)
				: canvasArtifactUrl(artifactUrl);
	const ready = safeArtifactUrl !== null && result.mime === "image/png";
	return {
		kind: "raster",
		status: ready ? "ready" : "unavailable",
		canvasId: result.canvasId,
		generation: result.generation,
		version: result.version,
		url: ready ? safeArtifactUrl : null,
		artifactUrl: ready ? safeArtifactUrl : null,
		mime: ready ? "image/png" : null,
		width: typeof result.width === "number" ? result.width : null,
		height: typeof result.height === "number" ? result.height : null,
		bytes: typeof result.bytes === "number" ? result.bytes : null,
		alt: `Canvas raster preview, version ${result.version}`,
		reason: ready ? null : "Raster preview unavailable.",
	};
}

/** Mark a previously rendered image stale while retaining it for comparison. */
export function staleCanvasPreview(preview: CanvasPreviewMetadata): CanvasPreviewMetadata {
	if (preview.status === "idle" || preview.status === "unavailable") return preview;
	return { ...preview, status: "stale", reason: "A newer Canvas version is being rendered." };
}

export function applyCanvasVersionEvent(
	state: CanvasState,
	event: CanvasVersionEvent,
): CanvasState {
	if (state.scope?.sessionId !== event.sessionId) return state;
	if (state.document && state.document.id !== event.canvasId) return state;
	if (state.document && state.document.generation !== event.generation) return state;
	if ((state.currentVersion ?? 0) >= event.version) return state;
	const document: CanvasDocument = {
		...(state.document ?? {
			id: event.canvasId,
			generation: event.generation,
			version: event.version,
			availability: "ready" as const,
		}),
		id: event.canvasId,
		generation: event.generation,
		version: event.version,
		availability: "ready",
		...(typeof event.updatedAt === "string" ? { updatedAt: event.updatedAt } : {}),
	};
	return {
		...state,
		status: "ready",
		document,
		currentVersion: event.version,
		preview: staleCanvasPreview(state.preview),
		error: null,
	};
}

export function applyCanvasScreenshot(
	state: CanvasState,
	result: CanvasScreenshotResult,
	artifactUrl: string | null,
): CanvasState {
	if (state.document?.id !== result.canvasId || state.document.generation !== result.generation) {
		return state;
	}
	if (state.currentVersion !== null && result.version > state.currentVersion) return state;
	const preview = canvasPreviewFromScreenshot(
		result,
		artifactUrl,
		state.scope?.projectId
			? { projectId: state.scope.projectId, sessionId: state.scope.sessionId }
			: undefined,
	);
	const current = state.currentVersion === result.version;
	return {
		...state,
		viewedVersion: result.version,
		preview: current
			? preview
			: { ...preview, status: "stale", reason: "This preview is from an older Canvas version." },
	};
}

export function setCanvasPreviewPending(state: CanvasState, version: number): CanvasState {
	return {
		...state,
		preview: {
			...state.preview,
			status: "pending",
			canvasId: state.document?.id ?? null,
			generation: state.document?.generation ?? null,
			version,
			url: state.preview.url,
			artifactUrl: state.preview.url,
			reason: null,
		},
	};
}
