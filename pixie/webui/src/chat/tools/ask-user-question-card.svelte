<script lang="ts">
import type { AskUserQuestionItem, AskUserQuestionResult } from "@pixie/contracts";
import { untrack } from "svelte";
import Icon from "../../components/icon.svelte";
import type { ToolRenderProps } from "../render/tool-registry";
import { getAskStatesContext } from "../runtime/ask-state";
import { getChatActions } from "../session/chat-actions";
import QuestionBody from "./ask-user-question-body.svelte";
import ResolvedRecord from "./ask-user-question-record.svelte";
import ReviewView from "./ask-user-question-review.svelte";
import {
	answerSupportsNote,
	type ChoiceNudge,
	createQuestionAttentionClaim,
	deriveAnswers,
	emptyQuestionState,
	nudgeShowsOnPage,
	parseQuestions,
	type QuestionFocusTarget,
	type QuestionState,
	questionPageForKey,
	readAskResult,
	shouldClaimQuestionFocus,
	shouldFocusPageTarget,
} from "./ask-user-question-state";
import SupersededRecord from "./ask-user-question-superseded.svelte";
import { resultText } from "./tool-helpers";

const panelDomId = (toolCallId: string) => `ask-panel-${toolCallId}`;
const tabDomId = (toolCallId: string, page: number | "review") => `ask-tab-${toolCallId}-${page}`;
const claimQuestionAttention = createQuestionAttentionClaim();
const ATTENTION_SETTLE_FRAMES = 30;
const MODAL_SURFACES = '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]';

interface CachedCardState {
	states: Record<number, QuestionState>;
	tab: number;
	submitted: boolean;
}
const cardStateCache = new Map<string, CachedCardState>();

let { toolCallId, args, result, status }: ToolRenderProps = $props();
const actions = getChatActions();
const askContext = getAskStatesContext();
const localFocusScope = {};
const focusScope = askContext?.focusScope ?? localFocusScope;
let card = $state<HTMLElement>();
let questions = $derived(parseQuestions(args));
let ask = $derived(askContext?.stateFor(toolCallId));
let resolvedResult = $derived(ask?.answer ?? readAskResult(result));
let awaiting = $derived(!resolvedResult && !ask?.superseded && status !== "error");
const cached = untrack(() => cardStateCache.get(toolCallId));
let states = $state<Record<number, QuestionState>>(cached?.states ?? {});
let tab = $state(cached?.tab ?? 0);
let submitted = $state(cached?.submitted ?? false);
let announced = $state(false);
let nudge = $state<ChoiceNudge | null>(null);
let nudgeSpoken = $state(false);
let nudgeSeq = 0;
let previousTab = untrack(() => tab);
let reclaimFocusAfterFailedSend = false;
let answers = $derived(deriveAnswers(questions, states));
let answeredIndices = $derived(new Set(answers.map((answer) => answer.questionIndex)));
let multipleQuestions = $derived(questions.length > 1);
let reviewTab = $derived(questions.length);
let onReview = $derived(tab >= reviewTab);
let questionIndex = $derived(Math.min(tab, questions.length - 1));
let question = $derived(questions[questionIndex]);
let questionState = $derived(states[questionIndex] ?? emptyQuestionState());
let showContinue = $derived(multipleQuestions && !onReview);
let canSubmit = $derived(
	!!actions &&
		(onReview || !multipleQuestions ? answers.length > 0 : answeredIndices.has(questionIndex)),
);
let nudgeVisible = $derived(
	nudgeShowsOnPage(nudge, questionIndex, onReview, answeredIndices.has(questionIndex)),
);

function focusTargetKind(active: Element | null, element: HTMLElement): QuestionFocusTarget {
	if (!active || active === document.body) return "none";
	if (element.contains(active)) return "non-editing";
	if (!(active instanceof HTMLElement)) return "editing";
	if (active.closest(MODAL_SURFACES)) return "modal";
	if (active.isContentEditable || active.closest('[contenteditable="true"]')) return "editing";
	const control = active.closest("input, textarea, select, iframe");
	if (!control) return "non-editing";
	if (control instanceof HTMLTextAreaElement && control.dataset.testid === "chat-input") {
		return control.value.length === 0 ? "empty-composer" : "draft-composer";
	}
	return "editing";
}

function hasCoarsePointer(): boolean {
	return window.matchMedia("(pointer: coarse)").matches;
}

