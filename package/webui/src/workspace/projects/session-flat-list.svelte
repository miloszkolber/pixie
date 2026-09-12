<script lang="ts">
import type { Project, SessionSummary } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { getTransport } from "../../connection";
import { appStore, appStoreApi, chatTabId } from "../../store";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import { openChatInTab } from "../navigation/open-chat";
import {
	buildSessionCatalog,
	displaySessionTitle,
	sessionHostKey,
	sessionRowAccessibleLabel,
} from "./session-catalog";
import { shortSessionAge } from "./session-age";

interface Props {
	projects: Project[];
	activeSessionId?: string | null;
}

let { projects, activeSessionId = null }: Props = $props();
let sessionsByProject = $state<Record<string, SessionSummary[]>>({});
// Host/session-keyed ungrouped metadata. Empty against the current
// project-required `session.list` transport; future host metadata populates
// it without a hidden all-files project.
let ungroupedSessions = $state<SessionSummary[]>([]);
let failed = $state(false);
let navigationSequence = 0;
let connectionStatus = $derived($appStore.status);
let connectionGeneration = $derived($appStore.connectionGeneration);
let catalogVersions = $derived($appStore.sessionCatalogVersionByProjectArea);
let projectIds = $derived(projects.map((project) => project.id).join("\0"));

let catalog = $derived(buildSessionCatalog(projects, sessionsByProject, ungroupedSessions));
let projectNameBySession = $derived.by(() => {
	const names = new Map<string, string>();
	for (const group of catalog.groups) {
		for (const session of group.sessions) names.set(sessionHostKey(session), group.project.name);
	}
	return names;
});

$effect(() => {
	void connectionGeneration;
	void catalogVersions;
	void projectIds;
	if (connectionStatus !== "connected") return;
	if (projects.length === 0) {
		sessionsByProject = {};
		ungroupedSessions = [];
		failed = false;
		return;
	}
	let cancelled = false;
	void Promise.all(
		projects.map((project) =>
			getTransport()
				.request("session.list", { projectId: project.id, archived: false })
				.then((items) => ({ projectId: project.id, items }))
				.catch(() => ({ projectId: project.id, items: null as SessionSummary[] | null })),
		),
	).then((results) => {
		if (cancelled) return;
		const next: Record<string, SessionSummary[]> = {};
		let anyFailed = false;
		for (const result of results) {
			if (result.items === null) anyFailed = true;
			else next[result.projectId] = result.items;
		}
		sessionsByProject = next;
		failed = anyFailed && catalog.flat.length === 0;
	});
	return () => {
		cancelled = true;
	};
});

$effect(() => () => {
	navigationSequence += 1;
});

async function openSession(projectId: string, sessionId: string): Promise<void> {
	const sequence = ++navigationSequence;
	appStoreApi.getState().selectProject(projectId);
	const area = await enterDefaultProjectArea(projectId);
	if (!area || sequence !== navigationSequence) return;
	await openChatInTab(area.id, sessionId, true);
	if (sequence !== navigationSequence) {
		appStoreApi.getState().closeTab(chatTabId(area.id, sessionId), false, area.id);
		return;
	}
	await openChatInTab(area.id, sessionId);
}
</script>

<div class="flex flex-col gap-sm">
	<ul data-testid="session-catalog-flat" class="tree-group flex flex-col gap-2xs">
		{#each catalog.flat as session (sessionHostKey(session))}
			{@const active = activeSessionId === session.sessionId}
			{@const projectName = projectNameBySession.get(sessionHostKey(session)) ?? "Ungrouped"}
			{@const projectKey = session.projectId ?? ""}
			<li class="tree-item">
				<button
					type="button"
					data-testid="catalog-flat-row"
					data-active={active || undefined}
					data-project-id={projectKey}
					title={displaySessionTitle(session.title)}
					aria-label={`${sessionRowAccessibleLabel(session)} in ${projectName}`}
					aria-current={active || undefined}
					class={`tree-leaf min-w-0 tr-text-metadata ${active ? "bg-control-bg-selected" : ""}`}
					onclick={() => void openSession(projectKey, session.sessionId)}
				>
					{#if session.isStreaming}
						<Icon name="loader-circle" size={12} class="shrink-0 animate-spin motion-reduce:animate-none" />
						<span class="sr-only">Running</span>
					{:else}
						<Icon name="message-square" size={12} class="shrink-0" />
					{/if}
					<span class="min-w-0 flex-1 truncate text-left">{displaySessionTitle(session.title)}</span>
					<span class="shrink-0 text-text-muted">{projectName}</span>
					<span class={`shrink-0 ${active ? "" : "text-text-muted"}`}>{shortSessionAge(session.updatedAt)}</span>
				</button>
			</li>
		{/each}
	</ul>
	{#if catalog.flat.length === 0 && !failed}
		<p class="px-sm py-xs tr-text-metadata text-text-muted">No chats yet.</p>
	{/if}
	{#if failed}
		<p class="px-sm text-feedback-error tr-text-metadata">Couldn't load sessions</p>
	{/if}
	{#if catalog.ungrouped.length > 0}
		<section data-testid="ungrouped-sessions" aria-label="Ungrouped chats" class="flex flex-col gap-2xs">
			<div class="dropdown-menu-label">Ungrouped</div>
			<p class="px-sm tr-text-metadata text-text-muted">Chats without a named project stay here; they are never moved into a hidden catch-all project.</p>
		</section>
	{/if}
</div>
