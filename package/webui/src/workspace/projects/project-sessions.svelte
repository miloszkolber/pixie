<script lang="ts">
import type { Project, SessionSummary } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { getTransport } from "../../connection";
import { appStore, appStoreApi, chatTabId } from "../../store";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import { openChatInTab } from "../navigation/open-chat";
import {
	SESSION_CATALOG_RECENT_LIMIT,
	displaySessionTitle,
	selectRecentSubset,
	sessionRowAccessibleLabel,
	sortSessionsRecentFirst,
} from "./session-catalog";
import { shortSessionAge } from "./session-age";

const VISIBLE_SESSIONS = SESSION_CATALOG_RECENT_LIMIT;

interface Props {
	project: Project;
	activeSessionId?: string | null;
}

let { project, activeSessionId = null }: Props = $props();
let sessions = $state<SessionSummary[]>([]);
let failed = $state(false);
let expanded = $state(false);
let navigationSequence = 0;
let connectionStatus = $derived($appStore.status);
let connectionGeneration = $derived($appStore.connectionGeneration);
let catalogVersion = $derived($appStore.sessionCatalogVersionByProjectArea[project.id] ?? 0);
// One catalog, recent-first: archived sessions belong to Archive, not Chats.
let orderedSessions = $derived(
	sortSessionsRecentFirst(sessions.filter((session) => session.archived !== true)),
);
// Recent subset pins the selected and running sessions so Show more/Expand
// never hides them behind the concise list.
let subset = $derived(
	selectRecentSubset(orderedSessions, {
		selectedSessionId: activeSessionId,
		expanded,
		limit: VISIBLE_SESSIONS,
	}),
);
let visibleSessions = $derived(subset.visible);
let hiddenCount = $derived(subset.hiddenCount);

$effect(() => {
	void connectionGeneration;
	void catalogVersion;
	if (connectionStatus !== "connected") return;
	let cancelled = false;
	void getTransport()
		.request("session.list", { projectId: project.id, archived: false })
		.then((items) => {
			if (cancelled) return;
			sessions = items;
			failed = false;
		})
		.catch(() => {
			if (!cancelled) failed = true;
		});
	return () => {
		cancelled = true;
	};
});

$effect(() => () => {
	navigationSequence += 1;
});

async function openSession(sessionId: string): Promise<void> {
	const sequence = ++navigationSequence;
	appStoreApi.getState().selectProject(project.id);
	const area = await enterDefaultProjectArea(project.id);
	if (!area || sequence !== navigationSequence) return;
	await openChatInTab(area.id, sessionId, true);
	if (sequence !== navigationSequence) {
		appStoreApi.getState().closeTab(chatTabId(area.id, sessionId), false, area.id);
		return;
	}
	await openChatInTab(area.id, sessionId);
}
</script>

<li class="tree-item flex flex-col gap-2xs">
	<div class="dropdown-menu-label">Sessions</div>
	{#each visibleSessions as session (session.sessionId)}
		{@const active = activeSessionId === session.sessionId}
		<button
			type="button"
			data-testid="project-session-row"
			data-active={active || undefined}
			title={displaySessionTitle(session.title)}
			aria-label={sessionRowAccessibleLabel(session)}
			aria-current={active || undefined}
			class={`tree-leaf min-w-0 tr-text-metadata ${active ? "bg-control-bg-selected" : ""}`}
			onclick={() => void openSession(session.sessionId)}
		>
			{#if session.isStreaming}
				<Icon name="loader-circle" size={12} class="shrink-0 animate-spin motion-reduce:animate-none" />
				<span class="sr-only">Running</span>
			{:else}
				<Icon name="message-square" size={12} class="shrink-0" />
			{/if}
			<span class="min-w-0 flex-1 truncate text-left">{displaySessionTitle(session.title)}</span>
			<span class={`shrink-0 ${active ? "" : "text-text-muted"}`}>{shortSessionAge(session.updatedAt)}</span>
		</button>
	{/each}
	{#if hiddenCount > 0}
		<button
			type="button"
			data-testid="project-sessions-toggle"
			class="tree-leaf tr-text-metadata underline"
			aria-expanded={expanded}
			aria-label={`Show ${hiddenCount} more chats`}
			onclick={() => (expanded = true)}
		>
			<span>Expand</span>
			<span class="shrink-0 text-text-muted">+{hiddenCount}</span>
			<Icon name="chevron-down" size={12} class="shrink-0" />
		</button>
	{/if}
	{#if expanded && orderedSessions.length > VISIBLE_SESSIONS}
		<button
			type="button"
			data-testid="project-sessions-collapse"
			class="tree-leaf tr-text-metadata underline"
			aria-expanded={expanded}
			aria-label="Show fewer chats"
			onclick={() => (expanded = false)}
		>
			<span>Show less</span>
			<Icon name="chevron-up" size={12} class="shrink-0" />
		</button>
	{/if}
	{#if orderedSessions.length === 0 && !failed}
		<span class="px-sm text-text-muted tr-text-metadata">No sessions yet</span>
	{/if}
	{#if failed}
		<span class="px-sm text-feedback-error tr-text-metadata">Couldn't load sessions</span>
	{/if}
</li>
