/** Shell-neutral descriptor consumed when the instance-wide Design module is registered. */
export const DESIGN_CONTRIBUTION = {
	id: "design",
	connection: "pixie-design",
	scope: "instance",
	railLabel: "Design",
	secondaryViewSlot: 4,
	secondarySidebarSlot: 5,
	coverLabel: "Document thumbnail",
	framePreviewAvailable: false,
	privateFocusChangesSharedFocus: false,
	agentReadsAreReadOnly: true,
} as const;

export type DesignContribution = typeof DESIGN_CONTRIBUTION;
export const designContribution = DESIGN_CONTRIBUTION;
