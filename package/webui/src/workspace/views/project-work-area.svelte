<script lang="ts">
import type { Project, RuntimeStatusReport } from "@pixie/contracts";
import ChatView from "../../chat/chat-view.svelte";
import SessionLifecycleMenu from "../../chat/session/session-lifecycle-controls.svelte";
import Button from "../../components/button.svelte";
import ErrorBoundary from "../../components/error-boundary.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport, logoutController } from "../../connection";
import ChangesPanel from "../../files/changes/changes-panel.svelte";
import DiffPane from "../../files/changes/diff-pane.svelte";
import FilePane from "../../files/tabs/file-pane.svelte";
import FileTree from "../../files/tree/file-tree.svelte";
import { openSettingsFrom } from "../../settings/open-settings";
import { SettingsSection } from "../../settings/state";
import {
	appStore,
	appStoreApi,
	clearSecondary,
	type ChatTab,
	type ContentTab,
	type PrimaryArea,
	type SecondaryArea,
	selectContextProject,
	selectProjectAreaById,
	toast,
} from "../../store";
import {
	selectPrimaryArea as selectPrimaryAreaAction,
	selectSecondaryArea as selectSecondaryAreaAction,
	setLayout as setWorkspaceLayout,
} from "../store/selection-state";
import {
	captureNavigationOwner,
	navigationOwnerIsCurrent,
	navigationOwnerProjectIsCurrent,
} from "../navigation/ownership";
import BrowserPanel from "../browser/browser-panel.svelte";
import { focusFirstVisible, panelHasFocusableContent } from "../focus-control";
import {
	hydrateChatResource,
	initProjectAreaChatReconciliation,
} from "../navigation/chat-reconciliation";
import { startChatSession } from "../navigation/start-chat";
import PanelHeader from "../panel-header.svelte";
import AddProjectMenu from "../projects/add-project-menu.svelte";
import OpenProjectDialogs from "../projects/open-project-dialogs.svelte";
import ProjectChatHistory from "../projects/project-chat-history.svelte";
import ProjectTree from "../projects/project-tree.svelte";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import ShellRail from "../shell-rail.svelte";
import {
	browserPanelAvailable,
	browserRestartTargetOpen,
	claimBrowserRestart,
	selectPrimaryContentTab,
	selectSecondaryContentTab,
	selectTabSessionStreaming,
} from "./project-work-area-state";

interface Props {
	projectAreaId: string;
}
type ShellResizerComponent = typeof import("../shell-resizer.svelte").default;
let { projectAreaId }: Props = $props();
let ShellResizer = $state<ShellResizerComponent | null>(null);

type MobilePane = "projects" | "primary" | "secondary";
type MobileSecondarySurface = "view" | "sidebar";

	let mobilePane = $state<MobilePane>(initialMobilePane());
	let mobileSecondarySurface = $state<MobileSecondarySurface>(initialMobileSecondarySurface());
let browserStatus = $state<RuntimeStatusReport | null>(null);
let previousTabs: ContentTab[] = [];
const browserRestartsInFlight = new Set<string>();

interface ProjectOpener {
	openProject: (path: string) => Promise<void>;
	pickAndOpen: () => void;
}
let opener = $state<ProjectOpener>();
let projectFilterOpen = $state(false);
let projectFilter = $state("");
let filesFilterOpen = $state(false);
let filesFilter = $state("");
let grid: HTMLDivElement | null = $state(null);

$effect(() => {
	let cancelled = false;
	import("../shell-resizer.svelte")
		.then((module) => {
			if (!cancelled) ShellResizer = module.default;
		})
		.catch(() => undefined);
	return () => {
		cancelled = true;
	};
});

const STATUS_LABEL = {
	connected: "Connected",
	connecting: "Connecting…",
	disconnected: "Disconnected",
} as const;
const STATUS_DOT = {
	connected: "bg-feedback-success",
	connecting: "bg-feedback-warning",
	disconnected: "bg-feedback-error",
} as const;

