import type {
	AskUserQuestionAnswer,
	AskUserQuestionItem,
	AskUserQuestionResult,
} from "@pixie/contracts";

export function parseQuestions(args: Record<string, unknown>): AskUserQuestionItem[] {
	const qs = args.questions;
	return Array.isArray(qs)
		? qs.filter((q) => q && typeof q.question === "string" && Array.isArray(q.options))
		: [];
}

export function splitRecommended(label: string): { text: string; recommended: boolean } {
	const m = /\s*\(recommended\)\s*$/i.exec(label);
	return m
		? { text: label.slice(0, m.index).trim(), recommended: true }
		: { text: label, recommended: false };
}

/** Decode persisted results only. Pending interaction is owned by the generic dialog bridge. */
export function readAskResult(raw: unknown): AskUserQuestionResult | null {
	const isResult = (v: unknown): v is AskUserQuestionResult =>
		!!v &&
		typeof v === "object" &&
		Array.isArray((v as AskUserQuestionResult).answers) &&
		typeof (v as AskUserQuestionResult).cancelled === "boolean";
	if (isResult(raw)) return raw;
	if (typeof raw === "string") {
		try {
			const parsed: unknown = JSON.parse(raw);
			return isResult(parsed) ? parsed : null;
		} catch {
			return null;
		}
	}
	if (!raw || typeof raw !== "object") return null;
	const record = raw as { details?: unknown; structuredContent?: unknown; content?: unknown };
	if (isResult(record.details)) return record.details;
	if (isResult(record.structuredContent)) return record.structuredContent;
	if (Array.isArray(record.content)) {
		for (const item of record.content) {
			if (!item || typeof item !== "object" || typeof item.text !== "string") continue;
			const result = readAskResult(item.text);
			if (result) return result;
		}
	}
	return null;
}

export function deriveRecapState(answer: AskUserQuestionAnswer | undefined) {
	return {
		selectedLabels:
			answer?.kind === "multi"
				? (answer.selected ?? [])
				: answer?.kind === "option" && answer.answer
					? [answer.answer]
					: [],
		customAnswer:
			answer && (answer.kind === "custom" || answer.kind === "multi") ? answer.answer : null,
		showOptions: !!answer && answer.kind !== "custom",
	};
}
