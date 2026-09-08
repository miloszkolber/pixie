import { errorText, getTransport } from "../../connection";
import { appStoreApi, isConnectedGeneration, selectProjectAreaById, toast } from "../../store";

const createFlights = new Map<string, Promise<boolean>>();

function createTargetIsAvailable(
	state: ReturnType<typeof appStoreApi.getState>,
	projectAreaId: string,
	projectId: string,
	connectionGeneration: number | null,
): boolean {
	return (
		state.status === "connected" &&
		(connectionGeneration === null || isConnectedGeneration(state, connectionGeneration)) &&
		state.activeProjectAreaId === projectAreaId &&
		state.projects.some((project) => project.id === projectId) &&
		!state.removedProjectAreaIds[projectAreaId]
	);
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
	const connectionGeneration = initial.status === "connected" ? initial.connectionGeneration : null;
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
				!createTargetIsAvailable(current, projectAreaId, projectId, connectionGeneration) ||
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
				current.status === "connected" &&
				(connectionGeneration === null || isConnectedGeneration(current, connectionGeneration)) &&
				current.projects.some((project) => project.id === projectId) &&
				!current.removedProjectAreaIds[projectAreaId]
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
