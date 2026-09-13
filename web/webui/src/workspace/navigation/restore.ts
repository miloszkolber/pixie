import {
	appStoreApi,
	type ProjectArea,
	selectCurrentRouteChatTarget,
	selectProjectAreaById,
	selectProjectAreaNavTick,
} from "../../store";
import {
	selectPrimary,
	selectSecondary,
	selectSecondaryArea,
	type WorkspaceSelectionSnapshot,
} from "../store/selection-state";
import type { NavigationDriver } from "./driver";
import {
	isNavigationLocationV2,
	type NavigationLocation,
	type NavigationLocationV2,
	parseFragment,
	serializeLocation,
} from "./location";

export interface NavigationDeps {
	driver: NavigationDriver;
	listProjectAreas: (projectId: string) => Promise<ProjectArea[]>;
}

export function deriveLocation(state: {
	activeProjectAreaId: string | null;
	selectedProjectId: string | null;
	projectAreas: Record<string, ProjectArea[]>;
	tabsByProjectArea: Record<string, { id: string; kind: string; sessionId?: string }[]>;
	activeTabByProjectArea: Record<string, string | null>;
	workspaceSelection?: WorkspaceSelectionSnapshot;
}): NavigationLocation | null {
	if (state.workspaceSelection) {
		const projectArea = state.activeProjectAreaId
			? selectProjectAreaById(state, state.activeProjectAreaId)
			: null;
		const secondary = state.workspaceSelection.secondarySelection;
		const selectionProjectId =
			state.workspaceSelection.primarySelection?.kind === "session"
				? (state.workspaceSelection.primarySelection.projectId ?? null)
				: state.workspaceSelection.primarySelection?.kind === "schedule"
					? state.workspaceSelection.primarySelection.projectId
					: secondary?.kind === "file" || secondary?.kind === "diff"
						? secondary.projectId
						: secondary?.kind === "module" && secondary.context.scope === "project"
							? secondary.context.projectId
							: secondary?.kind === "module" && secondary.context.scope === "session"
								? (secondary.context.projectId ?? null)
								: null;
		return {
			version: 2,
			primaryArea: state.workspaceSelection.primaryArea,
			primarySelection: state.workspaceSelection.primarySelection,
			secondaryArea: state.workspaceSelection.secondaryArea,
			secondarySelection: state.workspaceSelection.secondarySelection,
			projectId: projectArea?.projectId ?? selectionProjectId ?? state.selectedProjectId,
			projectAreaId: projectArea?.id ?? state.activeProjectAreaId,
		};
	}
	const projectAreaId = state.activeProjectAreaId;
	if (projectAreaId) {
		const projectArea = selectProjectAreaById(state, projectAreaId);
		if (!projectArea) return null;
		const activeId = state.activeTabByProjectArea[projectAreaId];
		const active = (state.tabsByProjectArea[projectAreaId] ?? []).find(
			(tab) => tab.id === activeId,
		);
		if (active?.kind === "chat" && active.sessionId) {
			return {
				kind: "chat",
				projectId: projectArea.projectId,
				projectAreaId,
				sessionId: active.sessionId,
			};
		}
		return { kind: "projectArea", projectId: projectArea.projectId, projectAreaId };
	}
	if (state.selectedProjectId) return { kind: "project", projectId: state.selectedProjectId };
	return { kind: "main" };
}

interface NavigationIntentState {
	selectedProjectId: string | null;
	activeProjectAreaId: string | null;
	navTickByProjectArea: Record<string, number>;
	workspaceSelection: WorkspaceSelectionSnapshot;
}

function isUserNavigationEdge(
	state: NavigationIntentState,
	previous: NavigationIntentState,
): boolean {
	if (state.selectedProjectId !== previous.selectedProjectId) return true;
	if (state.activeProjectAreaId !== previous.activeProjectAreaId) return true;
	if (state.workspaceSelection !== previous.workspaceSelection) return true;
	const projectAreaId = state.activeProjectAreaId;
	if (!projectAreaId) return false;
	return (
		selectProjectAreaNavTick(state, projectAreaId) >
		selectProjectAreaNavTick(previous, projectAreaId)
	);
}

function projectIdForLocation(location: NavigationLocationV2): string | null {
	if (location.projectId) return location.projectId;
	if (location.primarySelection?.kind === "session")
		return location.primarySelection.projectId ?? null;
	if (location.primarySelection?.kind === "schedule") return location.primarySelection.projectId;
	const secondary = location.secondarySelection;
	if (secondary?.kind === "file" || secondary?.kind === "diff") return secondary.projectId;
	if (secondary?.kind === "module") {
		if (secondary.context.scope === "project") return secondary.context.projectId;
		if (secondary.context.scope === "session") return secondary.context.projectId ?? null;
	}
	return null;
}

function locationNeedsProjectArea(location: NavigationLocationV2): boolean {
	if (location.projectAreaId) return true;
	if (location.primarySelection?.kind === "session") return true;
	const secondary = location.secondarySelection;
	if (secondary?.kind === "file" || secondary?.kind === "diff") return true;
	return secondary?.kind === "module" && secondary.context.scope !== "instance";
}

function applyWorkspaceLocation(location: NavigationLocationV2): void {
	const state = appStoreApi.getState();
	state.dispatchWorkspaceSelection(selectPrimary(location.primarySelection, location.primaryArea));
	state.dispatchWorkspaceSelection(
		selectSecondary(location.secondarySelection, location.secondaryArea),
	);
	if (location.secondarySelection === null)
		appStoreApi.getState().dispatchWorkspaceSelection(selectSecondaryArea(location.secondaryArea));
}