let projectArea = $derived(selectProjectAreaById($appStore, projectAreaId));
let contextProject = $derived(selectContextProject($appStore));
let contentTabs = $derived($appStore.tabsByProjectArea[projectAreaId] ?? []);
let workspaceSelection = $derived($appStore.workspaceSelection);
let primaryArea = $derived(workspaceSelection.primaryArea);
let secondaryArea = $derived(workspaceSelection.secondaryArea);
let primarySelection = $derived(workspaceSelection.primarySelection);
let secondarySelection = $derived(workspaceSelection.secondarySelection);
let layout = $derived(workspaceSelection.layout);
let primaryTab = $derived(
	primaryArea === "chats"
		? selectPrimaryContentTab(contentTabs, primarySelection, projectAreaId)
		: null,
);
let secondaryTab = $derived(
	selectSecondaryContentTab(contentTabs, secondarySelection, projectAreaId),
);
let sessionStreaming = $derived(selectTabSessionStreaming($appStore, projectAreaId));
let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let removed = $derived($appStore.removedProjectAreaIds[projectAreaId] === true);
let hasSecondarySelection = $derived(secondarySelection !== null);
let primarySidebarVisible = $derived(!layout.leftCollapsed && layout.focus !== "primary");
let primaryViewVisible = $derived(layout.focus !== "secondary");
let secondaryViewVisible = $derived(hasSecondarySelection && layout.focus !== "primary");
let secondarySidebarVisible = $derived(!layout.rightCollapsed && layout.focus !== "primary");
let sessionDetailsVisible = $derived(
	primaryArea === "chats" && primarySelection?.kind === "session",
);
let contentMinimum = $derived(
	hasSecondarySelection
		? "min(22.5rem, max(0px, calc((100% - 6rem - var(--pixie-primary-sidebar-track, 16rem) - var(--pixie-secondary-sidebar-track, 16rem)) / 2)))"
		: "0px",
);
let gridStyle = $derived(
	[
		`--pixie-primary-sidebar-track:${primarySidebarVisible ? `${layout.leftWidth}px` : "0px"}`,
		`--pixie-secondary-sidebar-track:${secondarySidebarVisible ? `${layout.rightWidth}px` : "0px"}`,
		`--pixie-primary-content-min:${primaryViewVisible ? contentMinimum : "0px"}`,
		`--pixie-secondary-content-min:${secondaryViewVisible ? contentMinimum : "0px"}`,
		`--pixie-primary-view-track:${primaryViewVisible ? `${layout.primaryFraction}fr` : "0fr"}`,
		`--pixie-secondary-view-track:${secondaryViewVisible ? `${1 - layout.primaryFraction}fr` : "0px"}`,
	].join(";"),
);
let primaryTitle = $derived(
	primaryTab?.name ??
		(primaryArea === "chats"
			? "Chats"
			: primaryArea === "archive"
				? "Archive"
				: primaryArea === "schedules"
					? "Schedules"
					: "Settings"),
);
let secondaryTitle = $derived(
	secondaryTab?.name ??
		(secondaryArea === "details"
			? "Details"
			: secondaryArea === "files"
				? "Files"
				: secondaryArea === "git"
					? "Git"
					: secondaryArea.slice("module:".length)),
);

$effect(() => initProjectAreaChatReconciliation(projectAreaId));

$effect(() => {
	// A secondary-focused layout cannot strand the primary view after its
	// selected resource is closed or invalidated by project navigation.
	if (!hasSecondarySelection && layout.focus === "secondary") dispatchLayout({ focus: "none" });
});

$effect(() => {
	if (!connected) {
		browserStatus = null;
		return;
	}
	const generation = connectionGeneration;
	let current = true;
	let next: ReturnType<typeof setTimeout> | undefined;
	const poll = async (): Promise<void> => {
		try {
			const report = await getTransport().request("runtime.status", {}, { timeoutMs: 5_000 });
			if (current && appStoreApi.getState().connectionGeneration === generation)
				browserStatus = report;
		} catch {
			if (current && appStoreApi.getState().connectionGeneration === generation)
				browserStatus = null;
		} finally {
			if (current) next = setTimeout(() => void poll(), 5_000);
		}
	};
	void poll();
	return () => {
		current = false;
		if (next) clearTimeout(next);
	};
});

$effect(() => {
	const tabs = contentTabs;
	if (removed) {
		for (const tab of previousTabs) {
			if (tab.kind !== "browser") continue;
			void getTransport()
				.request("browser.panelClose", { panelId: tab.panelId }, { timeoutMs: 10_000 })
				.catch(() => undefined);
		}
	}
	previousTabs = tabs;
});

function dispatchLayout(patch: Parameters<typeof setWorkspaceLayout>[0]): void {
	appStoreApi.getState().dispatchWorkspaceSelection(setWorkspaceLayout(patch));
}

function selectPrimaryArea(area: PrimaryArea): void {
	appStoreApi.getState().dispatchWorkspaceSelection(selectPrimaryAreaAction(area));
}

function setFocus(focus: "primary" | "secondary"): void {
	dispatchLayout({ focus: layout.focus === focus ? "none" : focus });
}

function restoreLayout(): void {
	// Restore is presentation-only: selections, drafts, and accepted work stay intact.
	dispatchLayout({ leftCollapsed: false, rightCollapsed: false, focus: "none" });
}

function restoreLeft(): void {
	dispatchLayout({ leftCollapsed: false });
}

	function restoreRight(): void {
		dispatchLayout({ rightCollapsed: false });
	}

	// Narrow viewports show one surface: start on the persisted secondary
	// content when desktop focus already sits there, otherwise the grid paints
	// blank until the first tap. Later switches recompute both values explicitly.
	function initialMobilePane(): MobilePane {
		return appStoreApi.getState().workspaceSelection.layout.focus === "secondary"
			? "secondary"
			: "primary";
	}

	function initialMobileSecondarySurface(): MobileSecondarySurface {
		return appStoreApi.getState().workspaceSelection.secondarySelection === null
			? "sidebar"
			: "view";
	}

function showPrimarySurface(): void {
	mobilePane = "primary";
	mobileSecondarySurface = "view";
	if (layout.focus !== "none") dispatchLayout({ focus: "none" });
}

