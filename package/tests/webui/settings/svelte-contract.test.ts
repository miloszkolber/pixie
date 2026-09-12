import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const settingsRoot = new URL("../../../webui/src/settings/", import.meta.url);
const requiredComponents = [
	"login/login-dialog.svelte",
	"settings-area.svelte",
	"sections/agent-settings.svelte",
	"sections/deletion-recovery.svelte",
	"sections/pi-settings.svelte",
	"sections/pi-tools-settings.svelte",
	"sections/models-settings.svelte",
	"sections/provider-card.svelte",
	"sections/providers-settings.svelte",
	"sections/system-settings.svelte",
] as const;

test("every settings Svelte component parses without compiler warnings or React imports", async () => {
	for (const relativePath of requiredComponents) {
		const url = new URL(relativePath, settingsRoot);
		const source = await Bun.file(url).text();
		expect(source.length).toBeGreaterThan(0);
		expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
		const result = compile(source, { filename: url.pathname, generate: false });
		expect(result.warnings).toEqual([]);
	}
});

test("the Svelte settings surface retains the settings and login selectors", async () => {
	const sources = await Promise.all(
		requiredComponents.map((relativePath) => Bun.file(new URL(relativePath, settingsRoot)).text()),
	);
	const source = sources.join("\n");
	for (const testId of [
		"settings-pi",
		"settings-pi-tools",
		"settings-models",
		"settings-providers",
		"system-settings",
		"deletion-recovery",
		"deletion-recovery-confirm",
		"deletion-recovery-retain",
		"tool-inventory",
		"in-process-mcp-module-row",
		"model-row",
		"models-filter",
		"providers-refresh",
		"providers-filter",
		"providers-error",
		"system-card-agent",
		"system-refresh",
		"login-dialog",
		"login-success",
		"login-error",
		"login-open-url",
		"login-device-code",
		"login-device-url",
		"login-option",
		"login-input",
		"login-submit",
		"login-progress",
		"login-working",
		"login-close",
		"login-cancel",
	]) {
		expect(
			source.includes(`data-testid="${testId}"`) || source.includes(`testid="${testId}"`),
		).toBeTrue();
	}
	expect(source).toMatch(/data-testid=\{`system-card-\$\{name\.toLowerCase\(\)\}`\}/);
});

test("the settings modal is gone and the primary-area view owns settings navigation", async () => {
	const workArea = await Bun.file(
		new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	).text();
	expect(await Bun.file(new URL("settings-dialog.svelte", settingsRoot)).exists()).toBe(false);
	expect(workArea).toContain('data-testid="settings-detail"');
	expect(workArea).toContain('data-testid="settings-section-row"');
	expect(workArea).toContain('role="tablist"');
	expect(workArea).not.toContain("settings-dialog.svelte");

	// The standalone fallback surface is a primary-area region, not a modal.
	const shell = await Bun.file(
		new URL("../../../webui/src/workspace/shell.svelte", import.meta.url),
	).text();
	const standalone = await Bun.file(new URL("settings-area.svelte", settingsRoot)).text();
	expect(shell).toContain("<SettingsArea");
	expect(standalone).toContain('data-testid="settings-area"');
	expect(standalone).not.toContain('role="dialog"');
	expect(shell).not.toContain('data-testid="settings-dialog"');
	expect(standalone).not.toContain("settings-dialog.svelte");
});
