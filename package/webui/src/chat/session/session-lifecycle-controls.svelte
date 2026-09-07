<script lang="ts">
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore } from "../../store";
import { openChatInTab } from "../../workspace/navigation/open-chat";
import ArchiveSessionDialog from "./archive-session-dialog.svelte";
import RenameSessionDialog from "./rename-session-dialog.svelte";
import {
	forkActionState,
	type SessionLifecycleTarget,
	unsupportedLifecycleReason,
} from "./session-lifecycle";

interface Props {
	target: SessionLifecycleTarget;
	streaming: boolean;
}
let { target, streaming }: Props = $props();
let renameOpen = $state(false);
let archiveOpen = $state(false);
let forkBusy = $state(false);
let forkError = $state<string | null>(null);
let menu: HTMLElement;
const componentId = $props.id();
const menuId = `session-actions-${componentId}`;
let canFork = $derived($appStore.agentProfile?.operations.forkSession === true);
let canRename = $derived($appStore.agentProfile?.operations.renameSession === true);
let canArchive = $derived($appStore.agentProfile?.operations.archiveSession === true);
let forkAction = $derived(
	forkActionState(streaming, forkBusy, canFork, $appStore.agentProfile?.name),
);
let renameUnavailable = $derived(
	canRename ? undefined : unsupportedLifecycleReason($appStore.agentProfile?.name, "renaming"),
);
let archiveUnavailable = $derived(
	canArchive ? undefined : unsupportedLifecycleReason($appStore.agentProfile?.name, "archiving"),
);
let unavailableActions = $derived(
	[
		forkAction.disabled ? forkAction.title : undefined,
		renameUnavailable,
		archiveUnavailable ?? (streaming ? "Stop the running chat before archiving it" : undefined),
	].filter((reason): reason is string => !!reason),
);

$effect(() => {
	if (!canRename) renameOpen = false;
	if (!canArchive) archiveOpen = false;
});

function choose(action: () => void): void {
	menu?.hidePopover();
	action();
}
function fork(): void {
	if (!canFork || forkAction.disabled) return;
	forkBusy = true;
	forkError = null;
	void getTransport()
		.request("session.fork", { projectId: target.projectId, sessionId: target.sessionId })
		.then(async (summary) => {
			menu?.hidePopover();
			await openChatInTab(target.projectId, summary.sessionId);
		})
		.catch((cause) => {
			forkError = errorText(cause);
		})
		.finally(() => {
			forkBusy = false;
		});
}
</script>

<span class="contents" {@attach mewa(dropdownBehavior)}>
	<Button
		variant="ghost"
		size="icon-sm"
		data-dropdown-menu-trigger={menuId}
		aria-haspopup="menu"
		aria-controls={menuId}
		aria-expanded="false"
		aria-label={`Chat actions for ${target.title}`}
	>
		<Icon name="ellipsis" size={14} />
	</Button>
	<div bind:this={menu} id={menuId} popover="auto" role="menu" class="dropdown-menu-content session-menu">
		<button type="button" role="menuitem" class="dropdown-menu-item" data-testid="session-fork" disabled={forkAction.disabled} title={forkAction.title} aria-label={forkAction.title ? `Fork: ${forkAction.title}` : "Fork"} onclick={fork}>
			<Icon name="git-fork" size={14} /> {forkAction.label}
		</button>
		<button type="button" role="menuitem" class="dropdown-menu-item" disabled={!canRename} title={renameUnavailable} aria-label={renameUnavailable ? `Rename: ${renameUnavailable}` : "Rename"} onclick={() => choose(() => (renameOpen = true))}>
			<Icon name="pencil" size={14} /> Rename
		</button>
		<button type="button" role="menuitem" class="dropdown-menu-item" disabled={!canArchive || streaming} title={archiveUnavailable ?? (streaming ? "Stop the running chat before archiving it" : undefined)} onclick={() => choose(() => (archiveOpen = true))}>
			<Icon name="archive" size={14} /> Archive
		</button>
		{#each unavailableActions as reason}<p class="max-w-64 px-sm py-xs text-text-muted tr-text-metadata">{reason}</p>{/each}
		{#if forkError}<p role="alert" class="max-w-64 px-sm py-xs text-feedback-error tr-text-metadata">{forkError}</p>{/if}
	</div>
</span>
<RenameSessionDialog target={target} bind:open={renameOpen} />
<ArchiveSessionDialog target={target} bind:open={archiveOpen} />

<style>
	.session-menu { left: auto; right: anchor(right); }
</style>
