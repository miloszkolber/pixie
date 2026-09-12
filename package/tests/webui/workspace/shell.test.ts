import { beforeEach, expect, test } from "bun:test";
import type { AgentProfile, Project } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import {
	appStoreApi,
	EMPTY_RUNTIME,
	type ProjectArea,
	projectArea,
	SettingsSection,
} from "@/store";
import { openSettingsArea } from "@/workspace/navigation/open-settings-area";
import {
	hasConfiguredProvider,
	resolveShellAvailability,
	resolveShellPrimarySurface,
} from "@/workspace/shell-state";
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
const piProfile: AgentProfile = {
	...genericProfile,
	pi: true,
	operations: { ...genericProfile.operations, administration: true },
};
const incompatibleProfile: AgentProfile = {
	...genericProfile,
	compatible: false,
	missingRequired: ["session.load"],
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

test("opening settings activates the primary settings area without a modal", async () => {
	await openSettingsArea(SettingsSection.Providers);
	const selection = appStoreApi.getState().workspaceSelection;
	expect(selection.primaryArea).toBe("settings");
	expect(selection.primarySelection).toEqual({
		kind: "settings",
		sectionId: SettingsSection.Providers,
	});
	expect(appStoreApi.getState().settingsSection).toBe(SettingsSection.Providers);
});

test("settings stay reachable through the standalone surface in every non-ready state", () => {
	function surface(
		status: Parameters<typeof resolveShellAvailability>[0],
		profile: AgentProfile | null,
		providerConfigured: boolean | null,
		providerError: boolean,
		activeProjectAreaId: string | null,
		settingsRequested: boolean,
	) {
		const availability = resolveShellAvailability(
			status,
			profile,
			providerConfigured,
			providerError,
		);
		const hasActiveProjectArea = availability === "ready" && activeProjectAreaId !== null;
		return {
			availability,
			surface: resolveShellPrimarySurface(hasActiveProjectArea, settingsRequested),
		};
	}

	// Fresh install: Pi is compatible but no provider is configured and no
	// project area exists, so the old open path could not enter anywhere.
	expect(surface("connected", piProfile, false, false, null, true)).toEqual({
		availability: "unconfigured",
		surface: "standalone-settings",
	});
	// Controller could not read provider status (error panel).
	expect(surface("connected", piProfile, null, true, null, true)).toEqual({
		availability: "error",
		surface: "standalone-settings",
	});
	expect(surface("connected", incompatibleProfile, null, false, null, true)).toEqual({
		availability: "incompatible",
		surface: "standalone-settings",
	});
	expect(surface("disconnected", null, null, false, null, true)).toEqual({
		availability: "disconnected",
		surface: "standalone-settings",
	});
	// Without a settings request the explanatory panel keeps the content surface.
	expect(surface("connected", piProfile, false, false, null, false).surface).toBe("content");
	// Ready with an active project area keeps ProjectWorkArea as the owner.
	expect(surface("connected", piProfile, true, false, area.id, true)).toEqual({
		availability: "ready",
		surface: "project-work-area",
	});
});

test("openSettingsArea records the request even when no project area can be entered", async () => {
	appStoreApi.setState({
		status: "connected",
		agentProfile: piProfile,
		providerConfigured: false,
		projects: [],
		recentProjects: [],
		projectAreas: {},
		selectedProjectId: null,
		activeProjectAreaId: null,
		settingsSection: SettingsSection.Models,
	});
	await openSettingsArea(SettingsSection.Providers);
	const selection = appStoreApi.getState().workspaceSelection;
	expect(appStoreApi.getState().activeProjectAreaId).toBeNull();
	expect(selection.primaryArea).toBe("settings");
	expect(selection.primarySelection).toEqual({
		kind: "settings",
		sectionId: SettingsSection.Providers,
	});
	expect(appStoreApi.getState().settingsSection).toBe(SettingsSection.Providers);
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
		new URL("../../../webui/src/settings/settings-area.svelte", import.meta.url),
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
		'data-testid="mobile-pane-navigation"',
		'data-testid="workspace-grid"',
		'data-slot="primary-view"',
		'data-slot="secondary-view"',
		'data-testid="secondary-sidebar"',
		'aria-label="Sign out"',
		'data-testid="rail-canvas"',
		'data-testid="rail-design"',
		'data-testid="canvas-module-view"',
		'data-testid="design-module-view"',
		'data-testid="canvas-sidebar"',
		'data-testid="design-sidebar"',
		"canvasManagementStatusUrl",
		'"/api/design/status"',
		'data-testid="settings-area"',
		'data-settings-surface="standalone"',
		'data-testid="settings-area-sidebar"',
		'data-testid="settings-area-close"',
		'aria-label="Back from settings"',
		'primarySurface === "standalone-settings"',
		"resolveShellPrimarySurface",
		"<SettingsArea",
	]) {
		expect(source).toContain(contract);
	}
	expect(source).toContain(".catch(() => {");
	expect(source).toContain("openSettingsArea");
	expect(source).toContain('data-testid="settings-detail"');
	expect(source).not.toContain("settings-dialog.svelte");
	expect(source).toContain("onOpenChanges={showActivity}");
	expect(source.match(/id="activity-panel"/g)).toHaveLength(1);

	// Ready-with-area routes to ProjectWorkArea; the standalone surface is a
	// fallback that only renders when no project area is active.
	const shell = sources[0] ?? "";
	const projectBranch = shell.lastIndexOf('primarySurface === "project-work-area"');
	const standaloneBranch = shell.lastIndexOf('primarySurface === "standalone-settings"');
	const standaloneComponent = shell.indexOf("<SettingsArea");
	expect(projectBranch).toBeGreaterThanOrEqual(0);
	expect(standaloneBranch).toBeGreaterThan(projectBranch);
	expect(standaloneComponent).toBeGreaterThan(standaloneBranch);
	expect(shell).not.toContain('data-testid="settings-dialog"');
	expect(shell).not.toContain('role="dialog"');
});
