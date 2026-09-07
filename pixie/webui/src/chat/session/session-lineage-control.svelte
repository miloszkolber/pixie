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
		class="flex min-w-0 shrink items-center gap-2xs rounded-[var(--radius-sm)] px-xs py-2xs text-text-muted tr-text-metadata hover:bg-control-bg-hovered hover:text-text-default disabled:cursor-not-allowed disabled:opacity-70"
	>
		<Icon name="git-fork" size={12} class="size-3 shrink-0" />
		<span class="truncate">Forked from chat</span>
	</button>
{/if}
