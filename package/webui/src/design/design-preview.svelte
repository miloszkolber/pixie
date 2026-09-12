<script lang="ts">
import type { DesignPreviewMetadata } from "./design-model";

type Props = {
	preview: DesignPreviewMetadata;
	class?: string;
};

let { preview, class: className = "" }: Props = $props();
let imageUrl = $derived(preview.url ?? preview.artifactUrl ?? null);
</script>

<figure class={`flex min-w-0 flex-col gap-xs ${className}`} data-testid="design-preview">
	<figcaption class="tr-text-metadata text-text-muted">{preview.label}</figcaption>
	{#if preview.status === "ready" && imageUrl}
		<img
			src={imageUrl}
			alt={preview.kind === "cover" ? "Document thumbnail" : "Design frame preview"}
			loading="lazy"
			decoding="async"
			class="max-h-[28rem] max-w-full rounded-[var(--radius-sm)] border border-border-default object-contain"
		/>
	{:else if preview.kind === "frame"}
		<p data-testid="design-frame-preview-unavailable" role="status" class="tr-text-metadata text-text-muted">Frame preview unavailable. Structure and text remain available.</p>
	{:else if preview.status === "pending"}
		<p role="status" class="tr-text-metadata text-text-muted">Loading document thumbnail…</p>
	{:else if preview.status === "error"}
		<p role="alert" class="tr-text-metadata text-feedback-error">{preview.reason ?? "Document thumbnail failed."}</p>
	{:else}
		<p role="status" class="tr-text-metadata text-text-muted">{preview.reason ?? "Document thumbnail unavailable."}</p>
	{/if}
</figure>
