<script lang="ts">
import type { SessionStats } from "@pixie/contracts";
import type { Snippet } from "svelte";
import SessionStatsBar from "./session-stats-bar.svelte";

interface Props {
	stats: SessionStats | null;
	left?: Snippet;
}

let { stats, left }: Props = $props();
</script>

<div
	data-testid="chat-toolbar"
	class="flex min-h-panel-header-row shrink-0 flex-wrap items-center gap-xs border-border-muted border-b bg-container-project-bg px-sm py-xs"
>
	<div class="flex min-w-0 flex-1 flex-wrap items-center gap-xs">{@render left?.()}</div>
	<div class="flex min-w-0 flex-wrap items-center justify-end gap-md">

		<SessionStatsBar {stats} />
	</div>
</div>
