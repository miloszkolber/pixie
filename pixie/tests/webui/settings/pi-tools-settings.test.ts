import { beforeEach, expect, test } from "bun:test";
import type { AgentProfile, PiToolSummary } from "@pixie/contracts";
import { agentOperationRows } from "@/settings/sections/agent-settings";
import {
	extensionWarningText,
	isSessionInventoryCurrent,
} from "@/settings/sections/pi-tools-settings";
import { resolveSettingsSection, settingsTabs } from "@/settings/settings-dialog";
import { SettingsSection } from "@/settings/state";
import { appStoreApi } from "@/store";

beforeEach(() => {
	appStoreApi.setState({
		activeProjectAreaId: null,
		projectAreas: {},
		tabsByProjectArea: {},
		activeTabByProjectArea: {},
		settingsOpen: true,
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

test("generic agent settings expose agent identity, Signet and System", () => {
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
	appStoreApi.setState({ agentProfile: profile, settingsOpen: false });
	appStoreApi.getState().openSettings();
	expect(appStoreApi.getState().settingsSection).toBe(SettingsSection.Agent);
	expect(settingsTabs(true, false).map(({ label }) => label)).toEqual([
		"Agent",
		"Signet",
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
	expect(settingsTabs(false, true)).toEqual([{ section: SettingsSection.System, label: "System" }]);
});

test("settings tabs wrap without a native horizontal scroll container", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/settings-dialog.svelte", import.meta.url),
	).text();
	expect(source).toContain('role="tablist"');
	expect(source).toContain("flex-wrap");
	expect(source).not.toContain("overflow-x-auto");
	expect(source).toContain('role="tabpanel"');
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
		"System",
	]);
	const extended = { ...profile, capabilities: { ...profile.capabilities, mcp: 1 } };
	expect(settingsTabs(false, false, extended).map((t) => t.label)).not.toContain("Automation");
	expect(settingsTabs(false, false, extended).map((t) => t.label)).toContain("Signet");
});
