import type { SlashCommandInfo } from "@pixie/contracts";
import type { ChatAttachment } from "../runtime/types";

export type SubmitBehavior = "send" | "steer" | "queue" | "interrupt";

export interface ComposerHandle {
	insertText: (text: string) => void;
	insertAndSubmit: (text: string, behavior: SubmitBehavior) => void;
	openHistory: () => void;
	refocus: () => void;
}

export interface ComposerProps {
	value: string;
	onChange: (value: string) => void;
	isStreaming: boolean;
	commands: SlashCommandInfo[];
	mentionCandidates: MentionCandidate[];
	recentPrompts: string[];
	onMentionQuery: (query: string | null) => void;
	onSubmit: (
		text: string,
		attachments: ChatAttachment[],
		behavior: SubmitBehavior,
	) => boolean | undefined;
	onAbort: () => void;
	onHistoryOpen?: (() => void) | undefined;
	supportsImages?: boolean | null;
	supportsTextResources?: boolean | null;
	supportsSteer?: boolean;
}

export type MentionCandidate =
	| { path: string; name: string; kind: "file" | "dir" }
	| {
			name: string;
			description: string;
			sourceType: "skill" | "builtinSkill" | "agent" | "project";
			mention: string;
			kind: "agent";
	  };

export interface StreamingSendMode {
	behavior: Exclude<SubmitBehavior, "send">;
	name: string;
	meaning: string;
	keys: string;
	testid: string;
}

const STREAMING_SEND_MODES: readonly StreamingSendMode[] = [
	{
		behavior: "queue",
		name: "Queue follow-up",
		meaning: "runs after the agent finishes",
		keys: "Cmd/Ctrl+Enter",
		testid: "send-mode-queue",
	},
	{
		behavior: "steer",
		name: "Steer",
		meaning: "delivers at the agent's next step",
		keys: "Enter",
		testid: "send-mode-steer",
	},
	{
		behavior: "interrupt",
		name: "Interrupt",
		meaning: "stops the current response and sends now",
		keys: "Cmd/Ctrl+Shift+Enter",
		testid: "send-mode-interrupt",
	},
];

export function streamingSubmitBehavior(supportsSteer: boolean): "steer" | "queue" {
	return supportsSteer ? "steer" : "queue";
}

export function streamingSendModes(supportsSteer: boolean): readonly StreamingSendMode[] {
	return supportsSteer
		? STREAMING_SEND_MODES
		: STREAMING_SEND_MODES.filter((mode) => mode.behavior !== "steer");
}

export function insertedMention(candidate: MentionCandidate): string {
	return candidate.kind === "agent"
		? candidate.mention
		: candidate.kind === "dir"
			? `@${candidate.path}/`
			: `@${candidate.path}`;
}

export function agentMentionLabel(candidate: Extract<MentionCandidate, { kind: "agent" }>): string {
	return `${agentMentionTypeLabel(candidate.sourceType)} mention: ${candidate.name}. ${candidate.description}`;
}

export function agentMentionSummary(
	candidate: Extract<MentionCandidate, { kind: "agent" }>,
): string {
	return `${agentMentionTypeLabel(candidate.sourceType)} · ${candidate.description}`;
}

export function agentMentionTypeLabel(
	sourceType: Extract<MentionCandidate, { kind: "agent" }>["sourceType"],
): string {
	return {
		skill: "skill",
		builtinSkill: "built-in skill",
		agent: "agent",
		project: "project",
	}[sourceType];
}

export function clampedMentionActiveIndex(activeIndex: number, candidateCount: number): number {
	return candidateCount > 0 ? Math.min(Math.max(activeIndex, 0), candidateCount - 1) : 0;
}

export type MentionCompletionKeyAction =
	| { type: "none" }
	| { type: "move"; index: number }
	| { type: "select"; index: number }
	| { type: "dismiss" };

export function mentionCompletionKeyAction(
	key: string,
	open: boolean,
	activeIndex: number,
	candidateCount: number,
): MentionCompletionKeyAction {
	if (!open || candidateCount === 0) return { type: "none" };
	const visibleIndex = clampedMentionActiveIndex(activeIndex, candidateCount);
	if (key === "ArrowDown") return { type: "move", index: (visibleIndex + 1) % candidateCount };
	if (key === "ArrowUp") {
		return { type: "move", index: (visibleIndex - 1 + candidateCount) % candidateCount };
	}
	if (key === "Enter" || key === "Tab") return { type: "select", index: visibleIndex };
	if (key === "Escape") return { type: "dismiss" };
	return { type: "none" };
}

const IMAGE_EXTENSION_BY_MIME: Record<string, string> = {
	"image/png": "png",
	"image/jpeg": "jpg",
	"image/gif": "gif",
	"image/webp": "webp",
};

