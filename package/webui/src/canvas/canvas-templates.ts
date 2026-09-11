import { CANVAS_CONTRIBUTION } from "./canvas-contribution";

/**
 * Owned Canvas test templates that demonstrate Mewa-based drafting.
 *
 * These IDs match the controller's built-in templates (blank, basic,
 * mewa-basic, mewa-card). Only the two Mewa-owned entries are exposed here;
 * the UI never renders draft HTML itself — previews stay raster-first via
 * {@link EMPTY_CANVAS_PREVIEW}-style metadata and cookie-authenticated
 * artifact URLs. This module carries template identity and Mewa-component
 * documentation only, never executable markup.
 */

export type OwnedCanvasTemplateId = "mewa-basic" | "mewa-card";

export interface CanvasTemplate {
	id: OwnedCanvasTemplateId;
	label: string;
	description: string;
	/** Pinned Mewa components the drafted page exercises. */
	mewaComponents: readonly string[];
	/** Controller template argument used by canvas_create. */
	templateId: OwnedCanvasTemplateId;
}

export const CANVAS_TEMPLATES: readonly CanvasTemplate[] = [
	{
		id: "mewa-basic",
		label: "Mewa basic page",
		description: "Single-column Mewa layout with heading, text, and one action.",
		mewaComponents: ["Layout", "Typography", "Button"],
		templateId: "mewa-basic",
	},
	{
		id: "mewa-card",
		label: "Mewa card",
		description: "Standalone Mewa card with heading, text, and one action.",
		mewaComponents: ["Card", "Typography", "Button"],
		templateId: "mewa-card",
	},
] as const;

export function canvasTemplateById(id: string | null | undefined): CanvasTemplate | null {
	if (id === null || id === undefined) return null;
	return CANVAS_TEMPLATES.find((template) => template.id === id) ?? null;
}

export function isOwnedCanvasTemplate(id: string | null | undefined): id is OwnedCanvasTemplateId {
	return canvasTemplateById(id) !== null;
}

/** Labels for a template picker. Session scope is inherited from the contribution. */
export function canvasTemplateOptions(): Array<{ value: OwnedCanvasTemplateId; label: string }> {
	return CANVAS_TEMPLATES.map((template) => ({
		value: template.id,
		label: `${template.label} (${template.id})`,
	}));
}

/** Build validated canvas_create args without embedding draft HTML. */
export function canvasCreateArgs(
	templateId: string | null | undefined,
): { templateId?: OwnedCanvasTemplateId } {
	const template = canvasTemplateById(templateId);
	if (!template) return {};
	return { templateId: template.templateId };
}

/** Human-readable summary used by slot 5 template cards. */
export function canvasTemplateSummary(template: CanvasTemplate): string {
	return `${template.label} · ${template.mewaComponents.join(" + ")} · session-scoped (${CANVAS_CONTRIBUTION.scope})`;
}
