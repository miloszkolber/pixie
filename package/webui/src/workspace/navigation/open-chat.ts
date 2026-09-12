import { messagesToRuntime } from "../../chat/runtime/hydrate";
import { errorText, getTransport } from "../../connection";
import { appStoreApi, chatTabId, selectProjectAreaById, toast } from "../../store";
import {
	captureNavigationOwner,
	navigationOwnerIsCurrent,
	navigationOwnerProjectIsCurrent,
} from "./ownership";

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
	const navigation = captureNavigationOwner(initial, projectAreaId, projectId);
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
		const { summary, messages, pendingTools, pendingDialogs, commands, planState, page } = response;
		const current = appStoreApi.getState();
		if (
			!navigationOwnerProjectIsCurrent(current, navigation) ||
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
			if (
				!background &&
				navigationOwnerIsCurrent(current, navigation, "primary", {
					checkConnection: false,
				})
			)
				return openChatInTab(projectAreaId, sessionId, background);
			return;
		}
		const activate = !background && navigationOwnerIsCurrent(current, navigation, "primary");
		current.hydrateSession(
			summary,
			messagesToRuntime(messages, {
				lastSettlement: summary.lastSettlement,
				pendingTools,
				page,
				isStreaming: summary.isStreaming,
			}),
			planState,
			activate,
			undefined,
			{ activate },
		);
		appStoreApi.getState().setCommands(sessionId, commands);
		appStoreApi.getState().reconcileUiDialogs(sessionId, pendingDialogs ?? []);
		const settled = appStoreApi.getState();
		const installed =
			settled.sessions[sessionId] !== undefined &&
			(settled.tabsByProjectArea[projectAreaId] ?? []).some(
				(tab) => tab.kind === "chat" && tab.sessionId === sessionId,
			);
		if (
			!installed &&
			activate &&
			!settled.removedProjectAreaIds[projectAreaId] &&
			!settled.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
		) {
			toast.error("The chat could not be restored.", "Couldn't open the chat");
		}
	} catch (err) {
		const current = appStoreApi.getState();
		if (
			!background &&
			navigationOwnerIsCurrent(current, navigation, "primary") &&
			!current.removedProjectAreaIds[projectAreaId] &&
			!current.deletedSessionsByProjectArea[projectAreaId]?.[sessionId]
		) {
			toast.error(errorText(err), "Couldn't open the chat");
		}
	}
}
