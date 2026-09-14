<script lang="ts">
import type { UserMessage } from "@pixie/shared";
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
const bubble = "user-message-bubble tr-text-reading u-text-text-muted";
</script>

<div data-testid="chat-message" data-role="user" class="u-flex user-message-row">
	{#if skill}
		<div class="u-flex u-w-full u-flex-col u-items-end u-gap-xs">
			<SkillInvocationCard foldId={`${id}:skill`} invocation={skill} />
			{#if skill.userMessage}<div data-testid="skill-user-request" class={bubble}>{skill.userMessage}</div>{/if}
		</div>
	{:else}
		<div class={bubble}>
			{#if resources.length > 0}
				<div class="u-flex u-flex-wrap u-gap-xs user-message-attachments" data-testid="chat-message-text-attachments">
					{#each resources as resource (resource.key)}
						<div title={`${resource.name} · ${resource.mimeType}`} class="u-flex u-max-w-full u-items-center u-gap-2xs user-resource-chip tr-text-metadata">
							<Icon name="file-text" size={12} class="u-shrink-0" /><span class="u-truncate">{resource.name}</span>
						</div>
					{/each}
				</div>
			{/if}
			{#if attachments.length > 0}
				<div class="u-flex u-flex-wrap u-gap-xs user-message-attachments" data-testid="chat-message-images">
					{#each attachments as attachment (attachment.key)}<ImageChip label={attachment.label} image={attachment.image} />{/each}
	</div>

<style>
	.user-message-row { justify-content: end; }
	.user-message-bubble { max-width: 85%; white-space: pre-wrap; overflow-wrap: break-word; border: 1px solid var(--bubble-user-border); border-radius: var(--radius-lg); background: var(--bubble-user-bg); background-clip: padding-box; padding: var(--space-sm) var(--space-md); }
	.user-message-attachments { padding-block-end: var(--space-xs); }
	.user-resource-chip { border: 1px solid var(--border-default); border-radius: var(--radius-sm); background: var(--container-elevated-bg); background-clip: padding-box; padding: var(--space-2xs) var(--space-xs); }
</style>
			{/if}
			{text}
		</div>
	{/if}
</div>
