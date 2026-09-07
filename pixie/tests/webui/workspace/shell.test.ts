import { beforeEach, expect, test } from "bun:test";
import type { AgentProfile, Project } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import { openSettingsFrom, restoreSettingsFocus } from "@/settings/open-settings";
import {
	appStoreApi,
	EMPTY_RUNTIME,
	type ProjectArea,
	projectArea,
	SettingsSection,
} from "@/store";
import { hasConfiguredProvider, resolveShellAvailability } from "@/workspace/shell-state";
import { selectTabSessionStreaming } from "@/workspace/views/project-work-area-state";

const project: Project = {
	id: "project-1",
	name: "Existing project",
	roots: ["/projects/existing"],
	slug: "existing-project",
	lastOpened: 1,
};
const area: ProjectArea = projectArea(project);
const genericProfile: AgentProfile = {
	name: "Example Pi agent",
	version: "1.2.3",
	pi: false,
	compatible: true,
	missingRequired: [],
	operations: {
		deleteSession: false,
		forkSession: false,
		promptImage: false,
		promptEmbeddedContext: false,
		httpMcp: false,
		steer: false,
		renameSession: false,
		archiveSession: false,
		administration: false,
	},
};

beforeEach(() => {
	appStoreApi.setState({
		status: "connected",
		projects: [project],
		recentProjects: [project],
		projectAreas: { [project.id]: [area] },
		selectedProjectId: project.id,
		activeProjectAreaId: area.id,
		providerConfigured: null,
		agentProfile: null,
		settingsOpen: false,
		settingsSection: SettingsSection.Models,
	});
});

test("shell availability respects connectivity, compatibility, and Pi provider state", () => {
	expect(resolveShellAvailability("connecting", null, null, false)).toBe("loading");
	expect(resolveShellAvailability("disconnected", null, null, false)).toBe("disconnected");
	expect(resolveShellAvailability("connected", null, null, true)).toBe("error");
	expect(resolveShellAvailability("connected", genericProfile, null, false)).toBe("ready");
	expect(
		resolveShellAvailability(
			"connected",
			{ ...genericProfile, compatible: false, missingRequired: ["session.load"] },
			null,
			false,
		),
	).toBe("incompatible");
	const piProfile = {
		...genericProfile,
		pi: true,
		operations: { ...genericProfile.operations, administration: true },
	};
	expect(resolveShellAvailability("connected", piProfile, null, false)).toBe("loading");
	expect(resolveShellAvailability("connected", piProfile, false, false)).toBe("unconfigured");
	expect(resolveShellAvailability("connected", piProfile, true, false)).toBe("ready");
	expect(
		hasConfiguredProvider({
			providers: [
				{
					id: "openai",
					name: "OpenAI",
					configured: true,
					modelCount: 1,
					availableModelCount: 1,
					readinessCheck: false,
				},
			],
		}),
	).toBeTrue();
});

test("settings restore focus to the control that opened them", () => {
	let focused = false;
	openSettingsFrom({ isConnected: true, focus: () => (focused = true) }, SettingsSection.Providers);
	expect(appStoreApi.getState().settingsOpen).toBeTrue();
	expect(appStoreApi.getState().settingsSection).toBe(SettingsSection.Providers);
	expect(restoreSettingsFocus()).toBeTrue();
	expect(focused).toBeTrue();
});

test("workspace streaming selection ignores transcript content", () => {
	appStoreApi.setState({
		tabsByProjectArea: {
			[area.id]: [
				{ kind: "chat", id: "tab", projectAreaId: area.id, name: "Chat", sessionId: "open" },
			],
		},
		sessions: { open: { ...EMPTY_RUNTIME }, background: { ...EMPTY_RUNTIME } },
	});
	expect(selectTabSessionStreaming(appStoreApi.getState(), area.id)).toEqual({ open: false });
	appStoreApi.getState().handleAgentEvent({ type: "run-start" }, "open");
	expect(selectTabSessionStreaming(appStoreApi.getState(), area.id)).toEqual({ open: true });
	for (let index = 0; index < 10; index += 1) {
		appStoreApi.getState().handleAgentEvent({ type: "text", text: "." }, "open");
	}
	expect(selectTabSessionStreaming(appStoreApi.getState(), area.id)).toEqual({ open: true });
	appStoreApi.getState().handleAgentEvent({ type: "complete" }, "open");
	expect(selectTabSessionStreaming(appStoreApi.getState(), area.id)).toEqual({ open: false });
});

test("the Svelte shell keeps one responsive activity surface and every blocked state", async () => {
	const urls = [
		new URL("../../../webui/src/workspace/shell.svelte", import.meta.url),
		new URL("../../../webui/src/workspace/views/project-work-area.svelte", import.meta.url),
	];
	const sources = await Promise.all(urls.map((url) => Bun.file(url).text()));
	for (const [index, source] of sources.entries()) {
		expect(
			compile(source, { filename: urls.at(index)?.pathname ?? "unknown.svelte", generate: false })
				.warnings,
		).toEqual([]);
		expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	}
	const source = sources.join("\n");
	for (const contract of [
		'data-testid="provider-status-loading"',
		"<NoProviderWelcome />",
		"Controller disconnected",
		'data-testid="project-shell"',
		'id="activity-panel"',
		`data-testid={\`tab-\${item}\`}`,
		'aria-label="Mobile panes"',
		'aria-label="Sign out"',
	]) {
		expect(source).toContain(contract);
	}
	expect(source).toContain('["files", "changes"]');
	expect(source).toContain(".catch(() => {");
	expect(source).toContain("Couldn't open settings");
	expect(source).toContain("closeSettings()");
	expect(source).toContain("onOpenChanges={showActivity}");
	expect(source.match(/id="activity-panel"/g)).toHaveLength(1);
});
