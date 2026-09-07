import type {
	AgentEvent,
	AskUserQuestionResult,
	AssistantMessage,
	SessionConfigOption,
	SessionGoal,
	SessionModeState,
	SessionPlanState,
	SessionQueueState,
	SessionStats,
	SlashCommandInfo,
	ThinkingLevel,
	TranscriptPage,
	UserMessage,
	WireModel,
} from "@pixie/contracts";
import { matchesSkillInvocationCommand, parseSkillInvocation, randomId, userText } from "../../lib";
import { assistantFailureText, terminalOutcome } from "./assistant-failure";
import { createFoldState, type FoldState } from "./fold-state";
import type { ChatSubmission, ChatTurn, CompactionState, ToolResultState } from "./types";

export interface SessionRuntime {
	capabilities?: Record<string, number>;
	configOptions: readonly SessionConfigOption[];
	/** The immediate Pi session parent for a forked chat, when recorded. */
	parentSessionId?: string;
	turns: ChatTurn[];
	turnIdByMessageIndex: Record<number, string | null>;
	transcript: TranscriptPage | null;
	toolResults: Record<string, ToolResultState>;
	askAnswers: Record<string, AskUserQuestionResult>;
	currentAssistantId: string | null;
	attemptAssistantId: string | null;
	isStreaming: boolean;
	queue: SessionQueueState;
	model: WireModel | null;
	thinkingLevel: ThinkingLevel;
	modes: SessionModeState | null;
	planState: SessionPlanState | null;
	stats: SessionStats | null;
	commands: SlashCommandInfo[];
	commandRevision: number;
	configRevision: number;
	draft: string;
	disclosures: FoldState;
	submission: ChatSubmission | null;
	activity: string | null;
	goal: SessionGoalRuntime;
	goalRevision: number;
}

export interface SessionGoalRuntime {
	projectAreaId: string | null;
	status: "idle" | "loading" | "saving" | "ready" | "error";
	goal: string | null;
	tasks: SessionGoal["tasks"];
	updatedAt: number | null;
	error: string | null;
}

const EMPTY_QUEUE: SessionQueueState = { steering: [], followUp: [] };

export function createSessionRuntime(
	model: WireModel | null,
	thinkingLevel: ThinkingLevel,
	modes: SessionModeState | null = null,
): SessionRuntime {
	return {
		configOptions: [],
		turns: [],
		turnIdByMessageIndex: {},
		transcript: null,
		toolResults: {},
		askAnswers: {},
		currentAssistantId: null,
		attemptAssistantId: null,
		isStreaming: false,
		queue: EMPTY_QUEUE,
		model,
		thinkingLevel,
		modes,
		planState: null,
		stats: null,
		commands: [],
		commandRevision: 0,
		configRevision: 0,
		draft: "",
		disclosures: createFoldState(),
		submission: null,
		activity: null,
		goal: {
			projectAreaId: null,
			status: "idle",
			goal: null,
			tasks: [],
			updatedAt: null,
			error: null,
		},
		goalRevision: 0,
	};
}

export const EMPTY_RUNTIME: SessionRuntime = createSessionRuntime(null, "medium");

export function clearTurnStreaming(turns: ChatTurn[]): ChatTurn[] {
	if (!turns.some((t) => t.kind === "assistant" && t.streaming)) return turns;
	return turns.map((t) => (t.kind === "assistant" && t.streaming ? { ...t, streaming: false } : t));
}

function removeSupersededAssistant(
	turns: ChatTurn[],
	attemptAssistantId: string | null,
): ChatTurn[] {
	if (!attemptAssistantId) return turns;
	const index = turns.findIndex(
		(turn) =>
			turn.id === attemptAssistantId &&
			turn.kind === "assistant" &&
			assistantFailureText(turn.message) !== null,
	);
	return index < 0 ? turns : [...turns.slice(0, index), ...turns.slice(index + 1)];
}

