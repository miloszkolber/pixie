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
	"u-flex u-max-w-full u-items-center u-gap-xs composer-file-chip u-px-sm u-py-xs tr-text-metadata";
let toneClass = $derived(
	tone === "error" ? "composer-file-chip-error" : "composer-file-chip-default",
);
</script>

{#snippet content()}
	{#if icon}<Icon name="file" size={12} class="u-shrink-0" />{/if}
	<span class="u-min-w-0 u-truncate">{label}</span>
	{#if meta}<span class="u-shrink-0">{meta}</span>{/if}
	{#if trailing}<span class="u-flex u-shrink-0 u-items-center">{@render trailing()}</span>{/if}
	{#if onRemove}
		<button
			type="button"
			aria-label={removeLabel}
			class="u-flex composer-file-chip-remove"
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
		class={`${base} ${toneClass} composer-file-chip-button`}
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

<style>
	.composer-file-chip { border: 1px solid var(--border-default); border-radius: var(--radius-sm); background-clip: padding-box; }
	.composer-file-chip-default { background: var(--container-elevated-bg); color: var(--text-default); }
	.composer-file-chip-error { border-color: var(--feedback-error-muted); background: var(--feedback-error-subtle); color: var(--feedback-error); }
	.composer-file-chip-remove { width: 1.25rem; height: 1.25rem; flex-shrink: 0; align-items: center; justify-content: center; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--text-muted); }
	.composer-file-chip-remove:hover, .composer-file-chip-button:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	.composer-file-chip-remove:focus-visible, .composer-file-chip-button:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); }
	.composer-file-chip-button { transition: background-color var(--transition-fast); }
</style>