export function startNavigation({ driver, listProjectAreas }: NavigationDeps): () => void {
	let generation = 0;
	let pending: { generation: number; location: NavigationLocation } | null = null;
	const attempting = new Set<number>();
	let lastWritten = "";
	let armedPush = false;
	let legacyRouteActive = false;
	let applyingRoute = false;

	const applyRoute = (write: () => void) => {
		applyingRoute = true;
		try {
			write();
		} finally {
			applyingRoute = false;
		}
	};

	const syncNow = () => {
		if (pending) return;
		const state = appStoreApi.getState();
		if (state.routeChatTarget) return;
		const location = deriveLocation(state);
		if (!location) return;
		const fragment = serializeLocation(location);
		if (fragment === lastWritten) return;
		// An incoming v1 link is a supported migration boundary. Keep its
		// address stable until the user makes a canonical selection change;
		// that avoids adding a history entry while reconnecting or hydrating it.
		if (legacyRouteActive && !armedPush && isNavigationLocationV2(location)) return;
		if (armedPush) driver.push(fragment);
		else driver.replace(fragment);
		armedPush = false;
		lastWritten = fragment;
	};

	const resolvePending = (gen: number) => {
		if (pending?.generation === gen) pending = null;
		syncNow();
	};

	const attempt = async (gen: number) => {
		if (pending?.generation !== gen || attempting.has(gen)) return;
		const location = pending.location;
		const state = appStoreApi.getState();
		if (!isNavigationLocationV2(location) && location.kind === "main") {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		const v2 = isNavigationLocationV2(location);
		const projectId = v2 ? projectIdForLocation(location) : location.projectId;
		const requestedProjectAreaId = v2
			? location.projectAreaId
			: location.kind === "project"
				? null
				: location.projectAreaId;
		// A v2 schedule/settings route still carries its project area for shell
		// chrome; honor it so the selection restores in its own column instead
		// of dropping to the project home.
		const needsProjectArea = v2
			? locationNeedsProjectArea(location) || requestedProjectAreaId !== null
			: location.kind !== "project";
		if (!projectId) {
			if (!v2) return;
			applyRoute(() => {
				const current = appStoreApi.getState();
				if (current.activeProjectAreaId) current.selectMain();
				applyWorkspaceLocation(location);
			});
			resolvePending(gen);
			return;
		}
		if (
			state.status !== "connected" ||
			state.welcomeGeneration === 0 ||
			state.welcomeConnectionGeneration !== state.connectionGeneration
		) {
			return;
		}
		if (!state.projects.some((project) => project.id === projectId)) {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		if (!needsProjectArea) {
			applyRoute(() => {
				appStoreApi.getState().selectProject(projectId);
				if (v2) applyWorkspaceLocation(location);
			});
			resolvePending(gen);
			return;
		}
		attempting.add(gen);
		let rows: ProjectArea[];
		try {
			rows = await listProjectAreas(projectId);
		} catch {
			attempting.delete(gen);
			return;
		}
		attempting.delete(gen);
		if (pending?.generation !== gen) return;
		const now = appStoreApi.getState();
		if (!now.projects.some((project) => project.id === projectId)) {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		applyRoute(() => now.setProjectAreas(projectId, rows));
		const projectArea = requestedProjectAreaId
			? rows.find((candidate) => candidate.id === requestedProjectAreaId)
			: rows[0];
		if (!projectArea) {
			applyRoute(() => {
				appStoreApi.getState().selectProject(projectId);
				if (v2) applyWorkspaceLocation(location);
			});
			resolvePending(gen);
			return;
		}
		applyRoute(() => {
			const current = appStoreApi.getState();
			if (v2) {
				if (location.primaryArea === "chats" && location.primarySelection?.kind === "session")
					current.activateProjectAreaFromRoute(projectArea, location.primarySelection.sessionId);
				else current.activateProjectArea(projectArea);
				applyWorkspaceLocation(location);
			} else {
				current.activateProjectAreaFromRoute(
					projectArea,
					location.kind === "chat" ? location.sessionId : undefined,
				);
			}
		});
		resolvePending(gen);
	};

	const acceptFragment = (fragment: string) => {
		const location = parseFragment(fragment);
		generation += 1;
		pending = { generation, location };
		armedPush = false;
		legacyRouteActive = !isNavigationLocationV2(location);
		applyRoute(() => {
			const state = appStoreApi.getState();
			if (state.activeProjectAreaId) state.noteNavigation(state.activeProjectAreaId);
			appStoreApi.getState().clearRouteChatTarget();
		});
		const canonical = serializeLocation(location);
		if (canonical !== fragment) driver.replace(canonical);
		lastWritten = canonical;
		void attempt(generation);
	};

	const unsubscribeDriver = driver.onIncoming(acceptFragment);
	const unsubscribeStore = appStoreApi.subscribe((state, previous) => {
		if (previous.routeChatTarget && !state.routeChatTarget) armedPush = false;
		if (!applyingRoute && isUserNavigationEdge(state, previous)) {
			armedPush = true;
			legacyRouteActive = false;
		}
		if (
			!applyingRoute &&
			pending &&
			(state.selectedProjectId !== previous.selectedProjectId ||
				state.activeProjectAreaId !== previous.activeProjectAreaId ||
				state.navTickByProjectArea !== previous.navTickByProjectArea ||
				state.workspaceSelection !== previous.workspaceSelection)
		) {
			pending = null;
		}
		if (state.welcomeGeneration !== previous.welcomeGeneration && pending) {
			void attempt(pending.generation);
		}
		if (state.routeChatTarget && !selectCurrentRouteChatTarget(state)) {
			state.clearRouteChatTarget();
			return;
		}
		syncNow();
	});

	acceptFragment(driver.read());

	return () => {
		unsubscribeDriver();
		unsubscribeStore();
		pending = null;
	};
}
