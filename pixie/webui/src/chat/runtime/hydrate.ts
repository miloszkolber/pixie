import type {
	AgentSettlement,
	PendingToolPreview,
	TranscriptMessage,
	TranscriptPage,
} from "@pixie/contracts";
import { randomId } from "@/lib";
import { terminalOutcome } from "./assistant-failure";
import { type SessionRuntime, settleUnfinishedTools } from "./session-runtime";
import type { ChatTurn, ToolResultState } from "./types";

export interface HydratedRuntime {
	turns: ChatTurn[];
	toolResults: Record<string, ToolResultState>;
	askAnswers: Record<string, never>;
	turnIdByMessageIndex: Record<number, string | null>;
	currentAssistantId: string | null;
	transcript: TranscriptPage | null;
	messageCount: number;
}

interface HydrationOptions {
	lastSettlement?: AgentSettlement | null | undefined;
	pendingTools?: PendingToolPreview[];
	page?: TranscriptPage;
	isStreaming?: boolean;
}

function replayTurnId(page: TranscriptPage | undefined, messageIndex: number): string {
	return page ? `transcript:${page.projectionId}:${messageIndex}` : randomId("turn");
}

/** Pi session.load replays are normalized in the server adapter before UI hydration. */
export function messagesToRuntime(
	messages: TranscriptMessage[],
	options: HydrationOptions = {},
): HydratedRuntime {
	const { lastSettlement, pendingTools = [], page, isStreaming = false } = options;
	const turns: ChatTurn[] = [];
	const toolResults = Object.create(null) as Record<string, ToolResultState>;
	const latestToolInvocation = Object.create(null) as Record<
		string,
		{ results: Record<number, ToolResultState | null>; blockIndex: number }
	>;
	const turnIdByMessageIndex = Object.create(null) as Record<number, string | null>;
	for (let offset = 0; offset < messages.length; offset++) {
		const message = messages[offset];
		if (!message) continue;
		const messageIndex = (page?.start ?? 0) + offset;
		if (message.role === "user") {
			const id = replayTurnId(page, messageIndex);
			turns.push({ kind: "user", id, message });
			turnIdByMessageIndex[messageIndex] = id;
		} else if (message.role === "assistant") {
			const id = replayTurnId(page, messageIndex);
			const toolResultsByBlock = Object.create(null) as Record<number, ToolResultState | null>;
			turns.push({ kind: "assistant", id, message, streaming: false, toolResultsByBlock });
			for (let blockIndex = 0; blockIndex < message.content.length; blockIndex++) {
				const block = message.content[blockIndex];
				if (block?.type !== "toolCall") continue;
				delete toolResults[block.id];
				toolResultsByBlock[blockIndex] = null;
				latestToolInvocation[block.id] = { results: toolResultsByBlock, blockIndex };
			}
			turnIdByMessageIndex[messageIndex] = id;
		} else {
			const result: ToolResultState = {
				status: message.isError ? "error" : "done",
				raw: message.content,
				...(message.app ? { app: message.app } : {}),
				...(message.subagentActivity ? { subagentActivity: message.subagentActivity } : {}),
			};
			toolResults[message.toolCallId] = result;
			const invocation = latestToolInvocation[message.toolCallId];
			if (invocation) invocation.results[invocation.blockIndex] = result;
			turnIdByMessageIndex[messageIndex] = null;
		}
	}
	for (const preview of pendingTools) {
		const result: ToolResultState = {
			status: "running",
			raw: preview.output,
			...(preview.app ? { app: preview.app } : {}),
			...(preview.subagentActivity ? { subagentActivity: preview.subagentActivity } : {}),
		};
		toolResults[preview.toolCallId] = result;
		const invocation = latestToolInvocation[preview.toolCallId];
		if (invocation) invocation.results[invocation.blockIndex] = result;
	}

	let currentAssistantId: string | null = null;
	const includesTail = !page || page.start + messages.length === page.total;
	const lastMessage = messages.at(-1);
	if (isStreaming && includesTail && lastMessage?.role === "assistant") {
		const index = turns.length - 1;
		const assistant = turns[index];
		if (assistant?.kind === "assistant") {
			currentAssistantId = assistant.id;
			turns[index] = { ...assistant, streaming: true };
		}
	}

	const outcome = includesTail && !isStreaming ? terminalOutcome(lastSettlement) : null;
	if (outcome) {
		const settled = settleUnfinishedTools(turns, toolResults);
		turns.splice(0, turns.length, ...settled.turns);
		Object.assign(toolResults, settled.toolResults);
		turns.push({
			kind: outcome.failed ? "error" : "system",
			id: page ? `settlement:${page.projectionId}:${page.total}` : randomId("settlement"),
			text: outcome.text,
		});
	}
	return {
		turns,
		toolResults,
		askAnswers: {},
		turnIdByMessageIndex,
		currentAssistantId,
		transcript: page ?? null,
		messageCount: messages.length,
	};
}

/** Prepend one contiguous page. A stale or out-of-order response is ignored. */
export function prependTranscriptPage(
	runtime: SessionRuntime,
	older: HydratedRuntime,
): SessionRuntime | null {
	const current = runtime.transcript;
	const page = older.transcript;
	if (
		!current ||
		!page ||
		page.projectionId !== current.projectionId ||
		page.start + older.messageCount !== current.start
	) {
		return null;
	}
	return {
		...runtime,
		turns: [...older.turns, ...runtime.turns],
		toolResults: Object.assign(
			Object.create(null) as Record<string, ToolResultState>,
			older.toolResults,
			runtime.toolResults,
		),
		turnIdByMessageIndex: Object.assign(
			Object.create(null) as Record<number, string | null>,
			older.turnIdByMessageIndex,
			runtime.turnIdByMessageIndex,
		),
		transcript: { ...current, start: page.start, total: Math.max(current.total, page.total) },
	};
}
