<script lang="ts">
import type { Snippet } from "svelte";
import { COLLAPSIBLE_LINE_THRESHOLD } from "./collapsible";

interface Props {
	lines: number;
	children: Snippet;
	fadeTone?: "header" | "elevated";
}

let { lines, children, fadeTone = "header" }: Props = $props();
let expanded = $state(false);
</script>

{#if lines <= COLLAPSIBLE_LINE_THRESHOLD}
	{@render children()}
{:else}
	<div data-testid="collapsible" data-expanded={expanded} class="u-flex u-flex-col u-gap-xs">
		<div class={expanded ? undefined : "collapsible-preview"}>
			{@render children()}
			{#if !expanded}
				<div class="collapsible-fade" data-tone={fadeTone}></div>
			{/if}
		</div>
		<button
			type="button"
			data-testid="collapsible-toggle"
			onclick={() => (expanded = !expanded)}
			class="collapsible-toggle tr-text-metadata"
		>
			{expanded ? "Show less" : `Show all ${lines} lines`}
		</button>
	</div>
{/if}

<style>
	.collapsible-preview { position: relative; max-height: 24rem; overflow: hidden; }
	.collapsible-fade { pointer-events: none; position: absolute; inset-inline: 0; inset-block-end: 0; height: 2rem; background: linear-gradient(to top, var(--container-header-bg), transparent); }
	.collapsible-fade[data-tone="elevated"] { background: linear-gradient(to top, var(--container-elevated-bg), transparent); }
	.collapsible-toggle { align-self: flex-start; color: var(--primary); }
	.collapsible-toggle:hover { text-decoration: underline; }
</style>
