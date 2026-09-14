<script lang="ts">
import type { SessionStats } from "@pixie/shared";
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
	class="u-flex u-shrink-0 u-flex-wrap u-items-center u-gap-xs chat-toolbar u-px-sm u-py-xs"
>
	<div class="u-flex u-min-w-0 u-flex-1 u-flex-wrap u-items-center u-gap-xs">{@render left?.()}</div>
	<div class="u-flex u-min-w-0 u-flex-wrap u-items-center chat-toolbar-end u-gap-md">

		<SessionStatsBar {stats} />
	</div>
</div>

<style>
	.chat-toolbar { min-height: var(--panel-header-row-height); border-bottom: 1px solid var(--border-muted); background: var(--container-project-bg); }
	.chat-toolbar-end { justify-content: end; }
</style>
