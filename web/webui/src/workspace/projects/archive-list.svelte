<script lang="ts">
import type { SessionSummary } from "@pixie/shared";
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

<div class="u-flex u-flex-col u-gap-2xs">
	<div class="dropdown-menu-label">Archived chats</div>
	<p class="u-px-sm tr-text-metadata u-text-text-muted">Archive is Pixie metadata. Restoring keeps the same chat; it never clones it.</p>
	{#if !canArchive}
		<p class="u-px-sm tr-text-metadata u-text-text-muted">Archiving is unavailable for this connection.</p>
	{:else if loading}
		<p role="status" class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">Loading archived chats…</p>
	{:else if loadFailed}
		<button type="button" class="tree-leaf tr-text-metadata archive-list__retry" onclick={() => void load()}>Couldn't load archived chats · Retry</button>
	{:else if ordered.length === 0}
		<p class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">No archived chats.</p>
	{:else}
		<ul class="tree-group u-flex u-flex-col">
			{#each ordered as session (session.sessionId)}
				<li class="tree-item">
					<div class="u-flex u-w-full u-min-w-0 u-items-center u-gap-2xs">
						<span class="u-min-w-0 u-flex-1">
							<span class="archive-list__line u-truncate tr-text-ui" title={displaySessionTitle(session.title)}>{displaySessionTitle(session.title)}</span>
							<span class="archive-list__line u-truncate tr-text-metadata u-text-text-muted">{sessionRowAccessibleLabel(session)} · {shortSessionAge(session.updatedAt)}</span>
						</span>
						{#if session.isStreaming}
							<span class="archive-list__spinner u-inline-flex u-shrink-0"><Icon name="loader-circle" size={12} /></span>
							<span class="u-sr-only">Running</span>
						{/if}
						<button
							type="button"
							data-testid="archive-restore"
							data-session-id={session.sessionId}
							class="tree-leaf u-shrink-0 tr-text-metadata archive-list__restore"
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

<style>
	.archive-list__line {
		display: block;
	}

	.archive-list__retry,
	.archive-list__restore {
		text-decoration: underline;
	}

	.archive-list__spinner {
		animation: archive-list-spin 1s linear infinite;
	}

	@keyframes archive-list-spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.archive-list__spinner {
			animation: none;
		}
	}
</style>
