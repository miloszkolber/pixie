import { errorText, getTransport } from "../../connection";
import { appStoreApi, selectProjectAreaById, toast } from "../../store";
import {
	captureNavigationOwner,
	navigationOwnerIsCurrent,
	navigationOwnerProjectIsCurrent,
} from "./ownership";

const createFlights = new Map<string, Promise<boolean>>();

function createTargetIsAvailable(
	state: ReturnType<typeof appStoreApi.getState>,
	navigation: ReturnType<typeof captureNavigationOwner>,
): boolean {
	return state.status === "connected" && navigationOwnerIsCurrent(state, navigation, "primary");
}

/** Create one chat for the selected project area and ignore stale replies. */
export function startChatSession(projectAreaId: string): Promise<boolean> {
	const existing = createFlights.get(projectAreaId);
	if (existing) return existing;

	const initial = appStoreApi.getState();
	const area = selectProjectAreaById(initial, projectAreaId);
	const projectId = area?.projectId ?? projectAreaId;
	if (
		!initial.projects.some((project) => project.id === projectId) ||
		initial.removedProjectAreaIds[projectAreaId]
	)
		return Promise.resolve(false);
	const navigation = captureNavigationOwner(initial, projectAreaId, projectId);
	const navigationGeneration = initial.navTickByProjectArea[projectAreaId] ?? 0;
	const routeGeneration = initial.routeChatTargetGeneration;
	const request = getTransport()
		.request("session.create", {
			projectId,
			...(area?.root ? { cwd: area.root } : {}),
		})
		.then((result) => {
			const current = appStoreApi.getState();
			if (
				!createTargetIsAvailable(current, navigation) ||
				(current.navTickByProjectArea[projectAreaId] ?? 0) !== navigationGeneration ||
				current.routeChatTargetGeneration !== routeGeneration
			)
				return false;
			current.openChatSession(
				projectAreaId,
				result.sessionId,
				result.model,
				result.thinkingLevel,
				undefined,
				{ activate: true },
			);
			current.setCommands(result.sessionId, result.commands);
			return true;
		})
		.catch((cause: unknown) => {
			const current = appStoreApi.getState();
			if (
				createTargetIsAvailable(current, navigation) &&
				navigationOwnerProjectIsCurrent(current, navigation)
			)
				toast.error(errorText(cause), "Couldn't start the chat");
			return false;
		})
		.finally(() => {
			if (createFlights.get(projectAreaId) === request) createFlights.delete(projectAreaId);
		});
	createFlights.set(projectAreaId, request);
	return request;
}
