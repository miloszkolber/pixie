<script lang="ts">
import type { AskUserQuestionAnswer, AskUserQuestionItem } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import Recap from "./ask-user-question-recap.svelte";

interface Props {
	questions: AskUserQuestionItem[];
	answers: AskUserQuestionAnswer[];
	submitEnabled: boolean;
	onJump: (index: number) => void;
}
let { questions, answers, submitEnabled, onJump }: Props = $props();
let byIndex = $derived(new Map(answers.map((answer) => [answer.questionIndex, answer])));
let unanswered = $derived(
	questions
		.map((question, index) => ({ question, index }))
		.filter(({ index }) => !byIndex.has(index)),
);
</script>

<div class="flex flex-col gap-sm">
	<div class="flex items-start gap-sm">
		<Icon name="message-circle-question" size={16} class="mt-0.5 shrink-0 text-text-muted" />
		<p data-testid="ask-review-title" class="tr-title-dialog text-text-default">Review your answers</p>
	</div>
	<ul class="flex flex-col gap-md">
		{#each questions as question, index (question.question)}
			<li data-testid="ask-review-item" class="flex flex-col gap-xs">
				<span class="text-text-muted tr-text-metadata">{question.header || `Q${index + 1}`}</span>
				<Recap {question} answer={byIndex.get(index)} variant="review" />
			</li>
		{/each}
	</ul>
	{#if unanswered.length > 0}
		<button
			type="button"
			data-testid="ask-unanswered"
			data-ask-page-focus={submitEnabled ? undefined : "true"}
			onclick={() => onJump(unanswered[0]?.index ?? 0)}
			class="self-start rounded-[var(--radius-sm)] text-feedback-warning tr-text-metadata outline-none hover:underline focus-visible:ring-2 focus-visible:ring-primary"
		>⚠ Unanswered: {unanswered.map(({ question, index }) => question.header || `Q${index + 1}`).join(", ")}</button>
	{/if}
</div>
