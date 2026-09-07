<script lang="ts">
import { ACCEPTED_IMAGE_TYPES, type ImageContent } from "@pixie/contracts";
import ImageChip from "../composer/image-chip.svelte";
import { toolContent, toText } from "./tool-helpers";

interface Props {
	result: unknown;
	error?: boolean;
}

let { result, error = false }: Props = $props();
let blocks = $derived(toolContent(result, error));

function imageBlock(block: unknown): ImageContent | null {
	if (!block || typeof block !== "object" || Reflect.get(block, "type") !== "image") return null;
	const data = Reflect.get(block, "data");
	const mimeType = Reflect.get(block, "mimeType");
	return typeof data === "string" &&
		typeof mimeType === "string" &&
		ACCEPTED_IMAGE_TYPES.includes(mimeType)
		? { type: "image", data, mimeType }
		: null;
}

function textBlock(block: unknown): string {
	if (!block || typeof block !== "object") return toText(block);
	if (Reflect.get(block, "type") === "text" && typeof Reflect.get(block, "text") === "string") {
		return Reflect.get(block, "text") as string;
	}
	const resource =
		Reflect.get(block, "type") === "resource" ? Reflect.get(block, "resource") : undefined;
	const resourceText =
		resource && typeof resource === "object" ? Reflect.get(resource, "text") : undefined;
	return typeof resourceText === "string"
		? `${String(Reflect.get(resource, "uri") ?? "")}\n${resourceText}`
		: toText(block);
}
</script>

{#if blocks.length > 0}
	<div class="flex flex-col gap-xs" data-testid="tool-output">
		{#each blocks as block, index (`content-${index}`)}
			{@const image = imageBlock(block)}
			{#if image}
				<ImageChip label={image.mimeType} {image} />
			{:else}
				<pre
					class={`overflow-auto tr-code-text ${error ? "text-feedback-error" : "text-text-default"}`}
				>{textBlock(block)}</pre>
			{/if}
		{/each}
	</div>
{/if}
