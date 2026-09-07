<script lang="ts">
import type { AskUserQuestionItem } from "@pixie/contracts";
import { tick } from "svelte";
import Icon from "../../components/icon.svelte";
import Markdown from "../render/markdown.svelte";
import AskOption from "./ask-user-question-option.svelte";
import AskOther from "./ask-user-question-other.svelte";
import {
	choiceKeyAction,
	confirmStateFor,
	customTextPatch,
	noteKeyAction,
	type QuestionState,
	selectOptionPatch,
	splitRecommended,
	toggleMultiPatch,
} from "./ask-user-question-state";

interface Props {
	question: AskUserQuestionItem;
	state: QuestionState;
	pageKeys: boolean;
	onChange: (next: Partial<QuestionState>) => void;
	onConfirm: (next: QuestionState) => void;
}

let { question, state: questionState, pageKeys, onChange, onConfirm }: Props = $props();
let choiceRefs = $state<Array<HTMLButtonElement | undefined>>([]);
let otherRef = $state<HTMLInputElement>();
let noteRef = $state<HTMLTextAreaElement>();
let focusNoteAfterRender = false;
let otherIndex = $derived(question.options.length);
let choiceCount = $derived(otherIndex + 1);
let cursor = $derived(Math.min(Math.max(questionState.cursor, 0), Math.max(otherIndex - 1, 0)));
let customOwnsPageFocus = $derived(
	questionState.customActive && (!question.multiSelect || questionState.multi.length === 0),
);
let anyPreview = $derived(
	!question.multiSelect && question.options.some((option) => option.preview),
);
let previewSource = $derived(
	question.options.find((option) => option.label === questionState.option && option.preview) ??
		question.options.find((option) => option.preview),
);

$effect(() => {
	const noteFor = questionState.noteFor;
	if (!focusNoteAfterRender || !noteFor) return;
	void tick().then(() => {
		if (!focusNoteAfterRender || !noteRef) return;
		focusNoteAfterRender = false;
		noteRef.focus({ preventScroll: true });
	});
});

function openNote(label: string, index: number): void {
	focusNoteAfterRender = true;
	onChange(
		question.multiSelect
			? { cursor: index, noteFor: label }
			: { cursor: index, option: label, customActive: false, noteFor: label },
	);
}

function selectOption(label: string, index: number): void {
	onChange(selectOptionPatch(questionState, label, index));
}

function toggleMulti(label: string, index: number): void {
	onChange(toggleMultiPatch(questionState, label, index));
}

function setCursor(index: number): void {
	if (index !== questionState.cursor) onChange({ cursor: index });
}

function focusChoice(index: number): void {
	if (index < otherIndex) setCursor(index);
	(index === otherIndex ? otherRef : choiceRefs[index])?.focus({ preventScroll: true });
}

function moveCursor(index: number): void {
	focusChoice(index);
	const target = question.options[index];
	if (!question.multiSelect && target) selectOption(target.label, index);
}

function finishNote(index: number): void {
	onChange({ noteFor: null });
	requestAnimationFrame(() => choiceRefs[index]?.focus({ preventScroll: true }));
}

function choiceKeyDown(event: KeyboardEvent, label: string, index: number): void {
	if (event.altKey || event.ctrlKey || event.metaKey) return;
	const action = choiceKeyAction(event.key, index, choiceCount);
	if (action.type === "none") return;
	event.preventDefault();
	if (action.type === "move") moveCursor(action.index);
	else if (action.type === "select") {
		if (question.multiSelect) toggleMulti(label, index);
		else selectOption(label, index);
	} else if (action.type === "confirm") {
		onConfirm(
			confirmStateFor(questionState, !!question.multiSelect, {
				kind: "choice",
				label,
				cursor: index,
			}),
		);
	}
}
</script>

