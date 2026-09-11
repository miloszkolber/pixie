<script lang="ts">
import type { CanvasState } from "./canvas-model";
import { canvasSidebarViewModel } from "./canvas-controls";
import { CANVAS_TEMPLATES, canvasTemplateSummary } from "./canvas-templates";

type Props = {
	state: CanvasState;
	ready: boolean;
	pending: boolean;
	error?: string | null;
	onscreenshot?: () => void;
	onrefresh?: () => void;
	onremove?: () => void;
	ontemplate?: (templateId: string) => void;
	class?: string;
};

let {
	state,
	ready,
	pending,
	error = null,
	onscreenshot,
	onrefresh,
	onremove,
	ontemplate,
	class: className = "",
}: Props = $props();
let model = $derived(canvasSidebarViewModel(state, { ready, pending }));
</script>

<aside
	class={`flex min-w-0 flex-col gap-sm ${className}`}
	data-testid="canvas-sidebar"
	data-scope="session"
	aria-label="Canvas revision and status controls"
>
	<p class="tr-text-eyebrow text-text-muted">Canvas</p>
	<p class="tr-text-metadata text-text-muted" data-testid="canvas-scope-label">{model.scopeLabel}</p>
	<dl class="flex min-w-0 flex-col gap-xs">
		<div class="flex min-w-0 items-baseline justify-between gap-xs">
			<dt class="tr-text-metadata text-text-muted">Version</dt>
			<dd class="truncate tr-text-metadata text-text-default" data-testid="canvas-version-label">
				{model.version.label}
			</dd>
		</div>
		<div class="flex min-w-0 items-baseline justify-between gap-xs">
			<dt class="tr-text-metadata text-text-muted">Rendering</dt>
			<dd class="truncate tr-text-metadata text-text-default" data-testid="canvas-rendering-label">
				{model.rendering.label}
			</dd>
		</div>
		<div class="flex min-w-0 items-baseline justify-between gap-xs">
			<dt class="tr-text-metadata text-text-muted">Updated</dt>
			<dd class="truncate tr-text-metadata text-text-muted" data-testid="canvas-updated-label">
				{model.updated.label}
			</dd>
		</div>
	</dl>
	{#if model.version.detail}
		<p class="tr-text-metadata text-text-muted" data-testid="canvas-version-detail">{model.version.detail}</p>
	{/if}
	{#if state.warnings.length > 0}
		<ul class="flex min-w-0 flex-col gap-0.5" aria-label="Canvas warnings">
			{#each state.warnings as warning}
				<li class="break-words tr-text-metadata text-text-muted">{warning.message}</li>
			{/each}
		</ul>
	{/if}
	{#if error}
		<p role="alert" class="tr-text-metadata text-feedback-error" data-testid="canvas-sidebar-error">{error}</p>
	{/if}
	<div class="flex flex-wrap gap-xs">
		<button
			type="button"
			class="btn"
			data-variant="outline"
			data-size="sm"
			data-testid="canvas-screenshot-button"
			disabled={!model.availability.canScreenshot}
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
			data-testid="canvas-refresh-button"
			disabled={!model.availability.canRefresh}
			title={model.availability.refreshReason ?? "Refresh Canvas status and preview"}
			onclick={() => onrefresh?.()}
		>
			{pending ? "Refreshing…" : "Refresh"}
		</button>
		<button
			type="button"
			class="btn"
			data-variant="destructive-outline"
			data-size="sm"
			data-testid="canvas-remove-button"
			disabled={!model.availability.canRemove}
			title={model.availability.removalReason ?? "Remove this Canvas"}
			onclick={() => onremove?.()}
		>
			Remove
		</button>
	</div>
	{#if model.removal}
		<p class="tr-text-metadata text-text-muted" data-testid="canvas-removal-confirmation">
			{model.removal.title} {model.removal.message}
		</p>
	{/if}
	<section aria-label="Mewa test templates" class="flex min-w-0 flex-col gap-xs">
		<h3 class="tr-text-metadata text-text-muted">Mewa test templates</h3>
		<ul class="flex min-w-0 flex-col gap-xs">
			{#each CANVAS_TEMPLATES as template}
				<li class="card" data-testid={`canvas-template-${template.id}`}>
					<div class="card-header">
						<p class="card-title tr-text-ui">{template.label}</p>
						<p class="card-description tr-text-metadata">{template.description}</p>
					</div>
					<div class="card-content">
						<p class="tr-text-metadata text-text-muted">{canvasTemplateSummary(template)}</p>
					</div>
					<div class="card-footer">
						<button
							type="button"
							class="btn"
							data-variant="outline"
							data-size="sm"
							data-testid={`canvas-template-use-${template.id}`}
							disabled={!ready}
							onclick={() => ontemplate?.(template.id)}
						>
							Use {template.id}
						</button>
					</div>
				</li>
			{/each}
		</ul>
	</section>
</aside>
