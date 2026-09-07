<script lang="ts">
import type { RuntimeStatusReport } from "@pixie/contracts";
import ChatView from "../../chat/chat-view.svelte";
import SessionLifecycleMenu from "../../chat/session/session-lifecycle-controls.svelte";
import Button from "../../components/button.svelte";
import ErrorBoundary from "../../components/error-boundary.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import ChangesPanel from "../../files/changes/changes-panel.svelte";
import DiffPane from "../../files/changes/diff-pane.svelte";
import FilePane from "../../files/tabs/file-pane.svelte";
import FileTree from "../../files/tree/file-tree.svelte";
import {
	appStore,
	appStoreApi,
	type ContentTab,
	selectActiveContentTab,
	selectContextProject,
	selectProjectAreaById,
	toast,
} from "../../store";
import BrowserPanel from "../browser/browser-panel.svelte";
import {
	hydrateChatResource,
	initProjectAreaChatReconciliation,
} from "../navigation/chat-reconciliation";
import ProjectChatHistory from "../projects/project-chat-history.svelte";
import ProjectTree from "../projects/project-tree.svelte";
import {
	browserPanelAvailable,
	browserRestartTargetOpen,
	claimBrowserRestart,
	selectTabSessionStreaming,
} from "./project-work-area-state";

type Activity = "files" | "changes";
interface Props {
	projectAreaId: string;
}
let { projectAreaId }: Props = $props();
let mobilePane = $state<"projects" | "content" | "activity">("content");
let browserStatus = $state<RuntimeStatusReport | null>(null);
let previousTabs: ContentTab[] = [];
const browserRestartsInFlight = new Set<string>();

let projectArea = $derived(selectProjectAreaById($appStore, projectAreaId));
let contextProject = $derived(selectContextProject($appStore));
let contentTabs = $derived($appStore.tabsByProjectArea[projectAreaId] ?? []);
let previewTabId = $derived($appStore.previewTabByProjectArea[projectAreaId] ?? null);
let activeTab = $derived(selectActiveContentTab($appStore, projectAreaId));
let sessionStreaming = $derived(selectTabSessionStreaming($appStore, projectAreaId));
let activity = $derived(
	($appStore.activeActivityByProjectArea[projectAreaId] ?? "files") as Activity,
);
let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let removed = $derived($appStore.removedProjectAreaIds[projectAreaId] === true);

$effect(() => initProjectAreaChatReconciliation(projectAreaId));

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

function showContent(): void {
	if (mobilePane === "activity") mobilePane = "content";
}

function showActivity(): void {
	mobilePane = "activity";
}

function startChat(): void {
	void getTransport()
		.request("session.create", {
			projectId: projectAreaId,
			...(projectArea?.root ? { cwd: projectArea.root } : {}),
		})
		.then(({ sessionId, model, thinkingLevel, commands }) => {
			appStoreApi.getState().openChatSession(projectAreaId, sessionId, model, thinkingLevel);
			appStoreApi.getState().setCommands(sessionId, commands);
		})
		.catch((cause) => {
			if (!appStoreApi.getState().removedProjectAreaIds[projectAreaId]) {
				toast.error(errorText(cause), "Couldn't start the chat");
			}
		});
}

