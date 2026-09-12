<script lang="ts">
import type { SessionSummary } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, toast } from "../../store";
import {
	applyArchiveRestored,
	buildArchiveRestoreRequest,
	displaySessionTitle,
	isMetadataOnlyArchiveRestore,
	sessionRowAccessibleLabel,
	sortSessionsRecentFirst,
} from "./session-catalog";
import { shortSessionAge } from "./session-age";

interface Props {
	projectAreaId: string;
}

let { projectAreaId }: Props = $props();
let archived = $state<SessionSummary[]>([]);
let loading = $state(false);
let loadFailed = $state(false);
let restoring = $state<string | null>(null);
let connectionStatus = $derived($appStore.status);
let connectionGeneration = $derived($appStore.connectionGeneration);
let catalogVersion = $derived($appStore.sessionCatalogVersionByProjectArea[projectAreaId] ?? 0);
let canArchive = $derived($appStore.agentProfile?.operations.archiveSession === true);
let ordered = $derived(sortSessionsRecentFirst(archived));

async function load(): Promise<void> {
	if (!canArchive || connectionStatus !== "connected") return;
	loading = true;
	loadFailed = false;
	try {
		const items = await getTransport().request("session.list", {
			projectId: projectAreaId,
			archived: true,
		});
		archived = items.filter((session) => session.archived === true);
	} catch {
		loadFailed = true;
	} finally {
		loading = false;
	}
}

$effect(() => {
	void connectionGeneration;
	void catalogVersion;
	void canArchive;
	void load();
});

function restore(sessionId: string): void {
	if (!canArchive || restoring !== null) return;
	const request = buildArchiveRestoreRequest(projectAreaId, sessionId);
	// Metadata-only restore: unarchive keeps the native session identity.
	// Never fork/clone/delete here.
	if (!isMetadataOnlyArchiveRestore(request.method)) return;
	restoring = sessionId;
	void getTransport()
		.request(request.method, request.params)
		.then(() => {
			archived = applyArchiveRestored(archived, sessionId);
			appStoreApi.getState().applySessionLifecycle({
				projectId: projectAreaId,
				sessionId,
				operation: "unarchived",
			});
		})
		.catch((cause) => toast.error(errorText(cause), "Couldn't restore the chat"))
		.finally(() => {
			if (restoring === sessionId) restoring = null;
		});
}
</script>

<div class="flex flex-col gap-2xs">
	<div class="dropdown-menu-label">Archived chats</div>
	<p class="px-sm tr-text-metadata text-text-muted">Archive is Pixie metadata. Restoring keeps the same chat; it never clones it.</p>
	{#if !canArchive}
		<p class="px-sm tr-text-metadata text-text-muted">Archiving is unavailable for this connection.</p>
	{:else if loading}
		<p role="status" class="px-sm py-xs tr-text-metadata text-text-muted">Loading archived chats…</p>
	{:else if loadFailed}
		<button type="button" class="tree-leaf tr-text-metadata underline" onclick={() => void load()}>Couldn't load archived chats · Retry</button>
	{:else if ordered.length === 0}
		<p class="px-sm py-xs tr-text-metadata text-text-muted">No archived chats.</p>
	{:else}
		<ul class="tree-group flex flex-col">
			{#each ordered as session (session.sessionId)}
				<li class="tree-item">
					<div class="flex w-full min-w-0 items-center gap-2xs">
						<span class="min-w-0 flex-1">
							<span class="block truncate tr-text-ui" title={displaySessionTitle(session.title)}>{displaySessionTitle(session.title)}</span>
							<span class="block truncate tr-text-metadata text-text-muted">{sessionRowAccessibleLabel(session)} · {shortSessionAge(session.updatedAt)}</span>
						</span>
						{#if session.isStreaming}
							<Icon name="loader-circle" size={12} class="shrink-0 animate-spin motion-reduce:animate-none" />
							<span class="sr-only">Running</span>
						{/if}
						<button
							type="button"
							data-testid="archive-restore"
							data-session-id={session.sessionId}
							class="tree-leaf shrink-0 tr-text-metadata underline"
							disabled={restoring === session.sessionId}
							aria-label={restoring === session.sessionId
								? `Restoring ${displaySessionTitle(session.title)}`
								: `Restore ${displaySessionTitle(session.title)}`}
							onclick={() => restore(session.sessionId)}
						>
							<Icon name="archive-restore" size={14} />
							<span>{restoring === session.sessionId ? "Restoring…" : "Restore"}</span>
						</button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</div>
