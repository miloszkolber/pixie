<script lang="ts">
import Icon from "../../components/icon.svelte";
import { openChatInTab } from "../../workspace/navigation/open-chat";

interface Props {
	projectAreaId: string;
	parentSessionId?: string | undefined;
	parentDeleted: boolean;
}

let { projectAreaId, parentSessionId, parentDeleted }: Props = $props();

function openParent(): void {
	if (!parentSessionId) return;
	void openChatInTab(projectAreaId, parentSessionId);
}
</script>

{#if parentSessionId}
	<button
		type="button"
		disabled={parentDeleted}
		aria-label={parentDeleted ? "Forked from an unavailable chat" : "Open parent chat"}
		title={parentDeleted ? "Parent chat is unavailable" : "Open parent chat"}
		onclick={openParent}
		class="u-flex u-min-w-0 session-lineage-control u-items-center u-gap-2xs u-rounded u-px-xs session-lineage-padding u-text-text-muted tr-text-metadata"
	>
		<Icon name="git-fork" size={12} class="session-lineage-icon u-shrink-0" />
		<span class="u-truncate">Forked from chat</span>
	</button>
{/if}

<style>
	.session-lineage-control { flex-shrink: 1; border: 0; background: transparent; }
	.session-lineage-control:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	.session-lineage-control:disabled { cursor: not-allowed; opacity: .7; }
	.session-lineage-padding { padding-block: var(--space-2xs); }
	:global(.session-lineage-icon) { width: var(--space-lg); height: var(--space-lg); }
</style>
