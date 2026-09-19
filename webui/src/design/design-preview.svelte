<script lang="ts">
import type { DesignPreviewMetadata } from "./design-model";

type Props = {
	preview: DesignPreviewMetadata;
	class?: string;
};

let { preview, class: className = "" }: Props = $props();
let imageUrl = $derived(preview.url ?? preview.artifactUrl ?? null);
</script>

<figure class={`u-flex u-min-w-0 u-flex-col u-gap-xs ${className}`} data-testid="design-preview">
	<figcaption class="tr-text-metadata u-text-text-muted">{preview.label}</figcaption>
	{#if preview.status === "ready" && imageUrl}
		<img
			src={imageUrl}
			alt={preview.kind === "cover" ? "Document thumbnail" : "Design frame preview"}
			loading="lazy"
			decoding="async"
			class="design-preview-image u-max-w-full u-rounded u-border u-border-border-default"
		/>
	{:else}
		<p role="status" class="tr-text-metadata u-text-text-muted">{preview.reason ?? "Document thumbnail unavailable."}</p>
	{/if}
</figure>

<style>
	.design-preview-image {
		max-height: calc(var(--space-800) * 14);
		object-fit: contain;
	}
</style>
