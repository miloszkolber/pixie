import type {
	SessionLifecycleChangedPayload,
	SessionPlanState,
	SessionQueueState,
	SessionSummary,
	ThinkingLevel,
	WireModel,
} from "@pixie/contracts";
import type { HydratedRuntime } from "@/chat/runtime/hydrate";
import { createSessionRuntime, type SessionRuntime } from "@/chat/runtime/session-runtime";
import { randomId } from "@/lib";
import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { omitKey } from "@/store/record";
import { bumpProjectAreaNavigation } from "./content-state";
import {
	availableContentTabId,
	type ChatLocationRequest,
	type ChatTab,
	type ClosedChat,
	type ContentOpenOptions,
	chatTabId,
	contentSessionId,
	type RouteChatTarget,
} from "./model";
import { type HistoryTarget, selectProjectAreaTick } from "./selectors";

export interface SessionWorkspaceState {
	routeChatTarget: RouteChatTarget | null;
	routeChatTargetGeneration: number;
	closedChatsByProjectArea: Record<string, ClosedChat[]>;
	sessionCatalogVersionByProjectArea: Record<string, number>;
	deletedSessionsByProjectArea: Record<string, Record<string, true>>;
	chatLocationRequest: ChatLocationRequest | null;
	historyOpenRequest: { id: string; sessionId: string } | null;
	skillsSyncedTickBySession: Record<string, number>;
	commandCatalogGeneration: number;
	validateRouteChatTarget: (sessionId: string) => void;
	clearRouteChatTarget: () => void;
	markSkillsSynced: (sessionId: string, syncedTick: number) => void;
	noteCommandCatalogChanged: () => void;
	openChatSession: (
		projectAreaId: string,
		sessionId: string,
		model: WireModel | null,
		thinkingLevel: ThinkingLevel,
		syncedTick?: number,
		options?: ContentOpenOptions,
	) => void;
	closeChatRuntime: (sessionId: string) => void;
	closeChatToHistory: (
		sessionId: string,
		projectAreaId?: string,
		countNavigation?: boolean,
	) => void;
	deleteChat: (projectAreaId: string, sessionId: string, countNavigation?: boolean) => void;
	applySessionLifecycle: (payload: SessionLifecycleChangedPayload) => void;
	reconcileProjectAreaSessions: (
		projectAreaId: string,
		baselineSessionIds: readonly string[],
		authoritativeSessions: readonly Pick<
			SessionSummary,
			"sessionId" | "title" | "archived" | "queue"
		>[],
	) => void;
	reopenChat: (projectAreaId: string, sessionId: string, options?: ContentOpenOptions) => void;
	noteClosedChats: (projectAreaId: string, entries: ClosedChat[]) => void;
	hydrateSession: (
		summary: SessionSummary,
		hydrated: HydratedRuntime,
		planState: SessionPlanState | null,
		activate?: boolean,
		syncedTick?: number,
		options?: ContentOpenOptions,
	) => void;
	requestChatLocation: (request: ChatLocationRequest) => void;
	clearChatLocation: () => void;
	requestHistoryOpen: (target: HistoryTarget) => void;
	clearHistoryOpen: () => void;
}

function isSessionDeleted(
	state: Pick<AppState, "deletedSessionsByProjectArea">,
	projectAreaId: string,
	sessionId: string,
): boolean {
	return state.deletedSessionsByProjectArea[projectAreaId]?.[sessionId] === true;
}

function sameStrings(left: readonly string[], right: readonly string[]): boolean {
	return left.length === right.length && left.every((value, index) => value === right[index]);
}

function sameQueue(left: SessionQueueState, right: SessionQueueState): boolean {
	return (
		left.revision === right.revision &&
		sameStrings(left.steering, right.steering) &&
		sameStrings(left.followUp, right.followUp) &&
		left.blocked?.lane === right.blocked?.lane &&
		left.blocked?.index === right.blocked?.index &&
		left.blocked?.reason === right.blocked?.reason
	);
}

