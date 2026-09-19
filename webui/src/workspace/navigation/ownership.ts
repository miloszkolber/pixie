import { isConnectedGeneration } from "../../connection/state";
import type { AppState } from "../../store/app-store";
import type { PrimarySelection, SecondarySelection } from "../store/selection-state";

export type NavigationOwnerSide = "primary" | "secondary" | "all";

export interface NavigationOwnerSnapshot {
	projectAreaId: string;
	projectId: string;
	activeProjectAreaId: string | null;
	selectedProjectId: string | null;
	workspaceNavigationGeneration: number;
	navigationGeneration: number;
	routeGeneration: number;
	connectionGeneration: number | null;
	primaryArea: AppState["workspaceSelection"]["primaryArea"];
	primarySelection: string;
	secondaryArea: AppState["workspaceSelection"]["secondaryArea"];
	secondarySelection: string;
}

function selectionKey(selection: PrimarySelection | SecondarySelection): string {
	return JSON.stringify(selection) ?? "";
}

export function captureNavigationOwner(
	state: Pick<
		AppState,
		| "activeProjectAreaId"
		| "selectedProjectId"
		| "workspaceNavigationGeneration"
		| "navTickByProjectArea"
		| "routeChatTargetGeneration"
		| "connectionGeneration"
		| "status"
		| "workspaceSelection"
	>,
	projectAreaId: string,
	projectId: string,
): NavigationOwnerSnapshot {
	return {
		projectAreaId,
		projectId,
		activeProjectAreaId: state.activeProjectAreaId,
		selectedProjectId: state.selectedProjectId,
		workspaceNavigationGeneration: state.workspaceNavigationGeneration,
		navigationGeneration: state.navTickByProjectArea[projectAreaId] ?? 0,
		routeGeneration: state.routeChatTargetGeneration,
		connectionGeneration: state.status === "connected" ? state.connectionGeneration : null,
		primaryArea: state.workspaceSelection.primaryArea,
		primarySelection: selectionKey(state.workspaceSelection.primarySelection),
		secondaryArea: state.workspaceSelection.secondaryArea,
		secondarySelection: selectionKey(state.workspaceSelection.secondarySelection),
	};
}

export function navigationOwnerProjectIsCurrent(
	state: Pick<AppState, "projects" | "removedProjectAreaIds">,
	snapshot: NavigationOwnerSnapshot,
): boolean {
	return (
		state.projects.some((project) => project.id === snapshot.projectId && !project.closed) &&
		!state.removedProjectAreaIds[snapshot.projectAreaId]
	);
}

export function navigationOwnerIsCurrent(
	state: Pick<
		AppState,
		| "projects"
		| "removedProjectAreaIds"
		| "activeProjectAreaId"
		| "selectedProjectId"
		| "workspaceNavigationGeneration"
		| "navTickByProjectArea"
		| "routeChatTargetGeneration"
		| "connectionGeneration"
		| "status"
		| "workspaceSelection"
	>,
	snapshot: NavigationOwnerSnapshot,
	side: NavigationOwnerSide = "all",
	options: {
		requireActive?: boolean;
		checkConnection?: boolean;
		allowUnknownProject?: boolean;
	} = {},
): boolean {
	const { requireActive = true, checkConnection = true, allowUnknownProject = false } = options;
	if (
		!navigationOwnerProjectIsCurrent(state, snapshot) &&
		!(allowUnknownProject && state.projects.length === 0)
	)
		return false;
	if (
		(requireActive &&
			(state.activeProjectAreaId !== snapshot.projectAreaId ||
				state.selectedProjectId !== snapshot.projectId)) ||
		state.workspaceNavigationGeneration !== snapshot.workspaceNavigationGeneration ||
		(state.navTickByProjectArea[snapshot.projectAreaId] ?? 0) !== snapshot.navigationGeneration ||
		state.routeChatTargetGeneration !== snapshot.routeGeneration
	) {
		return false;
	}
	if (
		checkConnection &&
		snapshot.connectionGeneration !== null &&
		!isConnectedGeneration(state, snapshot.connectionGeneration)
	) {
		return false;
	}
	if (
		(side === "primary" || side === "all") &&
		(state.workspaceSelection.primaryArea !== snapshot.primaryArea ||
			selectionKey(state.workspaceSelection.primarySelection) !== snapshot.primarySelection)
	) {
		return false;
	}
	if (
		(side === "secondary" || side === "all") &&
		(state.workspaceSelection.secondaryArea !== snapshot.secondaryArea ||
			selectionKey(state.workspaceSelection.secondarySelection) !== snapshot.secondarySelection)
	) {
		return false;
	}
	return true;
}
