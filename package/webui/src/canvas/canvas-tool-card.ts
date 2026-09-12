import { type CanvasArtifactScope, canvasArtifactUrl } from "./canvas-artifact-url";
import type { CanvasPreviewMetadata } from "./canvas-model";

export type CanvasToolCardStatus = "running" | "done" | "error";
export type CanvasToolCardOutcome = "running" | "completed" | "stale" | "failed" | "unavailable";

export interface CanvasToolCardInput {
	toolName?: string;
	args?: Record<string, unknown>;
	result?: unknown;
	status?: CanvasToolCardStatus;
	artifactBaseUrl?: string | URL;
	/** Controller project/session scope for cookie-authenticated artifacts. */
	artifactScope?: CanvasArtifactScope;
}

export interface CanvasToolCardViewModel {
	operation: string;
	label: string;
	outcome: CanvasToolCardOutcome;
	version: number | null;
	currentVersion: number | null;
	canvasId: string | null;
	summary: string;
	message: string;
	warnings: string[];
	preview: CanvasPreviewMetadata | null;
}

function objectRecord(value: unknown): Record<string, unknown> | null {
	return value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: null;
}

function textBlock(value: unknown): string | null {
	const block = objectRecord(value);
	if (block?.type !== "text" || typeof block.text !== "string") return null;
	return block.text;
}

function resultRecord(value: unknown): Record<string, unknown> | null {
	const direct = objectRecord(value);
	if (direct?.outcome || direct?.code || direct?.canvasId || direct?.version || direct?.artifact)
		return direct;
	const structured = direct ? objectRecord(direct.structuredContent) : null;
	if (structured) return structured;
	const content = direct?.content;
	if (Array.isArray(content)) {
		for (const block of content) {
			const text = textBlock(block);
			if (!text || text.length > 128 * 1024) continue;
			try {
				const parsed = objectRecord(JSON.parse(text));
				if (parsed) return parsed;
			} catch {
				// Plain tool text is not a structured Canvas result.
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
	const name = stringValue(toolName) ?? "canvas";
	return name.replace(/^(?:pixie_canvas__|canvas_)/, "") || "canvas";
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
	scope: CanvasArtifactScope | undefined,
): CanvasPreviewMetadata | null {
	if (!result || typeof result.artifact !== "string") return null;
	const url = scope
		? canvasArtifactUrl(result.artifact, scope, baseUrl)
		: canvasArtifactUrl(result.artifact, baseUrl);
	const version = numberValue(result.version);
	const canvasId = stringValue(result.canvasId);
	return {
		kind: "raster",
		status: url !== null && result.mime === "image/png" ? "ready" : "unavailable",
		canvasId,
		generation: numberValue(result.generation),
		version,
		url,
		artifactUrl: url,
		mime: url !== null && result.mime === "image/png" ? "image/png" : null,
		width: numberValue(result.width),
		height: numberValue(result.height),
		bytes: numberValue(result.bytes),
		alt: version === null ? "Canvas raster preview" : `Canvas raster preview, version ${version}`,
		reason: url !== null && result.mime === "image/png" ? null : "Raster preview unavailable.",
	};
}

/** Build the one-line operation summary used by compact Canvas tool cards. */
export function canvasToolCardSummary(
	toolName: string | undefined,
	args: Record<string, unknown> = {},
	result?: unknown,
): string {
	const operation = operationName(toolName);
	const payload = resultRecord(result);
	const version =
		numberValue(payload?.version) ?? numberValue(args.version) ?? numberValue(args.expectedVersion);
	const suffix = version === null ? "" : ` · version ${version}`;
	return `${operation}${suffix}`;
}

/** Parse a bounded MCP result into data suitable for a compact, non-executing card. */
export function canvasToolCardViewModel(input: CanvasToolCardInput): CanvasToolCardViewModel {
	const operation = operationName(input.toolName);
	const args = input.args ?? {};
	const payload = resultRecord(input.result);
	const running = input.status === "running";
	const code = stringValue(payload?.code);
	const outcome = running
		? "running"
		: code === "conflict" || code === "stale_version" || code === "generation_revoked"
			? "stale"
			: input.status === "error" || payload?.outcome === "rejected" || payload?.outcome === "failed"
				? code === "disabled" || code === "unavailable"
					? "unavailable"
					: "failed"
				: "completed";
	const version =
		numberValue(payload?.version) ?? numberValue(args.version) ?? numberValue(args.expectedVersion);
	const currentVersion =
		numberValue(payload?.currentVersion) ?? numberValue(payload?.latestVersion);
	const canvasId = stringValue(payload?.canvasId) ?? stringValue(args.canvasId);
	const preview = previewFromResult(payload, input.artifactBaseUrl, input.artifactScope);
	const summary = canvasToolCardSummary(input.toolName, args, payload);
	const message =
		outcome === "running"
			? "Rendering Canvas raster preview…"
			: outcome === "stale"
				? "Canvas version is stale. Refresh before retrying."
				: outcome === "unavailable"
					? "Canvas preview unavailable."
					: outcome === "failed"
						? "Canvas operation failed."
						: payload?.outcome === "screenshot"
							? "Raster preview ready."
							: "Canvas operation completed.";
	return {
		operation,
		label: operation === "canvas" ? "Canvas" : `Canvas · ${operation}`,
		outcome,
		version,
		currentVersion,
		canvasId,
		summary,
		message,
		warnings: warnings(payload?.warnings),
		preview,
	};
}

export const canvasCardViewModel = canvasToolCardViewModel;
export const compactCanvasToolCard = canvasToolCardViewModel;
