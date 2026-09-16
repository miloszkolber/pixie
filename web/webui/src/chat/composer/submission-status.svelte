<script lang="ts">
import Button from "../../components/button.svelte";
import type { ChatSubmission } from "../runtime/types";
import { submissionStatusState } from "./submission-status";

interface Props {
	pending: ChatSubmission;
	onText: (text: string) => void;
	onRetry: () => void;
	onDiscard: () => void;
}

let { pending, onText, onRetry, onDiscard }: Props = $props();
let state = $derived(submissionStatusState(pending));
</script>

<div
	data-testid="chat-submission-status"
	data-state={state}
	role={state === "failed" ? "alert" : "status"}
	class="chat-submission-status u-overflow-y-auto u-rounded u-border u-border-border-default chat-submission-padding tr-text-ui"
>
	{#if state === "busy"}
		<p>Sending message…</p>
	{:else if state === "quiescing"}
		<p>
			Pixie is quiescing for an update, so new messages are paused while in-flight work
			finishes. Your message is retained; retry when the update completes.
		</p>
		<p class="u-text-text-muted">Check the transcript before retrying if this pause is unexpected.</p>
	{:else}
		<p class="u-text-feedback-error">{pending.error}</p>
		<p class="u-text-text-muted">Check the transcript before retrying: a lost connection can leave delivery uncertain.</p>
	{/if}
	{#if state !== "busy"}
		<textarea
			aria-label="Retained message"
			class="input chat-submission-textarea u-w-full"
			value={pending.text}
			oninput={(event) => onText(event.currentTarget.value)}
		></textarea>
		{#if pending.attachments.length}
			<p class="u-break-words">Attachments: {pending.attachments.map((attachment) => attachment.name).join(", ")}</p>
		{/if}
		<div class="u-flex u-flex-wrap u-gap-xs chat-submission-actions">
			<Button onclick={onRetry}>Retry message</Button>
			<Button variant="ghost" onclick={onDiscard}>Discard retained message</Button>
		</div>
	{/if}
</div>

<style>
	.chat-submission-status { max-height: min(40dvh, 20rem); margin: var(--space-xs) var(--space-sm); }
	.chat-submission-padding { padding: var(--space-sm); }
	.chat-submission-textarea, .chat-submission-actions { margin-block-start: var(--space-xs); }
</style>
