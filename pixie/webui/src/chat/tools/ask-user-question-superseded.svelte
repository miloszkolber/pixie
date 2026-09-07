<script lang="ts">
import type { AskUserQuestionItem } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";

interface Props {
	questions: AskUserQuestionItem[];
}
let { questions }: Props = $props();
</script>

<div data-testid="ask-user-question" data-tone="superseded" class="flex flex-col gap-xs text-text-muted tr-text-metadata">
	<div class="flex items-center gap-xs">
		<Icon name="skip-forward" size={14} class="shrink-0" />
		Superseded — you replied in chat instead of answering these.
	</div>
	{#each questions as question (question.question)}
		<div class="pl-[calc(0.875rem+var(--spacing-sm))] text-text-muted">{question.question}</div>
	{/each}
</div>
