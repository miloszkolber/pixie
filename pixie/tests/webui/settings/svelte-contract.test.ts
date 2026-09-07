import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const settingsRoot = new URL("../../../webui/src/settings/", import.meta.url);
const requiredComponents = [
	"login/login-dialog.svelte",
	"sections/agent-settings.svelte",
	"sections/pi-settings.svelte",
	"sections/pi-tools-settings.svelte",
	"sections/models-settings.svelte",
	"sections/provider-card.svelte",
	"sections/providers-settings.svelte",
	"sections/signet-settings.svelte",
	"sections/system-settings.svelte",
	"settings-dialog.svelte",
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
		"settings-dialog",
		"settings-pi",
		"settings-pi-tools",
		"settings-models",
		"settings-providers",
		"system-settings",
		"tool-inventory",
		"mcp-module-row",
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

test("closed settings retain the native dialog lifecycle without retaining section effects", async () => {
	const source = await Bun.file(new URL("settings-dialog.svelte", settingsRoot)).text();
	const dialogStart = source.indexOf("<Dialog");
	const openGuard = source.indexOf("{#if $appStore.settingsOpen}", dialogStart);
	expect(dialogStart).toBeGreaterThanOrEqual(0);
	expect(openGuard).toBeGreaterThan(dialogStart);
	expect(source.indexOf("<Section />", openGuard)).toBeGreaterThan(openGuard);
});
