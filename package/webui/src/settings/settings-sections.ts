import type { Component } from "svelte";
import type { SettingsSection } from "./state";

/**
 * Lazy loaders for the primary-area Settings sections. Both the project work
 * area and the standalone settings surface use this map so a section is loaded
 * through the same owner and bundle slice regardless of which one is mounted.
 */
export const SETTINGS_SECTION_LOADERS: Partial<
	Record<SettingsSection, () => Promise<{ default: Component<any> }>>
> = {
	pi: () => import("./sections/pi-settings.svelte"),
	tools: () => import("./sections/pi-tools-settings.svelte"),
	extensions: () => import("./sections/extensions-settings.svelte"),
	models: () => import("./sections/models-settings.svelte"),
	providers: () => import("./sections/providers-settings.svelte"),
	system: () => import("./sections/system-settings.svelte"),
	schedules: () => import("./sections/schedules-section.svelte"),
};
