<script lang="ts">
import type { Snippet } from "svelte";
import { COLLAPSIBLE_LINE_THRESHOLD } from "./collapsible";

interface Props {
	lines: number;
	children: Snippet;
	fadeClass?: string;
}

let {
	lines,
	children,
	fadeClass = "bg-[linear-gradient(to_top,var(--container-header-bg),transparent)]",
}: Props = $props();
let expanded = $state(false);
</script>

{#if lines <= COLLAPSIBLE_LINE_THRESHOLD}
	{@render children()}
{:else}
	<div data-testid="collapsible" data-expanded={expanded} class="flex flex-col gap-xs">
		<div class={expanded ? undefined : "relative max-h-96 overflow-hidden"}>
			{@render children()}
			{#if !expanded}
				<div class={`pointer-events-none absolute inset-x-0 bottom-0 h-8 ${fadeClass}`}></div>
			{/if}
		</div>
		<button
			type="button"
			data-testid="collapsible-toggle"
			onclick={() => (expanded = !expanded)}
			class="self-start text-primary tr-text-metadata hover:underline"
		>
			{expanded ? "Show less" : `Show all ${lines} lines`}
		</button>
	</div>
{/if}
