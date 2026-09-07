<script lang="ts">
import type { Snippet } from "svelte";
import Button from "./button.svelte";
import Icon from "./icon.svelte";

interface Props {
	open?: boolean;
	title: string;
	description?: string | undefined;
	children?: Snippet;
	actions?: Snippet;
	closeLabel?: string;
	hideClose?: boolean;
	role?: "dialog" | "alertdialog";
	class?: string;
	testid?: string;
	onOpenChange?: ((open: boolean) => unknown) | undefined;
	onClosedAutoFocus?: (() => void) | undefined;
}

let {
	open = $bindable(false),
	title,
	description,
	children,
	actions,
	closeLabel = "Close",
	hideClose = false,
	role = "dialog",
	class: className = "",
	testid,
	onOpenChange,
	onClosedAutoFocus,
}: Props = $props();
let element: HTMLDialogElement;
const componentId = $props.id();
const titleId = `dialog-title-${componentId}`;
const descriptionId = `dialog-description-${componentId}`;

function setOpen(next: boolean): void {
	if (open === next) return;
	if (onOpenChange?.(next) === false) return;
	open = next;
}

$effect(() => {
	if (!element) return;
	if (open && !element.open) element.showModal();
	if (!open && element.open) element.close();
});
</script>

<dialog
	bind:this={element}
	class={`dialog ${className}`}
	{role}
	data-testid={testid}
	aria-labelledby={titleId}
	aria-describedby={description ? descriptionId : undefined}
	oncancel={(event) => {
		event.preventDefault();
		setOpen(false);
	}}
	onclose={() => {
		setOpen(false);
		onClosedAutoFocus?.();
	}}
	onclick={(event) => {
		if (event.target === element) setOpen(false);
	}}
>
	<div class="dialog-content">
		<header class="dialog-header flex items-start justify-between gap-sm">
			<div class="min-w-0 flex-1">
				<h2 id={titleId} class="dialog-title">{title}</h2>
				{#if description}<p id={descriptionId} class="dialog-description">{description}</p>{/if}
			</div>
			{#if !hideClose}
				<Button variant="ghost" size="icon-sm" aria-label={closeLabel} onclick={() => setOpen(false)}>
					<Icon name="x" size={16} />
				</Button>
			{/if}
		</header>
		<div class="dialog-body">{@render children?.()}</div>
		{#if actions}<footer class="dialog-footer">{@render actions()}</footer>{/if}
	</div>
</dialog>
