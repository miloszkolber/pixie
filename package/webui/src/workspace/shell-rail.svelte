<script lang="ts">
import type { Snippet } from "svelte";

interface Props {
	side: "left" | "right";
	label: string;
	top?: Snippet;
	bottom?: Snippet;
	children?: Snippet;
}

let { side, label, top, bottom, children }: Props = $props();
</script>

<div role="toolbar" aria-orientation="vertical" aria-label={label} data-side={side} class="pixie-rail">
	<div class="pixie-rail-top">
		{@render top?.()}
		{@render children?.()}
	</div>
	{#if bottom}
		<div class="pixie-rail-bottom">
			{@render bottom?.()}
		</div>
	{/if}
</div>
