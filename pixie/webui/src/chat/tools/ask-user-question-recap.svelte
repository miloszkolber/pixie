<script lang="ts">
import type { AskUserQuestionAnswer, AskUserQuestionItem } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import { deriveRecapState, splitRecommended } from "./ask-user-question-state";

interface Props {
	question: AskUserQuestionItem;
	answer: AskUserQuestionAnswer | undefined;
	variant: "review" | "resolved";
}

let { question, answer, variant }: Props = $props();
let reviewing = $derived(variant === "review");
let recap = $derived(deriveRecapState(answer, variant));
let selected = $derived(new Set(recap.selectedLabels));
</script>

<div class="flex flex-col gap-xs">
	<div class="flex items-start gap-sm">
		<Icon name="message-circle-question" size={14} class="mt-0.5 shrink-0 text-text-muted" />
		<p data-testid={reviewing ? "ask-review-question" : undefined} class={`tr-text-ui ${reviewing ? "text-text-default" : "text-text-muted"}`}>
			{question.question}
		</p>
	</div>
	{#if recap.showOptions}
		<ul class="flex flex-col gap-0.5 pl-[calc(0.875rem+var(--spacing-sm))]">
			{#each question.options as option (option.label)}
				{@const isSelected = selected.has(option.label)}
				<li
					data-testid={reviewing ? "ask-review-option" : "ask-record-option"}
					data-selected={isSelected}
					class={`flex items-center gap-xs tr-text-ui ${isSelected ? "text-text-default" : "text-text-muted"}`}
				>
					{#if isSelected}<Icon name="check" size={14} class="shrink-0 text-feedback-success" />
					{:else}<span aria-hidden="true" class="size-3 shrink-0 rounded-full border border-border-default"></span>{/if}
					<span data-testid="ask-selection-status" class="sr-only">{isSelected ? "Selected: " : "Not selected: "}</span>
					<span>{splitRecommended(option.label).text}</span>
				</li>
			{/each}
			{#if recap.customAnswer}
				<li data-testid={reviewing ? "ask-review-custom" : "ask-record-custom"} class="flex items-center gap-xs tr-text-ui text-text-default">
					<Icon name="check" size={14} class="shrink-0 text-feedback-success" />
					<span data-testid="ask-selection-status" class="sr-only">Selected custom answer: </span>
					<span>“{recap.customAnswer}”</span>
				</li>
			{/if}
		</ul>
		{#if !answer}
			<div data-testid="ask-review-unanswered" class="flex items-center gap-xs pl-[calc(0.875rem+var(--spacing-sm))] text-text-muted tr-text-metadata italic">
				<Icon name="skip-forward" size={12} /> Not answered
			</div>
		{/if}
	{:else if !answer}
		<div class="flex items-center gap-xs pl-[calc(0.875rem+var(--spacing-sm))] text-text-muted tr-text-metadata italic">
			<Icon name="skip-forward" size={12} /> No answer (skipped).
		</div>
	{:else}
		<div class="flex items-center gap-xs border-border-default border-l-2 pl-sm">
			<Icon name="check" size={14} class="shrink-0 text-feedback-success" />
			<span data-testid="ask-selection-status" class="sr-only">Selected custom answer: </span>
			<span class="tr-text-ui text-text-default">“{answer.answer}”</span>
		</div>
	{/if}
	{#if answer?.notes}
		<div class="pl-[calc(0.875rem+var(--spacing-sm))] text-text-muted tr-text-metadata">Note: {answer.notes}</div>
	{/if}
</div>
