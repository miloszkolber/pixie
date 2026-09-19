<script lang="ts">
import type { AskUserQuestionItem, AskUserQuestionResult } from "@pixie/shared";
import Recap from "./ask-user-question-recap.svelte";

interface Props {
	questions: AskUserQuestionItem[];
	result: AskUserQuestionResult | null;
	rawText: string;
}
let { questions, result, rawText }: Props = $props();
let byIndex = $derived(
	new Map((result?.answers ?? []).map((answer) => [answer.questionIndex, answer])),
);
</script>

{#if !result}
	<div data-testid="ask-user-question" data-tone="pending" class="u-text-text-muted tr-text-metadata">{rawText || "Question closed."}</div>
{:else}
	<div data-testid="ask-user-question" data-tone={result.cancelled ? "skipped" : "answered"} class="u-flex u-flex-col u-gap-md">
		{#each questions as question, index (question.question)}
			<Recap {question} answer={byIndex.get(index)} />
		{/each}
		{#if questions.length === 0}<div class="u-text-text-muted tr-text-metadata">{rawText || "Answered."}</div>{/if}
	</div>
{/if}