function showSecondarySurface(): void {
	mobilePane = "secondary";
	const hasSelection = appStoreApi.getState().workspaceSelection.secondarySelection !== null;
	mobileSecondarySurface = hasSelection ? "view" : "sidebar";
	if (!hasSelection && layout.rightCollapsed) dispatchLayout({ rightCollapsed: false });
	if (layout.focus !== "none") dispatchLayout({ focus: "none" });
}

function showProjects(): void {
	mobilePane = "projects";
	mobileSecondarySurface = "view";
	if (layout.leftCollapsed) dispatchLayout({ leftCollapsed: false });
	if (layout.focus !== "none") dispatchLayout({ focus: "none" });
}

function showSecondarySidebar(): void {
	mobilePane = "secondary";
	mobileSecondarySurface = "sidebar";
	if (layout.rightCollapsed) dispatchLayout({ rightCollapsed: false });
	if (layout.focus !== "none") dispatchLayout({ focus: "none" });
}

function showActivity(): void {
	showSecondarySidebar();
	revealSecondaryArea("git");
	appStoreApi.getState().requestToolView(projectAreaId, "changes");
}

function startChat(): void {
	void startChatSession(projectAreaId);
}

async function openBrowserTab(replacing?: Extract<ContentTab, { kind: "browser" }>): Promise<void> {
	const restartTabId = replacing?.id;
	const initial = appStoreApi.getState();
	const navigation = captureNavigationOwner(
		initial,
		projectAreaId,
		selectProjectAreaById(initial, projectAreaId)?.projectId ?? projectAreaId,
	);
	if (replacing) {
		if (!browserRestartTargetOpen(initial.tabsByProjectArea[projectAreaId], replacing)) return;
		if (!claimBrowserRestart(browserRestartsInFlight, replacing.id)) return;
	}
	try {
		if (replacing) {
			await getTransport()
				.request("browser.panelClose", { panelId: replacing.panelId }, { timeoutMs: 10_000 })
				.catch(() => undefined);
		}
		const panel = await getTransport().request("browser.panelOpen", {
			projectId: projectArea?.projectId ?? projectAreaId,
		});
		const state = appStoreApi.getState();
		const targetStillOpen =
			!replacing || browserRestartTargetOpen(state.tabsByProjectArea[projectAreaId], replacing);
		if (
			!navigationOwnerProjectIsCurrent(state, navigation) ||
			!navigationOwnerIsCurrent(state, navigation, "secondary") ||
			!targetStillOpen
		) {
			if (replacing && targetStillOpen) state.closeTab(replacing.id, false, projectAreaId);
			void getTransport()
				.request("browser.panelClose", { panelId: panel.id }, { timeoutMs: 10_000 })
				.catch(() => undefined);
			return;
		}
		if (replacing) state.closeTab(replacing.id, false, projectAreaId);
		state.setBrowserPanelState(panel.id, {});
		state.openTab(
			{
				kind: "browser",
				id: `browser-${panel.id}`,
				projectAreaId,
				name: "Browser",
				panelId: panel.id,
			},
			"keep",
		);
		showSecondarySurface();
	} catch (cause) {
		if (navigationOwnerIsCurrent(appStoreApi.getState(), navigation, "secondary"))
			toast.error(
				errorText(cause),
				replacing ? "Couldn't restart the browser" : "Couldn't open the browser",
			);
	} finally {
		if (restartTabId) browserRestartsInFlight.delete(restartTabId);
	}
}

function startBrowser(): void {
	void openBrowserTab();
}

function closeTab(tab: ContentTab): void {
	if (tab.kind === "chat") {
		appStoreApi.getState().closeChatToHistory(tab.sessionId, projectAreaId, true);
	} else if (tab.kind === "browser") {
		void getTransport()
			.request("browser.panelClose", { panelId: tab.panelId }, { timeoutMs: 10_000 })
			.then(() => {
				appStoreApi.getState().removeBrowserPanelState(tab.panelId);
				appStoreApi.getState().closeTab(tab.id, true, projectAreaId);
			})
			.catch((cause) => toast.error(errorText(cause), "Couldn't close the browser"));
	} else appStoreApi.getState().closeTab(tab.id, true, projectAreaId);
}

function closeSecondary(): void {
	const selection = appStoreApi.getState().workspaceSelection.secondarySelection;
	if (!selection) {
		showSecondarySidebar();
		return;
	}
	showSecondarySidebar();
	if (secondaryTab) closeTab(secondaryTab);
	else appStoreApi.getState().dispatchWorkspaceSelection(clearSecondary());
}

function revealSecondaryArea(area: SecondaryArea): void {
	appStoreApi.getState().dispatchWorkspaceSelection(selectSecondaryAreaAction(area));
	dispatchLayout({ rightCollapsed: false });
}

function selectSecondaryRail(area: SecondaryArea): void {
	const current = appStoreApi.getState().workspaceSelection;
	const active = current.secondaryArea === area;
	revealSecondaryArea(area);
	if (active) dispatchLayout({ rightCollapsed: !current.layout.rightCollapsed });
	showSecondarySidebar();
}

