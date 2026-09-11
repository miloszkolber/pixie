import type { DesignNode, DesignPage, DesignState } from "./design-model";
import { designFrameCandidates, setSharedDesignFocusOptimistic } from "./design-model";

/**
 * Pages/layers/frame-candidate inspector plus the explicit shared-focus
 * publish control (FIG-04).
 *
 * Structure stays usable while frame rendering is unavailable. When the
 * document cannot be inspected, the model fails closed with diagnostics
 * instead of presenting an empty success.
 */

export interface DesignInspectorPageRow {
	id: string;
	name: string;
	position: number;
	nodeCount: number;
	internal: boolean;
	selected: boolean;
}

export interface DesignInspectorNodeRow {
	id: string;
	name: string;
	type: string;
	depth: number;
	visible: boolean;
	frameCandidate: boolean;
	hasText: boolean;
}

export interface DesignInspectorAvailability {
	available: boolean;
	reason: string | null;
	diagnostics: string[];
}

export interface DesignInspectorModel {
	availability: DesignInspectorAvailability;
	pages: DesignInspectorPageRow[];
	selectedPageId: string | null;
	layers: DesignInspectorNodeRow[];
	frameCandidates: DesignInspectorNodeRow[];
	pageCount: number;
	nodeCount: number;
	visibleNodeCount: number;
	sharedRevision: number;
}

export interface DesignSharedFocusPublish {
	label: string;
	hint: string;
	enabled: boolean;
	reason: string | null;
	request: {
		documentId: string;
		expectedGeneration: number;
		expectedRevision: number;
		pageId?: string;
		nodeId?: string;
	} | null;
	optimisticRevision: number | null;
}

function inspectorUnavailable(
	state: DesignState,
	diagnostics: string[],
): DesignInspectorAvailability {
	const reason =
		state.availability === "empty"
			? "No Design document is loaded."
			: state.availability === "disabled"
				? "Design is disabled."
				: state.availability === "unavailable"
					? "Design inspection is currently unavailable."
					: state.availability === "removed"
						? "The Design document was removed."
						: state.availability === "corrupt"
							? "The Design document failed validation."
							: state.availability === "uploading"
								? "The Design document is still uploading."
								: "Design structure is not available.";
	return { available: false, reason, diagnostics };
}

export function designInspectorAvailability(state: DesignState): DesignInspectorAvailability {
	if (!state.document) {
		return inspectorUnavailable(state, ["No committed Design source is present."]);
	}
	if (state.availability !== "ready" && state.availability !== "stale") {
		return inspectorUnavailable(state, [
			`Availability is ${state.availability}.`,
			"Structure is withheld until the committed source is readable again.",
		]);
	}
	return { available: true, reason: null, diagnostics: [] };
}

function toNodeRow(node: DesignNode): DesignInspectorNodeRow {
	return {
		id: node.id,
		name: node.name,
		type: node.type,
		depth: node.depth,
		visible: node.visible,
		frameCandidate: node.frameCandidate === true,
		hasText: typeof node.text === "string" && node.text.trim().length > 0,
	};
}

export function designInspectorModel(
	state: DesignState,
	selectedPageId?: string | null,
): DesignInspectorModel {
	const availability = designInspectorAvailability(state);
	const pages: DesignPage[] = availability.available ? [...state.pages] : [];
	pages.sort((a, b) => a.position - b.position);
	const fallbackPageId = pages[0]?.id ?? null;
	const resolvedPageId = selectedPageId ?? fallbackPageId;
	const layers = availability.available
		? state.nodes
				.filter((node) => (resolvedPageId ? node.pageId === resolvedPageId : true))
				.map(toNodeRow)
				.slice(0, 200)
		: [];
	const frameCandidates = availability.available
		? designFrameCandidates(state.nodes).map(toNodeRow).slice(0, 100)
		: [];
	return {
		availability,
		pages: pages.map((page) => ({
			id: page.id,
			name: page.name,
			position: page.position,
			nodeCount: page.nodeCount,
			internal: page.internal === true,
			selected: page.id === resolvedPageId,
		})),
		selectedPageId: resolvedPageId,
		layers,
		frameCandidates,
		pageCount: state.document?.pageCount ?? 0,
		nodeCount: state.document?.nodeCount ?? 0,
		visibleNodeCount: state.nodes.filter((node) => node.visible).length,
		sharedRevision: state.selectionRevision,
	};
}

/**
 * Explicit shared-focus publish control. Private browsing never changes
 * shared focus; only this action publishes a page and optional node/frame
 * with an optimistic selection revision.
 */
export function designSharedFocusPublish(
	state: DesignState,
	options: { pageId?: string | null; nodeId?: string | null } = {},
): DesignSharedFocusPublish {
	const base = {
		label: "Publish shared focus",
		hint: "Publishes the selected page and optional node as shared focus with an optimistic revision. Private browsing never changes shared focus on its own.",
	} as const;
	if (!state.document) {
		return { ...base, enabled: false, reason: "No Design document is loaded.", request: null, optimisticRevision: null };
	}
	if (state.availability !== "ready" && state.availability !== "stale") {
		return {
			...base,
			enabled: false,
			reason: "Design inspection is currently unavailable.",
			request: null,
			optimisticRevision: null,
		};
	}
	const optimistic = setSharedDesignFocusOptimistic(state, {
		pageId: options.pageId ?? null,
		nodeId: options.nodeId ?? null,
	});
	if (!optimistic) {
		return {
			...base,
			enabled: false,
			reason: "Shared focus must name the current Design document.",
			request: null,
			optimisticRevision: null,
		};
	}
	return {
		...base,
		enabled: true,
		reason: null,
		request: optimistic.request,
		optimisticRevision: optimistic.state.selectionRevision,
	};
}

export function designInspectorEmptyLabel(model: DesignInspectorModel): string {
	if (!model.availability.available) return model.availability.reason ?? "Unavailable.";
	if (model.pages.length === 0) return "No pages in this Design document.";
	if (model.layers.length === 0) return "No layers on the selected page.";
	return `${model.layers.length} layers`;
}
