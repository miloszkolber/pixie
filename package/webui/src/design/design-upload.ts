import type { DesignDocument, DesignState } from "./design-model";
import { designRemovalRequest } from "./design-model";

/**
 * Instance-wide Design upload/register view-model (FIG-04).
 *
 * Upload uses streamed binary HTTP, not chat-WebSocket base64. These helpers
 * cover progress/cancel, format/limit errors, file name/size/date metadata,
 * and explicit removal confirmation. They are pure projections: the shell
 * owns the actual fetch/XHR wiring and cancellation.
 */

export const DESIGN_UPLOAD_LIMITS = {
	/** Initial source-upload budget from the roadmap (tested, not capacity). */
	maxBytes: 50 * 1024 * 1024,
	acceptedExtension: ".fig",
	acceptedLabel: "Figma Design (.fig)",
} as const;

export type DesignUploadPhase = "idle" | "ready" | "uploading" | "cancelling" | "failed" | "done";

export interface DesignUploadProgress {
	phase: DesignUploadPhase;
	fileName: string | null;
	fileSize: number | null;
	bytesSent: number;
	percent: number;
	cancellable: boolean;
	errorCode: string | null;
	error: string | null;
}

export interface DesignUploadValidation {
	ok: boolean;
	code: "invalid-format" | "too-large" | "empty" | null;
	message: string | null;
}

export interface DesignDocumentMetadata {
	name: string;
	sourceName: string;
	sizeLabel: string;
	dateLabel: string;
	countsLabel: string;
	scopeNotice: string;
}

export interface DesignRemovalConfirmation {
	title: string;
	message: string;
	copiesNotice: string;
	requiresConfirmation: true;
	request: { documentId: string; expectedGeneration: number; selectionRevision: number } | null;
}

export const EMPTY_DESIGN_UPLOAD: DesignUploadProgress = {
	phase: "idle",
	fileName: null,
	fileSize: null,
	bytesSent: 0,
	percent: 0,
	cancellable: false,
	errorCode: null,
	error: null,
};

export function validateDesignUploadFile(
	fileName: string | null | undefined,
	fileSize: number | null | undefined,
): DesignUploadValidation {
	const name = (fileName ?? "").trim();
	if (!name) {
		return { ok: false, code: "invalid-format", message: "Choose a local .fig file." };
	}
	if (!name.toLowerCase().endsWith(DESIGN_UPLOAD_LIMITS.acceptedExtension)) {
		return {
			ok: false,
			code: "invalid-format",
			message: `Unsupported format: expected ${DESIGN_UPLOAD_LIMITS.acceptedLabel}.`,
		};
	}
	if (typeof fileSize !== "number" || !Number.isFinite(fileSize)) {
		return { ok: false, code: "empty", message: "File size is unavailable." };
	}
	if (fileSize <= 0) {
		return { ok: false, code: "empty", message: "File is empty." };
	}
	if (fileSize > DESIGN_UPLOAD_LIMITS.maxBytes) {
		return {
			ok: false,
			code: "too-large",
			message: `File exceeds the ${formatDesignFileSize(DESIGN_UPLOAD_LIMITS.maxBytes)} limit.`,
		};
	}
	return { ok: true, code: null, message: null };
}

export function designUploadPercent(bytesSent: number, bytesTotal: number | null): number {
	if (!bytesTotal || bytesTotal <= 0) return 0;
	const percent = Math.round((Math.min(Math.max(bytesSent, 0), bytesTotal) / bytesTotal) * 100);
	return Math.min(Math.max(percent, 0), 100);
}

export function startDesignUpload(
	fileName: string,
	fileSize: number,
): DesignUploadProgress | { failure: DesignUploadValidation } {
	const validation = validateDesignUploadFile(fileName, fileSize);
	if (!validation.ok) return { failure: validation };
	return {
		phase: "uploading",
		fileName,
		fileSize,
		bytesSent: 0,
		percent: 0,
		cancellable: true,
		errorCode: null,
		error: null,
	};
}

export function advanceDesignUpload(
	progress: DesignUploadProgress,
	bytesSent: number,
): DesignUploadProgress {
	if (progress.phase !== "uploading") return progress;
	const total = progress.fileSize ?? 0;
	return {
		...progress,
		bytesSent: Math.min(Math.max(bytesSent, 0), total),
		percent: designUploadPercent(bytesSent, progress.fileSize),
	};
}

export function cancelDesignUpload(progress: DesignUploadProgress): DesignUploadProgress {
	if (progress.phase !== "uploading") return progress;
	return { ...progress, phase: "cancelling", cancellable: true };
}

export function failDesignUpload(
	progress: DesignUploadProgress,
	code: string,
	message: string,
): DesignUploadProgress {
	return { ...progress, phase: "failed", cancellable: false, errorCode: code, error: message };
}

export function formatDesignFileSize(bytes: number | null): string {
	if (bytes === null || !Number.isFinite(bytes) || bytes < 0) return "—";
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

export function formatDesignUploadDate(iso: string | null): string {
	if (!iso) return "—";
	return iso;
}

export function designDocumentMetadata(
	document: DesignDocument | null,
): DesignDocumentMetadata | null {
	if (!document) return null;
	return {
		name: document.name,
		sourceName: document.sourceName ?? document.name,
		sizeLabel: formatDesignFileSize(document.sourceBytes),
		dateLabel: formatDesignUploadDate(document.uploadedAt),
		countsLabel: `${document.pageCount} pages · ${document.nodeCount} nodes`,
		scopeNotice:
			"Single instance-wide slot: every authorized Design reader can inspect this document.",
	};
}

/** Explicit removal confirmation. Always requires confirmation when a source exists. */
export function designRemovalConfirmation(state: DesignState): DesignRemovalConfirmation {
	return {
		title: "Remove this Design document?",
		message:
			"Removal tombstones the current generation, revokes access immediately, and deletes the retained source. It stays available while Design is disabled and never restores via a late upload.",
		copiesNotice:
			"User removal cannot erase copies already downloaded or recorded in native transcripts.",
		requiresConfirmation: true,
		request: designRemovalRequest(state),
	};
}

export function designUploadStatusLabel(progress: DesignUploadProgress): string {
	if (progress.phase === "uploading") return `Uploading ${progress.percent}%`;
	if (progress.phase === "cancelling") return "Cancelling upload…";
	if (progress.phase === "failed") return progress.error ?? "Upload failed.";
	if (progress.phase === "done") return "Upload complete.";
	if (progress.phase === "ready") return "File ready to upload.";
	return "No upload in progress.";
}