const GENERIC_CLIPBOARD_IMAGE_NAMES = new Set([
	"",
	"clipboard",
	"clipboard image",
	"image",
	"image.png",
	"image.jpg",
	"image.jpeg",
	"image.gif",
	"image.webp",
]);

export function imageAttachmentTag(name: string): string {
	return `[${name}]`;
}

export function clipboardImageName(
	name: string,
	mimeType: string,
	existingNames: readonly string[],
	draft: string,
): string {
	const sourceName = name.trim();
	const taken = (candidate: string) =>
		existingNames.includes(candidate) || draft.includes(imageAttachmentTag(candidate));
	if (GENERIC_CLIPBOARD_IMAGE_NAMES.has(sourceName.toLowerCase())) {
		const extension = IMAGE_EXTENSION_BY_MIME[mimeType] ?? "png";
		for (let index = 1; ; index += 1) {
			const candidate = `image-${index}.${extension}`;
			const sequence = `image-${index}.`;
			const sequenceTaken =
				existingNames.some((existingName) => existingName.startsWith(sequence)) ||
				draft.includes(`[${sequence}`);
			if (!taken(candidate) && !sequenceTaken) return candidate;
		}
	}
	if (!taken(sourceName)) return sourceName;
	const dot = sourceName.lastIndexOf(".");
	const stem = dot > 0 ? sourceName.slice(0, dot) : sourceName;
	const extension = dot > 0 ? sourceName.slice(dot) : "";
	for (let index = 2; ; index += 1) {
		const candidate = `${stem}-${index}${extension}`;
		if (!taken(candidate)) return candidate;
	}
}

export function insertImageTags(
	value: string,
	selectionStart: number,
	selectionEnd: number,
	names: readonly string[],
): { value: string; caret: number } {
	if (names.length === 0) return { value, caret: selectionStart };
	const before = value.slice(0, selectionStart);
	const after = value.slice(selectionEnd);
	const tags = names.map(imageAttachmentTag).join(" ");
	const beforeSpace = before.length > 0 && !/\s$/.test(before) ? " " : "";
	const afterSpace = after.length > 0 && !/^\s/.test(after) ? " " : "";
	const inserted = `${beforeSpace}${tags}${afterSpace}`;
	return {
		value: `${before}${inserted}${after}`,
		caret: before.length + inserted.length,
	};
}

export function removeImageTag(value: string, name: string): string {
	const tag = imageAttachmentTag(name);
	const index = value.indexOf(tag);
	return index < 0 ? value : `${value.slice(0, index)}${value.slice(index + tag.length)}`;
}

export function removeImageTags(
	value: string,
	caret: number,
	names: readonly string[],
): { value: string; caret: number } {
	let nextValue = value;
	let nextCaret = caret;
	for (const name of names) {
		const tag = imageAttachmentTag(name);
		const index = nextValue.indexOf(tag);
		if (index < 0) continue;
		nextValue = `${nextValue.slice(0, index)}${nextValue.slice(index + tag.length)}`;
		if (nextCaret > index) nextCaret = Math.max(index, nextCaret - tag.length);
	}
	return { value: nextValue, caret: nextCaret };
}

export function reserveClipboardImageNames(
	files: readonly Pick<File, "name" | "type">[],
	existingNames: readonly string[],
	draft: string,
): string[] {
	const occupiedNames = [...existingNames];
	return files.map((file) => {
		const name = clipboardImageName(file.name, file.type, occupiedNames, draft);
		occupiedNames.push(name);
		return name;
	});
}

export function activeToken(value: string, caret: number): { token: string; start: number } {
	const match = /(\S+)$/.exec(value.slice(0, caret));
	if (!match) return { token: "", start: caret };
	return { token: match[0], start: caret - match[0].length };
}

export interface ComposerEnterKey {
	key: string;
	shiftKey: boolean;
	metaKey: boolean;
	ctrlKey: boolean;
	isComposing?: boolean;
	keyCode?: number;
}

export function composerEnterBehavior(
	event: ComposerEnterKey,
	isStreaming: boolean,
	supportsSteer: boolean,
): SubmitBehavior | null {
	if (event.isComposing || event.keyCode === 229 || event.key !== "Enter") return null;
	if (event.shiftKey && (event.metaKey || event.ctrlKey)) {
		return isStreaming ? "interrupt" : "send";
	}
	if (!event.shiftKey && (event.metaKey || event.ctrlKey)) {
		return isStreaming ? "queue" : "send";
	}
	if (!event.shiftKey) return isStreaming ? streamingSubmitBehavior(supportsSteer) : "send";
	return null;
}

export function slashCommandKey(command: SlashCommandInfo): string {
	return `${command.source}:${command.sourceInfo.path}:${command.name}`;
}
