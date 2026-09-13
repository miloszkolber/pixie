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
	class="flex min-w-0 flex-col gap-xs"
		data-testid="design-tool-card"
	data-scope="instance"
		aria-label={`${view.label} ${stateLabel.toLowerCase()}`}
>
	<header class="flex min-w-0 items-center gap-xs tr-text-metadata">
		<strong class="truncate text-primary">{view.label}</strong>
		<span class="shrink-0 text-text-muted">{stateLabel}</span>
		{#if view.selectionRevision !== null}<span class="shrink-0 text-text-muted">focus {view.selectionRevision}</span>{/if}
	</header>
	<p class="tr-text-metadata text-text-muted">Instance-wide · {view.message}</p>
	{#if view.preview}<DesignPreview preview={view.preview} />{/if}
	{#if view.warnings.length > 0}
		<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Design warnings">
			{#each view.warnings as warning}
				<li class="break-words tr-text-metadata text-text-muted">{warning}</li>
			{/each}
		</ul>
	{/if}
</article>