function compactionOutcome(
	event: Extract<AgentEvent, { type: "compaction_end" }>,
): CompactionState {
	if (event.aborted) return { status: "cancelled" };
	if (event.errorMessage) return { status: "failed", detail: event.errorMessage };
	const tokensBefore = event.result?.tokensBefore;
	const tokensAfter = event.result?.estimatedTokensAfter;
	return {
		status: "done",
		...(typeof tokensBefore === "number" ? { tokensBefore } : {}),
		...(typeof tokensAfter === "number" ? { tokensAfter } : {}),
		...(event.willRetry ? { resuming: true } : {}),
	};
}

function clearCompactionResuming(turns: ChatTurn[]): ChatTurn[] {
	if (!turns.some((t) => t.kind === "compaction" && t.resuming)) return turns;
	return turns.map((t) => {
		if (t.kind !== "compaction" || !t.resuming) return t;
		const { resuming, ...rest } = t;
		return rest;
	});
}

function settleCompactionTurn(
	turns: ChatTurn[],
	event: Extract<AgentEvent, { type: "compaction_end" }>,
): ChatTurn[] {
	const outcome = compactionOutcome(event);
	const index = turns.findLastIndex((t) => t.kind === "compaction" && t.status === "running");
	if (index < 0) return [...turns, { kind: "compaction", id: randomId("turn"), ...outcome }];
	return turns.map((t, i) => (i === index ? { kind: "compaction", id: t.id, ...outcome } : t));
}

type RetrySource = Extract<ChatTurn, { kind: "retry" }>["source"];

function appendRetryTurn(
	rt: SessionRuntime,
	source: RetrySource,
	event: { attempt: number; maxAttempts: number; delayMs: number },
): SessionRuntime {
	return {
		...rt,
		turns: [
			...rt.turns.filter((t) => !(t.kind === "retry" && t.source === source)),
			{
				kind: "retry",
				id: randomId("turn"),
				source,
				attempt: event.attempt,
				maxAttempts: event.maxAttempts,
				delayMs: event.delayMs,
			},
		],
	};
}

function clearRetryTurns(rt: SessionRuntime, source: RetrySource): SessionRuntime {
	return rt.turns.some((t) => t.kind === "retry" && t.source === source)
		? { ...rt, turns: rt.turns.filter((t) => !(t.kind === "retry" && t.source === source)) }
		: rt;
}

function appendAssistantText(
	content: AssistantMessage["content"],
	text: string,
): AssistantMessage["content"] {
	const last = content.at(-1);
	if (last?.type !== "text") return [...content, { type: "text", text }];
	return [...content.slice(0, -1), { ...last, text: `${last.text}${text}` }];
}

function updateStreamingAssistant(
	rt: SessionRuntime,
	updateContent: (content: AssistantMessage["content"]) => AssistantMessage["content"],
	messageId?: string | null,
): SessionRuntime {
	const current = rt.turns.find((turn) => turn.id === rt.currentAssistantId);
	const changed =
		typeof messageId === "string" &&
		messageId.length > 0 &&
		current?.kind === "assistant" &&
		current.message.messageId !== messageId;
	const id = changed ? randomId("turn") : (rt.currentAssistantId ?? randomId("turn"));
	const existing = rt.turns.find((turn) => turn.id === id && turn.kind === "assistant");
	const turn: ChatTurn = {
		kind: "assistant",
		id,
		message: {
			role: "assistant",
			...(messageId
				? { messageId }
				: existing?.kind === "assistant" && existing.message.messageId
					? { messageId: existing.message.messageId }
					: {}),
			content: updateContent(existing?.kind === "assistant" ? existing.message.content : []),
		},
		streaming: true,
		...(existing?.kind === "assistant" && existing.toolResultsByBlock
			? { toolResultsByBlock: existing.toolResultsByBlock }
			: {}),
	};
	return {
		...rt,
		isStreaming: true,
		currentAssistantId: id,
		turns: existing
			? rt.turns.map((item) => (item.id === id ? turn : item))
			: [...(changed ? clearTurnStreaming(rt.turns) : rt.turns), turn],
	};
}

