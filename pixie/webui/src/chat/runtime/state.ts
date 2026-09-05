import type {
	AgentEvent,
	AskUserQuestionResult,
	ImageContent,
	SessionGoal,
	SessionPlanState,
	SessionStats,
	SessionSummary,
	SlashCommandInfo,
	TextResourceAttachmentMarker,
	ThinkingLevel,
	WireModel,
} from "@pixie/contracts";
import type { ChatAttachment, ChatSubmission, ChatTurn } from "@/chat/runtime/types";
import { randomId } from "@/lib";
import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import {
	type HydratedRuntime,
	prependTranscriptPage as prependHydratedTranscriptPage,
} from "./hydrate";
import { clearTurnStreaming, reduceSessionEvent, type SessionRuntime } from "./session-runtime";

export interface ChatState {
	sessions: Record<string, SessionRuntime>;
	appendUserMessage: (sessionId: string, text: string, attachments?: ChatAttachment[]) => void;
	setSubmission: (sessionId: string, submission: ChatSubmission | null) => void;
	appendErrorTurn: (sessionId: string, text: string) => void;
	handleAgentEvent: (event: AgentEvent, sessionId: string) => void;
	setAskAnswer: (sessionId: string, toolCallId: string, result: AskUserQuestionResult) => void;
	setCurrentModel: (sessionId: string, model: WireModel, expectedRevision?: number) => void;
	setThinkingLevel: (sessionId: string, level: ThinkingLevel, expectedRevision?: number) => void;
	setStats: (sessionId: string, stats: SessionStats) => void;
	setCommands: (sessionId: string, commands: SlashCommandInfo[], expectedRevision?: number) => void;
	setChatDraft: (sessionId: string, text: string) => void;
	prependTranscriptPage: (sessionId: string, hydrated: HydratedRuntime) => boolean;
	replaceTranscriptSnapshot: (
		sessionId: string,
		summary: SessionSummary,
		hydrated: HydratedRuntime,
		planState: SessionPlanState | null,
	) => void;
	setSessionGoalLoading: (sessionId: string, projectAreaId: string) => void;
	setSessionGoalSaving: (sessionId: string, projectAreaId: string) => void;
	setSessionGoal: (sessionId: string, value: SessionGoal, expectedRevision?: number) => void;
	setSessionGoalError: (sessionId: string, projectAreaId: string, error: string) => void;
}

function withRuntime(
	s: AppState,
	sessionId: string,
	update: (rt: SessionRuntime) => SessionRuntime,
): Partial<AppState> {
	const rt = s.sessions[sessionId];
	if (!rt) return {};
	const next = update(rt);
	return next === rt ? {} : { sessions: { ...s.sessions, [sessionId]: next } };
}

function sameUserContent(a: ChatTurn, b: ChatTurn): boolean {
	if (a.kind !== "user" || b.kind !== "user") return false;
	const normalize = (content: typeof a.message.content) =>
		(typeof content === "string" ? [{ type: "text" as const, text: content }] : content).filter(
			(block) => block.type !== "text" || block.text.length > 0,
		);
	const left = normalize(a.message.content);
	const right = normalize(b.message.content);
	return (
		left.length === right.length &&
		left.every((block, index) => {
			const other = right[index];
			if (!other || block.type !== other.type) return false;
			return block.type === "text"
				? block.text === (other.type === "text" ? other.text : undefined)
				: block.type === "image"
					? block.data === (other.type === "image" ? other.data : undefined) &&
						block.mimeType === (other.type === "image" ? other.mimeType : undefined)
					: block.name === (other.type === "resource" ? other.name : undefined) &&
						block.mimeType === (other.type === "resource" ? other.mimeType : undefined);
		})
	);
}

function optimisticAttachmentBlocks(
	attachments: ChatAttachment[],
): (ImageContent | TextResourceAttachmentMarker)[] {
	return attachments.map((attachment) =>
		attachment.kind === "image"
			? attachment.content
			: {
					type: "resource" as const,
					name: attachment.content.name,
					mimeType: attachment.content.mimeType,
				},
	);
}

function unmatchedOptimisticTurns(
	runtime: SessionRuntime,
	hydrated: HydratedRuntime,
): Extract<ChatTurn, { kind: "user" }>[] {
	const pending = runtime.turns.filter(
		(turn): turn is Extract<ChatTurn, { kind: "user" }> =>
			turn.kind === "user" && turn.optimistic !== undefined,
	);
	if (pending.length === 0) return [];
	const messageIndexByTurnId = new Map<string, number>();
	for (const [index, turnId] of Object.entries(hydrated.turnIdByMessageIndex)) {
		if (turnId) messageIndexByTurnId.set(turnId, Number(index));
	}
	const available = hydrated.turns.filter(
		(turn): turn is Extract<ChatTurn, { kind: "user" }> => turn.kind === "user",
	);
	const consumed = new Set<string>();
	return pending.filter((optimistic) => {
		const baseline = optimistic.optimistic?.transcriptTotal ?? null;
		const match = available.find(
			(turn) =>
				!consumed.has(turn.id) &&
				(baseline === null || (messageIndexByTurnId.get(turn.id) ?? -1) >= baseline) &&
				sameUserContent(optimistic, turn),
		);
		if (!match) return true;
		consumed.add(match.id);
		return false;
	});
}

