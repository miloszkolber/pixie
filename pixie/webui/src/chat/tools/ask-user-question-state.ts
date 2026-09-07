import type {
	AskUserQuestionAnswer,
	AskUserQuestionArgs,
	AskUserQuestionItem,
	AskUserQuestionResult,
} from "@pixie/contracts";

export function parseQuestions(args: Record<string, unknown>): AskUserQuestionItem[] {
	const qs = (args as Partial<AskUserQuestionArgs>).questions;
	return Array.isArray(qs) ? qs.filter((q) => q && Array.isArray(q.options)) : [];
}

export function splitRecommended(label: string): { text: string; recommended: boolean } {
	const m = /\s*\(recommended\)\s*$/i.exec(label);
	return m
		? { text: label.slice(0, m.index).trim(), recommended: true }
		: { text: label, recommended: false };
}

export function readRecommendation(option: {
	label: string;
	recommendedReason?: string | undefined;
}): {
	text: string;
	recommended: boolean;
	reason?: string | undefined;
} {
	const { text, recommended } = splitRecommended(option.label);
	const reason = option.recommendedReason?.trim() || undefined;
	return { text, recommended: recommended || !!reason, reason };
}

export interface QuestionState {
	option: string | null;
	customText: string;
	customActive: boolean;
	multi: string[];
	cursor: number;
	notes: Record<string, string>;
	noteFor: string | null;
}

export const emptyQuestionState = (): QuestionState => ({
	option: null,
	customText: "",
	customActive: false,
	multi: [],
	cursor: 0,
	notes: {},
	noteFor: null,
});

export function deriveAnswer(
	question: AskUserQuestionItem,
	index: number,
	state: QuestionState,
): AskUserQuestionAnswer | null {
	const base = { questionIndex: index, question: question.question };
	if (question.multiSelect) {
		const valid = state.multi.filter((label) => question.options.some((o) => o.label === label));
		const custom = state.customActive ? state.customText.trim() : "";
		if (valid.length === 0 && !custom) return null;
		const noteLines = valid.flatMap((label) => {
			const note = state.notes[label]?.trim();
			return note ? [`${splitRecommended(label).text}: ${note}`] : [];
		});
		return {
			...base,
			kind: "multi",
			answer: custom || null,
			selected: valid,
			...(noteLines.length > 0 ? { notes: noteLines.join("\n") } : {}),
		};
	}
	if (state.customActive && state.customText.trim()) {
		return { ...base, kind: "custom", answer: state.customText.trim() };
	}
	if (state.option != null) {
		const opt = question.options.find((o) => o.label === state.option);
		if (!opt) return null;
		const note = state.notes[state.option]?.trim();
		return {
			...base,
			kind: "option",
			answer: state.option,
			...(opt.preview ? { preview: opt.preview } : {}),
			...(note ? { notes: note } : {}),
		};
	}
	return null;
}

export function deriveAnswers(
	questions: AskUserQuestionItem[],
	states: Record<number, QuestionState>,
): AskUserQuestionAnswer[] {
	return questions
		.map((question, index) => deriveAnswer(question, index, states[index] ?? emptyQuestionState()))
		.filter((answer): answer is AskUserQuestionAnswer => answer != null);
}

export function answerSupportsNote(answer: AskUserQuestionAnswer): boolean {
	if (answer.kind === "option") return true;
	return answer.kind === "multi" && (answer.selected?.length ?? 0) > 0;
}

export function readAskResult(raw: unknown): AskUserQuestionResult | null {
	const isResult = (v: unknown): v is AskUserQuestionResult =>
		!!v &&
		typeof v === "object" &&
		Array.isArray((v as AskUserQuestionResult).answers) &&
		typeof (v as AskUserQuestionResult).cancelled === "boolean";
	if (isResult(raw)) return raw;
	if (!raw || typeof raw !== "object") {
		if (typeof raw !== "string") return null;
		try {
			const parsed: unknown = JSON.parse(raw);
			return isResult(parsed) ? parsed : null;
		} catch {
			return null;
		}
	}
	const record = raw as {
		details?: unknown;
		structuredContent?: unknown;
		content?: unknown;
	};
	if (isResult(record.details)) return record.details;
	if (isResult(record.structuredContent)) return record.structuredContent;
	if (Array.isArray(record.content)) {
		for (const item of record.content) {
			if (!item || typeof item !== "object" || typeof Reflect.get(item, "text") !== "string")
				continue;
			try {
				const parsed: unknown = JSON.parse(Reflect.get(item, "text") as string);
				if (isResult(parsed)) return parsed;
			} catch {
				// Non-JSON tool text is rendered as a plain resolved record below.
			}
		}
	}
	return null;
}

