import type { AskUserQuestionResult } from "@pixie/contracts";
import { getContext, setContext } from "svelte";
import type { ChatTurn } from "./types";

export interface AskState {
	answer?: AskUserQuestionResult;
	superseded: boolean;
}

export function deriveAskStates(
	turns: ChatTurn[],
	askAnswers: Record<string, AskUserQuestionResult>,
): Record<string, AskState> {
	const callTurnIndex: Record<string, number> = {};
	let lastUserIndex = -1;
	for (let i = 0; i < turns.length; i++) {
		const turn = turns[i];
		if (!turn) continue;
		if (turn.kind === "user") {
			lastUserIndex = i;
		} else if (turn.kind === "assistant") {
			for (const block of turn.message.content) {
				if (block.type === "toolCall" && block.name === "ask_user_question")
					callTurnIndex[block.id] = i;
			}
		}
	}
	const states: Record<string, AskState> = {};
	for (const [toolCallId, turnIndex] of Object.entries(callTurnIndex)) {
		const answer = askAnswers[toolCallId];
		states[toolCallId] = {
			...(answer ? { answer } : {}),
			superseded: !answer && lastUserIndex > turnIndex,
		};
	}
	return states;
}

export interface AskContextValue {
	stateFor: (toolCallId: string) => AskState | undefined;
	focusScope: object;
}

const ASK_STATES_CONTEXT = Symbol("pixie.ask-states");

export function setAskStatesContext(value: AskContextValue): AskContextValue {
	return setContext(ASK_STATES_CONTEXT, value);
}

export function getAskStatesContext(): AskContextValue | null {
	return getContext<AskContextValue | undefined>(ASK_STATES_CONTEXT) ?? null;
}
