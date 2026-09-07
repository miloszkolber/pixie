import type { AskUserQuestionResult } from "@pixie/contracts";
import { getContext, setContext } from "svelte";

export interface ChatActions {
	answerQuestion: (toolCallId: string, result: AskUserQuestionResult) => Promise<void>;
	focusComposer: () => void;
}

const CHAT_ACTIONS_CONTEXT = Symbol("pixie.chat-actions");

export function setChatActionsContext(actions: ChatActions): ChatActions {
	return setContext(CHAT_ACTIONS_CONTEXT, actions);
}

export function getChatActions(): ChatActions | null {
	return getContext<ChatActions | undefined>(CHAT_ACTIONS_CONTEXT) ?? null;
}