function bindLatestToolResult(
	turns: ChatTurn[],
	toolCallId: string,
	result: ToolResultState,
): ChatTurn[] {
	for (let turnIndex = turns.length - 1; turnIndex >= 0; turnIndex--) {
		const turn = turns[turnIndex];
		if (turn?.kind !== "assistant") continue;
		for (let blockIndex = turn.message.content.length - 1; blockIndex >= 0; blockIndex--) {
			const block = turn.message.content[blockIndex];
			if (block?.type !== "toolCall" || block.id !== toolCallId) continue;
			const replacement: ChatTurn = {
				...turn,
				toolResultsByBlock: { ...turn.toolResultsByBlock, [blockIndex]: result },
			};
			return turns.map((candidate, index) => (index === turnIndex ? replacement : candidate));
		}
	}
	return turns;
}

export function reduceSessionEvent(rt: SessionRuntime, event: AgentEvent): SessionRuntime {
	switch (event.type) {
		case "activity":
			return { ...rt, activity: event.text || null };
		case "run-start":
			return { ...rt, isStreaming: true, attemptAssistantId: null };
		case "text":
			return updateStreamingAssistant(
				rt,
				(content) => appendAssistantText(content, event.text ?? ""),
				event.messageId,
			);
		case "image": {
			const image = event.image;
			return image
				? updateStreamingAssistant(rt, (content) => [...content, image], event.messageId)
				: rt;
		}
		case "thinking":
			return updateStreamingAssistant(
				rt,
				(content) => {
					const prior = content.at(-1);
					return prior?.type === "thinking"
						? [
								...content.slice(0, -1),
								{ ...prior, thinking: `${prior.thinking}${event.text ?? ""}` },
							]
						: [...content, { type: "thinking", thinking: event.text ?? "" }];
				},
				event.messageId,
			);
		case "tool-start": {
			const toolCallId = event.toolCallId;
			if (!toolCallId) return rt;
			const previous = rt.toolResults[toolCallId];
			const continuing = previous?.status === "running";
			const result: ToolResultState = {
				status: "running",
				raw: continuing ? previous.raw : undefined,
				...(continuing && previous.app ? { app: previous.app } : {}),
				...(continuing && previous.subagentActivity
					? { subagentActivity: previous.subagentActivity }
					: {}),
			};
			const updated = updateStreamingAssistant(rt, (content) =>
				continuing && content.some((part) => part.type === "toolCall" && part.id === toolCallId)
					? [...content]
					: [
							...content,
							{
								type: "toolCall",
								id: toolCallId,
								...(event.toolName ? { toolName: event.toolName } : {}),
								name: event.toolName ?? "tool",
								arguments: event.tool ?? {},
							},
						],
			);
			return {
				...updated,
				turns: bindLatestToolResult(updated.turns, toolCallId, result),
				toolResults: {
					...rt.toolResults,
					[toolCallId]: result,
				},
			};
		}
		case "tool-update":
		case "tool-end": {
			const toolCallId = event.toolCallId;
			if (!toolCallId) return rt;
			const app = event.app ?? rt.toolResults[toolCallId]?.app;
			const subagentActivity =
				event.subagentActivity ?? rt.toolResults[toolCallId]?.subagentActivity;
			const result: ToolResultState = {
				status:
					event.type === "tool-end" && /error|failed/i.test(event.status ?? "")
						? "error"
						: event.type === "tool-end"
							? "done"
							: "running",
				raw: event.tool ?? rt.toolResults[toolCallId]?.raw ?? event.status,
				...(app ? { app } : {}),
				...(subagentActivity ? { subagentActivity } : {}),
			};
			return {
				...rt,
				turns: bindLatestToolResult(
					event.toolCall ? patchToolCall(rt.turns, toolCallId, event.toolCall) : rt.turns,
					toolCallId,
					result,
				),
				toolResults: { ...rt.toolResults, [toolCallId]: result },
			};
		}
		case "usage":
			return event.usage
				? {
						...rt,
						stats: {
							sessionId: rt.stats?.sessionId ?? "",
							totalMessages: rt.stats?.totalMessages ?? 0,
							tokens: {
								input: event.usage.input ?? 0,
								output: event.usage.output ?? 0,
								cacheRead: event.usage.cacheRead ?? 0,
								cacheWrite: event.usage.cacheWrite ?? 0,
								total: event.usage.total ?? 0,
							},
							cost: event.usage.cost ?? 0,
							...(event.costCurrency
								? { costCurrency: event.costCurrency }
								: rt.stats?.costCurrency
									? { costCurrency: rt.stats.costCurrency }
									: {}),
							...(event.reported ? { reported: event.reported } : {}),
							...(rt.stats?.contextUsage ? { contextUsage: rt.stats.contextUsage } : {}),
						},
					}
				: rt;
		case "context":
			return event.contextUsage
				? {
						...rt,
						stats: rt.stats
							? { ...rt.stats, contextUsage: event.contextUsage }
							: {
									sessionId: "",
									totalMessages: 0,
									tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
									cost: 0,
									contextUsage: event.contextUsage,
								},
					}
				: rt;
		case "config": {
			const value = event.configOptions?.find(
				(option) => option.id === "thinking" || option.category === "thought_level",
			)?.currentValue;
			return event.configOptions !== undefined || event.model !== undefined
				? {
						...rt,
						configOptions: event.configOptions ?? rt.configOptions,
						model: event.model !== undefined ? event.model : rt.model,
						thinkingLevel:
							typeof value === "string"
								? value
								: event.configOptions !== undefined
									? "off"
									: rt.thinkingLevel,
						configRevision: rt.configRevision + 1,
					}
				: rt;
		}
		case "complete": {
			const outcome = terminalOutcome({ stopReason: event.status ?? "unknown" });
			const settled = settleUnfinishedTools(rt.turns, rt.toolResults);
			return {
				...rt,
				toolResults: settled.toolResults,
				isStreaming: false,
				activity: null,
				currentAssistantId: null,
				turns: [
					...clearTurnStreaming(settled.turns),
					{
						kind: outcome?.failed ? "error" : "system",
						id: randomId("turn"),
						text: outcome?.text ?? "Run ended.",
						endedAt: Date.now(),
					},
				],
			};
		}
		case "error": {
			const settled = settleUnfinishedTools(rt.turns, rt.toolResults);
			return {
				...rt,
				toolResults: settled.toolResults,
				isStreaming: false,
				activity: null,
				currentAssistantId: null,
				turns: [
					...clearTurnStreaming(settled.turns),
					{ kind: "error", id: randomId("turn"), text: event.error ?? "Agent request failed." },
				],
			};
		}
		case "session-info":
			return rt;
		case "agent_start":
			return { ...rt, isStreaming: true, attemptAssistantId: null };
		case "commands":
			return { ...rt, commands: event.commands, commandRevision: rt.commandRevision + 1 };
		case "current-mode":
			return rt.modes ? { ...rt, modes: { ...rt.modes, currentModeId: event.currentModeId } } : rt;
		case "plan":
			return { ...rt, planState: event.planState };
		case "queue_update":
			return {
				...rt,
				queue: {
					steering: event.steering,
					followUp: event.followUp,
					...(event.revision ? { revision: event.revision } : {}),
					...(event.blocked ? { blocked: event.blocked } : {}),
				},
			};
		case "message_start": {
			if (event.message.role === "assistant")
				return {
					...rt,
					currentAssistantId: randomId("turn"),
					attemptAssistantId: null,
					turns: clearTurnStreaming(rt.turns),
				};
			if (event.message.role === "user") {
				const message = event.message as UserMessage;
				const text = userText(message.content);
				const last = rt.turns[rt.turns.length - 1];
				if (last?.kind === "user") {
					const optimisticText = userText(last.message.content);
					if (
						optimisticText === text ||
						(optimisticText === "" && text !== "" && userResourceMarkerCount(last.message) > 0)
					) {
						return last.optimistic || !sameUserResourceMarkers(last.message, message)
							? {
									...rt,
									turns: [...rt.turns.slice(0, -1), { kind: "user", id: last.id, message }],
								}
							: rt;
					}
					const invocation = parseSkillInvocation(text);
					if (invocation && matchesSkillInvocationCommand(optimisticText, invocation)) {
						return {
							...rt,
							turns: [...rt.turns.slice(0, -1), { kind: "user", id: last.id, message }],
						};
					}
				}
				return {
					...rt,
					turns: [...rt.turns, { kind: "user", id: randomId("turn"), message }],
				};
			}
			return rt;
		}
		case "message_update": {
			const ame = event.assistantMessageEvent;
			const snapshot =
				"partial" in ame
					? ame.partial
					: ame.type === "done"
						? ame.message
						: ame.type === "error"
							? ame.error
							: null;
			if (!snapshot) return rt;
			const id = rt.currentAssistantId ?? randomId("turn");
			const streaming = !(ame.type === "done" || ame.type === "error");
			const existing = rt.turns.find((turn) => turn.id === id && turn.kind === "assistant");
			const turn: ChatTurn = {
				kind: "assistant",
				id,
				message: snapshot,
				streaming,
				...(existing?.kind === "assistant" && existing.toolResultsByBlock
					? { toolResultsByBlock: existing.toolResultsByBlock }
					: {}),
			};
			return {
				...rt,
				currentAssistantId: streaming ? id : null,
				attemptAssistantId: streaming ? rt.attemptAssistantId : id,
				turns: rt.turns.some((t) => t.id === id)
					? rt.turns.map((t) => (t.id === id ? turn : t))
					: [...rt.turns, turn],
			};
		}
		case "message_end": {
			if (event.message.role !== "assistant" || !rt.currentAssistantId) return rt;
			const id = rt.currentAssistantId;
			const existing = rt.turns.find((turn) => turn.id === id && turn.kind === "assistant");
			const turn: ChatTurn = {
				kind: "assistant",
				id,
				message: event.message,
				streaming: false,
				...(existing?.kind === "assistant" && existing.toolResultsByBlock
					? { toolResultsByBlock: existing.toolResultsByBlock }
					: {}),
			};
			return {
				...rt,
				currentAssistantId: null,
				attemptAssistantId: id,
				turns: rt.turns.some((t) => t.id === id)
					? rt.turns.map((t) => (t.id === id ? turn : t))
					: [...rt.turns, turn],
			};
		}
		case "tool_execution_start": {
			const result: ToolResultState = { status: "running", raw: undefined };
			return {
				...rt,
				turns: bindLatestToolResult(rt.turns, event.toolCallId, result),
				toolResults: {
					...rt.toolResults,
					[event.toolCallId]: result,
				},
			};
		}
		case "tool_execution_update": {
			const result: ToolResultState = { status: "running", raw: event.partialResult };
			return {
				...rt,
				turns: bindLatestToolResult(rt.turns, event.toolCallId, result),
				toolResults: {
					...rt.toolResults,
					[event.toolCallId]: result,
				},
			};
		}
		case "tool_execution_end": {
			const result: ToolResultState = {
				status: event.isError ? "error" : "done",
				raw: event.result,
			};
			return {
				...rt,
				turns: bindLatestToolResult(rt.turns, event.toolCallId, result),
				toolResults: {
					...rt.toolResults,
					[event.toolCallId]: result,
				},
			};
		}
		case "agent_end":
			return rt;
		case "agent_settled": {
			const failure = assistantFailureText(event.terminal);
			const closer: ChatTurn = failure
				? { kind: "error", id: randomId("turn"), text: failure }
				: { kind: "system", id: randomId("turn"), text: "✓ Done", endedAt: Date.now() };
			return {
				...rt,
				turns: [
					...clearCompactionResuming(clearTurnStreaming(rt.turns)).filter(
						(turn) => turn.kind !== "retry",
					),
					closer,
				],
				isStreaming: false,
				activity: null,
				currentAssistantId: null,
				attemptAssistantId: null,
			};
		}
		case "compaction_start":
			return {
				...rt,
				turns: [...rt.turns, { kind: "compaction", id: randomId("turn"), status: "running" }],
			};
		case "compaction_end": {
			const settled = settleCompactionTurn(rt.turns, event);
			return event.reason === "overflow" && event.willRetry
				? {
						...rt,
						turns: removeSupersededAssistant(settled, rt.attemptAssistantId),
						attemptAssistantId: null,
					}
				: { ...rt, turns: settled };
		}
		case "auto_retry_start":
			return appendRetryTurn(
				{
					...rt,
					turns: removeSupersededAssistant(rt.turns, rt.attemptAssistantId),
					attemptAssistantId: null,
				},
				"turn",
				event,
			);
		case "auto_retry_end":
			return clearRetryTurns(rt, "turn");
		case "summarization_retry_scheduled":
			return appendRetryTurn(rt, "summarization", event);
		case "summarization_retry_finished":
			return clearRetryTurns(rt, "summarization");
		case "thinking_level_changed":
			return { ...rt, thinkingLevel: event.level };
		default:
			return rt;
	}
}

