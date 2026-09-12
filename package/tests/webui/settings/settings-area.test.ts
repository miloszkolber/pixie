import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const settingsRoot = new URL("../../../webui/src/settings/", import.meta.url);
const shellUrl = new URL("../../../webui/src/workspace/shell.svelte", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, settingsRoot)).text();
}

test("the standalone settings surface parses without warnings and compiles cleanly", async () => {
	const url = new URL("settings-area.svelte", settingsRoot);
	const text = await source("settings-area.svelte");
	expect(text.length).toBeGreaterThan(0);
	expect(text).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(text, { filename: url.pathname, generate: false }).warnings).toEqual([]);
});

test("the standalone surface owns a primary settings region with a way back", async () => {
	const text = await source("settings-area.svelte");
	for (const contract of [
		'data-testid="settings-area"',
		'data-settings-surface="standalone"',
		'data-testid="settings-area-sidebar"',
		'data-testid="settings-area-close"',
		'aria-label="Back from settings"',
		'data-testid="settings-detail"',
		'id="main-content"',
		'role="tablist"',
		'role="tabpanel"',
		'data-testid="settings-section-row"',
		"aria-selected",
		"handleSettingsSectionKeydown",
		"hidden={section !== settingsActiveSection}",
	]) {
		expect(text).toContain(contract);
	}
	expect(text).not.toContain('role="dialog"');
	expect(text).not.toContain("settings-dialog.svelte");
});

test("the standalone surface reuses the shared lazy settings section loaders", async () => {
	const area = await source("settings-area.svelte");
	expect(area).toContain("SETTINGS_SECTION_LOADERS");
	expect(area).toContain("settingsTabs(");
	expect(area).toContain("resolveSettingsSection(");
	expect(area).toContain("resolveWorkspaceSettingsSection(");
	expect(area).toContain("AgentSettings");

	const loaders = await source("settings-sections.ts");
	for (const component of [
		"providers-settings.svelte",
		"system-settings.svelte",
		"pi-settings.svelte",
		"pi-tools-settings.svelte",
		"models-settings.svelte",
		"extensions-settings.svelte",
		"schedules-section.svelte",
	]) {
		expect(loaders).toContain(`./sections/${component}`);
	}
});

test("shell routes requested settings to the standalone surface without a modal", async () => {
	const shell = await Bun.file(shellUrl).text();
	expect(shell).toContain("resolveShellPrimarySurface");
	expect(shell).toContain('primarySurface === "standalone-settings"');
	expect(shell).toContain("<SettingsArea");
	expect(shell).toContain("closeSettingsArea");
	expect(shell).not.toContain('data-testid="settings-dialog"');
	expect(shell).not.toContain('role="dialog"');
});
