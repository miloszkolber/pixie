<script lang="ts">
import type { ImageContent } from "@pixie/shared";
import Dialog from "../../components/dialog.svelte";
import FileChip from "./file-chip.svelte";

interface Props {
	label: string;
	image: ImageContent;
}

let { label, image }: Props = $props();
let open = $state(false);
let trigger: HTMLButtonElement | undefined = $state();
let src = $derived(`data:${image.mimeType};base64,${image.data}`);
</script>

<FileChip
	bind:element={trigger}
	testid="chat-attachment-chip"
	title={label}
	ariaLabel={`View attachment ${label}`}
	ariaHaspopup="dialog"
	ariaExpanded={open}
	{label}
	onclick={() => (open = true)}
/>

<Dialog
	bind:open
	title={label}
	testid="chat-attachment-dialog"
	class="image-chip-dialog"
	onClosedAutoFocus={() => trigger?.focus()}
>
	{#if open}
		<div class="u-min-h-0 u-flex-1 u-overflow-auto">
			<img src={src} alt="" class="u-max-w-full u-rounded image-chip-image" />
		</div>
	{/if}
</Dialog>

<style>
	:global(.image-chip-dialog) { width: max-content; max-width: 95vw; max-height: 90vh; }
	.image-chip-image { max-height: 80vh; }
</style>
