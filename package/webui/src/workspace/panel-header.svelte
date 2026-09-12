<script lang="ts">
import type { Snippet } from "svelte";

interface Props {
	title: string;
	actions?: Snippet;
}

let { title, actions }: Props = $props();
</script>

<div class="pixie-panel-header">
	<span class="eyebrow pixie-panel-eyebrow">{title}</span>
	{#if actions}
		<div class="pixie-panel-header-actions">
			{@render actions?.()}
		</div>
	{/if}
</div>
