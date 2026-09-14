<script lang="ts">
import Icon from "../../components/icon.svelte";
import type { CompactionState } from "../runtime/types";
import { formatTokens } from "../session/session-stats";

let { status, detail, tokensBefore, tokensAfter, resuming }: CompactionState = $props();
let label = $derived(
	status === "running"
		? "Compacting context…"
		: status === "cancelled"
			? "Compaction cancelled"
			: resuming
				? "Context compacted — resuming…"
				: "Context compacted",
);
let tokens = $derived(
	tokensBefore != null && tokensAfter != null
		? `${formatTokens(tokensBefore)} → ${formatTokens(tokensAfter)} tokens`
		: null,
);
</script>

{#if status === "failed"}
	<div data-testid="compaction-notice" data-status="failed" class="u-flex u-items-start u-gap-sm compaction-error tr-text-ui">
		<Icon name="triangle-alert" size={16} class="compaction-error-icon" /><span class="u-min-w-0 compaction-pre-wrap u-break-words">{detail || "Compaction failed."}</span>
	</div>
{:else}
	<div data-testid="compaction-notice" data-status={status} class="u-flex u-items-center u-justify-center u-gap-sm u-text-text-muted tr-text-metadata">
		<Icon name={status === "running" ? "rotate-cw" : "fold-vertical"} size={12} class={status === "running" ? "compaction-spinning" : ""} />
		<span>{label}</span>{#if tokens}<span>({tokens})</span>{/if}
	</div>
{/if}

<style>
	.compaction-error { border: 1px solid var(--feedback-error-muted); border-radius: var(--radius-md); background: var(--feedback-error-subtle); background-clip: padding-box; padding: var(--space-sm) var(--space-md); color: var(--feedback-error); }
	:global(.compaction-error-icon) { margin-top: var(--space-2xs); flex-shrink: 0; }
	.compaction-pre-wrap { white-space: pre-wrap; }
	:global(.compaction-spinning) { animation: compaction-spin 1s linear infinite; }
	@keyframes compaction-spin { to { transform: rotate(360deg); } }
	@media (prefers-reduced-motion: reduce) { :global(.compaction-spinning) { animation: none; } }
</style>
