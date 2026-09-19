/** Browser-side Canvas status projection. Canvas HTML is never a UI value. */

export type CanvasAvailability =
	| "empty"
	| "ready"
	| "rendering"
	| "disabled"
	| "unavailable"
	| "removed"
	| "corrupt"
	| "stale";

export interface CanvasScope {
	sessionId: string;
}

export interface CanvasDocument {
	id: string;
	generation: number;
	version: number;
	availability: CanvasAvailability;
	createdAt?: string;
	updatedAt?: string;
}

export interface CanvasWarning {
	code?: string;
	message: string;
}

export interface CanvasState {
	scope: CanvasScope | null;
	status: CanvasAvailability;
	document: CanvasDocument | null;
	warnings: CanvasWarning[];
}

export interface CanvasStatusResponse {
	outcome?: string;
	canvas?: Partial<CanvasDocument> & {
		canvasId?: string;
		available?: boolean;
		availability?: string;
		removed?: boolean;
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

export function emptyCanvasState(sessionId: string | null = null): CanvasState {
	return {
		scope: sessionId === null ? null : { sessionId },
		status: "empty",
		document: null,
		warnings: [],
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
	const nestedID = "id" in source && typeof source.id === "string" ? source.id : undefined;
	const id =
		typeof source.canvasId === "string" ? source.canvasId : (nestedID ?? response.canvasId);
	const generation =
		typeof source.generation === "number" ? source.generation : response.generation;
	const version = typeof source.version === "number" ? source.version : response.version;
	const availability = statusFromResponse(
		response.availability ?? source.availability,
		response.available ?? source.available,
		response.removed,
	);
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
	return {
		...state,
		status: availability,
		document,
		warnings: warningList(response.warnings),
	};
}