function withoutChat(
	state: AppState,
	projectAreaId: string,
	sessionId: string,
	countNavigation: boolean,
	markDeleted = true,
): AppState {
	if (state.removedProjectAreaIds[projectAreaId]) return state;
	const alreadyDeleted = isSessionDeleted(state, projectAreaId, sessionId);
	const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
	const sessionTabs = tabs.filter((candidate) => contentSessionId(candidate) === sessionId);
	const closed = state.closedChatsByProjectArea[projectAreaId] ?? [];
	const inHistory = closed.some((chat) => chat.sessionId === sessionId);
	const hasRuntime = state.sessions[sessionId] !== undefined;
	const hasSkillBaseline = Object.hasOwn(state.skillsSyncedTickBySession, sessionId);
	const targetsLocation =
		state.chatLocationRequest?.projectAreaId === projectAreaId &&
		state.chatLocationRequest.sessionId === sessionId;
	const targetsRoute =
		state.routeChatTarget?.projectAreaId === projectAreaId &&
		state.routeChatTarget.sessionId === sessionId;
	const targetsHistory = state.historyOpenRequest?.sessionId === sessionId;
	if (
		alreadyDeleted &&
		sessionTabs.length === 0 &&
		!inHistory &&
		!hasRuntime &&
		!hasSkillBaseline &&
		!targetsLocation &&
		!targetsRoute &&
		!targetsHistory
	) {
		return state;
	}

	const removedTabIds = new Set(sessionTabs.map((candidate) => candidate.id));
	const remaining =
		sessionTabs.length > 0 ? tabs.filter((candidate) => !removedTabIds.has(candidate.id)) : tabs;
	const wasActive =
		state.activeTabByProjectArea[projectAreaId] !== null &&
		removedTabIds.has(state.activeTabByProjectArea[projectAreaId] ?? "");
	return {
		...state,
		...(markDeleted && !alreadyDeleted
			? {
					deletedSessionsByProjectArea: Object.assign(
						Object.create(null),
						state.deletedSessionsByProjectArea,
						{
							[projectAreaId]: Object.assign(
								Object.create(null),
								state.deletedSessionsByProjectArea[projectAreaId],
								{ [sessionId]: true as const },
							) as Record<string, true>,
						},
					) as Record<string, Record<string, true>>,
				}
			: {}),
		...(sessionTabs.length > 0
			? {
					tabsByProjectArea: { ...state.tabsByProjectArea, [projectAreaId]: remaining },
				}
			: {}),
		...(wasActive
			? {
					activeTabByProjectArea: {
						...state.activeTabByProjectArea,
						[projectAreaId]: remaining.at(-1)?.id ?? null,
					},
					navTickByProjectArea: countNavigation
						? bumpProjectAreaNavigation(state, projectAreaId)
						: state.navTickByProjectArea,
				}
			: {}),
		...(inHistory
			? {
					closedChatsByProjectArea: {
						...state.closedChatsByProjectArea,
						[projectAreaId]: closed.filter((chat) => chat.sessionId !== sessionId),
					},
				}
			: {}),
		...(hasRuntime ? { sessions: omitKey(state.sessions, sessionId) } : {}),
		...(hasSkillBaseline
			? {
					skillsSyncedTickBySession: omitKey(state.skillsSyncedTickBySession, sessionId),
				}
			: {}),
		...(targetsLocation ? { chatLocationRequest: null } : {}),
		...(targetsRoute ? { routeChatTarget: null } : {}),
		...(targetsHistory ? { historyOpenRequest: null } : {}),
	};
}