function selectPrimaryRail(area: PrimaryArea, target?: HTMLElement): void {
	const current = appStoreApi.getState().workspaceSelection;
	const active = current.primaryArea === area;
	appStoreApi.getState().dispatchWorkspaceSelection(selectPrimaryAreaAction(area));
	if (active) dispatchLayout({ leftCollapsed: !current.layout.leftCollapsed });
	else dispatchLayout({ leftCollapsed: false });
	if (area === "settings") {
		if (target) openSettingsFrom(target);
		else appStoreApi.getState().openSettings();
	}
	if (area === "schedules" && target) openSettingsFrom(target, SettingsSection.Schedules);
}

function openSettings(event: MouseEvent, section?: SettingsSection): void {
	selectPrimaryRail("settings", event.currentTarget as HTMLElement);
	if (section) appStoreApi.getState().setSettingsSection(section);
}

function openChats(): void {
	const previousSelection = appStoreApi.getState().workspaceSelection.primarySelection;
	const previousChat = selectPrimaryContentTab(contentTabs, previousSelection, projectAreaId);
	selectPrimaryRail("chats");
	if (previousSelection?.kind === "session") {
		if (!previousChat) void hydrateChatResource(projectAreaId, previousSelection.sessionId);
	} else if (!previousChat) startChat();
	showPrimarySurface();
}

function revealProjects(): void {
	selectPrimaryRail("chats");
	mobilePane = "projects";
	queueMicrotask(() => {
		const nav = document.querySelector<HTMLElement>(
			'[data-testid="primary-sidebar"], [data-testid="left-nav"]',
		);
		if (panelHasFocusableContent(nav)) nav?.focus();
		else
			focusFirstVisible('[data-testid="toggle-left-panel"]', '[data-testid="expand-left-panel"]');
	});
}

function collapseLeftPanel(): void {
	dispatchLayout({ leftCollapsed: true });
	if (mobilePane === "projects") mobilePane = "primary";
	queueMicrotask(() =>
		focusFirstVisible('[data-testid="toggle-left-panel"]', '[data-testid="expand-left-panel"]'),
	);
}

function collapseRightPanel(): void {
	dispatchLayout({ rightCollapsed: true });
	if (mobilePane === "secondary") {
		if (hasSecondarySelection) mobileSecondarySurface = "view";
		else mobilePane = "primary";
	}
	queueMicrotask(() =>
		focusFirstVisible('[data-testid="toggle-right-panel"]', '[data-testid="expand-right-panel"]'),
	);
}

function toggleLeftPanel(): void {
	dispatchLayout({ leftCollapsed: !layout.leftCollapsed });
}

function toggleRightPanel(): void {
	dispatchLayout({ rightCollapsed: !layout.rightCollapsed });
}

async function selectProjectArea(project: Project): Promise<void> {
	appStoreApi.getState().selectProject(project.id);
	await enterDefaultProjectArea(project.id);
}

function signOut(): void {
	void logoutController().finally(() => window.dispatchEvent(new Event("pixie-auth-lost")));
}
</script>

