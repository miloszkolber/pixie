<script lang="ts">
import type { CanvasState } from "./canvas-model";
import { canvasViewerViewModel } from "./canvas-controls";
import CanvasPreview from "./canvas-preview.svelte";

type Props = {
	state: CanvasState;
	ready: boolean;
	pending: boolean;
	onscreenshot?: () => void;
	onrefresh?: () => void;
	class?: string;
};

let { state, ready, pending, onscreenshot, onrefresh, class: className = "" }: Props = $props();
let model = $derived(canvasViewerViewModel(state, { ready, pending }));
</script>

<section
	class={`u-flex u-min-w-0 u-flex-col u-gap-sm ${className}`}
	data-testid="canvas-viewer"
	data-scope="session"
	aria-label={model.heading}
>
	<header class="u-flex u-min-w-0 u-flex-wrap u-items-start u-justify-between u-gap-sm">
		<div class="u-min-w-0">
			<p class="tr-text-eyebrow u-text-text-muted">Canvas</p>
			<h2 class="u-truncate tr-title-entity">{model.heading}</h2>
			<p class="tr-text-metadata u-text-text-muted" data-testid="canvas-viewer-version">
				{model.version.label}
			</p>
		</div>
		<div class="u-flex u-shrink-0 u-flex-wrap u-gap-xs">
			<button
				type="button"
				class="btn"
				data-variant="outline"
				data-size="sm"
				data-testid="canvas-viewer-screenshot"
				disabled={!onscreenshot || !model.availability.canScreenshot}
				title={model.availability.screenshotReason ?? "Capture an exact-version raster screenshot"}
				onclick={() => onscreenshot?.()}
			>
				Screenshot
			</button>
			<button
				type="button"
				class="btn"
				data-variant="outline"
				data-size="sm"
				data-testid="canvas-viewer-refresh"
				disabled={!onrefresh || !model.availability.canRefresh}
				title={model.availability.refreshReason ?? "Refresh Canvas status and preview"}
				onclick={() => onrefresh?.()}
			>
				{pending ? "Refreshing…" : "Refresh"}
			</button>
		</div>
	</header>
	<p class="tr-text-metadata u-text-text-muted" data-testid="canvas-viewer-rendering">
		{model.rendering.label}
	</p>
	<p class="tr-text-metadata u-text-text-muted" data-testid="canvas-viewer-updated">{model.updated.label}</p>
	{#if model.version.detail}
		<p class="tr-text-metadata u-text-text-muted" data-testid="canvas-viewer-stale-detail">
			{model.version.detail}
		</p>
	{/if}
	{#if model.isStalePreview}
		<p role="status" class="tr-text-metadata u-text-text-muted" data-testid="canvas-viewer-stale">
			Preview is stale while a newer version renders.
		</p>
	{/if}
	<CanvasPreview preview={state.preview} />
</section>