export const createSessionWorkspaceState: StateCreator<AppState, [], [], SessionWorkspaceState> = (
	set,
) => ({
	routeChatTarget: null,
	routeChatTargetGeneration: 0,
	closedChatsByProjectArea: {},
	sessionCatalogVersionByProjectArea: {},
	deletedSessionsByProjectArea: Object.create(null) as Record<string, Record<string, true>>,
	chatLocationRequest: null,
	historyOpenRequest: null,
	skillsSyncedTickBySession: {},
	commandCatalogGeneration: 0,
	validateRouteChatTarget: (sessionId) =>
		set((state) => {
			const target = state.routeChatTarget;
			if (!target || target.sessionId !== sessionId || target.validated) return state;
			return { routeChatTarget: { ...target, validated: true } };
		}),
	clearRouteChatTarget: () =>
		set((state) => (state.routeChatTarget ? { routeChatTarget: null } : state)),
	markSkillsSynced: (sessionId, syncedTick) =>
		set((state) => {
			if (!state.sessions[sessionId]) return {};
			const synced = Math.max(state.skillsSyncedTickBySession[sessionId] ?? 0, syncedTick);
			return {
				skillsSyncedTickBySession: {
					...state.skillsSyncedTickBySession,
					[sessionId]: synced,
				},
			};
		}),
	noteCommandCatalogChanged: () =>
		set((state) => ({ commandCatalogGeneration: state.commandCatalogGeneration + 1 })),
	openChatSession: (projectAreaId, sessionId, model, thinkingLevel, syncedTick, options = {}) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[projectAreaId] ||
				isSessionDeleted(state, projectAreaId, sessionId)
			) {
				return {};
			}
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const existing = tabs.find(
				(candidate): candidate is ChatTab =>
					candidate.kind === "chat" && candidate.sessionId === sessionId,
			);
			const preferred: ChatTab = existing ?? {
				kind: "chat",
				id: chatTabId(projectAreaId, sessionId),
				projectAreaId,
				name: "Chat",
				sessionId,
			};
			const id = existing?.id ?? availableContentTabId(tabs, preferred);
			const tab: ChatTab = id === preferred.id ? preferred : { ...preferred, id };
			const fresh = !state.sessions[sessionId];
			return {
				tabsByProjectArea: existing
					? state.tabsByProjectArea
					: { ...state.tabsByProjectArea, [projectAreaId]: [...tabs, tab] },
				activeTabByProjectArea:
					options.activate === false
						? state.activeTabByProjectArea
						: { ...state.activeTabByProjectArea, [projectAreaId]: id },
				navTickByProjectArea:
					options.activate === false
						? state.navTickByProjectArea
						: bumpProjectAreaNavigation(state, projectAreaId),
				sessions: fresh
					? {
							...state.sessions,
							[sessionId]: createSessionRuntime(model, thinkingLevel),
						}
					: state.sessions,
				...(fresh
					? {
							skillsSyncedTickBySession: {
								...state.skillsSyncedTickBySession,
								[sessionId]: syncedTick ?? selectProjectAreaTick(state, projectAreaId),
							},
						}
					: {}),
			};
		}),
	closeChatRuntime: (sessionId) =>
		set((state) => {
			if (!state.sessions[sessionId]) return {};
			return {
				sessions: omitKey(state.sessions, sessionId),
				skillsSyncedTickBySession: omitKey(state.skillsSyncedTickBySession, sessionId),
			};
		}),
	closeChatToHistory: (sessionId, projectAreaId, countNavigation = true) =>
		set((state) => {
			const currentProjectAreaId = projectAreaId ?? state.activeProjectAreaId;
			if (!currentProjectAreaId || state.removedProjectAreaIds[currentProjectAreaId]) return {};
			const tabs = state.tabsByProjectArea[currentProjectAreaId] ?? [];
			const tab = tabs.find(
				(candidate) => candidate.kind === "chat" && candidate.sessionId === sessionId,
			);
			if (!tab) return {};
			const remaining = tabs.filter((candidate) => candidate.id !== tab.id);
			const wasActive = state.activeTabByProjectArea[currentProjectAreaId] === tab.id;
			const entry: ClosedChat = { sessionId, title: tab.name, closedAt: Date.now() };
			const targetsLocation =
				state.chatLocationRequest?.projectAreaId === currentProjectAreaId &&
				state.chatLocationRequest.sessionId === sessionId;
			const targetsHistory = state.historyOpenRequest?.sessionId === sessionId;
			const runtime = state.sessions[sessionId];
			const hasAnotherTab = Object.entries(state.tabsByProjectArea).some(([areaId, areaTabs]) =>
				areaTabs.some(
					(candidate) =>
						candidate.kind === "chat" &&
						candidate.sessionId === sessionId &&
						(areaId !== currentProjectAreaId || candidate.id !== tab.id),
				),
			);
			const canDropRuntime =
				runtime !== undefined &&
				!hasAnotherTab &&
				!runtime.isStreaming &&
				runtime.submission == null &&
				!runtime.draft.trim() &&
				runtime.queue.steering.length === 0 &&
				runtime.queue.followUp.length === 0 &&
				runtime.goal.status !== "loading" &&
				runtime.goal.status !== "saving";
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[currentProjectAreaId]: remaining,
				},
				navTickByProjectArea:
					wasActive && countNavigation
						? bumpProjectAreaNavigation(state, currentProjectAreaId)
						: state.navTickByProjectArea,
				activeTabByProjectArea: {
					...state.activeTabByProjectArea,
					[currentProjectAreaId]: wasActive
						? (remaining.at(-1)?.id ?? null)
						: (state.activeTabByProjectArea[currentProjectAreaId] ?? null),
				},
				closedChatsByProjectArea: {
					...state.closedChatsByProjectArea,
					[currentProjectAreaId]: [
						entry,
						...(state.closedChatsByProjectArea[currentProjectAreaId] ?? []),
					],
				},
				...(canDropRuntime
					? {
							sessions: omitKey(state.sessions, sessionId),
							skillsSyncedTickBySession: omitKey(state.skillsSyncedTickBySession, sessionId),
						}
					: {}),
				...(targetsLocation ? { chatLocationRequest: null } : {}),
				...(targetsHistory ? { historyOpenRequest: null } : {}),
			};
		}),
	deleteChat: (projectAreaId, sessionId, countNavigation = true) =>
		set((state) => withoutChat(state, projectAreaId, sessionId, countNavigation)),
	applySessionLifecycle: (payload) =>
		set((state) => {
			if (state.removedProjectAreaIds[payload.projectId]) return {};
			const version = (state.sessionCatalogVersionByProjectArea[payload.projectId] ?? 0) + 1;
			const versionPatch = {
				sessionCatalogVersionByProjectArea: {
					...state.sessionCatalogVersionByProjectArea,
					[payload.projectId]: version,
				},
			};
			if (payload.operation === "renamed" && payload.title) {
				const tabs = state.tabsByProjectArea[payload.projectId] ?? [];
				const closed = state.closedChatsByProjectArea[payload.projectId] ?? [];
				return {
					...versionPatch,
					tabsByProjectArea: {
						...state.tabsByProjectArea,
						[payload.projectId]: tabs.map((tab) =>
							tab.kind === "chat" && tab.sessionId === payload.sessionId
								? { ...tab, name: payload.title as string }
								: tab,
						),
					},
					closedChatsByProjectArea: {
						...state.closedChatsByProjectArea,
						[payload.projectId]: closed.map((chat) =>
							chat.sessionId === payload.sessionId
								? { ...chat, title: payload.title as string }
								: chat,
						),
					},
				};
			}
			if (payload.operation === "archived") {
				const next = withoutChat(state, payload.projectId, payload.sessionId, false, false);
				return {
					...next,
					...versionPatch,
				};
			}
			return versionPatch;
		}),
	reconcileProjectAreaSessions: (projectAreaId, baselineSessionIds, authoritativeSessions) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const active = new Map(
				authoritativeSessions
					.filter((session) => !session.archived)
					.map((session) => [session.sessionId, session.title]),
			);
			const archived = new Set(
				authoritativeSessions
					.filter((session) => session.archived)
					.map((session) => session.sessionId),
			);
			const queues = new Map(
				authoritativeSessions
					.filter((session) => session.queue !== undefined)
					.map((session) => [session.sessionId, session.queue as SessionQueueState]),
			);
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const nextTabs = tabs.map((tab) => {
				if (tab.kind !== "chat") return tab;
				const title = active.get(tab.sessionId);
				return title !== undefined && title !== tab.name ? { ...tab, name: title } : tab;
			});
			const closed = state.closedChatsByProjectArea[projectAreaId] ?? [];
			const nextClosed = closed.map((chat) => {
				const title = active.get(chat.sessionId);
				return title !== undefined && title !== chat.title ? { ...chat, title } : chat;
			});
			let sessions = state.sessions;
			for (const [sessionId, runtime] of Object.entries(state.sessions)) {
				const queue = queues.get(sessionId);
				if (!queue || sameQueue(runtime.queue, queue)) continue;
				if (sessions === state.sessions) sessions = { ...state.sessions };
				sessions[sessionId] = { ...runtime, queue };
			}
			let next: AppState = state;
			if (nextTabs.some((tab, index) => tab !== tabs[index])) {
				next = {
					...next,
					tabsByProjectArea: { ...next.tabsByProjectArea, [projectAreaId]: nextTabs },
				};
			}
			if (nextClosed.some((chat, index) => chat !== closed[index])) {
				next = {
					...next,
					closedChatsByProjectArea: {
						...next.closedChatsByProjectArea,
						[projectAreaId]: nextClosed,
					},
				};
			}
			if (sessions !== state.sessions) next = { ...next, sessions };
			for (const sessionId of baselineSessionIds) {
				if (!active.has(sessionId)) {
					next = withoutChat(next, projectAreaId, sessionId, false, !archived.has(sessionId));
				}
			}
			return next === state ? {} : next;
		}),
	reopenChat: (projectAreaId, sessionId, options = {}) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[projectAreaId] ||
				isSessionDeleted(state, projectAreaId, sessionId)
			) {
				return {};
			}
			const closed = state.closedChatsByProjectArea[projectAreaId] ?? [];
			const entry = closed.find((chat) => chat.sessionId === sessionId);
			if (!entry) return {};
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const existing = tabs.find(
				(candidate): candidate is ChatTab =>
					candidate.kind === "chat" && candidate.sessionId === sessionId,
			);
			const preferred: ChatTab = {
				kind: "chat",
				id: existing?.id ?? chatTabId(projectAreaId, sessionId),
				projectAreaId,
				name: entry.title,
				sessionId,
			};
			const id = existing?.id ?? availableContentTabId(tabs, preferred);
			const tab: ChatTab = id === preferred.id ? preferred : { ...preferred, id };
			return {
				tabsByProjectArea: existing
					? existing.name === tab.name
						? state.tabsByProjectArea
						: {
								...state.tabsByProjectArea,
								[projectAreaId]: tabs.map((candidate) =>
									candidate === existing ? tab : candidate,
								),
							}
					: { ...state.tabsByProjectArea, [projectAreaId]: [...tabs, tab] },
				activeTabByProjectArea:
					options.activate === false
						? state.activeTabByProjectArea
						: { ...state.activeTabByProjectArea, [projectAreaId]: id },
				navTickByProjectArea:
					options.activate === false
						? state.navTickByProjectArea
						: bumpProjectAreaNavigation(state, projectAreaId),
				closedChatsByProjectArea: {
					...state.closedChatsByProjectArea,
					[projectAreaId]: closed.filter((chat) => chat.sessionId !== sessionId),
				},
			};
		}),
	noteClosedChats: (projectAreaId, entries) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const existing = state.closedChatsByProjectArea[projectAreaId] ?? [];
			const known = new Set([
				...existing.map((chat) => chat.sessionId),
				...(state.tabsByProjectArea[projectAreaId] ?? [])
					.filter((tab): tab is ChatTab => tab.kind === "chat")
					.map((tab) => tab.sessionId),
			]);
			const fresh = entries.filter(
				(entry) =>
					!isSessionDeleted(state, projectAreaId, entry.sessionId) &&
					!known.has(entry.sessionId) &&
					!state.sessions[entry.sessionId],
			);
			if (fresh.length === 0) return {};
			return {
				closedChatsByProjectArea: {
					...state.closedChatsByProjectArea,
					[projectAreaId]: [...existing, ...fresh].sort((a, b) => b.closedAt - a.closedAt),
				},
			};
		}),
	hydrateSession: (summary, hydrated, planState, activate = false, syncedTick, options = {}) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[summary.projectId] ||
				isSessionDeleted(state, summary.projectId, summary.sessionId)
			) {
				return {};
			}
			if (state.sessions[summary.sessionId]) return {};
			const projectAreaId = summary.projectId;
			const runtime: SessionRuntime = {
				...createSessionRuntime(summary.model, summary.thinkingLevel),
				planState,
				...(summary.parentSessionId ? { parentSessionId: summary.parentSessionId } : {}),
				configOptions: summary.configOptions ?? [],
				turns: hydrated.turns,
				toolResults: hydrated.toolResults,
				askAnswers: hydrated.askAnswers,
				turnIdByMessageIndex: hydrated.turnIdByMessageIndex,
				transcript: hydrated.transcript,
				currentAssistantId: hydrated.currentAssistantId,
				isStreaming: summary.isStreaming,
				...(summary.queue ? { queue: summary.queue } : {}),
			};
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const existing = tabs.find(
				(candidate): candidate is ChatTab =>
					candidate.kind === "chat" && candidate.sessionId === summary.sessionId,
			);
			const preferred: ChatTab = {
				kind: "chat",
				id: existing?.id ?? chatTabId(projectAreaId, summary.sessionId),
				projectAreaId,
				name: summary.title,
				sessionId: summary.sessionId,
			};
			const id = existing?.id ?? availableContentTabId(tabs, preferred);
			const tab: ChatTab = id === preferred.id ? preferred : { ...preferred, id };
			const hasActive = state.activeTabByProjectArea[projectAreaId] != null;
			const takesFocus = options.activate !== false && (activate || !hasActive);
			const closed = state.closedChatsByProjectArea[projectAreaId] ?? [];
			return {
				sessions: { ...state.sessions, [summary.sessionId]: runtime },
				...(syncedTick !== undefined
					? {
							skillsSyncedTickBySession: {
								...state.skillsSyncedTickBySession,
								[summary.sessionId]: syncedTick,
							},
						}
					: {}),
				tabsByProjectArea: existing
					? existing.name === tab.name
						? state.tabsByProjectArea
						: {
								...state.tabsByProjectArea,
								[projectAreaId]: tabs.map((candidate) =>
									candidate === existing ? tab : candidate,
								),
							}
					: { ...state.tabsByProjectArea, [projectAreaId]: [...tabs, tab] },
				activeTabByProjectArea: takesFocus
					? { ...state.activeTabByProjectArea, [projectAreaId]: id }
					: state.activeTabByProjectArea,
				navTickByProjectArea: takesFocus
					? bumpProjectAreaNavigation(state, projectAreaId)
					: state.navTickByProjectArea,
				closedChatsByProjectArea: closed.some((chat) => chat.sessionId === summary.sessionId)
					? {
							...state.closedChatsByProjectArea,
							[projectAreaId]: closed.filter((chat) => chat.sessionId !== summary.sessionId),
						}
					: state.closedChatsByProjectArea,
			};
		}),
	requestChatLocation: (request) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[request.projectAreaId] ||
				isSessionDeleted(state, request.projectAreaId, request.sessionId)
			) {
				return {};
			}
			return {
				chatLocationRequest: request,
				selectedProjectId: request.projectId,
				activeProjectAreaId: request.projectAreaId,
			};
		}),
	clearChatLocation: () => set({ chatLocationRequest: null }),
	requestHistoryOpen: (target) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[target.projectAreaId] ||
				isSessionDeleted(state, target.projectAreaId, target.sessionId)
			) {
				return {};
			}
			const cached = state.tabsByProjectArea[target.projectAreaId]?.find(
				(candidate): candidate is ChatTab =>
					candidate.kind === "chat" && candidate.sessionId === target.sessionId,
			);
			return {
				historyOpenRequest: {
					id: randomId("history-open"),
					sessionId: target.sessionId,
				},
				activeTabByProjectArea: cached
					? { ...state.activeTabByProjectArea, [target.projectAreaId]: cached.id }
					: state.activeTabByProjectArea,
			};
		}),
	clearHistoryOpen: () => set({ historyOpenRequest: null }),
});
