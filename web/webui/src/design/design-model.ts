/** Browser-side Openfig/Design status projection. Design is instance-wide. */

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

export interface DesignPreviewMetadata {
	kind: "cover";
	status: "idle" | "ready" | "unavailable";
	documentId: string | null;
	generation: number | null;
	url: string | null;
	artifactUrl?: string | null;
	mime: "image/png" | null;
	label: string;
	reason: string | null;
}

export interface DesignWarning {
	code?: string;
	message: string;
}

export interface DesignStatusResponse {
	enabled?: boolean;
	availability?: string;
	documentId?: string;
	generation?: number;
	name?: string;
	sourceName?: string;
	sourceBytes?: number;
	uploadedAt?: string;
	pageCount?: number;
	nodeCount?: number;
	coverAvailable?: boolean;
	coverArtifact?: string;
	warnings?: Array<string | DesignWarning>;
}

export interface DesignState {
	availability: DesignAvailability;
	enabled: boolean;
	document: DesignDocument | null;
	preview: DesignPreviewMetadata;
	warnings: DesignWarning[];
}

export const EMPTY_DESIGN_PREVIEW: DesignPreviewMetadata = {
	kind: "cover",
	status: "idle",
	documentId: null,
	generation: null,
	url: null,
	artifactUrl: null,
	mime: null,
	label: "Document thumbnail",
	reason: null,
};

export function emptyDesignState(): DesignState {
	return {
		availability: "empty",
		enabled: false,
		document: null,
		preview: { ...EMPTY_DESIGN_PREVIEW },
		warnings: [],
	};
}

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

/** Normalize the instance-wide status payload. */
export function designStateFromStatus(
	state: DesignState,
	response: DesignStatusResponse,
): DesignState {
	const document =
		response.documentId !== undefined && response.generation !== undefined
			? {
					id: response.documentId,
					generation: response.generation,
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
	return {
		...state,
		availability,
		enabled: response.enabled ?? state.enabled,
		document,
		preview: document
			? coverPreviewMetadata(document, response.coverArtifact ?? null, availability)
			: { ...EMPTY_DESIGN_PREVIEW },
		warnings: warningList(response.warnings),
	};
}

function coverPreviewMetadata(
	document: Pick<DesignDocument, "id" | "generation" | "coverAvailable">,
	coverArtifact: string | null,
	availability: DesignAvailability,
): DesignPreviewMetadata {
	const safeCoverURL = coverArtifact === null ? null : designArtifactUrl(coverArtifact);
	const available = availability === "ready" && document.coverAvailable && safeCoverURL !== null;
	return {
		kind: "cover",
		status: available ? "ready" : "unavailable",
		documentId: document.id,
		generation: document.generation,
		url: available ? safeCoverURL : null,
		artifactUrl: available ? safeCoverURL : null,
		mime: available ? "image/png" : null,
		label: "Document thumbnail",
		reason: available ? null : "Document thumbnail unavailable.",
	};
}
