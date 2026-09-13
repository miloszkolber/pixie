<script lang="ts">
import type { CanvasPreviewMetadata } from "./canvas-model";

type Props = {
	preview: CanvasPreviewMetadata;
	class?: string;
};

let { preview, class: className = "" }: Props = $props();
let heading = $derived(
	preview.version === null ? "Canvas preview" : `Canvas preview · version ${preview.version}`,
);
let imageUrl = $derived(preview.url ?? preview.artifactUrl ?? null);
</script>

<figure class={`flex min-w-0 flex-col gap-xs ${className}`} data-testid="canvas-preview">
	<figcaption class="tr-text-metadata text-text-muted">{heading}</figcaption>
	{#if preview.status === "ready" && imageUrl}
		<img
			src={imageUrl}
			alt={preview.alt}
			loading="lazy"
			decoding="async"
			class="max-h-[28rem] max-w-full rounded-[var(--radius-sm)] border border-border-default object-contain"
		/>
	{:else if preview.status === "pending"}
		<p data-testid="canvas-preview-pending" role="status" class="tr-text-metadata text-text-muted">Rendering raster preview…</p>
	{:else if preview.status === "stale"}
		<p data-testid="canvas-preview-stale" role="status" class="tr-text-metadata text-text-muted">Preview is stale while a newer version renders.</p>
	{:else if preview.status === "error"}
		<p role="alert" class="tr-text-metadata text-feedback-error">{preview.reason ?? "Preview failed."}</p>
	{:else if preview.status === "unavailable"}
		<p role="status" class="tr-text-metadata text-text-muted">{preview.reason ?? "Raster preview unavailable."}</p>
	{:else}
		<p role="status" class="tr-text-metadata text-text-muted">No Canvas preview yet.</p>
	{/if}
</figure>
