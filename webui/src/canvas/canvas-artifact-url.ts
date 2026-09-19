/** Controller-verified scope for the Canvas management status route. */
export interface CanvasManagementScope {
	projectId: string;
	sessionId: string;
}

function safePathSegment(value: string): string | null {
	if (
		!value ||
		value.includes("?") ||
		value.includes("#") ||
		value.includes("/") ||
		value.includes("\\")
	) {
		return null;
	}
	return encodeURIComponent(value);
}

/** Build the session-scoped management status route without credentials. */
export function canvasManagementStatusUrl(scope: CanvasManagementScope): string | null {
	const projectId = safePathSegment(scope.projectId);
	const sessionId = safePathSegment(scope.sessionId);
	return projectId === null || sessionId === null
		? null
		: `/api/canvas/status/${projectId}/${sessionId}`;
}
