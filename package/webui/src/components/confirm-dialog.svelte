<script lang="ts">
import type { Snippet } from "svelte";
import Button from "./button.svelte";
import Dialog from "./dialog.svelte";
import { errorText } from "../connection";
import Icon from "./icon.svelte";

interface Props {
	open?: boolean;
	title: string;
	description?: string | undefined;
	descriptionContent?: Snippet;
	confirmLabel?: string;
	cancelLabel?: string;
	destructive?: boolean;
	confirmTestId?: string;
	onConfirm: () => void | Promise<void>;
	onOpenChange?: ((open: boolean) => void) | undefined;
	onClosedAutoFocus?: (() => void) | undefined;
}

let {
	open = $bindable(false),
	title,
	description,
	descriptionContent,
	confirmLabel = "Confirm",
	cancelLabel = "Cancel",
	destructive = false,
	confirmTestId,
	onConfirm,
	onOpenChange,
	onClosedAutoFocus,
}: Props = $props();

let busy = $state(false);
let error = $state<string | null>(null);

function setOpen(next: boolean): boolean {
	if (busy) return false;
	error = null;
	open = next;
	onOpenChange?.(next);
	return true;
}

async function confirm(): Promise<void> {
	if (busy) return;
	busy = true;
	error = null;
	try {
		await onConfirm();
		busy = false;
		setOpen(false);
	} catch (cause) {
		error = errorText(cause);
	} finally {
		busy = false;
	}
}
</script>

<Dialog
	bind:open
	role="alertdialog"
	{title}
	{description}
	hideClose
	testid="confirm-dialog"
	class="max-w-[24rem]"
	{onClosedAutoFocus}
	onOpenChange={setOpen}
>
	{#if error}<p role="alert" class="text-feedback-error tr-text-ui">{error} You can retry.</p>{/if}
	{#if descriptionContent}
		<div class="dialog-description">{@render descriptionContent()}</div>
	{/if}
	{#snippet actions()}
		<Button variant="outline" disabled={busy} onclick={() => setOpen(false)}>{cancelLabel}</Button>
		<Button
			variant={destructive ? "destructive" : "default"}
			data-testid={confirmTestId}
			disabled={busy}
			onclick={confirm}
		>
			{#if destructive}<Icon name="triangle-alert" size={16} />{/if}
			{busy ? "Working…" : confirmLabel}
		</Button>
	{/snippet}
</Dialog>
