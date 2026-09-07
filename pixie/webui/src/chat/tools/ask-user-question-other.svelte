<script lang="ts">
import Icon from "../../components/icon.svelte";

interface Props {
	element?: HTMLInputElement | undefined;
	multi: boolean;
	active: boolean;
	text: string;
	pageFocus: boolean;
	onToggle: () => void;
	onText: (text: string) => void;
	onMove: (key: "ArrowUp" | "ArrowDown") => void;
	onConfirm: () => void;
}

let {
	element = $bindable(),
	multi,
	active,
	text,
	pageFocus,
	onToggle,
	onText,
	onMove,
	onConfirm,
}: Props = $props();
const componentId = $props.id();
const inputId = `ask-custom-${componentId}`;
</script>

<label
	for={inputId}
	data-testid="ask-custom-row"
	data-selected={active}
	class={`flex cursor-text items-center gap-sm rounded-[var(--radius-sm)] border px-md py-sm transition-colors focus-within:border-control-border-active focus-within:ring-2 focus-within:ring-primary ${active ? "border-primary bg-primary-subtle" : "border-border-default hover:bg-control-bg-hovered"}`}
>
	{#if multi}
		<button
			type="button"
			data-testid="ask-custom-toggle"
			aria-label={active ? "Exclude your own answer" : "Include your own answer"}
			onclick={(event) => {
				event.preventDefault();
				onToggle();
			}}
			class="flex items-center rounded-[var(--radius-sm)] outline-none focus-visible:ring-2 focus-visible:ring-primary"
		>
			<span class={`flex size-[18px] shrink-0 items-center justify-center rounded-[var(--radius-sm)] border ${active ? "border-primary bg-primary text-text-on-primary" : "border-border-default"}`}>
				{#if active}<Icon name="check" size={12} />{/if}
			</span>
		</button>
	{:else}
		<span class={`flex size-[18px] shrink-0 items-center justify-center rounded-full border ${active ? "border-primary" : "border-border-default"}`}>
			{#if active}<span class="size-2 rounded-full bg-primary"></span>{/if}
		</span>
	{/if}
	<span class="tr-text-ui text-text-default">Other</span>
	<input
		bind:this={element}
		id={inputId}
		data-testid="ask-custom"
		data-ask-page-focus={pageFocus || undefined}
		aria-label="Other answer"
		aria-keyshortcuts="ArrowUp ArrowDown Enter Shift+Escape"
		value={text}
		placeholder="type your own answer…"
		oninput={(event) => onText(event.currentTarget.value)}
		onkeydown={(event) => {
			if (
				(event.key === "ArrowUp" || event.key === "ArrowDown") &&
				!event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey
			) {
				event.preventDefault();
				onMove(event.key);
				return;
			}
			if (event.key === "Enter" && !event.isComposing) {
				event.preventDefault();
				onConfirm();
			}
		}}
		class="min-w-0 flex-1 border-none bg-transparent tr-text-ui text-text-default outline-none placeholder:text-text-muted"
	/>
</label>
