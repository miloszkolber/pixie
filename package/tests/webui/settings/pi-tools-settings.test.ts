import { beforeEach, expect, test } from "bun:test";
import type { AgentProfile, McpRegistryModule } from "@pixie/contracts";
import { agentOperationRows } from "@/settings/sections/agent-settings";
import {
	extensionWarningText,
	isSessionInventoryCurrent,
	registryModuleStatusLabel,
} from "@/settings/sections/pi-tools-settings";
import {
	resolveSettingsSection,
	selectVisibleSettingsSection,
	settingsTabs,
} from "@/settings/settings-dialog";
import { SettingsSection } from "@/settings/state";
import { appStoreApi } from "@/store";

beforeEach(() => {
	appStoreApi.setState({
		activeProjectAreaId: null,
		projectAreas: {},
		tabsByProjectArea: {},
		activeTabByProjectArea: {},
		settingsSection: SettingsSection.Tools,
		agentProfile: null,
	});
});

test("warning counts never expose warning text", () => {
	expect(extensionWarningText(0)).toBeNull();
	expect(extensionWarningText(1)).toBe("1 Pi configuration warning reported.");
	expect(extensionWarningText(2)).toBe("2 Pi configuration warnings reported.");
	expect(extensionWarningText(2)).not.toContain("warning text");
});

test("generic agent settings expose agent identity and System", () => {
	const profile: AgentProfile = {
		name: "Example agent",
		version: "1.2.3",
		pi: false,
		compatible: true,
		missingRequired: [],
		operations: {
			deleteSession: false,
			forkSession: true,
			promptImage: false,
			promptEmbeddedContext: false,
			httpMcp: false,
			steer: false,
			renameSession: false,
			archiveSession: false,
			administration: false,
		},
	};
	appStoreApi.setState({ agentProfile: profile });
	expect(resolveSettingsSection(SettingsSection.Tools, profile)).toBe(SettingsSection.Agent);
	expect(settingsTabs(true, false).map(({ label }) => label)).toEqual([
		"Schedules",
		"Agent",
		"System",
	]);
	expect(settingsTabs(true, false).map(({ label }) => label)).not.toContain("Pi");
	expect(agentOperationRows(profile)).toContainEqual({
		operation: "httpMcp",
		label: "HTTP MCP servers",
		available: false,
	});
	expect(resolveSettingsSection(SettingsSection.System, profile)).toBe(SettingsSection.System);
});

test("System remains reachable while agent capabilities are unavailable", () => {
	expect(resolveSettingsSection(SettingsSection.Tools, null)).toBe(SettingsSection.System);
	expect(settingsTabs(false, true)).toEqual([
		{ section: SettingsSection.Schedules, label: "Schedules" },
		{ section: SettingsSection.System, label: "System" },
	]);
});

test("profile loss falls back from an unavailable local selection to System", () => {
	const tabs = settingsTabs(false, true);
	const resolved = resolveSettingsSection(SettingsSection.Pi, null);
	expect(selectVisibleSettingsSection(SettingsSection.Pi, resolved, tabs)).toBe(
		SettingsSection.System,
	);
});

test("primary settings sections render as a keyboard tablist without horizontal scroll", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	).text();
	expect(source).toContain('role="tablist"');
	expect(source).toContain('data-testid="settings-section-row"');
	expect(source).toContain('role="tabpanel"');
	expect(source).toContain("handleSettingsSectionKeydown");
	expect(source).toContain("flex-col");
	expect(source).not.toContain('data-testid="settings-dialog"');
});

test("in-process publisher rows project enablement before readiness", () => {
	const base: McpRegistryModule = {
		id: "browser",
		extensionName: "pixie-browser",
		displayName: "Pixie Browser",
		description: "Bounded browser automation and browser guidance.",
		path: "/mcp/browser",
		transport: "streamable_http",
		enabled: true,
		state: "ready",
		endpoint: "http://127.0.0.1:7312/mcp/browser",
	};
	expect(registryModuleStatusLabel(base)).toBe("Enabled");
	expect(registryModuleStatusLabel({ ...base, state: "unavailable" })).toBe("Unavailable");
	expect(registryModuleStatusLabel({ ...base, enabled: false })).toBe("Disabled");
});

test("session controls are current only after the active target finishes loading", () => {
	expect(isSessionInventoryCurrent("project-a\0chat-a", "project-a\0chat-a", false)).toBe(true);
	expect(isSessionInventoryCurrent("project-a\0chat-a", "project-a\0chat-b", false)).toBe(false);
	expect(isSessionInventoryCurrent("project-a\0chat-a", "project-a\0chat-a", true)).toBe(false);
});

test("vanilla Pi exposes core settings and hides unavailable extension surfaces", () => {
	const profile: AgentProfile = {
		name: "Pi",
		version: "0.85.1",
		compatible: true,
		missingRequired: [],
		pi: true,
		operations: {
			administration: true,
			deleteSession: true,
			forkSession: true,
			promptImage: true,
			promptEmbeddedContext: true,
			httpMcp: false,
			steer: true,
			renameSession: true,
			archiveSession: true,
		},
		capabilities: { sessions: 1, providers: 1 },
	};
	expect(settingsTabs(false, false, profile).map((t) => t.label)).toEqual([
		"Pi",
		"Providers",
		"Models",
		"Tools",
		"Extensions",
		"Schedules",
		"System",
	]);
	const extended = { ...profile, capabilities: { ...profile.capabilities, mcp: 1 } };
	expect(settingsTabs(false, false, extended).map((t) => t.label)).not.toContain("Automation");
	expect(settingsTabs(false, false, extended).map((t) => t.label)).not.toContain("Signet");
});
