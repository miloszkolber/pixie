<script lang="ts">
import type { SessionRuntime } from "../runtime/session-runtime";
let { widgets, placement }: { widgets: SessionRuntime["extensionWidgets"]; placement: string } =
	$props();
let entries = $derived(
	Object.entries(widgets)
		.filter(([, widget]) => widget.placement === placement)
		.sort(([, a], [, b]) => (a.order ?? 0) - (b.order ?? 0)),
);
</script>

{#if entries.length}
	<details class="extension-widgets tr-text-ui" data-testid="extension-widgets">
		<summary>Extension widgets ({entries.length})</summary>
		<div class="widget-content">
			{#each entries as [key, widget] (key)}
				<section aria-label={key}>
					<h3 class="tr-text-metadata">{key}</h3>
					<pre>{widget.lines.join("\n")}</pre>
				</section>
			{/each}
		</div>
	</details>
{/if}

<style>
	.extension-widgets { padding: var(--space-xs) var(--space-md); min-width: 0; }
	summary { cursor: pointer; }
	.widget-content { max-height: min(25dvh, 12rem); overflow: auto; }
	pre { white-space: pre-wrap; overflow-wrap: anywhere; margin: var(--space-xs) 0; }
</style>
