import { messagesToRuntime } from "../../chat/runtime/hydrate";
import { errorText, getTransport } from "../../connection";
import { tupleKey } from "../../lib";
import {
	appStoreApi,
	type ChatTab,
	type ClosedChat,
	chatTabId,
	isConnectedGeneration,
	selectProjectAreaById,
	selectProjectAreaSessionIds,
	toast,
} from "../../store";

const sessionHydration = new Map<
	string,
	{ promise: Promise<boolean>; closedChat: ClosedChat | undefined }
>();
const refreshedConnectionByProjectArea = new Map<string, number>();
const AUTO_OPEN_CHAT_LIMIT = 4;
const CHAT_REFRESH_BATCH = 4;

function chatTab(
	state: ReturnType<typeof appStoreApi.getState>,
	projectAreaId: string,
	sessionId: string,
) {
	return (state.tabsByProjectArea[projectAreaId] ?? []).find(
		(tab): tab is ChatTab => tab.kind === "chat" && tab.sessionId === sessionId,
	);
}

export function hydrateChatResource(
	projectAreaId: string,
	sessionId: string,
	refreshExisting = false,
): Promise<boolean> {
	const state = appStoreApi.getState();
	const projectId = selectProjectAreaById(state, projectAreaId)?.projectId ?? projectAreaId;
	if (
		!state.projects.some((project) => project.id === projectId) ||
		state.removedProjectAreaIds[projectAreaId] ||
		state.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
	) {
		return Promise.resolve(false);
	}
	const closedChat = state.closedChatsByProjectArea[projectAreaId]?.find(
		(chat) => chat.sessionId === sessionId,
	);
	if (state.sessions[sessionId] && !refreshExisting) {
		if (!chatTab(state, projectAreaId, sessionId)) {
			state.openTab(
				{
					kind: "chat",
					id: chatTabId(projectAreaId, sessionId),
					projectAreaId,
					name: closedChat?.title ?? "Chat",
					sessionId,
				},
				"keep",
				{ activate: false },
			);
		}
		return Promise.resolve(true);
	}
	const generation = state.connectionGeneration;
	const key = tupleKey("chat-hydration", projectAreaId, sessionId, String(generation));
	const existing = sessionHydration.get(key);
	if (existing && existing.closedChat === closedChat) return existing.promise;
	const request = getTransport()
		.request("session.getMessages", { projectId: projectAreaId, sessionId })
		.then((response) => {
			if (response.kind !== "snapshot") throw new Error("invalid chat snapshot");
			const { summary, messages, pendingTools, commands, planState, page } = response;
			const current = appStoreApi.getState();
			if (!isConnectedGeneration(current, generation)) return false;
			if (
				!current.projects.some((project) => project.id === projectId) ||
				current.closedChatsByProjectArea[projectAreaId]?.find(
					(chat) => chat.sessionId === sessionId,
				) !== closedChat ||
				current.removedProjectAreaIds[projectAreaId] ||
				current.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
			) {
				return false;
			}
			const hydrated = messagesToRuntime(messages, {
				lastSettlement: summary.lastSettlement,
				pendingTools,
				page,
				isStreaming: summary.isStreaming,
			});
			if (current.sessions[sessionId]) {
				if (!chatTab(current, projectAreaId, sessionId)) return false;
				current.replaceTranscriptSnapshot(sessionId, summary, hydrated, planState);
			} else {
				current.hydrateSession(summary, hydrated, planState, false, undefined, {
					activate: false,
				});
			}
			appStoreApi.getState().setCommands(sessionId, commands);
			const installed = appStoreApi.getState();
			return (
				installed.sessions[sessionId] !== undefined &&
				chatTab(installed, projectAreaId, sessionId) !== undefined
			);
		})
		.finally(() => {
			if (sessionHydration.get(key)?.promise === request) sessionHydration.delete(key);
		});
	sessionHydration.set(key, { promise: request, closedChat });
	return request;
}

