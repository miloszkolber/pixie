<script lang="ts">
import type { ImageContent } from "@pixie/contracts";
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
	class="max-h-[90vh] w-max max-w-[95vw]"
	onClosedAutoFocus={() => trigger?.focus()}
>
	{#if open}
		<div class="min-h-0 flex-1 overflow-auto">
			<img src={src} alt="" class="max-h-[80vh] max-w-full rounded-[var(--radius-sm)]" />
		</div>
	{/if}
</Dialog>
