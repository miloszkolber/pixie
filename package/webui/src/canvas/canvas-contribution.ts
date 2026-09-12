/** Shell-neutral descriptor consumed when the Canvas module is registered. */
export const CANVAS_CONTRIBUTION = {
	id: "canvas",
	connection: "pixie-canvas",
	scope: "session",
	railLabel: "Canvas",
	secondaryViewSlot: 4,
	secondarySidebarSlot: 5,
	preview: "raster",
	executableHtml: false,
} as const;

export type CanvasContribution = typeof CANVAS_CONTRIBUTION;
export const canvasContribution = CANVAS_CONTRIBUTION;
