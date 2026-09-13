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
	class="flex min-w-0 flex-col gap-xs"
		data-testid="canvas-tool-card"
		aria-label={`${view.label} ${stateLabel.toLowerCase()}`}
>
	<header class="flex min-w-0 items-center gap-xs tr-text-metadata">
		<strong class="truncate text-primary">{view.label}</strong>
		<span class="shrink-0 text-text-muted">{stateLabel}</span>
		{#if view.version !== null}<span class="shrink-0 text-text-muted">v{view.version}</span>{/if}
	</header>
	<p class="tr-text-metadata text-text-muted">{view.message}</p>
	{#if view.currentVersion !== null && view.currentVersion !== view.version}
		<p class="tr-text-metadata text-text-muted">Current version: {view.currentVersion}. Refresh before retrying.</p>
	{/if}
	{#if view.preview}<CanvasPreview preview={view.preview} />{/if}
	{#if view.warnings.length > 0}
		<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Canvas warnings">
			{#each view.warnings as warning}
				<li class="break-words tr-text-metadata text-text-muted">{warning}</li>
			{/each}
		</ul>
	{/if}
</article>
