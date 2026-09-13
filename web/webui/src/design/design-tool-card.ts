import { designArtifactUrl } from "./design-artifact-url";
import {
	type DesignPreviewMetadata,
	designPreviewFromResult,
	framePreviewUnavailable,
} from "./design-model";

export type DesignToolCardStatus = "running" | "done" | "error";
export type DesignToolCardOutcome = "running" | "completed" | "stale" | "failed" | "unavailable";

export interface DesignToolCardInput {
	toolName?: string;
	args?: Record<string, unknown>;
	result?: unknown;
	status?: DesignToolCardStatus;
	artifactBaseUrl?: string | URL;
}

export interface DesignToolCardViewModel {
	operation: string;
	label: string;
	outcome: DesignToolCardOutcome;
	documentId: string | null;
	selectionRevision: number | null;
	summary: string;
	message: string;
	warnings: string[];
	preview: DesignPreviewMetadata | null;
	instanceWide: true;
}

function objectRecord(value: unknown): Record<string, unknown> | null {
	return value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

function resultRecord(value: unknown): Record<string, unknown> | null {
	const direct = objectRecord(value);
	if (
		direct?.outcome ||
		direct?.code ||
		direct?.documentId ||
		direct?.previewKind ||
		direct?.availability
	)
		return direct;
	const structured = direct ? objectRecord(direct.structuredContent) : null;
	if (structured) return structured;
	const content = direct?.content;
	if (Array.isArray(content)) {
		for (const block of content) {
			const text = objectRecord(block)?.text;
			if (typeof text !== "string" || text.length > 128 * 1024) continue;
			try {
				const parsed = objectRecord(JSON.parse(text));
				if (parsed) return parsed;
			} catch {
				// Plain Design tool text is not a structured result.
			}
		}
	}
	return null;
}

function stringValue(value: unknown): string | null {
	return typeof value === "string" && value.trim() ? value : null;
}

function numberValue(value: unknown): number | null {
	return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function operationName(toolName: string | undefined): string {
	const name = stringValue(toolName) ?? "design";
	return name.replace(/^(?:pixie_design__|design_)/, "") || "design";
}

function warnings(value: unknown): string[] {
	if (!Array.isArray(value)) return [];
	return value
		.filter((entry): entry is string => typeof entry === "string" && entry.trim().length > 0)
		.slice(0, 8);
}

function previewFromResult(
	result: Record<string, unknown> | null,
	baseUrl: string | URL | undefined,
): DesignPreviewMetadata | null {
	if (!result || typeof result.previewKind !== "string") return null;
	const documentId = stringValue(result.documentId);
	const generation = numberValue(result.generation);
	const kind = result.previewKind === "frame" ? "frame" : "cover";
	const artifact = stringValue(result.artifact);
	const url = artifact ? designArtifactUrl(artifact, baseUrl) : null;
	if (kind === "frame" && url === null) return framePreviewUnavailable(documentId, generation);
	return designPreviewFromResult(
		{
			documentId: documentId ?? "",
			generation: generation ?? 0,
			selectionRevision: numberValue(result.selectionRevision) ?? 0,
			previewKind: kind,
			...(typeof result.nodeId === "string" ? { nodeId: result.nodeId } : {}),
			...(typeof result.mime === "string" ? { mime: result.mime } : {}),
			...(typeof result.width === "number" ? { width: result.width } : {}),
			...(typeof result.height === "number" ? { height: result.height } : {}),
			...(typeof result.bytes === "number" ? { bytes: result.bytes } : {}),
			...(artifact ? { artifact } : {}),
		},
		url,
	);
}

export function designToolCardSummary(
	toolName: string | undefined,
	args: Record<string, unknown> = {},
	result?: unknown,
): string {
	const operation = operationName(toolName);
	const payload = resultRecord(result);
	const documentId = stringValue(payload?.documentId) ?? stringValue(args.documentId);
	const revision = numberValue(payload?.selectionRevision) ?? numberValue(args.selectionRevision);
	const identity = documentId ? ` · ${documentId.slice(0, 12)}` : "";
	const version = revision === null ? "" : ` · focus ${revision}`;
	return `${operation}${identity}${version}`;
}

/** Keep Design cards compact and explicitly instance-wide. */
export function designToolCardViewModel(input: DesignToolCardInput): DesignToolCardViewModel {
	const operation = operationName(input.toolName);
	const args = input.args ?? {};
	const payload = resultRecord(input.result);
	const code = stringValue(payload?.code);
	const outcome =
		input.status === "running"
			? "running"
			: code === "stale_document" || code === "stale_selection" || code === "conflict"
				? "stale"
				: input.status === "error" ||
						payload?.outcome === "rejected" ||
						payload?.outcome === "failed"
					? code === "disabled" || code === "unavailable"
						? "unavailable"
						: "failed"
					: "completed";
	const documentId = stringValue(payload?.documentId) ?? stringValue(args.documentId);
	const selectionRevision =
		numberValue(payload?.selectionRevision) ?? numberValue(args.selectionRevision);
	const preview = previewFromResult(payload, input.artifactBaseUrl);
	const message =
		outcome === "running"
			? "Reading instance-wide Design…"
			: outcome === "stale"
				? "Design revision is stale. Refresh before retrying."
				: outcome === "unavailable"
					? "Design inspection unavailable."
					: outcome === "failed"
						? "Design operation failed."
						: preview?.kind === "frame" && preview.status === "unavailable"
							? "Frame preview unavailable; structure remains inspectable."
							: "Design operation completed.";
	return {
		operation,
		label: operation === "design" ? "Design" : `Design · ${operation}`,
		outcome,
		documentId,
		selectionRevision,
		summary: designToolCardSummary(input.toolName, args, payload),
		message,
		warnings: warnings(payload?.warnings),
		preview,
		instanceWide: true,
	};
}

export const designCardViewModel = designToolCardViewModel;
export const compactDesignToolCard = designToolCardViewModel;