export function initProjectAreaChatReconciliation(projectAreaId: string): () => void {
	let active = true;
	let catalogFlight = 0;
	let locationFlight = 0;
	let lastConnectionGeneration = -1;
	let lastCatalogKey = "";
	let lastLocationRequest = appStoreApi.getState().chatLocationRequest;
	let lastLocationStatus = "";

	function refreshOpenChats(connectionGeneration: number): void {
		if (connectionGeneration === 0) return;
		const state = appStoreApi.getState();
		if (!isConnectedGeneration(state, connectionGeneration)) return;
		if (refreshedConnectionByProjectArea.get(projectAreaId) === connectionGeneration) return;
		refreshedConnectionByProjectArea.set(projectAreaId, connectionGeneration);
		const sessionIds = (state.tabsByProjectArea[projectAreaId] ?? [])
			.filter((tab): tab is ChatTab => tab.kind === "chat")
			.map((tab) => tab.sessionId);
		void (async () => {
			for (let index = 0; index < sessionIds.length; index += CHAT_REFRESH_BATCH) {
				if (!isConnectedGeneration(appStoreApi.getState(), connectionGeneration)) return;
				await Promise.allSettled(
					sessionIds
						.slice(index, index + CHAT_REFRESH_BATCH)
						.map((sessionId) => hydrateChatResource(projectAreaId, sessionId, true)),
				);
			}
		})();
	}

	function reconcileCatalog(): void {
		const snapshot = appStoreApi.getState();
		const { status, connectionGeneration } = snapshot;
		const routeTarget =
			snapshot.routeChatTarget?.projectAreaId === projectAreaId ? snapshot.routeChatTarget : null;
		if (status !== "connected" || connectionGeneration === 0) {
			catalogFlight += 1;
			return;
		}
		const run = ++catalogFlight;
		const baseline = selectProjectAreaSessionIds(snapshot, projectAreaId);
		const live = () =>
			active &&
			catalogFlight === run &&
			isConnectedGeneration(appStoreApi.getState(), connectionGeneration) &&
			!appStoreApi.getState().removedProjectAreaIds[projectAreaId];

		void getTransport()
			.request("session.list", { projectId: projectAreaId, archived: "all" })
			.then(async (catalog) => {
				if (!live()) return;
				const summaries = catalog.filter((summary) => !summary.archived);
				appStoreApi.getState().reconcileProjectAreaSessions(projectAreaId, baseline, catalog);
				const targetSummary = routeTarget
					? summaries.find((summary) => summary.sessionId === routeTarget.sessionId)
					: undefined;
				if (routeTarget && targetSummary) {
					appStoreApi.getState().validateRouteChatTarget(routeTarget.sessionId);
					const targetTab = chatTab(appStoreApi.getState(), projectAreaId, routeTarget.sessionId);
					if (targetTab) appStoreApi.getState().setActiveTab(targetTab.id, "keep");
					else {
						await hydrateChatResource(projectAreaId, routeTarget.sessionId);
						if (!live()) return;
						const hydrated = chatTab(appStoreApi.getState(), projectAreaId, routeTarget.sessionId);
						if (hydrated) appStoreApi.getState().setActiveTab(hydrated.id, "keep");
					}
					appStoreApi.getState().clearRouteChatTarget();
				}

				const state = appStoreApi.getState();
				const openSessions = new Set(
					(state.tabsByProjectArea[projectAreaId] ?? [])
						.filter((tab) => tab.kind === "chat")
						.map((tab) => tab.sessionId),
				);
				const closedSessions = new Set(
					(state.closedChatsByProjectArea[projectAreaId] ?? []).map((chat) => chat.sessionId),
				);
				const toOpen = summaries
					.filter(
						(summary) =>
							summary.live &&
							!openSessions.has(summary.sessionId) &&
							!closedSessions.has(summary.sessionId) &&
							summary.sessionId !== routeTarget?.sessionId,
					)
					.sort((a, b) => b.updatedAt - a.updatedAt)
					.slice(0, AUTO_OPEN_CHAT_LIMIT);
				const history = summaries.filter(
					(summary) => !summary.live && !openSessions.has(summary.sessionId),
				);
				let activated = Boolean(routeTarget);
				for (const summary of toOpen) {
					if (
						appStoreApi
							.getState()
							.closedChatsByProjectArea[projectAreaId]?.some(
								(chat) => chat.sessionId === summary.sessionId,
							)
					)
						continue;
					await hydrateChatResource(projectAreaId, summary.sessionId);
					if (!live()) return;
					const tab = chatTab(appStoreApi.getState(), projectAreaId, summary.sessionId);
					if (tab && !activated) {
						appStoreApi.getState().setActiveTab(tab.id, "keep");
						activated = true;
					}
				}
				if (history.length > 0 && live()) {
					appStoreApi.getState().noteClosedChats(
						projectAreaId,
						history.map((summary) => ({
							sessionId: summary.sessionId,
							title: summary.title,
							closedAt: summary.updatedAt,
						})),
					);
				}
			})
			.catch((cause: unknown) => {
				if (live()) toast.error(errorText(cause), "Couldn't load this project's chats");
			});
	}

	function reconcileLocation(): void {
		const state = appStoreApi.getState();
		const request = state.chatLocationRequest;
		const run = ++locationFlight;
		if (!request || request.projectAreaId !== projectAreaId) return;
		const existing = chatTab(state, projectAreaId, request.sessionId);
		if (existing) {
			state.setActiveTab(existing.id, "keep");
			return;
		}
		if (state.sessions[request.sessionId]) {
			const title =
				state.closedChatsByProjectArea[projectAreaId]?.find(
					(chat) => chat.sessionId === request.sessionId,
				)?.title ?? "Chat";
			state.openTab(
				{
					kind: "chat",
					id: chatTabId(projectAreaId, request.sessionId),
					projectAreaId,
					name: title,
					sessionId: request.sessionId,
				},
				"keep",
			);
			return;
		}
		if (state.status !== "connected" || !isConnectedGeneration(state, state.connectionGeneration)) {
			return;
		}
		const live = () => active && locationFlight === run;
		void hydrateChatResource(projectAreaId, request.sessionId)
			.then((installed) => {
				if (!live()) return;
				const latest = appStoreApi.getState();
				if (installed) {
					const tab = chatTab(latest, projectAreaId, request.sessionId);
					if (tab) latest.setActiveTab(tab.id, "keep");
				} else if (
					!latest.removedProjectAreaIds[projectAreaId] &&
					!latest.deletedSessionsByProjectArea[projectAreaId]?.[request.sessionId]
				)
					toast.error("The chat could not be restored.", "Couldn't open the chat");
				if (!installed && latest.chatLocationRequest === request) latest.clearChatLocation();
			})
			.catch((cause) => {
				if (live()) toast.error(errorText(cause), "Couldn't open the chat");
				const latest = appStoreApi.getState();
				if (latest.chatLocationRequest === request) latest.clearChatLocation();
			});
	}

	function update(): void {
		const state = appStoreApi.getState();
		if (state.connectionGeneration !== lastConnectionGeneration) {
			lastConnectionGeneration = state.connectionGeneration;
			refreshOpenChats(state.connectionGeneration);
		}
		const target =
			state.routeChatTarget?.projectAreaId === projectAreaId ? state.routeChatTarget : null;
		const catalogKey = [
			state.status,
			state.connectionGeneration,
			state.sessionCatalogVersionByProjectArea[projectAreaId] ?? 0,
			state.routeChatTargetGeneration,
			target?.sessionId ?? "",
		].join(":");
		if (catalogKey !== lastCatalogKey) {
			lastCatalogKey = catalogKey;
			reconcileCatalog();
		}
		const locationStatus = `${state.status}:${state.connectionGeneration}`;
		if (
			state.chatLocationRequest !== lastLocationRequest ||
			locationStatus !== lastLocationStatus
		) {
			lastLocationRequest = state.chatLocationRequest;
			lastLocationStatus = locationStatus;
			reconcileLocation();
		}
	}

	const unsubscribe = appStoreApi.subscribe(update);
	lastLocationRequest = null;
	update();
	return () => {
		active = false;
		catalogFlight += 1;
		locationFlight += 1;
		unsubscribe();
	};
}
