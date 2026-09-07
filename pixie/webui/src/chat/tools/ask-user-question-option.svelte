<script lang="ts">
import Icon from "../../components/icon.svelte";
import { readRecommendation } from "./ask-user-question-state";

interface Props {
	element?: HTMLButtonElement | undefined;
	label: string;
	description: string;
	recommendedReason?: string | undefined;
	selected: boolean;
	cursor: boolean;
	pageFocus: boolean;
	multi: boolean;
	pageKeys: boolean;
	onfocus: () => void;
	onkeydown: (event: KeyboardEvent) => void;
	onclick: () => void;
}

let {
	element = $bindable(),
	label,
	description,
	recommendedReason,
	selected,
	cursor,
	pageFocus,
	multi,
	pageKeys,
	onfocus,
	onkeydown,
	onclick,
}: Props = $props();
let recommendation = $derived(readRecommendation({ label, recommendedReason }));
</script>

<button
	bind:this={element}
	type="button"
	role={multi ? "checkbox" : "radio"}
	aria-checked={selected}
	aria-keyshortcuts={`ArrowUp ArrowDown Home End Space Enter${pageKeys ? " ArrowLeft ArrowRight" : ""} Shift+Escape`}
	tabindex={cursor ? 0 : -1}
	data-testid="ask-option"
	data-selected={selected}
	data-cursor={cursor}
	data-ask-page-focus={pageFocus || undefined}
	{onfocus}
	{onkeydown}
	{onclick}
	class={`flex items-start gap-sm rounded-[var(--radius-sm)] border px-md py-sm text-left outline-none transition-colors focus-visible:border-control-border-active focus-visible:ring-2 focus-visible:ring-primary ${selected ? "border-primary bg-primary-subtle" : "border-border-default hover:bg-control-bg-hovered"}`}
>
	<span
		class={`mt-0.5 flex size-[18px] shrink-0 items-center justify-center border ${multi ? "rounded-[var(--radius-sm)]" : "rounded-full"} ${selected ? multi ? "border-primary bg-primary text-text-on-primary" : "border-primary" : "border-border-default"}`}
	>
		{#if selected && multi}<Icon name="check" size={12} />
		{:else if selected}<span class="size-2 rounded-full bg-primary"></span>{/if}
	</span>
	<span class="flex min-w-0 flex-col gap-0.5">
		<span class="flex items-center gap-xs">
			<span data-testid="ask-option-label" class="tr-text-ui text-text-default">{recommendation.text}</span>
			{#if recommendation.recommended}
				<span class="inline-flex items-center rounded-full bg-primary-subtle px-xs py-0 tr-text-label-pill text-primary">Recommended</span>
			{/if}
		</span>
		{#if description}<span class="text-text-muted tr-text-metadata">{description}</span>{/if}
		{#if recommendation.reason}
			<span data-testid="ask-recommended-reason" class="mt-0.5 text-text-muted tr-text-metadata">
				<span class="tr-text-emphasis text-primary">Why:</span> {recommendation.reason}
			</span>
		{/if}
	</span>
</button>
