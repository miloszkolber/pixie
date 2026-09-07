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
	<div data-testid="compaction-notice" data-status="failed" class="flex items-start gap-sm rounded-[var(--radius-md)] border border-feedback-error-muted bg-clip-padding bg-feedback-error-subtle px-md py-sm text-feedback-error tr-text-ui">
		<Icon name="triangle-alert" size={16} class="mt-0.5 shrink-0" /><span class="min-w-0 whitespace-pre-wrap break-words">{detail || "Compaction failed."}</span>
	</div>
{:else}
	<div data-testid="compaction-notice" data-status={status} class="flex items-center justify-center gap-sm text-text-muted tr-text-metadata">
		<Icon name={status === "running" ? "rotate-cw" : "fold-vertical"} size={12} class={status === "running" ? "animate-spin" : ""} />
		<span>{label}</span>{#if tokens}<span>({tokens})</span>{/if}
	</div>
{/if}
