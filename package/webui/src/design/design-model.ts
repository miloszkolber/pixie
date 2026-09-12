/** Browser-side Openfig/Design projections. Design is instance-wide. */

import { designArtifactUrl } from "./design-artifact-url";

export type DesignAvailability =
	| "disabled"
	| "empty"
	| "uploading"
	| "ready"
	| "unavailable"
	| "corrupt"
	| "removed"
	| "stale";

export type DesignPreviewKind = "cover" | "frame";
export type DesignPreviewStatus = "idle" | "pending" | "ready" | "unavailable" | "stale" | "error";

export interface DesignScope {
	scope: "instance";
}

export interface DesignDocument {
	id: string;
	generation: number;
	name: string;
	sourceName: string | null;
	uploadedAt: string | null;
	sourceBytes: number | null;
	pageCount: number;
	nodeCount: number;
	coverAvailable: boolean;
	coverArtifact: string | null;
}

export interface DesignPage {
	id: string;
	name: string;
	position: number;
	nodeCount: number;
	internal?: boolean;
}

export interface DesignNode {
	id: string;
	pageId: string;
	parentId: string | null;
	position: number;
	depth: number;
	type: string;
	name: string;
	visible: boolean;
	x: number | null;
	y: number | null;
	width: number | null;
	height: number | null;
	rotation: number | null;
	text: string | null;
	children: string[];
	style: Record<string, string>;
	overrides: Record<string, string>;
	/** Set by the normalized index; a FRAME type alone is not enough. */
	frameCandidate?: boolean;
}

export type DesignFrameCandidate = DesignNode & { frameCandidate: true };

export interface DesignSelection {
	documentId: string;
	generation: number;
	pageId: string | null;
	nodeId: string | null;
}

export interface SharedDesignSelection extends DesignSelection {
	revision: number;
}

/** Private inspection never changes the selection visible to agents. */
export interface DesignFocusState {
	scope: "instance";
	privateFocus: DesignSelection | null;
	sharedFocus: SharedDesignSelection | null;
	sharedRevision: number;
}

export interface DesignPreviewMetadata {
	kind: DesignPreviewKind;
	status: DesignPreviewStatus;
	documentId: string | null;
	generation: number | null;
	nodeId: string | null;
	url: string | null;
	/** Alias used by API adapters; identical to url and never contains a token. */
	artifactUrl?: string | null;
	mime: "image/png" | null;
	width: number | null;
	height: number | null;
	bytes: number | null;
	label: string;
	reason: string | null;
}

export interface DesignWarning {
	code?: string;
	message: string;
}

export interface DesignStatusResponse {
	outcome?: string;
	enabled?: boolean;
	availability?: string;
	documentId?: string;
	generation?: number;
	selectionRevision?: number;
	name?: string;
	sourceName?: string;
	sourceBytes?: number;
	uploadedAt?: string;
	pageCount?: number;
	nodeCount?: number;
	coverAvailable?: boolean;
	coverArtifact?: string;
	warnings?: Array<string | DesignWarning>;
	selection?: {
		documentId?: string;
		generation?: number;
		pageId?: string;
		nodeId?: string;
		revision?: number;
	};
}

export interface DesignPreviewResult {
	outcome?: string;
	documentId: string;
	generation: number;
	selectionRevision: number;
	previewKind: string;
	nodeId?: string;
	mime?: string;
	width?: number;
	height?: number;
	bytes?: number;
	artifact?: string;
	warnings?: string[];
}

export interface DesignStatus {
	scope: "instance";
	enabled: boolean;
	availability: DesignAvailability;
	documentId: string | null;
	generation: number | null;
	selectionRevision: number;
	focus: DesignFocusState;
	cover: DesignPreviewMetadata;
	framePreviewAvailable: false;
	warnings: DesignWarning[];
}

export interface DesignState {
	scope: DesignScope;
	availability: DesignAvailability;
	enabled: boolean;
	document: DesignDocument | null;
	selectionRevision: number;
	focus: DesignFocusState;
	pages: DesignPage[];
	nodes: DesignNode[];
	preview: DesignPreviewMetadata;
	warnings: DesignWarning[];
	error: DesignWarning | null;
}

export const EMPTY_DESIGN_PREVIEW: DesignPreviewMetadata = {
	kind: "cover",
	status: "idle",
	documentId: null,
	generation: null,
	nodeId: null,
	url: null,
	artifactUrl: null,
	mime: null,
	width: null,
	height: null,
	bytes: null,
	label: "Document thumbnail",
	reason: null,
};

export function emptyDesignFocus(): DesignFocusState {
	return { scope: "instance", privateFocus: null, sharedFocus: null, sharedRevision: 0 };
}

export function emptyDesignState(): DesignState {
	return {
		scope: { scope: "instance" },
		availability: "empty",
		enabled: false,
		document: null,
		selectionRevision: 0,
		focus: emptyDesignFocus(),
		pages: [],
		nodes: [],
		preview: { ...EMPTY_DESIGN_PREVIEW },
		warnings: [],
		error: null,
	};
}

