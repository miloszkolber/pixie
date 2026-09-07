<script lang="ts">
import type { Snippet } from "svelte";
import Button from "./button.svelte";
import Dialog from "./dialog.svelte";
import Icon from "./icon.svelte";

interface Props {
	open?: boolean;
	title: string;
	description?: string | undefined;
	descriptionContent?: Snippet;
	dismissLabel?: string;
	tone?: "error" | "info";
	testid?: string;
	onOpenChange?: ((open: boolean) => void) | undefined;
}

let {
	open = $bindable(false),
	title,
	description,
	descriptionContent,
	dismissLabel = "OK",
	tone = "error",
	testid = "notice-dialog",
	onOpenChange,
}: Props = $props();

function setOpen(next: boolean): void {
	open = next;
	onOpenChange?.(next);
}
</script>

<Dialog bind:open {title} {description} hideClose {testid} class="max-w-[24rem]" onOpenChange={setOpen}>
	{#if tone === "error"}
		<Icon name="triangle-alert" size={16} class="text-feedback-error" />
	{/if}
	{#if descriptionContent}<div class="dialog-description">{@render descriptionContent()}</div>{/if}
	{#snippet actions()}
		<Button data-testid="notice-dismiss" onclick={() => setOpen(false)}>{dismissLabel}</Button>
	{/snippet}
</Dialog>
