<script lang="ts">
import type { SessionSummary } from "@pixie/contracts";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
import ArchiveSessionDialog from "../../chat/session/archive-session-dialog.svelte";
import RenameSessionDialog from "../../chat/session/rename-session-dialog.svelte";
import type { SessionLifecycleTarget } from "../../chat/session/session-lifecycle";
import { unsupportedLifecycleReason } from "../../chat/session/session-lifecycle";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { activateCheckableMenuItem } from "../../components/menu-keyboard";
import { errorText, getTransport } from "../../connection";
import { relativeTime } from "../../lib";
import { appStore, appStoreApi, toast } from "../../store";
import { openChatInTab } from "../navigation/open-chat";
import { shouldLoadArchivedChats } from "./project-chat-history-state";

interface Props {
	projectAreaId: string;
}
let { projectAreaId }: Props = $props();
let menuOpen = $state(false);
let showArchived = $state(false);
let archived = $state<SessionSummary[]>([]);
let archivedLoading = $state(false);
let archivedError = $state(false);
let restoring = $state<string | null>(null);
let renameTarget = $state<SessionLifecycleTarget | null>(null);
let archiveTarget = $state<SessionLifecycleTarget | null>(null);
let loadSequence = 0;
let menu: HTMLElement;
const componentId = $props.id();
const menuId = `chat-history-${componentId}`;
let connectionStatus = $derived($appStore.status);
let connectionGeneration = $derived($appStore.connectionGeneration);
let closed = $derived($appStore.closedChatsByProjectArea[projectAreaId] ?? []);
let catalogVersion = $derived($appStore.sessionCatalogVersionByProjectArea[projectAreaId] ?? 0);
let canRename = $derived($appStore.agentProfile?.operations.renameSession === true);
let canArchive = $derived($appStore.agentProfile?.operations.archiveSession === true);
let canDelete = $derived($appStore.agentProfile?.operations.deleteSession === true);
let renameUnavailable = $derived(
	canRename ? undefined : unsupportedLifecycleReason($appStore.agentProfile?.name, "renaming"),
);
let archiveUnavailable = $derived(
	canArchive ? undefined : unsupportedLifecycleReason($appStore.agentProfile?.name, "archiving"),
);
let deleteUnavailable = $derived(
	canDelete ? undefined : unsupportedLifecycleReason($appStore.agentProfile?.name, "deleting"),
);
let unavailableActions = $derived(
	[renameUnavailable, archiveUnavailable, deleteUnavailable].filter(
		(reason): reason is string => !!reason,
	),
);

async function loadArchived(): Promise<void> {
	if (!canArchive || !showArchived) return;
	const sequence = ++loadSequence;
	archivedLoading = true;
	try {
		const sessions = await getTransport().request("session.list", {
			projectId: projectAreaId,
			archived: true,
		});
		if (sequence !== loadSequence) return;
		archived = sessions;
		archivedError = false;
	} catch {
		if (sequence === loadSequence) archivedError = true;
	} finally {
		if (sequence === loadSequence) archivedLoading = false;
	}
}

$effect(() => {
	void catalogVersion;
	void connectionGeneration;
	if (shouldLoadArchivedChats(menuOpen, showArchived, connectionStatus, canArchive))
		void loadArchived();
});

$effect(() => {
	if (!canRename) renameTarget = null;
	if (!canArchive) archiveTarget = null;
});

function toggleArchived(): void {
	if (showArchived) {
		loadSequence += 1;
		archivedLoading = false;
		archivedError = false;
	}
	showArchived = !showArchived;
}

function restore(sessionId: string): void {
	if (!canArchive) return;
	restoring = sessionId;
	void getTransport()
		.request("session.unarchive", { projectId: projectAreaId, sessionId })
		.then(() => {
			archived = archived.filter((session) => session.sessionId !== sessionId);
			appStoreApi
				.getState()
				.applySessionLifecycle({ projectId: projectAreaId, sessionId, operation: "unarchived" });
		})
		.catch((cause) => toast.error(errorText(cause), "Couldn't restore the chat"))
		.finally(() => {
			if (restoring === sessionId) restoring = null;
		});
}

function closeMenu(): void {
	menu?.hidePopover();
}
function remove(chat: { sessionId: string; title: string }): void {
	if (!canDelete) return;
	void getTransport()
		.request("session.delete", { projectId: projectAreaId, sessionId: chat.sessionId })
		.then(() => appStoreApi.getState().deleteChat(projectAreaId, chat.sessionId))
		.catch((cause) => {
			const state = appStoreApi.getState();
			if (
				!state.removedProjectAreaIds[projectAreaId] &&
				!state.deletedSessionsByProjectArea[projectAreaId]?.[chat.sessionId]
			) {
				toast.error(errorText(cause), "Couldn't delete the chat");
			}
		});
}
</script>