export function createDesignState(): DesignState {
	return emptyDesignState();
}

export function designStatusFromState(state: DesignState): DesignStatus {
	return {
		scope: "instance",
		enabled: state.enabled,
		availability: state.availability,
		documentId: state.document?.id ?? null,
		generation: state.document?.generation ?? null,
		selectionRevision: state.selectionRevision,
		focus: state.focus,
		cover: state.preview.kind === "cover" ? state.preview : { ...EMPTY_DESIGN_PREVIEW },
		framePreviewAvailable: false,
		warnings: state.warnings,
	};
}

export const designStatus = designStatusFromState;

function designAvailability(value: unknown, enabled: boolean | undefined): DesignAvailability {
	if (
		value === "disabled" ||
		value === "empty" ||
		value === "uploading" ||
		value === "ready" ||
		value === "unavailable" ||
		value === "corrupt" ||
		value === "removed" ||
		value === "stale"
	) {
		return value;
	}
	if (enabled === false) return "disabled";
	return "empty";
}

function warningList(values: DesignStatusResponse["warnings"]): DesignWarning[] {
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

function selectionFromStatus(
	response: DesignStatusResponse,
	documentId: string,
	generation: number,
): SharedDesignSelection | null {
	const selection = response.selection;
	const revision = selection?.revision ?? response.selectionRevision ?? 0;
	if (revision < 1 || !selection) return null;
	return {
		documentId: selection.documentId ?? documentId,
		generation: selection.generation ?? generation,
		pageId: selection.pageId ?? null,
		nodeId: selection.nodeId ?? null,
		revision,
	};
}

/** Normalize the instance-wide status payload and keep private focus local. */
export function designStateFromStatus(
	state: DesignState,
	response: DesignStatusResponse,
	coverUrl?: string | null,
): DesignState {
	const documentId = response.documentId ?? response.selection?.documentId ?? null;
	const generation = response.generation ?? response.selection?.generation ?? null;
	const oldDocument = state.document;
	const replaced =
		documentId !== oldDocument?.id ||
		(generation !== null && oldDocument !== null && generation !== oldDocument.generation);
	const document =
		documentId !== null && generation !== null
			? {
					id: documentId,
					generation,
					name: response.name ?? response.sourceName ?? "Untitled Design",
					sourceName: response.sourceName ?? null,
					uploadedAt: response.uploadedAt ?? null,
					sourceBytes: response.sourceBytes ?? null,
					pageCount: response.pageCount ?? 0,
					nodeCount: response.nodeCount ?? 0,
					coverAvailable: response.coverAvailable === true,
					coverArtifact: response.coverArtifact ?? null,
				}
			: null;
	const availability = designAvailability(response.availability, response.enabled);
	const sharedFocus = document
		? selectionFromStatus(response, document.id, document.generation)
		: null;
	const selectionRevision = response.selectionRevision ?? sharedFocus?.revision ?? 0;
	const preview = document
		? coverPreviewMetadata(
				document,
				coverUrl ?? (response.coverArtifact ? designArtifactUrl(response.coverArtifact) : null),
				availability,
			)
		: { ...EMPTY_DESIGN_PREVIEW };
	return {
		...state,
		scope: { scope: "instance" },
		availability,
		enabled: response.enabled ?? state.enabled,
		document,
		selectionRevision,
		focus: {
			scope: "instance",
			privateFocus: replaced ? null : state.focus.privateFocus,
			sharedFocus,
			sharedRevision: selectionRevision,
		},
		pages: replaced ? [] : state.pages,
		nodes: replaced ? [] : state.nodes,
		preview,
		warnings: warningList(response.warnings),
		error: null,
	};
}

export function coverPreviewMetadata(
	document: Pick<DesignDocument, "id" | "generation" | "coverAvailable"> & {
		coverArtifact?: string | null;
	},
	coverUrl: string | null,
	availability: DesignAvailability = "ready",
): DesignPreviewMetadata {
	const safeCoverUrl = coverUrl === null ? null : designArtifactUrl(coverUrl);
	const available = availability === "ready" && document.coverAvailable && safeCoverUrl !== null;
	return {
		kind: "cover",
		status: available ? "ready" : "unavailable",
		documentId: document.id,
		generation: document.generation,
		nodeId: null,
		url: available ? safeCoverUrl : null,
		artifactUrl: available ? safeCoverUrl : null,
		mime: available ? "image/png" : null,
		width: null,
		height: null,
		bytes: null,
		label: "Document thumbnail",
		reason: available ? null : "Document thumbnail unavailable.",
	};
}

/** Frame preview is explicit; a cover is never substituted for a frame. */
export function framePreviewUnavailable(
	documentId: string | null,
	generation: number | null,
	nodeId: string | null = null,
): DesignPreviewMetadata {
	return {
		kind: "frame",
		status: "unavailable",
		documentId,
		generation,
		nodeId,
		url: null,
		artifactUrl: null,
		mime: null,
		width: null,
		height: null,
		bytes: null,
		label: "Frame preview unavailable",
		reason: "Frame preview unavailable until a supported renderer is available.",
	};
}

export function isFramePreviewUnavailable(preview: DesignPreviewMetadata): boolean {
	return preview.kind === "frame" && preview.status === "unavailable";
}

export function designPreviewFromResult(
	result: DesignPreviewResult,
	artifactUrl: string | null,
): DesignPreviewMetadata {
	const kind: DesignPreviewKind = result.previewKind === "frame" ? "frame" : "cover";
	const safeArtifactUrl = artifactUrl === null ? null : designArtifactUrl(artifactUrl);
	if (kind === "frame" && safeArtifactUrl === null) {
		return framePreviewUnavailable(result.documentId, result.generation, result.nodeId ?? null);
	}
	const available = safeArtifactUrl !== null && result.mime === "image/png";
	return {
		kind,
		status: available ? "ready" : "unavailable",
		documentId: result.documentId,
		generation: result.generation,
		nodeId: result.nodeId ?? null,
		url: available ? safeArtifactUrl : null,
		artifactUrl: available ? safeArtifactUrl : null,
		mime: available ? "image/png" : null,
		width: typeof result.width === "number" ? result.width : null,
		height: typeof result.height === "number" ? result.height : null,
		bytes: typeof result.bytes === "number" ? result.bytes : null,
		label: kind === "cover" ? "Document thumbnail" : "Frame preview",
		reason: available
			? null
			: kind === "cover"
				? "Document thumbnail unavailable."
				: "Frame preview unavailable.",
	};
}

export function setPrivateDesignFocus(
	state: DesignState,
	focus: DesignSelection | null,
): DesignState {
	return { ...state, focus: { ...state.focus, privateFocus: focus, scope: "instance" } };
}

export interface SharedFocusRequest {
	documentId: string;
	expectedGeneration: number;
	expectedRevision: number;
	pageId?: string;
	nodeId?: string;
}

export interface DesignRemovalRequest {
	documentId: string;
	expectedGeneration: number;
	selectionRevision: number;
}

/** Removal remains available for a retained source even while Design is disabled. */
export function designRemovalRequest(state: DesignState): DesignRemovalRequest | null {
	if (!state.document || state.availability === "removed" || state.availability === "empty")
		return null;
	return {
		documentId: state.document.id,
		expectedGeneration: state.document.generation,
		selectionRevision: state.selectionRevision,
	};
}

export interface OptimisticSharedFocus {
	state: DesignState;
	request: SharedFocusRequest;
}

/** Prepare the explicit shared-focus mutation with an optimistic revision. */
export function setSharedDesignFocusOptimistic(
	state: DesignState,
	focus: {
		documentId?: string;
		generation?: number;
		pageId?: string | null;
		nodeId?: string | null;
	},
): OptimisticSharedFocus | null {
	if (!state.document) return null;
	const documentId = focus.documentId ?? state.document.id;
	const generation = focus.generation ?? state.document.generation;
	if (documentId !== state.document.id || generation !== state.document.generation) return null;
	const expectedRevision = state.selectionRevision;
	const revision = expectedRevision + 1;
	const sharedFocus: SharedDesignSelection = {
		documentId,
		generation,
		pageId: focus.pageId ?? null,
		nodeId: focus.nodeId ?? null,
		revision,
	};
	return {
		state: {
			...state,
			selectionRevision: revision,
			focus: { ...state.focus, sharedFocus, sharedRevision: revision, scope: "instance" },
		},
		request: {
			documentId,
			expectedGeneration: generation,
			expectedRevision,
			...(sharedFocus.pageId ? { pageId: sharedFocus.pageId } : {}),
			...(sharedFocus.nodeId ? { nodeId: sharedFocus.nodeId } : {}),
		},
	};
}

export function applySharedDesignFocus(
	state: DesignState,
	focus: SharedDesignSelection | null,
): DesignState {
	if (focus && state.document && focus.documentId !== state.document.id) return state;
	if (focus && state.document && focus.generation !== state.document.generation) return state;
	const revision = focus?.revision ?? state.selectionRevision;
	return {
		...state,
		selectionRevision: revision,
		focus: { ...state.focus, sharedFocus: focus, sharedRevision: revision, scope: "instance" },
	};
}

export function isDesignFrameCandidate(node: DesignNode): node is DesignFrameCandidate {
	return node.frameCandidate === true;
}

export function designFrameCandidates(nodes: readonly DesignNode[]): DesignFrameCandidate[] {
	return nodes.filter(isDesignFrameCandidate);
}

export const frameCandidates = designFrameCandidates;
