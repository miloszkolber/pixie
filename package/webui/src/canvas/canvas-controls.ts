import type { CanvasDocument, CanvasState } from "./canvas-model";
import { canvasRemovalRequest } from "./canvas-model";

/**
 * Slot 4/5 control projections for Canvas (CAN-04).
 *
 * Canvas is session-scoped and raster-first. These helpers turn state into
 * explicit version/rendering-state/update-time labels and gate the
 * screenshot/refresh/removal actions without touching host wiring. The shell
 * keeps Focus/Restore ownership; this module only describes what the sidebar
 * (slot 5) and viewer (slot 4) may offer.
 */

export interface CanvasControlAvailability {
	canScreenshot: boolean;
	canRefresh: boolean;
	canRemove: boolean;
	screenshotReason: string | null;
	refreshReason: string | null;
	removalReason: string | null;
}

export interface CanvasVersionDisplay {
	viewedVersion: number | null;
	currentVersion: number | null;
	isStale: boolean;
	label: string;
	detail: string | null;
}

export interface CanvasRenderingDisplay {
	status: string;
	label: string;
	isPending: boolean;
	isStale: boolean;
	isUnavailable: boolean;
}

export interface CanvasUpdateDisplay {
	iso: string | null;
	label: string;
}

export interface CanvasRemovalConfirmation {
	title: string;
	message: string;
	requiresConfirmation: true;
	request: { canvasId: string; expectedGeneration: number; expectedVersion: number };
}

export interface CanvasSidebarViewModel {
	scopeLabel: string;
	version: CanvasVersionDisplay;
	rendering: CanvasRenderingDisplay;
	updated: CanvasUpdateDisplay;
	availability: CanvasControlAvailability;
	removal: CanvasRemovalConfirmation | null;
	warningCount: number;
}

export interface CanvasViewerViewModel {
	heading: string;
	version: CanvasVersionDisplay;
	rendering: CanvasRenderingDisplay;
	updated: CanvasUpdateDisplay;
	isStalePreview: boolean;
	availability: CanvasControlAvailability;
}

function renderingStatus(state: CanvasState): string {
	if (state.preview.status === "pending") return "rendering";
	if (state.preview.status === "stale") return "stale";
	return state.status;
}

export function canvasRenderingDisplay(state: CanvasState): CanvasRenderingDisplay {
	const previewStatus = state.preview.status;
	const status = renderingStatus(state);
	const label =
		previewStatus === "pending"
			? "Rendering raster preview…"
			: previewStatus === "stale"
				? "Stale preview — a newer version is rendering"
				: status === "ready"
					? "Ready"
					: status === "rendering"
						? "Rendering"
						: status === "empty"
							? "Empty — no Canvas yet"
							: status === "disabled"
								? "Disabled"
								: status === "unavailable"
									? "Unavailable"
									: status === "removed"
										? "Removed"
										: status === "corrupt"
											? "Corrupt"
											: status === "stale"
												? "Stale — refresh status"
												: status;
	return {
		status,
		label,
		isPending: previewStatus === "pending",
		isStale: previewStatus === "stale" || status === "stale",
		isUnavailable:
			status === "disabled" ||
			status === "unavailable" ||
			status === "removed" ||
			previewStatus === "unavailable",
	};
}

export function canvasVersionDisplay(state: CanvasState): CanvasVersionDisplay {
	const viewedVersion = state.viewedVersion;
	const currentVersion = state.currentVersion;
	const isStale =
		state.preview.status === "stale" ||
		(viewedVersion !== null && currentVersion !== null && viewedVersion !== currentVersion);
	if (currentVersion === null) {
		return {
			viewedVersion,
			currentVersion,
			isStale: false,
			label: "No Canvas version yet",
			detail: null,
		};
	}
	if (viewedVersion === null) {
		return {
			viewedVersion,
			currentVersion,
			isStale: false,
			label: `Version ${currentVersion}`,
			detail: "Preview not yet captured for this version.",
		};
	}
	if (viewedVersion === currentVersion) {
		return {
			viewedVersion,
			currentVersion,
			isStale: state.preview.status === "stale",
			label: `Version ${viewedVersion}`,
			detail: state.preview.status === "stale" ? "A newer render is pending." : null,
		};
	}
	return {
		viewedVersion,
		currentVersion,
		isStale: true,
		label: `Viewing version ${viewedVersion} · current ${currentVersion}`,
		detail: "This preview is from an older Canvas version.",
	};
}

