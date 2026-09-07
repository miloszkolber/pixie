<script lang="ts">
import type { UserMessage } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { parseSkillInvocation, userText } from "../../lib";
import ImageChip from "../composer/image-chip.svelte";
import SkillInvocationCard from "./skill-invocation.svelte";
import { userImageAttachments, userResourceMarkers } from "./turns";

interface Props {
	id: string;
	message: UserMessage;
	imageAttachmentNames?: string[] | undefined;
}
let { id, message, imageAttachmentNames }: Props = $props();
let text = $derived(userText(message.content));
let attachments = $derived(userImageAttachments(message.content, imageAttachmentNames));
let resources = $derived(userResourceMarkers(message.content));
let skill = $derived(parseSkillInvocation(text));
const bubble =
	"max-w-[85%] whitespace-pre-wrap break-words rounded-[var(--radius-lg)] border border-bubble-user-border bg-clip-padding bg-bubble-user-bg px-md py-sm tr-text-reading text-text-muted";
</script>

<div data-testid="chat-message" data-role="user" class="flex justify-end">
	{#if skill}
		<div class="flex w-full flex-col items-end gap-xs">
			<SkillInvocationCard foldId={`${id}:skill`} invocation={skill} />
			{#if skill.userMessage}<div data-testid="skill-user-request" class={bubble}>{skill.userMessage}</div>{/if}
		</div>
	{:else}
		<div class={bubble}>
			{#if resources.length > 0}
				<div class="flex flex-wrap gap-xs pb-xs" data-testid="chat-message-text-attachments">
					{#each resources as resource (resource.key)}
						<div title={`${resource.name} · ${resource.mimeType}`} class="flex max-w-full items-center gap-2xs rounded-[var(--radius-sm)] border border-border-default bg-container-elevated-bg px-xs py-2xs tr-text-metadata">
							<Icon name="file-text" size={12} class="shrink-0" /><span class="truncate">{resource.name}</span>
						</div>
					{/each}
				</div>
			{/if}
			{#if attachments.length > 0}
				<div class="flex flex-wrap gap-xs pb-xs" data-testid="chat-message-images">
					{#each attachments as attachment (attachment.key)}<ImageChip label={attachment.label} image={attachment.image} />{/each}
				</div>
			{/if}
			{text}
		</div>
	{/if}
</div>
