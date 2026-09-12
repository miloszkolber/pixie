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

function handleKeydown(event: KeyboardEvent): void {
	if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
	const toolbar = event.currentTarget;
	if (!(toolbar instanceof HTMLElement)) return;
	const buttons = Array.from(toolbar.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
	const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
	if (current === -1) return;

	event.preventDefault();
	const next =
		event.key === "Home"
			? 0
			: event.key === "End"
				? buttons.length - 1
				: (current + (event.key === "ArrowUp" ? -1 : 1) + buttons.length) % buttons.length;
	buttons[next]?.focus();
}
</script>

<div role="toolbar" aria-orientation="vertical" aria-label={label} data-side={side} tabindex="-1" class="pixie-rail" onkeydown={handleKeydown}>
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
