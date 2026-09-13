<script lang="ts">
import type { DesignState } from "./design-model";
import { designInspectorModel } from "./design-inspector";
import { designDocumentMetadata } from "./design-upload";
import DesignPreview from "./design-preview.svelte";

type Props = {
	state: DesignState;
	selectedPageId?: string | null;
	class?: string;
};

let { state, selectedPageId = null, class: className = "" }: Props = $props();
let inspector = $derived(designInspectorModel(state, selectedPageId));
let metadata = $derived(designDocumentMetadata(state.document));
let selectedPage = $derived(
	inspector.pages.find((page) => page.id === inspector.selectedPageId) ?? null,
);
</script>

<section
	class={`flex min-w-0 flex-col gap-sm ${className}`}
	data-testid="design-viewer"
	data-scope="instance"
	aria-label="Design document viewer"
>
	<header class="min-w-0">
		<p class="tr-text-eyebrow text-text-muted">Design · instance-wide</p>
		<h2 class="truncate tr-title-entity" data-testid="design-viewer-title">
			{state.document?.name ?? "No Design document"}
		</h2>
		{#if metadata}
			<p class="tr-text-metadata text-text-muted" data-testid="design-viewer-metadata">
				{metadata.countsLabel} · shared focus revision {inspector.sharedRevision}
			</p>
		{/if}
	</header>
	{#if selectedPage}
		<p class="tr-text-metadata text-text-muted" data-testid="design-viewer-page">
			Page {selectedPage.name} · {selectedPage.nodeCount} nodes
		</p>
	{/if}
	<DesignPreview preview={state.preview} />
	{#if !inspector.availability.available}
		<p role="status" class="tr-text-metadata text-text-muted" data-testid="design-viewer-unavailable">
			{inspector.availability.reason ?? "Design content unavailable."}
		</p>
	{/if}
	{#if state.document}
		<section aria-label="Selected content" class="flex min-w-0 flex-col gap-xs">
			<h3 class="tr-text-metadata text-text-muted">Selected content</h3>
			{#if inspector.layers.length > 0}
				<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Selected page layers">
					{#each inspector.layers.slice(0, 20) as layer}
						<li class="break-words tr-text-metadata text-text-muted" data-testid={`design-viewer-layer-${layer.id}`}>
							{layer.name} · {layer.type}
						</li>
					{/each}
				</ul>
			{:else}
				<p class="tr-text-metadata text-text-muted">No layers on the selected page.</p>
			{/if}
			<p class="tr-text-metadata text-text-muted" data-testid="design-viewer-frame-note">
				Frame preview unavailable. Structure and text remain available; the Document thumbnail above
				is never repeated as a frame render.
			</p>
		</section>
	{/if}
</section>
