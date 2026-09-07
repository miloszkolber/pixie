<script lang="ts">
import type { Snippet } from "svelte";
import Icon from "../../components/icon.svelte";

interface Props {
	label: string;
	meta?: string | undefined;
	trailing?: Snippet;
	onRemove?: (() => void) | undefined;
	removeLabel?: string | undefined;
	onclick?: (() => void) | undefined;
	element?: HTMLButtonElement | undefined;
	tone?: "default" | "error";
	icon?: boolean;
	title?: string | undefined;
	ariaLabel?: string | undefined;
	ariaHaspopup?: "dialog" | undefined;
	ariaExpanded?: boolean | undefined;
	testid?: string | undefined;
	width?: number | undefined;
	height?: number | undefined;
	mime?: string | undefined;
}

let {
	label,
	meta,
	trailing,
	onRemove,
	removeLabel = "Remove",
	onclick,
	element = $bindable(),
	tone = "default",
	icon = true,
	title,
	ariaLabel,
	ariaHaspopup,
	ariaExpanded,
	testid,
	width,
	height,
	mime,
}: Props = $props();

const base =
	"flex max-w-full items-center gap-xs rounded-[var(--radius-sm)] border bg-clip-padding px-sm py-xs tr-text-metadata";
let toneClass = $derived(
	tone === "error"
		? "border-feedback-error-muted bg-feedback-error-subtle text-feedback-error"
		: "border-border-default bg-container-elevated-bg text-text-default",
);
</script>

{#snippet content()}
	{#if icon}<Icon name="file" size={12} class="shrink-0" />{/if}
	<span class="min-w-0 truncate">{label}</span>
	{#if meta}<span class="shrink-0">{meta}</span>{/if}
	{#if trailing}<span class="flex shrink-0 items-center">{@render trailing()}</span>{/if}
	{#if onRemove}
		<button
			type="button"
			aria-label={removeLabel}
			class="flex size-5 shrink-0 items-center justify-center rounded-[var(--radius-sm)] text-text-muted hover:bg-control-bg-hovered hover:text-text-default focus-visible:ring-2 focus-visible:ring-primary"
			onclick={onRemove}
		>
			<Icon name="x" size={12} />
		</button>
	{/if}
{/snippet}

{#if onclick}
	<button
		bind:this={element}
		type="button"
		{title}
		aria-label={ariaLabel}
		aria-haspopup={ariaHaspopup}
		aria-expanded={ariaExpanded}
		data-testid={testid}
		data-width={width}
		data-height={height}
		data-mime={mime}
		class={`${base} ${toneClass} transition-colors hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary`}
		{onclick}
	>
		{@render content()}
	</button>
{:else}
	<span
		{title}
		aria-label={ariaLabel}
		data-testid={testid}
		data-width={width}
		data-height={height}
		data-mime={mime}
		class={`${base} ${toneClass}`}
	>
		{@render content()}
	</span>
{/if}
