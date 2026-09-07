import { messagesToRuntime } from "../../chat/runtime/hydrate";
import { errorText, getTransport } from "../../connection";
import { appStoreApi, chatTabId, selectProjectAreaById, toast } from "../../store";

/** Open a session in the fixed editor strip. */
export async function openChatInTab(
	projectAreaId: string,
	sessionId: string,
	background = false,
): Promise<void> {
	const initial = appStoreApi.getState();
	const projectId = selectProjectAreaById(initial, projectAreaId)?.projectId ?? projectAreaId;
	const closedChat = initial.closedChatsByProjectArea[projectAreaId]?.find(
		(chat) => chat.sessionId === sessionId,
	);
	const requestConnectionGeneration =
		initial.status === "connected" ? initial.connectionGeneration : null;
	if (
		!initial.projects.some((project) => project.id === projectId) ||
		initial.removedProjectAreaIds[projectAreaId] ||
		initial.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
	) {
		return;
	}
	const options = background ? { activate: false } : undefined;
	const store = appStoreApi.getState();
	const tab = (store.tabsByProjectArea[projectAreaId] ?? []).find(
		(t) => t.kind === "chat" && t.sessionId === sessionId,
	);
	if (tab) {
		store.openTab(tab, "keep", options);
		return;
	}
	if (store.sessions[sessionId]) {
		if (closedChat) {
			store.reopenChat(projectAreaId, sessionId, options);
			return;
		}
		store.openTab(
			{
				kind: "chat",
				id: chatTabId(projectAreaId, sessionId),
				projectAreaId,
				name: "Chat",
				sessionId,
			},
			"keep",
			options,
		);
		return;
	}
	try {
		const response = await getTransport().request("session.getMessages", {
			sessionId,
			projectId: projectAreaId,
		});
		if (response.kind !== "snapshot") throw new Error("invalid chat snapshot");
		const { summary, messages, pendingTools, commands, modes, planState, page } = response;
		const current = appStoreApi.getState();
		if (
			!current.projects.some((project) => project.id === projectId) ||
			current.closedChatsByProjectArea[projectAreaId]?.find(
				(chat) => chat.sessionId === sessionId,
			) !== closedChat
		) {
			return;
		}
		if (
			requestConnectionGeneration !== null &&
			current.connectionGeneration !== requestConnectionGeneration &&
			!current.removedProjectAreaIds[projectAreaId] &&
			!current.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
		) {
			return openChatInTab(projectAreaId, sessionId, background);
		}
		current.hydrateSession(
			summary,
			messagesToRuntime(messages, {
				lastSettlement: summary.lastSettlement,
				pendingTools,
				page,
				isStreaming: summary.isStreaming,
			}),
			modes,
			planState,
			!background,
			undefined,
			options,
		);
		appStoreApi.getState().setCommands(sessionId, commands);
		const settled = appStoreApi.getState();
		const installed =
			settled.sessions[sessionId] !== undefined &&
			(settled.tabsByProjectArea[projectAreaId] ?? []).some(
				(tab) => tab.kind === "chat" && tab.sessionId === sessionId,
			);
		if (
			!installed &&
			!background &&
			!settled.removedProjectAreaIds[projectAreaId] &&
			!settled.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
		) {
			toast.error("The chat could not be restored.", "Couldn't open the chat");
		}
	} catch (err) {
		const current = appStoreApi.getState();
		if (
			!background &&
			!current.removedProjectAreaIds[projectAreaId] &&
			!current.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
		) {
			toast.error(errorText(err), "Couldn't open the chat");
		}
	}
}