async function openBrowserTab(replacing?: Extract<ContentTab, { kind: "browser" }>): Promise<void> {
	const restartTabId = replacing?.id;
	if (replacing) {
		const current = appStoreApi.getState();
		if (!browserRestartTargetOpen(current.tabsByProjectArea[projectAreaId], replacing)) return;
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
		if (
			state.removedProjectAreaIds[projectAreaId] ||
			(replacing && !browserRestartTargetOpen(state.tabsByProjectArea[projectAreaId], replacing))
		) {
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
	} catch (cause) {
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
		appStoreApi.getState().closeChatToHistory(tab.sessionId, projectAreaId, false);
	} else if (tab.kind === "browser") {
		void getTransport()
			.request("browser.panelClose", { panelId: tab.panelId }, { timeoutMs: 10_000 })
			.then(() => {
				appStoreApi.getState().removeBrowserPanelState(tab.panelId);
				appStoreApi.getState().closeTab(tab.id, false, projectAreaId);
			})
			.catch((cause) => toast.error(errorText(cause), "Couldn't close the browser"));
	} else appStoreApi.getState().closeTab(tab.id, false, projectAreaId);
}

function selectActivity(item: Activity, event?: KeyboardEvent): void {
	const tabList = (event?.currentTarget as HTMLElement | null)?.parentElement;
	appStoreApi.getState().setActiveActivity(projectAreaId, item);
	if (event) {
		queueMicrotask(() => tabList?.querySelector<HTMLElement>(`[data-activity="${item}"]`)?.focus());
	}
}

function activityLabel(item: Activity): string {
	return item.slice(0, 1).toUpperCase() + item.slice(1);
}
</script>

<div data-testid="project-work-area" class="flex h-full min-h-0 min-w-0 flex-col lg:flex-row">
	<nav aria-label="Mobile panes" class="tab-list flex shrink-0 border-b lg:hidden">
		{#each ["projects", "content", "activity"] as pane}
			<button type="button" class="tab-trigger min-h-11 flex-1 capitalize" aria-pressed={mobilePane === pane} onclick={() => (mobilePane = pane as typeof mobilePane)}>{pane}</button>
		{/each}
	</nav>
	<aside aria-label="Projects" data-testid="left-nav" tabindex="-1" class={`${mobilePane === "projects" ? "block" : "hidden"} app-sidebar min-h-0 flex-1 overflow-auto p-md outline-none lg:block lg:w-[clamp(12rem,20vw,16rem)] lg:flex-none lg:border-r`}>
		<ProjectTree />
	</aside>
	<div class={`${mobilePane !== "projects" ? "flex" : "hidden"} min-h-0 min-w-0 flex-1 flex-col lg:flex`}>
		<div role="region" aria-label="Open content" class={`${mobilePane === "content" ? "flex" : "hidden"} toolbar min-h-10 shrink-0 items-center gap-xs border-b px-xs lg:flex`}>
			<div class="tab-list flex min-w-0 flex-1 items-center gap-0 overflow-x-auto" role="toolbar" aria-label="Open tabs">
				{#each contentTabs as tab (tab.id)}
					<div data-testid="content-tab" data-kind={tab.kind} data-active={activeTab?.id === tab.id ? "true" : "false"} data-preview={previewTabId === tab.id ? "true" : "false"} class="flex shrink-0 items-center border-r">
						<button type="button" class={`tab-trigger max-w-[12rem] truncate ${previewTabId === tab.id ? "italic" : "not-italic"}`} aria-pressed={activeTab?.id === tab.id} onclick={() => appStoreApi.getState().setActiveTab(tab.id, "keep")}>{tab.name}</button>
						{#if tab.kind === "chat"}
							<SessionLifecycleMenu target={{ projectId: projectAreaId, sessionId: tab.sessionId, title: tab.name }} streaming={sessionStreaming[tab.sessionId] === true} />
						{/if}
						<Button variant="ghost" size="icon-sm" data-testid="content-tab-close" aria-label={`Close ${tab.name}`} onclick={() => closeTab(tab)}><Icon name="x" size={12} /></Button>
					</div>
				{/each}
			</div>
			<ProjectChatHistory {projectAreaId} />
			{#if browserPanelAvailable(browserStatus)}
				<Button variant="ghost" size="icon-sm" data-testid="open-browser" aria-label="Open browser" title="Open browser" onclick={startBrowser}><Icon name="globe" size={16} /></Button>
			{/if}
			<Button variant="ghost" size="icon-sm" data-testid="new-chat" aria-label="New chat" title="New chat" onclick={startChat}><Icon name="message-square-plus" size={16} /></Button>
		</div>
		<div class="flex min-h-0 flex-1">
			<main data-testid="primary-content" aria-label={activeTab?.name ?? "Project content"} class={`${mobilePane === "content" ? "block" : "hidden"} app-content min-h-0 min-w-0 flex-1 lg:block`}>
				{#if !activeTab}
					<div data-testid="project-ready" class="app-empty flex h-full flex-col gap-xs px-lg text-center">
						<span class="eyebrow">Project ready</span>
						{#if projectArea}
							<h2 class="max-w-full truncate tr-title-entity">{contextProject?.name ?? projectArea.name}</h2>
							<p class="max-w-full truncate tr-text-metadata text-text-muted">{projectArea.root}</p>
						{/if}
						<p class="mt-xs tr-text-ui text-text-muted">Files, chats, and discovered repositories are scoped to this project.</p>
						<Button class="mt-xs" data-testid="start-chat" onclick={startChat}><Icon name="message-square-plus" size={16} /> New chat</Button>
					</div>
				{:else if activeTab.kind === "chat"}
					{#if Object.hasOwn(sessionStreaming, activeTab.sessionId)}
						{#key activeTab.sessionId}<ErrorBoundary label="chat"><ChatView sessionId={activeTab.sessionId} {projectAreaId} onOpenChanges={showActivity} /></ErrorBoundary>{/key}
					{:else}
						<div class="app-empty h-full"><p>Restoring chat…</p><Button variant="outline" onclick={() => void hydrateChatResource(projectAreaId, activeTab.sessionId)}>Retry</Button></div>
					{/if}
				{:else if activeTab.kind === "browser"}
					{#key activeTab.panelId}<ErrorBoundary label="browser"><BrowserPanel panelId={activeTab.panelId} onRestart={() => openBrowserTab(activeTab)} /></ErrorBoundary>{/key}
				{:else if activeTab.kind === "file"}
					{#key activeTab.id}<ErrorBoundary label="preview"><FilePane tab={activeTab} /></ErrorBoundary>{/key}
				{:else}
					{#key activeTab.id}<ErrorBoundary label="preview"><DiffPane tab={activeTab} /></ErrorBoundary>{/key}
				{/if}
			</main>
			<aside aria-label="Project activity" class={`${mobilePane === "activity" ? "flex" : "hidden"} app-sidebar min-h-0 flex-1 flex-col lg:flex lg:w-[clamp(14rem,26vw,22rem)] lg:flex-none lg:border-l`}>
				<div id="activity-tabs" data-testid="activity-tabs" tabindex="-1" role="tablist" aria-label="Project activities" class="tab-list flex shrink-0 border-b">
					{#each ["files", "changes"] as item}
						<button
							type="button"
							data-testid={`tab-${item}`}
							role="tab"
							aria-selected={activity === item}
							aria-controls="activity-panel"
							tabindex={activity === item ? 0 : -1}
							data-activity={item}
							class="tab-trigger min-h-11 flex-1 lg:min-h-0"
							onkeydown={(event) => {
								if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
								event.preventDefault();
								selectActivity(item === "files" ? "changes" : "files", event);
							}}
							onclick={() => selectActivity(item as Activity)}
						>{activityLabel(item as Activity)}</button>
					{/each}
				</div>
			<div id="activity-panel" role="tabpanel" class="min-h-0 flex-1 overflow-auto">
				{#key `${projectAreaId}:${activity}`}
					<ErrorBoundary label={`${activity} activity`}>
						{#if activity === "files"}<FileTree {projectAreaId} onOpen={showContent} />{:else}<ChangesPanel {projectAreaId} onOpen={showContent} />{/if}
					</ErrorBoundary>
				{/key}
			</div>
			</aside>
		</div>
	</div>
</div>