export function canvasUpdateDisplay(document: CanvasDocument | null): CanvasUpdateDisplay {
	const iso = document?.updatedAt ?? document?.createdAt ?? null;
	if (!iso) return { iso: null, label: "Not updated yet" };
	return { iso, label: `Updated ${iso}` };
}

export function canvasControlAvailability(
	state: CanvasState,
	options: { ready: boolean; pending: boolean },
): CanvasControlAvailability {
	const hasSession = state.scope?.sessionId !== null && state.scope?.sessionId !== undefined;
	const hasDocument = state.document !== null;
	const removal = canvasRemovalRequest(state);
	const blocked = state.status === "removed" || state.status === "empty";
	const unavailable =
		state.status === "disabled" ||
		state.status === "unavailable" ||
		state.status === "corrupt" ||
		state.status === "removed";
	let screenshotReason: string | null = null;
	let refreshReason: string | null = null;
	let removalReason: string | null = null;
	if (!options.ready) screenshotReason = "Canvas module is not ready.";
	else if (!hasSession) screenshotReason = "Select a chat session first.";
	else if (!hasDocument || blocked) screenshotReason = "No Canvas to capture yet.";
	else if (unavailable) screenshotReason = "Canvas preview is currently unavailable.";
	else if (options.pending) screenshotReason = "A refresh is already running.";
	if (!options.ready) refreshReason = "Canvas module is not ready.";
	else if (!hasSession) refreshReason = "Select a chat session first.";
	else if (options.pending) refreshReason = "A refresh is already running.";
	if (removal === null) {
		removalReason = blocked ? "Nothing to remove." : "Removal needs the current version.";
	}
	return {
		canScreenshot: screenshotReason === null,
		canRefresh: refreshReason === null,
		canRemove: removal !== null,
		screenshotReason,
		refreshReason,
		removalReason,
	};
}

export function canvasRemovalConfirmation(state: CanvasState): CanvasRemovalConfirmation | null {
	const request = canvasRemovalRequest(state);
	if (!request) return null;
	return {
		title: "Remove this Canvas?",
		message: `Remove Canvas version ${request.expectedVersion} (generation ${request.expectedGeneration})? This tombstones the document, revokes artifact access, and cannot be undone by a late render. A later Canvas starts a new generation.`,
		requiresConfirmation: true,
		request,
	};
}

export function canvasSidebarViewModel(
	state: CanvasState,
	options: { ready: boolean; pending: boolean },
): CanvasSidebarViewModel {
	const sessionId = state.scope?.sessionId ?? "no session";
	return {
		scopeLabel: `Session-scoped · session ${sessionId}`,
		version: canvasVersionDisplay(state),
		rendering: canvasRenderingDisplay(state),
		updated: canvasUpdateDisplay(state.document),
		availability: canvasControlAvailability(state, options),
		removal: canvasRemovalConfirmation(state),
		warningCount: state.warnings.length,
	};
}

export function canvasViewerViewModel(
	state: CanvasState,
	options: { ready: boolean; pending: boolean },
): CanvasViewerViewModel {
	const version = canvasVersionDisplay(state);
	const heading =
		version.currentVersion === null
			? "Canvas preview"
			: version.viewedVersion === null
				? `Canvas preview · version ${version.currentVersion}`
				: `Canvas preview · version ${version.viewedVersion}`;
	return {
		heading,
		version,
		rendering: canvasRenderingDisplay(state),
		updated: canvasUpdateDisplay(state.document),
		isStalePreview: state.preview.status === "stale",
		availability: canvasControlAvailability(state, options),
	};
}
