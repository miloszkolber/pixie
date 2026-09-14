<script lang="ts">
import CanvasPreview from "./canvas-preview.svelte";
import type { CanvasToolCardViewModel } from "./canvas-tool-card";

let { view }: { view: CanvasToolCardViewModel } = $props();
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
		data-testid="canvas-tool-card"
		aria-label={`${view.label} ${stateLabel.toLowerCase()}`}
>
	<header class="u-flex u-min-w-0 u-items-center u-gap-xs tr-text-metadata">
		<strong class="canvas-tool-card-label u-truncate">{view.label}</strong>
		<span class="u-shrink-0 u-text-text-muted">{stateLabel}</span>
		{#if view.version !== null}<span class="u-shrink-0 u-text-text-muted">v{view.version}</span>{/if}
	</header>
	<p class="tr-text-metadata u-text-text-muted">{view.message}</p>
	{#if view.currentVersion !== null && view.currentVersion !== view.version}
		<p class="tr-text-metadata u-text-text-muted">Current version: {view.currentVersion}. Refresh before retrying.</p>
	{/if}
	{#if view.preview}<CanvasPreview preview={view.preview} />{/if}
	{#if view.warnings.length > 0}
		<ul class="u-flex u-min-w-0 u-flex-col u-gap-0.5" aria-label="Canvas warnings">
			{#each view.warnings as warning}
				<li class="u-break-words tr-text-metadata u-text-text-muted">{warning}</li>
			{/each}
		</ul>
	{/if}
</article>

<style>
	.canvas-tool-card-label {
		color: var(--primary);
	}
</style>