export const createChatState: StateCreator<AppState, [], [], ChatState> = (set, get) => ({
	sessions: {},
	appendUserMessage: (sessionId, text, attachments) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				turns: [
					...rt.turns,
					{
						kind: "user",
						id: randomId("turn"),
						message: {
							role: "user",
							content:
								attachments && attachments.length > 0
									? [
											...(text ? [{ type: "text" as const, text }] : []),
											...optimisticAttachmentBlocks(attachments),
										]
									: text,
							timestamp: Date.now(),
						},
						optimistic: { transcriptTotal: rt.transcript?.total ?? null },
						...(attachments && attachments.length > 0
							? {
									imageAttachmentNames: attachments
										.filter((attachment) => attachment.kind === "image")
										.map((attachment) => attachment.name),
								}
							: {}),
					},
				],
			})),
		),
	setSubmission: (sessionId, submission) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				submission,
				turns:
					submission?.error && submission.optimisticTurnId
						? rt.turns.filter(
								(turn) =>
									turn.id !== submission.optimisticTurnId ||
									turn.kind !== "user" ||
									!turn.optimistic,
							)
						: rt.turns,
			})),
		),

	appendErrorTurn: (sessionId, text) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				isStreaming: false,
				currentAssistantId: null,
				attemptAssistantId: null,
				turns: [...clearTurnStreaming(rt.turns), { kind: "error", id: randomId("turn"), text }],
			})),
		),
	handleAgentEvent: (event, sessionId) => {
		if (event.type === "session-info" && event.title) {
			const state = get();
			for (const projectId of new Set([
				...Object.keys(state.tabsByProjectArea),
				...Object.keys(state.closedChatsByProjectArea),
			])) {
				if (
					state.tabsByProjectArea[projectId]?.some(
						(tab) => tab.kind === "chat" && tab.sessionId === sessionId,
					) ||
					state.closedChatsByProjectArea[projectId]?.some((chat) => chat.sessionId === sessionId)
				) {
					state.applySessionLifecycle({
						projectId,
						sessionId,
						operation: "renamed",
						title: event.title,
					});
				}
			}
		}
		set((s) => withRuntime(s, sessionId, (rt) => reduceSessionEvent(rt, event)));
	},
	setCurrentModel: (sessionId, model, expectedRevision) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) =>
				expectedRevision !== undefined && rt.configRevision !== expectedRevision
					? rt
					: { ...rt, model, configRevision: rt.configRevision + 1 },
			),
		),
	setThinkingLevel: (sessionId, level, expectedRevision) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) =>
				expectedRevision !== undefined && rt.configRevision !== expectedRevision
					? rt
					: { ...rt, thinkingLevel: level, configRevision: rt.configRevision + 1 },
			),
		),
	setStats: (sessionId, stats) => set((s) => withRuntime(s, sessionId, (rt) => ({ ...rt, stats }))),
	setCommands: (sessionId, commands, expectedRevision) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) =>
				expectedRevision !== undefined && rt.commandRevision !== expectedRevision
					? rt
					: { ...rt, commands },
			),
		),
	setChatDraft: (sessionId, draft) =>
		set((s) => withRuntime(s, sessionId, (rt) => ({ ...rt, draft }))),
	prependTranscriptPage: (sessionId, hydrated) => {
		let applied = false;
		set((s) =>
			withRuntime(s, sessionId, (rt) => {
				const next = prependHydratedTranscriptPage(rt, hydrated);
				applied = next !== null;
				return next ?? rt;
			}),
		);
		return applied;
	},
	replaceTranscriptSnapshot: (sessionId, summary, hydrated, planState) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => {
				const optimisticTurns = unmatchedOptimisticTurns(rt, hydrated);
				const next: SessionRuntime = {
					...rt,
					turns: [...hydrated.turns, ...optimisticTurns],
					toolResults: hydrated.toolResults,
					turnIdByMessageIndex: hydrated.turnIdByMessageIndex,
					transcript: hydrated.transcript,
					currentAssistantId: hydrated.currentAssistantId,
					attemptAssistantId: null,
					isStreaming: summary.isStreaming,
					model: summary.model,
					thinkingLevel: summary.thinkingLevel,
					configOptions: summary.configOptions ?? [],
					capabilities: summary.capabilities ?? {},
					configRevision: rt.configRevision + 1,
					planState,
					...(summary.queue ? { queue: summary.queue } : {}),
				};
				if (summary.parentSessionId) next.parentSessionId = summary.parentSessionId;
				else delete next.parentSessionId;
				return next;
			}),
		),
	setSessionGoalLoading: (sessionId, projectAreaId) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				goal: { ...rt.goal, projectAreaId, status: "loading", error: null },
			})),
		),
	setSessionGoalSaving: (sessionId, projectAreaId) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				goal: { ...rt.goal, projectAreaId, status: "saving", error: null },
			})),
		),
	setSessionGoal: (sessionId, value, expectedRevision) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) =>
				expectedRevision !== undefined && rt.goalRevision !== expectedRevision
					? rt
					: {
							...rt,
							goal: {
								projectAreaId: value.projectId,
								status: "ready",
								goal: value.goal,
								tasks: value.tasks,
								updatedAt: value.updatedAt,
								error: null,
							},
							goalRevision: rt.goalRevision + 1,
						},
			),
		),
	setSessionGoalError: (sessionId, projectAreaId, error) =>
		set((s) =>
			withRuntime(s, sessionId, (rt) => ({
				...rt,
				goal: { ...rt.goal, projectAreaId, status: "error", error },
			})),
		),
	setAskAnswer: (sessionId, toolCallId, result) =>
		set((state) => {
			const runtime = state.sessions[sessionId];
			if (!runtime) return state;
			return {
				sessions: {
					...state.sessions,
					[sessionId]: {
						...runtime,
						askAnswers: { ...runtime.askAnswers, [toolCallId]: result },
					},
				},
			};
		}),
});
