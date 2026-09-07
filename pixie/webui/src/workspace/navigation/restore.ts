import {
	appStoreApi,
	type ProjectArea,
	selectCurrentRouteChatTarget,
	selectProjectAreaById,
	selectProjectAreaNavTick,
} from "../../store";
import type { NavigationDriver } from "./driver";
import { type NavigationLocation, parseFragment, serializeLocation } from "./location";

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
}): NavigationLocation | null {
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
}

function isUserNavigationEdge(
	state: NavigationIntentState,
	previous: NavigationIntentState,
): boolean {
	if (state.selectedProjectId !== previous.selectedProjectId) return true;
	if (state.activeProjectAreaId !== previous.activeProjectAreaId) return true;
	const projectAreaId = state.activeProjectAreaId;
	if (!projectAreaId) return false;
	return (
		selectProjectAreaNavTick(state, projectAreaId) >
		selectProjectAreaNavTick(previous, projectAreaId)
	);
}

export function startNavigation({ driver, listProjectAreas }: NavigationDeps): () => void {
	let generation = 0;
	let pending: { generation: number; location: NavigationLocation } | null = null;
	const attempting = new Set<number>();
	let lastWritten = "";
	let armedPush = false;
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
		if (location.kind === "main") {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		const state = appStoreApi.getState();
		if (
			state.status !== "connected" ||
			state.welcomeGeneration === 0 ||
			state.welcomeConnectionGeneration !== state.connectionGeneration
		) {
			return;
		}
		if (!state.projects.some((p) => p.id === location.projectId)) {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		if (location.kind === "project") {
			applyRoute(() => appStoreApi.getState().selectProject(location.projectId));
			resolvePending(gen);
			return;
		}
		attempting.add(gen);
		let rows: ProjectArea[];
		try {
			rows = await listProjectAreas(location.projectId);
		} catch {
			attempting.delete(gen);
			return;
		}
		attempting.delete(gen);
		if (pending?.generation !== gen) return;
		const now = appStoreApi.getState();
		if (!now.projects.some((p) => p.id === location.projectId)) {
			applyRoute(() => appStoreApi.getState().selectMain());
			resolvePending(gen);
			return;
		}
		applyRoute(() => now.setProjectAreas(location.projectId, rows));
		const projectArea = rows.find((w) => w.id === location.projectAreaId);
		if (!projectArea) {
			applyRoute(() => appStoreApi.getState().selectProject(location.projectId));
			resolvePending(gen);
			return;
		}
		applyRoute(() =>
			appStoreApi
				.getState()
				.activateProjectAreaFromRoute(
					projectArea,
					location.kind === "chat" ? location.sessionId : undefined,
				),
		);
		resolvePending(gen);
	};

	const acceptFragment = (fragment: string) => {
		const location = parseFragment(fragment);
		generation += 1;
		pending = { generation, location };
		armedPush = false;
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
		if (!applyingRoute && isUserNavigationEdge(state, previous)) {
			armedPush = true;
		}
		if (
			!applyingRoute &&
			pending &&
			(state.selectedProjectId !== previous.selectedProjectId ||
				state.activeProjectAreaId !== previous.activeProjectAreaId ||
				state.navTickByProjectArea !== previous.navTickByProjectArea)
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