function isTextEntryTarget(target: EventTarget | null): boolean {
	return (
		target instanceof HTMLElement &&
		(!!target.closest("input, textarea, select") ||
			target.isContentEditable ||
			!!target.closest('[contenteditable="true"]'))
	);
}

function focusCurrentQuestionPage(element: HTMLElement): void {
	const target = element.querySelector<HTMLElement>('[data-ask-page-focus="true"]:not([disabled])');
	if (!target || !shouldFocusPageTarget(isTextEntryTarget(target), hasCoarsePointer())) return;
	target.focus({ preventScroll: true });
}

function focusQuestionAttention(element: HTMLElement): void {
	const selected = element.querySelector<HTMLElement>(
		'[data-testid="ask-option"][data-selected="true"], [data-testid="ask-custom-row"][data-selected="true"] input',
	);
	if (selected) selected.focus({ preventScroll: true });
	else focusCurrentQuestionPage(element);
}

$effect(() => {
	if (awaiting) cardStateCache.set(toolCallId, { states, tab, submitted });
	else cardStateCache.delete(toolCallId);
});

$effect(() => {
	const current = nudge;
	nudgeSpoken = false;
	if (!current) return;
	const frame = requestAnimationFrame(() => (nudgeSpoken = true));
	const timer = setTimeout(() => {
		if (nudge === current) nudge = null;
	}, 2500);
	return () => {
		cancelAnimationFrame(frame);
		clearTimeout(timer);
	};
});

$effect(() => {
	if (!reclaimFocusAfterFailedSend || submitted) return;
	reclaimFocusAfterFailedSend = false;
	if (card) focusQuestionAttention(card);
});

$effect(() => {
	const current = tab;
	if (previousTab === current) return;
	previousTab = current;
	const frame = requestAnimationFrame(() => {
		if (card) focusCurrentQuestionPage(card);
	});
	return () => cancelAnimationFrame(frame);
});

$effect(() => {
	if (!awaiting || questions.length === 0 || submitted) return;
	const currentToolCallId = toolCallId;
	let frame: number | null = null;
	let attempts = 0;
	let userTookOver = false;
	const yieldToUser = () => {
		userTookOver = true;
	};
	window.addEventListener("pointerdown", yieldToUser, { capture: true, once: true });
	window.addEventListener("keydown", yieldToUser, { capture: true, once: true });
	const settleFocus = () => {
		if (!card || userTookOver) return;
		if (
			!shouldClaimQuestionFocus(focusTargetKind(document.activeElement, card), hasCoarsePointer())
		)
			return;
		focusQuestionAttention(card);
		if (card.contains(document.activeElement)) return;
		attempts += 1;
		if (attempts < ATTENTION_SETTLE_FRAMES) frame = requestAnimationFrame(settleFocus);
	};
	frame = requestAnimationFrame(() => {
		if (!claimQuestionAttention(focusScope, currentToolCallId)) return;
		announced = true;
		if (!card) return;
		card.scrollIntoView({ block: "nearest" });
		settleFocus();
	});
	return () => {
		if (frame != null) cancelAnimationFrame(frame);
		window.removeEventListener("pointerdown", yieldToUser, { capture: true });
		window.removeEventListener("keydown", yieldToUser, { capture: true });
	};
});

function patch(index: number, next: Partial<QuestionState>): void {
	states = { ...states, [index]: { ...(states[index] ?? emptyQuestionState()), ...next } };
}

function reply(answer: AskUserQuestionResult): void {
	if (!actions) return;
	const held = document.activeElement;
	const handedOff = !!held && !!card?.contains(held) && held.matches(":focus-visible");
	if (handedOff) actions.focusComposer();
	submitted = true;
	void actions.answerQuestion(toolCallId, answer).catch(() => {
		reclaimFocusAfterFailedSend = handedOff;
		submitted = false;
	});
}

function confirmQuestion(nextState: QuestionState): void {
	const nextStates = { ...states, [questionIndex]: nextState };
	const nextAnswers = deriveAnswers(questions, nextStates);
	if (!nextAnswers.some((answer) => answer.questionIndex === questionIndex)) {
		nudgeSeq += 1;
		nudge = { question: questionIndex, seq: nudgeSeq };
		return;
	}
	nudge = null;
	states = nextStates;
	if (multipleQuestions) tab = Math.min(questionIndex + 1, reviewTab);
	else reply({ answers: nextAnswers, cancelled: false });
}

