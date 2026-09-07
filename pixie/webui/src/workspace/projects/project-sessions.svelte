<script lang="ts">
import type { Project, SessionSummary } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { getTransport } from "../../connection";
import { appStore, appStoreApi, chatTabId } from "../../store";
import { enterDefaultProjectArea } from "../navigation/default-project-area";
import { openChatInTab } from "../navigation/open-chat";

interface Props {
	project: Project;
}
let { project }: Props = $props();
let sessions = $state<SessionSummary[]>([]);
let failed = $state(false);
let navigationSequence = 0;
let connectionStatus = $derived($appStore.status);
let connectionGeneration = $derived($appStore.connectionGeneration);
let catalogVersion = $derived($appStore.sessionCatalogVersionByProjectArea[project.id] ?? 0);

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
	{#each sessions as session (session.sessionId)}
		<button
			type="button"
			data-testid="project-session-row"
			title={session.title}
			class="tree-leaf tr-text-metadata"
			onclick={() => void openSession(session.sessionId)}
		>
			<Icon name="message-square" size={12} />
			<span class="truncate">{session.title}</span>
		</button>
	{/each}
	{#if sessions.length === 0 && !failed}
		<span class="px-sm text-text-muted tr-text-metadata">No sessions yet</span>
	{/if}
	{#if failed}
		<span class="px-sm text-feedback-error tr-text-metadata">Couldn't load sessions</span>
	{/if}
</li>