<div class="flex flex-col gap-md">
	<div class="flex items-start gap-sm">
		<Icon name="message-circle-question" size={16} class="mt-0.5 shrink-0 text-text-muted" />
		<p data-testid="ask-question-text" class="tr-title-dialog text-text-default">{question.question}</p>
	</div>
	<div class={`grid gap-sm ${anyPreview ? "md:grid-cols-2" : ""}`}>
		<div class="flex min-w-0 flex-col gap-sm">
			<div role={question.multiSelect ? "group" : "radiogroup"} aria-label={question.question} class="flex flex-col gap-sm">
				{#each question.options as option, index (option.label)}
					{@const selected = question.multiSelect ? questionState.multi.includes(option.label) : questionState.option === option.label}
					{@const optionText = splitRecommended(option.label).text}
					{@const noteText = questionState.notes[option.label]?.trim()}
					<AskOption
						bind:element={choiceRefs[index]}
						label={option.label}
						description={option.description}
						recommendedReason={option.recommendedReason}
						{selected}
						cursor={index === cursor}
						pageFocus={index === cursor && !customOwnsPageFocus}
						multi={!!question.multiSelect}
						{pageKeys}
						onfocus={() => setCursor(index)}
						onkeydown={(event) => choiceKeyDown(event, option.label, index)}
						onclick={() => question.multiSelect ? toggleMulti(option.label, index) : selectOption(option.label, index)}
					/>
					{#if selected}
						<div class="pl-[calc(1.125rem+var(--spacing-sm))]">
							{#if questionState.noteFor === option.label}
								<textarea
									bind:this={noteRef}
									data-testid="ask-note"
									aria-label={`Note for ${optionText}`}
									aria-keyshortcuts="Enter Shift+Enter Escape"
									rows={2}
									value={questionState.notes[option.label] ?? ""}
									placeholder="Add a note for the model…"
									oninput={(event) => onChange({ notes: { ...questionState.notes, [option.label]: event.currentTarget.value } })}
									onkeydown={(event) => {
										const action = noteKeyAction(event.key, event.shiftKey, event.isComposing);
										if (action === "none") return;
										event.stopPropagation();
										if (action === "consume") return;
										event.preventDefault();
										finishNote(index);
									}}
									class="w-full resize-none rounded-[var(--radius-sm)] border border-control-border-default bg-control-bg px-sm py-xs text-text-default tr-text-metadata outline-none focus-visible:border-control-border-active"
								></textarea>
							{:else}
								<button
									type="button"
									data-testid="ask-note-toggle"
									aria-label={`${noteText ? "Edit" : "Add"} note for ${optionText}`}
									onclick={() => openNote(option.label, index)}
									class="flex items-center gap-xs rounded-[var(--radius-sm)] text-text-muted tr-text-metadata outline-none hover:text-text-default focus-visible:ring-2 focus-visible:ring-primary"
								><Icon name="pencil" size={12} />{noteText ? "Edit note" : "Add note"}</button>
							{/if}
						</div>
					{/if}
				{/each}
			</div>

			<AskOther
				bind:element={otherRef}
				multi={!!question.multiSelect}
				active={questionState.customActive}
				text={questionState.customText}
				pageFocus={question.options.length === 0 || customOwnsPageFocus}
				onToggle={() => onChange({ customActive: !questionState.customActive })}
				onText={(text) => onChange(customTextPatch(text))}
				onMove={(key) => {
					const action = choiceKeyAction(key, otherIndex, choiceCount);
					if (action.type === "move") moveCursor(action.index);
				}}
				onConfirm={() => onConfirm(questionState)}
			/>
		</div>

		{#if anyPreview && previewSource?.preview}
			<div data-testid="ask-preview" class="min-w-0 overflow-auto rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-sm py-xs tr-text-metadata">
				<div class="mb-xs text-text-muted tr-text-metadata">Preview · {previewSource.label}</div>
				<Markdown text={previewSource.preview} />
			</div>
		{/if}
	</div>
</div>
