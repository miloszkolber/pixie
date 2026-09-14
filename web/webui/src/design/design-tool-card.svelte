<script lang="ts">
import DesignPreview from "./design-preview.svelte";
import type { DesignToolCardViewModel } from "./design-tool-card";

let { view }: { view: DesignToolCardViewModel } = $props();
let stateLabel = $derived(
	view.outcome === "running"
		? "Running"
		: view.outcome === "completed"
			? "Completed"
			: view.outcome === "stale"
				? "Stale"
				: view.outcome === "unavailable"
					? "Unavailable"
					: "Failed",
);
</script>

<article
	class="u-flex u-min-w-0 u-flex-col u-gap-xs"
		data-testid="design-tool-card"
	data-scope="instance"
		aria-label={`${view.label} ${stateLabel.toLowerCase()}`}
>
	<header class="u-flex u-min-w-0 u-items-center u-gap-xs tr-text-metadata">
		<strong class="design-tool-card-label u-truncate">{view.label}</strong>
		<span class="u-shrink-0 u-text-text-muted">{stateLabel}</span>
		{#if view.selectionRevision !== null}<span class="u-shrink-0 u-text-text-muted">focus {view.selectionRevision}</span>{/if}
	</header>
	<p class="tr-text-metadata u-text-text-muted">Instance-wide · {view.message}</p>
	{#if view.preview}<DesignPreview preview={view.preview} />{/if}
	{#if view.warnings.length > 0}
		<ul class="u-flex u-min-w-0 u-flex-col u-gap-0.5" aria-label="Design warnings">
			{#each view.warnings as warning}
				<li class="u-break-words tr-text-metadata u-text-text-muted">{warning}</li>
			{/each}
		</ul>
	{/if}
</article>

<style>
	.design-tool-card-label {
		color: var(--primary);
	}
</style>
