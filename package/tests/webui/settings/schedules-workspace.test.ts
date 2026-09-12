import { expect, test } from "bun:test";
import { resolveWorkspaceSettingsSection } from "@/schedules/schedules-workspace";
import { resolveSettingsSection, settingsTabs } from "@/settings/settings-dialog";
import { SettingsSection } from "@/settings/state";

test("workspace settings selection reuses the dialog section inventory", () => {
	for (const tabs of [settingsTabs(), settingsTabs(true), settingsTabs(false, true)]) {
		expect(tabs.some((tab) => tab.label === "Schedules")).toBe(true);
	}
	expect(resolveSettingsSection(SettingsSection.Schedules, null)).toBe(SettingsSection.Schedules);
	expect(resolveWorkspaceSettingsSection({ kind: "settings", sectionId: "schedules" }, "system")).toBe(
		"schedules",
	);
	expect(
		resolveWorkspaceSettingsSection({ kind: "settings", sectionId: "providers" }, "system"),
	).toBe("providers");
	expect(resolveWorkspaceSettingsSection(null, SettingsSection.System)).toBe("system");
	// A stale schedule selection never invents a settings section.
	expect(
		resolveWorkspaceSettingsSection(
			{ kind: "schedule", scheduleId: "schedule-1", projectId: "project-1" },
			SettingsSection.Providers,
		),
	).toBe(SettingsSection.Providers);
});

test("the primary settings area reuses section components through list/detail slots", async () => {
	const workArea = await Bun.file(
		new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	).text();
	for (const contract of [
		'settings-sidebar"',
		'settings-detail"',
		'settings-section-row"',
		"settings-panel-",
		"AgentSettings",
		"SETTINGS_SECTION_LOADERS",
		"Settings is a primary area",
	]) {
		expect(workArea).toContain(contract);
	}
	const loaders = await Bun.file(
		new URL("../../../webui/src/settings/settings-sections.ts", import.meta.url),
	).text();
	expect(loaders).toContain("./sections/schedules-section.svelte");
	const section = await Bun.file(
		new URL("../../../webui/src/settings/sections/schedules-section.svelte", import.meta.url),
	).text();
	expect(section).toContain('settings-schedules-section"');
	expect(section).toContain("SchedulesView");
	expect(section).toContain("project");
});