<span class="contents" {@attach mewa(dropdownBehavior)}>
	<Button
		variant="ghost"
		size="icon-sm"
		data-testid="chat-history"
		data-dropdown-menu-trigger={menuId}
		aria-haspopup="menu"
		aria-controls={menuId}
		aria-expanded="false"
		aria-label="View chat history"
		title="View chat history"
	>
		<Icon name="clock-arrow-left" size={16} />
	</Button>
	<div bind:this={menu} id={menuId} popover="auto" role="menu" class="dropdown-menu-content history-menu" ontoggle={(event) => { menuOpen = event.newState === "open"; }}>
		<div class="dropdown-menu-label">Recently closed</div>
		{#if closed.length === 0}
			<p class="px-sm py-xs text-text-muted tr-text-metadata">No recently closed chats</p>
		{:else}
			{#each closed as chat (chat.sessionId)}
				<div role="group" data-testid="closed-chat-row" class="flex items-center">
					<button type="button" role="menuitem" class="dropdown-menu-item min-w-0 flex-1" data-testid="closed-chat-item" data-session-id={chat.sessionId} onclick={() => { closeMenu(); void openChatInTab(projectAreaId, chat.sessionId); }}>
						<span class="flex-1 truncate">{chat.title}</span><span class="dropdown-menu-shortcut">{relativeTime(chat.closedAt)}</span><Icon name="rotate-ccw" size={14} />
					</button>
					<button type="button" role="menuitem" class="dropdown-menu-item shrink-0 px-xs" disabled={!canRename} title={renameUnavailable ?? "Rename chat"} aria-label={renameUnavailable ? `Rename ${chat.title}: ${renameUnavailable}` : `Rename ${chat.title}`} onclick={() => { closeMenu(); renameTarget = { projectId: projectAreaId, sessionId: chat.sessionId, title: chat.title }; }}><Icon name="pencil" size={14} /></button>
					<button type="button" role="menuitem" class="dropdown-menu-item shrink-0 px-xs" disabled={!canArchive} title={archiveUnavailable ?? "Archive chat"} aria-label={archiveUnavailable ? `Archive ${chat.title}: ${archiveUnavailable}` : `Archive ${chat.title}`} onclick={() => { closeMenu(); archiveTarget = { projectId: projectAreaId, sessionId: chat.sessionId, title: chat.title }; }}><Icon name="archive" size={14} /></button>
					<button type="button" role="menuitem" class="dropdown-menu-item shrink-0 px-xs" data-variant="destructive" data-testid="closed-chat-delete" disabled={!canDelete} title={deleteUnavailable ?? "Move chat to trash"} aria-label={deleteUnavailable ? `Move ${chat.title} to trash: ${deleteUnavailable}` : `Move ${chat.title} to trash`} onclick={() => remove(chat)}><Icon name="trash-2" size={14} /></button>
				</div>
			{/each}
		{/if}
		{#each unavailableActions as reason}<p class="max-w-[18rem] px-sm py-xs text-text-muted tr-text-metadata">{reason}</p>{/each}
		<div class="dropdown-menu-separator"></div>
		<button type="button" role="menuitemcheckbox" aria-checked={showArchived} class="dropdown-menu-item dropdown-menu-check" data-testid="chat-history-archived-toggle" disabled={!canArchive} title={archiveUnavailable} onkeydown={activateCheckableMenuItem} onclick={(event) => { event.preventDefault(); toggleArchived(); }}>
			{showArchived ? "Hide archived chats" : "Show archived chats"}
		</button>
		{#if showArchived && !canArchive}
			<p class="max-w-[18rem] px-sm py-xs text-text-muted tr-text-metadata">{archiveUnavailable}</p>
		{:else if showArchived && archivedLoading}
			<p role="status" aria-live="polite" class="px-sm py-xs text-text-muted tr-text-metadata">Loading archived chats…</p>
		{:else if showArchived && archivedError}
			<button type="button" role="menuitem" class="dropdown-menu-item" aria-label="Retry loading archived chats" onclick={() => void loadArchived()}>Couldn't load archived chats · Retry</button>
		{:else if showArchived && archived.length === 0}
			<p class="px-sm py-xs text-text-muted tr-text-metadata">No archived chats</p>
		{:else if showArchived}
			{#each archived as session (session.sessionId)}
				<button type="button" role="menuitem" class="dropdown-menu-item" disabled={restoring === session.sessionId} aria-label={restoring === session.sessionId ? `Restoring ${session.title}` : `Restore ${session.title}`} onclick={() => restore(session.sessionId)}>
					<span class="min-w-0 flex-1 truncate">{session.title}</span><span class="dropdown-menu-shortcut">{relativeTime(session.updatedAt)}</span><Icon name="archive-restore" size={14} />
				</button>
			{/each}
		{/if}
	</div>
</span>
{#if renameTarget}<RenameSessionDialog target={renameTarget} open onOpenChange={(next) => { if (!next) renameTarget = null; }} />{/if}
{#if archiveTarget}<ArchiveSessionDialog target={archiveTarget} open onOpenChange={(next) => { if (!next) archiveTarget = null; }} />{/if}

<style>
	.history-menu { left: auto; right: anchor(right); min-width: 18rem; max-height: min(32rem, 80vh); overflow: auto; }
</style>
