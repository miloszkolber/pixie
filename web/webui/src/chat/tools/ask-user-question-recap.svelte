<script lang="ts">
import type { AskUserQuestionAnswer, AskUserQuestionItem } from "@pixie/shared";
import Icon from "../../components/icon.svelte";
import { deriveRecapState, splitRecommended } from "./ask-user-question-state";

interface Props {
	question: AskUserQuestionItem;
	answer: AskUserQuestionAnswer | undefined;
}

let { question, answer }: Props = $props();
let recap = $derived(deriveRecapState(answer));
let selected = $derived(new Set(recap.selectedLabels));
</script>

<div class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-start u-gap-sm">
		<Icon name="message-circle-question" size={14} class="ask-question-icon" />
		<p class="tr-text-ui u-text-text-muted">
			{question.question}
		</p>
	</div>
	{#if recap.showOptions}
		<ul class="u-flex u-flex-col u-gap-0.5 ask-question-options">
			{#each question.options as option (option.label)}
				{@const isSelected = selected.has(option.label)}
				<li
					data-testid="ask-record-option"
					data-selected={isSelected}
					class={`u-flex u-items-center u-gap-xs tr-text-ui ${isSelected ? "u-text-text-default" : "u-text-text-muted"}`}
				>
					{#if isSelected}<Icon name="check" size={14} class="ask-question-success-icon" />
					{:else}<span aria-hidden="true" class="ask-question-option-marker"></span>{/if}
					<span data-testid="ask-selection-status" class="u-sr-only">{isSelected ? "Selected: " : "Not selected: "}</span>
					<span>{splitRecommended(option.label).text}</span>
				</li>
			{/each}
			{#if recap.customAnswer}
				<li data-testid="ask-record-custom" class="u-flex u-items-center u-gap-xs tr-text-ui u-text-text-default">
					<Icon name="check" size={14} class="ask-question-success-icon" />
					<span data-testid="ask-selection-status" class="u-sr-only">Selected custom answer: </span>
					<span>“{recap.customAnswer}”</span>
				</li>
			{/if}
		</ul>
	{:else if !answer}
		<div class="u-flex u-items-center u-gap-xs ask-question-options u-text-text-muted tr-text-metadata ask-question-italic">
			<Icon name="skip-forward" size={12} /> No answer (skipped).
		</div>
	{:else}
		<div class="u-flex u-items-center u-gap-xs ask-question-answer">
			<Icon name="check" size={14} class="ask-question-success-icon" />
			<span data-testid="ask-selection-status" class="u-sr-only">Selected custom answer: </span>
			<span class="tr-text-ui u-text-text-default">“{answer.answer}”</span>
		</div>
	{/if}
	{#if answer?.notes}
		<div class="ask-question-options u-text-text-muted tr-text-metadata">Note: {answer.notes}</div>
	{/if}
</div>

<style>
	.ask-question-icon { margin-top: var(--space-2xs); flex-shrink: 0; color: var(--text-muted); }
	.ask-question-options { padding-inline-start: calc(0.875rem + var(--space-sm)); }
	.ask-question-option-marker { width: var(--space-lg); height: var(--space-lg); flex-shrink: 0; border: 1px solid var(--border-default); border-radius: 50%; }
	.ask-question-success-icon { flex-shrink: 0; color: var(--feedback-success); }
	.ask-question-answer { border-inline-start: 2px solid var(--border-default); padding-inline-start: var(--space-sm); }
	.ask-question-italic { font-style: italic; }
</style>