function cardKeyDown(event: KeyboardEvent): void {
	if (event.isComposing) return;
	if (
		event.key === "Escape" &&
		event.shiftKey &&
		!event.altKey &&
		!event.ctrlKey &&
		!event.metaKey
	) {
		event.preventDefault();
		event.stopPropagation();
		reply({ answers: [], cancelled: true });
		return;
	}
	if (
		!multipleQuestions ||
		event.altKey ||
		event.ctrlKey ||
		event.metaKey ||
		isTextEntryTarget(event.target)
	)
		return;
	const next = questionPageForKey(event.key, tab, reviewTab);
	if (next == null) return;
	event.preventDefault();
	if (next !== tab) tab = next;
}
</script>

{#if resolvedResult}
	<ResolvedRecord {questions} result={resolvedResult} rawText={resultText(result)} />
{:else if ask?.superseded}
	<SupersededRecord {questions} />
{:else if status === "error"}
	<ResolvedRecord {questions} result={null} rawText={resultText(result)} />
{:else if questions.length === 0}
	<div class="flex flex-col gap-xs">
		<div class="text-text-muted tr-text-metadata">Agent is preparing questions…</div>
		<div data-testid="ask-user-question" data-tone="pending" class="flex flex-col gap-sm rounded-[var(--radius-lg)] border border-border-default bg-container-elevated-bg px-md py-sm">
			<div class="flex items-center gap-xs text-text-muted tr-text-metadata"><Icon name="message-circle-question" size={14} />Preparing questions…</div>
			<div class="flex animate-pulse flex-col gap-xs" aria-hidden="true"><div class="h-8 rounded-[var(--radius-sm)] bg-control-bg-selected"></div><div class="h-8 rounded-[var(--radius-sm)] bg-control-bg-selected"></div></div>
		</div>
	</div>
{:else if submitted}
	<div data-testid="ask-user-question" data-tone="pending" role="status" class="flex items-center gap-xs rounded-[var(--radius-lg)] border border-border-default bg-container-elevated-bg px-md py-sm text-text-muted tr-text-metadata">
		<Icon name="message-circle-question" size={14} /><span data-testid="ask-sent">Answer sent — continuing…</span>
	</div>
{:else if !question}
	<div data-testid="ask-user-question" data-tone="pending" role="status" class="flex items-center gap-xs rounded-[var(--radius-lg)] border border-border-default bg-container-elevated-bg px-md py-sm text-text-muted tr-text-metadata">
		<Icon name="message-circle-question" size={14} />Preparing questions…
	</div>
{:else}
	<div class="flex flex-col gap-xs motion-safe:animate-reveal">
		<div class="flex items-center gap-xs text-primary tr-text-action">
			<span aria-hidden={announced || undefined} class="flex items-center gap-xs">
				<Icon name="message-circle-question" size={14} /><span>Your input is needed</span><span class="text-text-muted tr-text-metadata">· Agent is waiting</span>
			</span>
			<span role="status" aria-live="polite" class="sr-only">{announced ? "Your input is needed — the agent is waiting." : ""}</span>
		</div>
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions (The question surface owns the documented cross-control keyboard shortcuts.) -->
		<section
			bind:this={card}
			data-testid="ask-user-question"
			data-tone="active"
			aria-label="Question from agent"
			aria-keyshortcuts="Shift+Escape"
			onkeydown={cardKeyDown}
			class="overflow-hidden rounded-[var(--radius-lg)] border border-primary bg-clip-padding bg-container-elevated-bg ring-2 ring-primary-soft"
		>
			{#if multipleQuestions}
				<div role="tablist" aria-label="Questions" class="flex items-center gap-xs overflow-x-auto border-border-default border-b px-md py-sm">
					{#each questions as item, index (item.question)}
						<button
							type="button" role="tab" id={tabDomId(toolCallId, index)} aria-controls={panelDomId(toolCallId)}
							aria-selected={tab === index} tabindex={tab === index ? 0 : -1} data-testid="ask-tab"
							data-active={tab === index} data-answered={answeredIndices.has(index)} onclick={() => (tab = index)}
							class={`flex shrink-0 items-center gap-xs whitespace-nowrap rounded-full px-sm py-0.5 tr-text-metadata outline-none focus-visible:ring-2 focus-visible:ring-primary ${tab === index ? "bg-primary-subtle text-primary" : "text-text-muted hover:bg-control-bg-hovered"}`}
						>
							<span class={`flex size-3.5 items-center justify-center rounded-full border ${answeredIndices.has(index) ? "border-primary text-primary" : "border-border-default"}`}>{#if answeredIndices.has(index)}<Icon name="check" size={10} />{/if}</span>
							{item.header || `Q${index + 1}`}
						</button>
					{/each}
					<button
						type="button" role="tab" id={tabDomId(toolCallId, "review")} aria-controls={panelDomId(toolCallId)}
						aria-selected={onReview} tabindex={onReview ? 0 : -1} data-testid="ask-tab" data-active={onReview} data-answered={false}
						onclick={() => (tab = reviewTab)}
						class={`flex shrink-0 items-center gap-xs whitespace-nowrap rounded-full px-sm py-0.5 tr-text-metadata outline-none focus-visible:ring-2 focus-visible:ring-primary ${onReview ? "bg-primary-subtle text-primary" : "text-text-muted hover:bg-control-bg-hovered"}`}
					><span class="flex size-3.5 items-center justify-center rounded-full border border-border-default"></span>Review & submit</button>
				</div>
			{/if}

			<div
				role={multipleQuestions ? "tabpanel" : undefined}
				id={multipleQuestions ? panelDomId(toolCallId) : undefined}
				aria-labelledby={multipleQuestions ? tabDomId(toolCallId, onReview ? "review" : questionIndex) : undefined}
				class="flex flex-col gap-md p-md"
			>
				{#if onReview}
					<ReviewView {questions} {answers} submitEnabled={canSubmit} onJump={(index) => (tab = index)} />
				{:else}
					<QuestionBody {question} state={questionState} pageKeys={multipleQuestions} onChange={(next) => patch(questionIndex, next)} onConfirm={confirmQuestion} />
				{/if}

				<div class="flex flex-col gap-sm sm:flex-row sm:items-center sm:justify-between">
					<span data-testid="ask-shortcuts" class="flex flex-wrap items-center gap-xs text-text-muted tr-text-metadata">
						<Icon name={onReview || question.multiSelect ? "list-checks" : "circle-dot"} size={14} class="shrink-0" />
						{#if onReview}
							Review · ←→ questions · Enter submit · Shift+Esc skip · Tab actions
						{:else}
							<span>{question.multiSelect ? "↑↓ move incl. Other" : "↑↓ move & select"}</span>
							<span>· Space {question.multiSelect ? "toggle" : "select"}</span><span>· Enter confirm</span>
							<span>· Tab {answers.some((answer) => answer.questionIndex === questionIndex && answerSupportsNote(answer)) ? "note/actions" : "actions"}</span>
							<span>· Shift+Esc skip</span>{#if multipleQuestions}<span>· ←→ questions</span>{/if}
						{/if}
					</span>
					<div class="flex items-center justify-end gap-md">
						{#if nudgeVisible}<span aria-hidden={nudgeSpoken || undefined} data-testid="ask-needs-choice" class="shrink-0 whitespace-nowrap text-feedback-warning tr-text-metadata">Choose an option first</span>{/if}
						<span role="status" aria-live="polite" class="sr-only">{nudgeVisible && nudgeSpoken ? "Choose an option first." : ""}</span>
						<button type="button" data-testid="ask-skip" onclick={() => reply({ answers: [], cancelled: true })} disabled={!actions} class="shrink-0 rounded-[var(--radius-sm)] px-xs text-text-muted tr-text-ui outline-none hover:text-text-default focus-visible:ring-2 focus-visible:ring-primary disabled:text-control-disabled-text">Skip</button>
						{#if showContinue}
							<button type="button" data-testid="ask-continue" onclick={() => (tab = Math.min(tab + 1, reviewTab))} class="shrink-0 whitespace-nowrap rounded-[var(--radius-sm)] bg-control-primary-bg px-md py-1.5 tr-text-action text-control-primary-text outline-none hover:bg-control-primary-bg-hovered focus-visible:ring-2 focus-visible:ring-primary">Next →</button>
						{:else}
							<button type="button" data-testid="ask-submit" data-ask-page-focus={onReview && canSubmit ? "true" : undefined} onclick={() => reply({ answers, cancelled: false })} disabled={!canSubmit} class="shrink-0 whitespace-nowrap rounded-[var(--radius-sm)] bg-control-primary-bg px-md py-1.5 tr-text-action text-control-primary-text outline-none hover:bg-control-primary-bg-hovered focus-visible:ring-2 focus-visible:ring-primary disabled:cursor-not-allowed disabled:bg-control-primary-disabled-bg disabled:text-control-primary-disabled-text">Submit</button>
						{/if}
					</div>
				</div>
			</div>
		</section>
	</div>
{/if}