interface RecapState {
	selectedLabels: string[];
	customAnswer: string | null;
	showOptions: boolean;
}

export function deriveRecapState(
	answer: AskUserQuestionAnswer | undefined,
	variant: "review" | "resolved",
): RecapState {
	const selectedLabels =
		answer?.kind === "multi"
			? (answer.selected ?? [])
			: answer?.kind === "option" && answer.answer
				? [answer.answer]
				: [];
	const customAnswer =
		answer && (answer.kind === "custom" || answer.kind === "multi") ? answer.answer : null;
	return {
		selectedLabels,
		customAnswer,
		showOptions: variant === "review" || (!!answer && answer.kind !== "custom"),
	};
}

export type ChoiceKeyAction =
	| { type: "move"; index: number }
	| { type: "select" }
	| { type: "confirm" }
	| { type: "none" };

export function choiceKeyAction(key: string, index: number, count: number): ChoiceKeyAction {
	if (count <= 0) return { type: "none" };
	if (key === "ArrowDown") return { type: "move", index: (index + 1) % count };
	if (key === "ArrowUp") return { type: "move", index: (index - 1 + count) % count };
	if (key === "Home") return { type: "move", index: 0 };
	if (key === "End") return { type: "move", index: count - 1 };
	if (key === " " || key === "Spacebar") return { type: "select" };
	if (key === "Enter") return { type: "confirm" };
	return { type: "none" };
}

export function customTextPatch(text: string): Partial<QuestionState> {
	return text.trim()
		? { customText: text, customActive: true, option: null }
		: { customText: text, customActive: false };
}

export function selectOptionPatch(
	state: QuestionState,
	label: string,
	cursor: number,
): Partial<QuestionState> {
	return {
		cursor,
		option: label,
		customActive: false,
		...(state.noteFor != null && state.noteFor !== label ? { noteFor: null } : {}),
	};
}

export function toggleMultiPatch(
	state: QuestionState,
	label: string,
	cursor: number,
): Partial<QuestionState> {
	const removing = state.multi.includes(label);
	return {
		cursor,
		multi: removing ? state.multi.filter((item) => item !== label) : [...state.multi, label],
		...(removing && state.noteFor === label ? { noteFor: null } : {}),
	};
}

export type ConfirmSource = { kind: "choice"; label: string; cursor: number } | { kind: "custom" };

export function confirmStateFor(
	state: QuestionState,
	multiSelect: boolean,
	source: ConfirmSource,
): QuestionState {
	if (source.kind === "custom") return state;
	return multiSelect
		? { ...state, cursor: source.cursor }
		: { ...state, cursor: source.cursor, option: source.label, customActive: false };
}

export function noteKeyAction(
	key: string,
	shiftKey: boolean,
	isComposing: boolean,
): "finish" | "consume" | "none" {
	if (key === "Escape") return isComposing ? "consume" : "finish";
	if (isComposing) return "none";
	if (key === "Enter" && !shiftKey) return "finish";
	return "none";
}

export interface ChoiceNudge {
	question: number;
	seq: number;
}

export function nudgeShowsOnPage(
	nudge: ChoiceNudge | null,
	page: number,
	onReview: boolean,
	answered: boolean,
): boolean {
	return !!nudge && !onReview && nudge.question === page && !answered;
}

export function questionPageForKey(key: string, current: number, last: number): number | null {
	if (key === "ArrowLeft") return Math.max(0, current - 1);
	if (key === "ArrowRight") return Math.min(last, current + 1);
	return null;
}

export type QuestionFocusTarget =
	| "none"
	| "non-editing"
	| "empty-composer"
	| "draft-composer"
	| "editing"
	| "modal";

export function shouldClaimQuestionFocus(
	target: QuestionFocusTarget,
	coarsePointer: boolean,
): boolean {
	if (coarsePointer) return false;
	return target === "none" || target === "non-editing" || target === "empty-composer";
}

export function shouldFocusPageTarget(textEntryTarget: boolean, coarsePointer: boolean): boolean {
	return !(textEntryTarget && coarsePointer);
}

export function createQuestionAttentionClaim(): (scope: object, toolCallId: string) => boolean {
	const claims = new WeakMap<object, Set<string>>();
	return (scope, toolCallId) => {
		let ids = claims.get(scope);
		if (!ids) {
			ids = new Set<string>();
			claims.set(scope, ids);
		}
		if (ids.has(toolCallId)) return false;
		ids.add(toolCallId);
		return true;
	};
}
