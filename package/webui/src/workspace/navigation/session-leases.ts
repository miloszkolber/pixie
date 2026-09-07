import type { WsParams } from "@pixie/contracts";
import { errorText, getTransport } from "../../connection";
import type { WsTransport } from "../../connection/transport";
import { appStoreApi, isConnectedGeneration, toast } from "../../store";

// The transport's client ID lasts for the page, including authentication resets.
let revision = 0;

export function initSessionLeases(
	transport: Pick<WsTransport, "request"> = getTransport(),
): () => void {
	let stopped = false;
	let scheduled = false;
	let previousSnapshot = "";
	const flush = () => {
		scheduled = false;
		if (stopped) return;
		const state = appStoreApi.getState();
		if (state.status !== "connected" || state.welcomeGeneration === 0) {
			previousSnapshot = "";
			return;
		}
		const projects = new Set(state.projects.map((project) => project.id));
		const areas = new Map(
			Object.values(state.projectAreas)
				.flat()
				.map((area) => [area.id, area.projectId]),
		);
		const open = new Map<string, { projectId: string; sessionId: string }>();
		for (const [areaId, tabs] of Object.entries(state.tabsByProjectArea)) {
			const projectId = areas.get(areaId) ?? areaId;
			if (!projects.has(projectId) || state.removedProjectAreaIds[areaId]) continue;
			for (const tab of tabs) {
				if (tab.kind === "chat") open.set(tab.sessionId, { projectId, sessionId: tab.sessionId });
			}
		}
		const sessions = [...open.values()].sort((left, right) =>
			left.sessionId.localeCompare(right.sessionId),
		);
		const generation = state.connectionGeneration;
		const snapshot = JSON.stringify([generation, state.welcomeGeneration, sessions]);
		if (snapshot === previousSnapshot) return;
		previousSnapshot = snapshot;
		const request: WsParams<"session.setLeases"> = { revision: ++revision, sessions };
		void transport.request("session.setLeases", request).catch((error: unknown) => {
			if (
				!stopped &&
				request.revision === revision &&
				isConnectedGeneration(appStoreApi.getState(), generation)
			) {
				previousSnapshot = "";
				toast.error(errorText(error), "Couldn't synchronize open chats");
			}
		});
	};
	const schedule = () => {
		if (scheduled || stopped) return;
		scheduled = true;
		queueMicrotask(flush);
	};
	const unsubscribe = appStoreApi.subscribe((state, previous) => {
		if (
			state.tabsByProjectArea !== previous.tabsByProjectArea ||
			state.projects !== previous.projects ||
			state.projectAreas !== previous.projectAreas ||
			state.removedProjectAreaIds !== previous.removedProjectAreaIds ||
			state.status !== previous.status ||
			state.welcomeGeneration !== previous.welcomeGeneration
		) {
			schedule();
		}
	});
	schedule();
	return () => {
		stopped = true;
		unsubscribe();
	};
}