{#snippet chatPane(tab: ChatTab)}
	{#if Object.hasOwn(sessionStreaming, tab.sessionId)}
		{#key tab.sessionId}<ErrorBoundary label="chat"><ChatView sessionId={tab.sessionId} {projectAreaId} onOpenChanges={showActivity} /></ErrorBoundary>{/key}
	{:else}
		<div class="app-empty flex flex-1"><p>Restoring chat…</p><Button variant="outline" onclick={() => void hydrateChatResource(projectAreaId, tab.sessionId)}>Retry</Button></div>
	{/if}
{/snippet}

{#snippet previewPane(tab: Extract<ContentTab, { kind: "file" | "diff" | "browser" }>)}
	{#if tab.kind === "browser"}
		{#key tab.panelId}<ErrorBoundary label="browser"><BrowserPanel panelId={tab.panelId} onRestart={() => openBrowserTab(tab)} /></ErrorBoundary>{/key}
	{:else if tab.kind === "file"}
		{#key tab.id}<ErrorBoundary label="preview"><FilePane {tab} /></ErrorBoundary>{/key}
	{:else}
		{#key tab.id}<ErrorBoundary label="preview"><DiffPane {tab} /></ErrorBoundary>{/key}
	{/if}
{/snippet}

{#snippet addTrigger(menuId: string)}
	<Button
		variant="ghost"
		size="icon-sm"
		data-testid="add-project-menu"
		data-dropdown-menu-trigger={menuId}
		aria-haspopup="menu"
		aria-controls={menuId}
		aria-expanded="false"
		aria-label="Add project"
		title="Add project"
	>
		<Icon name="plus" size={16} />
	</Button>
{/snippet}

<div data-testid="project-work-area" class="pixie-work-area">
	<nav aria-label="Mobile panes" data-testid="mobile-pane-navigation" class="tab-list flex shrink-0 border-b lg:hidden">
		<button type="button" data-testid="mobile-projects" class="tab-trigger min-h-11 flex-1 capitalize" aria-pressed={mobilePane === "projects"} onclick={showProjects}>Projects</button>
		<button type="button" data-testid="mobile-primary" class="tab-trigger min-h-11 flex-1 capitalize" aria-pressed={mobilePane === "primary"} onclick={showPrimarySurface}>Primary</button>
		<button type="button" data-testid="mobile-secondary" class="tab-trigger min-h-11 flex-1 capitalize" aria-pressed={mobilePane === "secondary"} onclick={showSecondarySurface}>Secondary</button>
	</nav>
	<div
		data-testid="workspace-grid"
		data-secondary-selection={hasSecondarySelection ? "true" : "false"}
		data-layout-focus={layout.focus}
		style={gridStyle}
		class="pixie-shell-grid"
		bind:this={grid}
	>
		<aside data-testid="primary-rail" data-slot="primary-rail" aria-label="Primary rail" class="pixie-slot pixie-slot-primary-rail hidden lg:flex">
			<ShellRail side="left" label="Primary navigation">
				{#snippet top()}
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="rail-chats"
						aria-label="Chats"
						title="Chats"
						aria-current={primaryArea === "chats" ? "page" : undefined}
						onclick={() => openChats()}
					>
						<Icon name="message-square" size={16} />
					</Button>
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="rail-archive"
						aria-label="Archive"
						title="Archive"
						aria-current={primaryArea === "archive" ? "page" : undefined}
						onclick={() => selectPrimaryRail("archive")}
					>
						<Icon name="archive" size={16} />
					</Button>
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="rail-schedules"
						aria-label="Schedules"
						title="Schedules"
						aria-current={primaryArea === "schedules" ? "page" : undefined}
						onclick={(event) => selectPrimaryRail("schedules", event.currentTarget as HTMLElement)}
					>
						<Icon name="clock-arrow-left" size={16} />
					</Button>
				{/snippet}
				{#snippet bottom()}
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="toggle-left-panel"
						aria-label={layout.leftCollapsed ? "Open primary sidebar" : "Close primary sidebar"}
						title={layout.leftCollapsed ? "Open primary sidebar" : "Close primary sidebar"}
						aria-pressed={!layout.leftCollapsed}
						onclick={toggleLeftPanel}
					>
						<Icon name={layout.leftCollapsed ? "chevron-right" : "chevron-left"} size={16} />
					</Button>
					{#if layout.leftCollapsed}
						<Button variant="ghost" size="icon-sm" data-testid="expand-left-panel" aria-label="Restore primary sidebar" title="Restore primary sidebar" onclick={restoreLeft}><Icon name="archive-restore" size={16} /></Button>
					{/if}
					<Button
						variant="ghost"
						size="icon-sm"
						data-testid="open-settings"
						aria-label="Settings"
						title="Settings"
						aria-current={primaryArea === "settings" ? "page" : undefined}
						onclick={(event) => openSettings(event)}
					>
						<Icon name="settings" size={16} />
					</Button>
					{#if $appStore.authenticationEnabled}
						<Button variant="ghost" size="icon-sm" aria-label="Sign out" title="Sign out" onclick={signOut}>
							<Icon name="log-out" size={16} />
						</Button>
					{/if}
					<span data-testid="connection-status" data-status={$appStore.status} role="status" aria-label={STATUS_LABEL[$appStore.status]} title={STATUS_LABEL[$appStore.status]} class="stat-status inline-flex items-center">
						<span aria-hidden="true" class={`status-dot ${STATUS_DOT[$appStore.status]}`}></span>
						<span class="sr-only">{STATUS_LABEL[$appStore.status]}</span>
					</span>
				{/snippet}
			</ShellRail>
		</aside>

		<aside
			data-testid="primary-sidebar"
			data-slot="primary-sidebar"
			aria-label="Primary sidebar"
			tabindex="-1"
			class={`pixie-slot pixie-slot-primary-sidebar outline-none ${mobilePane === "projects" && primarySidebarVisible ? "flex" : "hidden"} ${primarySidebarVisible ? "lg:flex" : "lg:hidden"}`}
		>
			{#if primarySidebarVisible}
				<div class="pixie-panel-box pixie-panel">
					<nav data-testid="mobile-primary-area-navigation" aria-label="Primary areas" class="tab-list flex shrink-0 border-b lg:hidden">
						<button type="button" data-testid="mobile-area-chats" class="tab-trigger min-h-10 flex-1" aria-current={primaryArea === "chats" ? "page" : undefined} onclick={() => selectPrimaryArea("chats")}>Chats</button>
						<button type="button" data-testid="mobile-area-archive" class="tab-trigger min-h-10 flex-1" aria-current={primaryArea === "archive" ? "page" : undefined} onclick={() => selectPrimaryArea("archive")}>Archive</button>
						<button type="button" data-testid="mobile-area-schedules" class="tab-trigger min-h-10 flex-1" aria-current={primaryArea === "schedules" ? "page" : undefined} onclick={() => selectPrimaryArea("schedules")}>Schedules</button>
						<button type="button" data-testid="mobile-area-settings" class="tab-trigger min-h-10 flex-1" aria-current={primaryArea === "settings" ? "page" : undefined} onclick={() => selectPrimaryArea("settings")}>Settings</button>
					</nav>
					<PanelHeader title={primaryArea === "chats" ? "PROJECTS" : primaryArea.toUpperCase()}>
						{#snippet actions()}
							{#if primaryArea === "chats"}
								<AddProjectMenu
									recentProjects={$appStore.recentProjects}
									onOpen={() => opener?.pickAndOpen()}
									onOpenRecent={(path) => void opener?.openProject(path)}
									trigger={addTrigger}
								/>
								{#if projectArea}<ProjectChatHistory {projectAreaId} />{/if}
								<Button variant="ghost" size="icon-sm" data-testid="toggle-project-filter" aria-label="Filter projects" title="Filter projects" aria-pressed={projectFilterOpen} onclick={() => (projectFilterOpen = !projectFilterOpen)}><Icon name="search" size={16} /></Button>
							{:else if primaryArea === "archive" && projectArea}
								<ProjectChatHistory {projectAreaId} />
							{/if}
							<Button variant="ghost" size="icon-sm" data-testid="collapse-left-panel" aria-label="Close primary sidebar" title="Close primary sidebar" onclick={collapseLeftPanel}><Icon name="chevron-left" size={16} /></Button>
						{/snippet}
					</PanelHeader>
					{#if primaryArea === "chats"}
						{#if projectFilterOpen}
							<div class="shrink-0 px-sm py-xs"><input type="search" aria-label="Filter projects" placeholder="Filter projects" class="input w-full" bind:value={projectFilter} /></div>
						{/if}
						<div data-testid="primary-sidebar-content" class="pixie-panel-scroll scroll-area px-xs py-xs"><ProjectTree chrome="bare" activeSessionId={primarySelection?.kind === "session" ? primarySelection.sessionId : null} filter={projectFilter} /></div>
					{:else if primaryArea === "archive"}
						<div data-testid="archive-sidebar" class="pixie-panel-scroll scroll-area px-sm py-sm">
							<p class="tr-text-metadata text-text-muted">Recently closed chats</p>
							{#if ($appStore.closedChatsByProjectArea[projectAreaId] ?? []).length === 0}
								<p class="mt-xs tr-text-metadata text-text-muted">No closed chats.</p>
							{:else}
								<ul class="tree-group mt-xs flex flex-col">
									{#each $appStore.closedChatsByProjectArea[projectAreaId] ?? [] as chat (chat.sessionId)}
										<li><button type="button" class="tree-leaf w-full text-left tr-text-ui" onclick={() => void appStoreApi.getState().reopenChat(projectAreaId, chat.sessionId)}>{chat.title}</button></li>
									{/each}
								</ul>
							{/if}
						</div>
					{:else if primaryArea === "schedules"}
						<div data-testid="schedules-sidebar" class="pixie-panel-scroll scroll-area px-sm py-sm"><p class="tr-text-metadata text-text-muted">Schedule definitions and run history are available in Settings.</p><Button class="mt-sm" variant="outline" size="sm" onclick={(event) => openSettings(event, SettingsSection.Schedules)}>Open schedules</Button></div>
					{:else}
						<div data-testid="settings-sidebar" class="pixie-panel-scroll scroll-area px-sm py-sm"><p class="tr-text-metadata text-text-muted">Choose a settings section to inspect the current connection and workspace configuration.</p><Button class="mt-sm" variant="outline" size="sm" onclick={(event) => openSettings(event)}>Open settings</Button></div>
					{/if}
				</div>
			{/if}
		</aside>

		<main
			data-testid="primary-view"
			data-slot="primary-view"
			id="main-content"
			aria-label={primaryTitle}
			class={`pixie-slot pixie-slot-primary-view min-w-0 ${mobilePane === "primary" && primaryViewVisible ? "flex" : "hidden"} ${primaryViewVisible ? "lg:flex" : "lg:hidden"}`}
		>
			<div class="pixie-panel pixie-center">
				<PanelHeader title={primaryTitle}>
					{#snippet actions()}
						{#if primaryTab}
							<SessionLifecycleMenu target={{ projectId: projectAreaId, sessionId: primaryTab.sessionId, title: primaryTab.name }} streaming={sessionStreaming[primaryTab.sessionId] === true} />
							<Button variant="ghost" size="icon-sm" data-testid="close-chat" aria-label={`Close ${primaryTab.name}`} title={`Close ${primaryTab.name}`} onclick={() => closeTab(primaryTab)}><Icon name="x" size={14} /></Button>
						{/if}
						<Button variant="ghost" size="sm" data-testid="focus-primary" aria-label="Focus primary view" aria-pressed={layout.focus === "primary"} onclick={() => setFocus("primary")}>Focus</Button>
						<Button variant="ghost" size="sm" data-testid="restore-primary" aria-label="Restore workspace" onclick={restoreLayout}>Restore</Button>
					{/snippet}
				</PanelHeader>
				<div class="pixie-center-content">
					{#if primaryArea === "chats"}
						{#if primaryTab}
							{@render chatPane(primaryTab)}
						{:else if primarySelection?.kind === "session"}
							<div class="app-empty flex flex-1 flex-col items-center justify-center gap-xs px-lg text-center"><p>Restoring chat…</p><Button variant="outline" onclick={() => void hydrateChatResource(projectAreaId, primarySelection.sessionId)}>Retry</Button></div>
						{:else}
							<div data-testid="project-ready" class="app-empty flex flex-1 flex-col gap-xs px-lg text-center">
								<span class="eyebrow">Project ready</span>
								{#if projectArea}<h2 class="max-w-full truncate tr-title-entity">{contextProject?.name ?? projectArea.name}</h2><p class="max-w-full truncate tr-text-metadata text-text-muted">{projectArea.root}</p>{/if}
								<p class="mt-xs tr-text-ui text-text-muted">Files, chats, and discovered repositories are scoped to this project.</p>
								<Button class="mt-xs self-center" data-testid="start-chat" onclick={startChat}><Icon name="message-square-plus" size={16} /> New chat</Button>
							</div>
						{/if}
					{:else if primaryArea === "archive"}
						<div data-testid="archive-detail" class="app-empty flex flex-1 flex-col gap-xs px-lg text-center"><span class="eyebrow">Archive</span><p class="tr-text-ui text-text-muted">Select a recently closed chat from the primary sidebar to restore it.</p></div>
					{:else if primaryArea === "schedules"}
						<div data-testid="schedule-detail" class="app-empty flex flex-1 flex-col gap-xs px-lg text-center"><span class="eyebrow">Schedule details</span><p class="tr-text-ui text-text-muted">Schedule definitions and run history are available in Settings.</p></div>
					{:else}
						<div data-testid="settings-detail" class="app-empty flex flex-1 flex-col gap-xs px-lg text-center"><span class="eyebrow">Settings</span><p class="tr-text-ui text-text-muted">Use the settings sections in the primary sidebar.</p></div>
					{/if}
				</div>
			</div>
		</main>

		{#if hasSecondarySelection}
			<section
				data-testid="secondary-view"
				data-slot="secondary-view"
				aria-label={secondaryTitle}
				class={`pixie-slot pixie-slot-secondary-view min-w-0 ${mobilePane === "secondary" && mobileSecondarySurface === "view" && secondaryViewVisible ? "flex" : "hidden"} ${secondaryViewVisible ? "lg:flex" : "lg:hidden"}`}
			>
				<div class="pixie-panel pixie-secondary-view">
					<PanelHeader title={secondaryTitle}>
						{#snippet actions()}
							<Button variant="ghost" size="sm" class="lg:hidden" data-testid="mobile-secondary-sidebar" aria-label="Open secondary sidebar" onclick={showSecondarySidebar}>Controls</Button>
							<Button variant="ghost" size="sm" data-testid="focus-secondary" aria-label="Focus secondary view" aria-pressed={layout.focus === "secondary"} onclick={() => setFocus("secondary")}>Focus</Button>
							<Button variant="ghost" size="sm" data-testid="restore-secondary" aria-label="Restore workspace" onclick={restoreLayout}>Restore</Button>
							<Button variant="ghost" size="icon-sm" data-testid="close-secondary" aria-label={`Close ${secondaryTitle}`} title={`Close ${secondaryTitle}`} onclick={closeSecondary}><Icon name="x" size={14} /></Button>
						{/snippet}
					</PanelHeader>
					<div class="pixie-center-content">
						{#if secondaryTab}
							{@render previewPane(secondaryTab)}
						{:else}
							<div data-testid="secondary-unavailable" class="app-empty flex flex-1 flex-col items-center justify-center gap-xs px-lg text-center"><p role="alert">The selected secondary resource is unavailable.</p><Button variant="outline" onclick={closeSecondary}>Close</Button></div>
						{/if}
					</div>
				</div>
			</section>
		{/if}

		<aside
			data-testid="secondary-sidebar"
			data-slot="secondary-sidebar"
			aria-label="Secondary sidebar"
			tabindex="-1"
			id="right-panel"
			class={`pixie-slot pixie-slot-secondary-sidebar min-w-0 ${mobilePane === "secondary" && mobileSecondarySurface === "sidebar" && secondarySidebarVisible ? "flex" : "hidden"} ${secondarySidebarVisible ? "lg:flex" : "lg:hidden"}`}
		>
			{#if secondarySidebarVisible}
				<div id="activity-panel" class="pixie-panel-box pixie-panel">
					<PanelHeader title={secondaryArea.toUpperCase()}>
						{#snippet actions()}
							{#if secondaryArea === "files"}
								<Button variant="ghost" size="icon-sm" data-testid="toggle-files-filter" aria-label="Filter files" title="Filter files" aria-pressed={filesFilterOpen} onclick={() => (filesFilterOpen = !filesFilterOpen)}><Icon name="search" size={16} /></Button>
							{/if}
							{#if hasSecondarySelection}<Button variant="ghost" size="sm" class="lg:hidden" data-testid="mobile-secondary-view" aria-label="Return to secondary preview" onclick={showSecondarySurface}>Preview</Button>{/if}
							<Button variant="ghost" size="sm" data-testid="restore-secondary-sidebar" aria-label="Restore workspace" onclick={restoreLayout}>Restore</Button>
							<Button variant="ghost" size="icon-sm" data-testid="collapse-right-panel" aria-label="Close secondary sidebar" title="Close secondary sidebar" onclick={collapseRightPanel}><Icon name="chevron-right" size={16} /></Button>
						{/snippet}
					</PanelHeader>
					{#if secondaryArea === "files"}
						{#if filesFilterOpen}<div class="shrink-0 px-sm py-xs"><input type="search" aria-label="Filter files" placeholder="Filter loaded files" class="input w-full" bind:value={filesFilter} /></div>{/if}
						<div role="tabpanel" aria-label="Files" class="pixie-panel-scroll scroll-area min-h-0 flex-1 px-xs py-xs"><ErrorBoundary label="files activity"><FileTree {projectAreaId} filter={filesFilter} onOpen={showSecondarySurface} /></ErrorBoundary></div>
					{:else if secondaryArea === "git"}
						<div role="tabpanel" aria-label="Git" class="pixie-panel-scroll scroll-area min-h-0 flex-1 px-xs py-xs"><ErrorBoundary label="git activity"><ChangesPanel {projectAreaId} onOpen={showSecondarySurface} /></ErrorBoundary></div>
					{:else if secondaryArea === "details"}
						<div data-testid="details-sidebar" class="pixie-panel-scroll scroll-area px-sm py-sm"><p class="tr-text-metadata text-text-muted">Session details</p>{#if sessionDetailsVisible && primarySelection?.kind === "session"}<p class="mt-xs break-all tr-code-text">{primarySelection.sessionId}</p>{:else}<p class="mt-xs tr-text-metadata text-text-muted">Select a chat to inspect its status and usage.</p>{/if}</div>
					{:else}
						<div data-testid="module-sidebar" class="pixie-panel-scroll scroll-area px-sm py-sm"><p class="tr-text-metadata text-text-muted">{secondaryTitle} controls</p><p class="mt-xs tr-text-metadata text-text-muted">The selected module preview owns its renderer and lifecycle.</p></div>
					{/if}
				</div>
			{/if}
		</aside>

		<aside data-testid="secondary-rail" data-slot="secondary-rail" aria-label="Secondary rail" class="pixie-slot pixie-slot-secondary-rail hidden lg:flex">
			<ShellRail side="right" label="Secondary navigation">
				{#snippet top()}
					<Button variant="ghost" size="icon-sm" data-testid="rail-details" aria-label="Details" title="Details" aria-current={secondaryArea === "details" ? "page" : undefined} onclick={() => selectSecondaryRail("details")}><Icon name="info" size={16} /></Button>
					<Button variant="ghost" size="icon-sm" data-testid="rail-files" aria-label="Files" title="Files" aria-current={secondaryArea === "files" ? "page" : undefined} onclick={() => selectSecondaryRail("files")}><Icon name="folder" size={16} /></Button>
					<Button variant="ghost" size="icon-sm" data-testid="rail-changes" aria-label="Git" title="Git" aria-current={secondaryArea === "git" ? "page" : undefined} onclick={() => selectSecondaryRail("git")}><Icon name="git-branch" size={16} /></Button>
					<Button variant="ghost" size="icon-sm" data-testid="rail-browser" aria-label="Browser" title="Browser" aria-current={secondaryArea === "module:browser" ? "page" : undefined} disabled={!browserPanelAvailable(browserStatus)} onclick={() => { revealSecondaryArea("module:browser"); if (hasSecondarySelection) showSecondarySurface(); else showSecondarySidebar(); }}><Icon name="globe" size={16} /></Button>
				{/snippet}
				{#snippet bottom()}
					<Button variant="ghost" size="icon-sm" data-testid="toggle-right-panel" aria-label={layout.rightCollapsed ? "Open secondary sidebar" : "Close secondary sidebar"} title={layout.rightCollapsed ? "Open secondary sidebar" : "Close secondary sidebar"} aria-pressed={!layout.rightCollapsed} onclick={toggleRightPanel}><Icon name={layout.rightCollapsed ? "chevron-left" : "chevron-right"} size={16} /></Button>
					{#if layout.rightCollapsed}<Button variant="ghost" size="icon-sm" data-testid="expand-right-panel" aria-label="Restore secondary sidebar" title="Restore secondary sidebar" onclick={restoreRight}><Icon name="archive-restore" size={16} /></Button>{/if}
					{#if browserPanelAvailable(browserStatus)}<Button variant="ghost" size="icon-sm" data-testid="open-browser" aria-label="Open browser" title="Open browser" onclick={startBrowser}><Icon name="globe" size={16} /></Button>{/if}
				{/snippet}
			</ShellRail>
		</aside>
		{#if ShellResizer}
			{#if primarySidebarVisible}
				<ShellResizer kind="left" {layout} {grid} onChange={dispatchLayout} />
			{/if}
			{#if hasSecondarySelection && primaryViewVisible && secondaryViewVisible}
				<ShellResizer kind="primary" {layout} {grid} onChange={dispatchLayout} />
			{/if}
			{#if secondarySidebarVisible}
				<ShellResizer kind="right" {layout} {grid} onChange={dispatchLayout} />
			{/if}
		{/if}
	</div>
	<OpenProjectDialogs bind:this={opener} onOpened={selectProjectArea} />
</div>