export function settleUnfinishedTools(turns: ChatTurn[], results: Record<string, ToolResultState>) {
	const toolResults = { ...results };
	const settledTurns = turns.map((turn): ChatTurn => {
		if (turn.kind !== "assistant") return turn;
		const blocks = { ...turn.toolResultsByBlock };
		for (let index = 0; index < turn.message.content.length; index++) {
			const block = turn.message.content[index];
			if (block?.type !== "toolCall") continue;
			const result = Object.hasOwn(blocks, index) ? blocks[index] : results[block.id];
			if (!result || result.status === "running") {
				const interrupted: ToolResultState = { ...result, status: "interrupted", raw: result?.raw };
				blocks[index] = interrupted;
				toolResults[block.id] = interrupted;
			}
		}
		return { ...turn, toolResultsByBlock: blocks };
	});
	return { turns: settledTurns, toolResults };
}

function userResourceMarkerCount(message: UserMessage): number {
	return typeof message.content === "string"
		? 0
		: message.content.filter((block) => block.type === "resource").length;
}

function sameUserResourceMarkers(left: UserMessage, right: UserMessage): boolean {
	const markers = (message: UserMessage) =>
		typeof message.content === "string"
			? []
			: message.content
					.filter((block) => block.type === "resource")
					.map((block) => `${block.name}\0${block.mimeType}`);
	const leftMarkers = markers(left);
	const rightMarkers = markers(right);
	return (
		leftMarkers.length === rightMarkers.length &&
		leftMarkers.every((marker, index) => marker === rightMarkers[index])
	);
}

function patchToolCall(
	turns: ChatTurn[],
	id: string,
	call: import("@pixie/contracts").ToolCall,
): ChatTurn[] {
	for (let index = turns.length - 1; index >= 0; index--) {
		const turn = turns[index];
		if (
			turn?.kind !== "assistant" ||
			!turn.message.content.some((block) => block.type === "toolCall" && block.id === id)
		)
			continue;
		return turns.map((candidate, i) =>
			i === index
				? {
						...turn,
						message: {
							...turn.message,
							content: turn.message.content.map((block) =>
								block.type === "toolCall" && block.id === id ? { ...block, ...call } : block,
							),
						},
					}
				: candidate,
		);
	}
	return turns;
}
