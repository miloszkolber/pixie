import type { Project } from "@pixie/contracts";
import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { omitKey } from "@/store/record";
import type { ProjectArea } from "./model";
import {
	selectActiveProjectAreaProjectId,
	selectProjectAreaNavTick,
	selectProjectAreaSessionIds,
} from "./selectors";

export interface ProjectWorkspaceState {
	projects: Project[];
	recentProjects: Project[];
	projectAreas: Record<string, ProjectArea[]>;
	removedProjectAreaIds: Record<string, true>;
	expandedProjectIds: Record<string, true>;
	selectedProjectId: string | null;
	activeProjectAreaId: string | null;
	installProjectSnapshot: (projects: Project[], recentProjects: Project[]) => void;
	applyProjectUpdated: (project: Project) => void;
	setProjectAreas: (projectId: string, projectAreas: ProjectArea[]) => void;
	addProjectArea: (projectArea: ProjectArea) => void;
	updateProjectArea: (projectArea: ProjectArea) => void;
	removeProjectArea: (projectId: string, projectAreaId: string) => void;
	applyProjectAreaRemoved: (projectId: string, projectAreaId: string) => void;
	selectProject: (projectId: string, opts?: { reveal?: boolean }) => void;
	toggleProjectExpanded: (projectId: string) => void;
	expandProject: (projectId: string) => void;
	hydrateExpandedProjects: (projectIds: readonly string[]) => void;
	selectMain: () => void;
	activateProjectArea: (projectArea: Pick<ProjectArea, "id" | "projectId">) => void;
	activateProjectAreaFromRoute: (
		projectArea: Pick<ProjectArea, "id" | "projectId">,
		sessionId?: string,
	) => void;
}

function sortProjects(projects: Project[]): Project[] {
	return [...projects].sort((a, b) => b.lastOpened - a.lastOpened);
}

function upsertProject(projects: Project[], project: Project): Project[] {
	return projects.some((candidate) => candidate.id === project.id)
		? projects.map((candidate) => (candidate.id === project.id ? project : candidate))
		: [...projects, project];
}

function withExpandedProject(
	record: Record<string, true>,
	projectId: string,
): Record<string, true> {
	return record[projectId] ? record : { ...record, [projectId]: true };
}

function pruneExpandedProjects(
	state: Pick<AppState, "expandedProjectIds">,
	projects: Project[],
): Pick<AppState, "expandedProjectIds"> | Record<string, never> {
	const open = new Set(projects.map((project) => project.id));
	const kept = Object.keys(state.expandedProjectIds).filter((id) => open.has(id));
	if (kept.length === Object.keys(state.expandedProjectIds).length) return {};
	return {
		expandedProjectIds: Object.fromEntries(kept.map((id) => [id, true as const])),
	};
}

function reconcileProjectNavigation(
	state: Pick<AppState, "selectedProjectId" | "activeProjectAreaId" | "projectAreas">,
	projects: Project[],
): Pick<AppState, "selectedProjectId" | "activeProjectAreaId"> | Record<string, never> {
	const currentProjectId = selectActiveProjectAreaProjectId(state) ?? state.selectedProjectId;
	if (!currentProjectId || projects.some((project) => project.id === currentProjectId)) return {};
	return { selectedProjectId: projects[0]?.id ?? null, activeProjectAreaId: null };
}

export function projectSnapshot(state: AppState, projects: Project[], recentProjects: Project[]) {
	const openProjects = sortProjects(projects.filter((project) => project.closed !== true));
	return {
		projects: openProjects,
		recentProjects: sortProjects(recentProjects),
		...reconcileProjectNavigation(state, openProjects),
		...pruneExpandedProjects(state, openProjects),
	};
}

export const createProjectWorkspaceState: StateCreator<AppState, [], [], ProjectWorkspaceState> = (
	set,
	get,
) => ({
	projects: [],
	recentProjects: [],
	projectAreas: {},
	removedProjectAreaIds: Object.create(null) as Record<string, true>,
	expandedProjectIds: Object.create(null) as Record<string, true>,
	selectedProjectId: null,
	activeProjectAreaId: null,
	installProjectSnapshot: (projects, recentProjects) =>
		set((state) => projectSnapshot(state, projects, recentProjects)),
	applyProjectUpdated: (project) =>
		set((state) => {
			const projects =
				project.closed === true
					? state.projects.filter((candidate) => candidate.id !== project.id)
					: sortProjects(upsertProject(state.projects, project));
			return {
				projects,
				recentProjects: sortProjects(upsertProject(state.recentProjects, project)),
				...reconcileProjectNavigation(state, projects),
				...pruneExpandedProjects(state, projects),
			};
		}),
	setProjectAreas: (projectId, projectAreas) =>
		set((state) => ({
			projectAreas: {
				...state.projectAreas,
				[projectId]: projectAreas.filter(
					(projectArea) => !state.removedProjectAreaIds[projectArea.id],
				),
			},
		})),
	addProjectArea: (projectArea) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectArea.id]) return {};
			const projectAreas = state.projectAreas[projectArea.projectId];
			if (!projectAreas) return {};
			return {
				projectAreas: {
					...state.projectAreas,
					[projectArea.projectId]: projectAreas.some((candidate) => candidate.id === projectArea.id)
						? projectAreas.map((candidate) =>
								candidate.id === projectArea.id ? { ...candidate, ...projectArea } : candidate,
							)
						: [...projectAreas, projectArea],
				},
			};
		}),
	updateProjectArea: (projectArea) =>
		set((state) => {
			const projectAreas = state.projectAreas[projectArea.projectId];
			if (!projectAreas?.some((candidate) => candidate.id === projectArea.id)) return {};
			return {
				projectAreas: {
					...state.projectAreas,
					[projectArea.projectId]: projectAreas.map((candidate) =>
						candidate.id === projectArea.id ? projectArea : candidate,
					),
				},
			};
		}),
	removeProjectArea: (projectId, projectAreaId) =>
		set((state) => {
			const projectAreas = state.projectAreas[projectId];
			if (!projectAreas) return {};
			return {
				projectAreas: {
					...state.projectAreas,
					[projectId]: projectAreas.filter((projectArea) => projectArea.id !== projectAreaId),
				},
			};
		}),
	applyProjectAreaRemoved: (projectId, projectAreaId) => {
		const state = get();
		const wasActive = state.activeProjectAreaId === projectAreaId;
		const name = state.projectAreas[projectId]?.find(
			(projectArea) => projectArea.id === projectAreaId,
		)?.name;
		set((current) => {
			const removedSessions = new Set(selectProjectAreaSessionIds(current, projectAreaId));
			return {
				removedProjectAreaIds: Object.assign(Object.create(null), current.removedProjectAreaIds, {
					[projectAreaId]: true,
				}) as Record<string, true>,
				fsChangesByProjectArea: omitKey(current.fsChangesByProjectArea, projectAreaId),
				skillChangeTickByProjectArea: omitKey(current.skillChangeTickByProjectArea, projectAreaId),
				sessionCatalogVersionByProjectArea: omitKey(
					current.sessionCatalogVersionByProjectArea,
					projectAreaId,
				),
				diffScopeByProjectArea: omitKey(current.diffScopeByProjectArea, projectAreaId),
				changesRequest:
					current.changesRequest?.projectAreaId === projectAreaId ? null : current.changesRequest,
				chatLocationRequest:
					current.chatLocationRequest?.projectAreaId === projectAreaId
						? null
						: current.chatLocationRequest,
				routeChatTarget:
					current.routeChatTarget?.projectAreaId === projectAreaId ? null : current.routeChatTarget,
				historyOpenRequest:
					current.historyOpenRequest && removedSessions.has(current.historyOpenRequest.sessionId)
						? null
						: current.historyOpenRequest,
			};
		});
		state.removeProjectArea(projectId, projectAreaId);
		state.clearProjectAreaTabs(projectAreaId);
		if (wasActive) {
			state.selectProject(projectId);
			get().pushToast({ variant: "info", message: `ProjectArea "${name ?? "?"}" was removed` });
		}
	},
	selectProject: (selectedProjectId, options) =>
		set((state) => ({
			selectedProjectId,
			activeProjectAreaId: null,
			...(options?.reveal
				? {
						expandedProjectIds: withExpandedProject(state.expandedProjectIds, selectedProjectId),
					}
				: {}),
		})),
	toggleProjectExpanded: (projectId) =>
		set((state) => ({
			expandedProjectIds: state.expandedProjectIds[projectId]
				? omitKey(state.expandedProjectIds, projectId)
				: withExpandedProject(state.expandedProjectIds, projectId),
		})),
	expandProject: (projectId) =>
		set((state) => {
			const expandedProjectIds = withExpandedProject(state.expandedProjectIds, projectId);
			return expandedProjectIds === state.expandedProjectIds ? {} : { expandedProjectIds };
		}),
	hydrateExpandedProjects: (projectIds) =>
		set(() => ({
			expandedProjectIds: Object.fromEntries(projectIds.map((id) => [id, true as const])),
		})),
	selectMain: () =>
		set({ selectedProjectId: null, activeProjectAreaId: null, routeChatTarget: null }),
	activateProjectArea: (projectArea) =>
		set((state) =>
			state.removedProjectAreaIds[projectArea.id]
				? {}
				: { selectedProjectId: projectArea.projectId, activeProjectAreaId: projectArea.id },
		),
	activateProjectAreaFromRoute: (projectArea, sessionId) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectArea.id]) return {};
			const navTick = selectProjectAreaNavTick(state, projectArea.id) + 1;
			return {
				selectedProjectId: projectArea.projectId,
				activeProjectAreaId: projectArea.id,
				navTickByProjectArea: sessionId
					? { ...state.navTickByProjectArea, [projectArea.id]: navTick }
					: state.navTickByProjectArea,
				routeChatTarget: sessionId
					? {
							projectAreaId: projectArea.id,
							sessionId,
							navTick,
							validated: false,
						}
					: null,
				routeChatTargetGeneration: sessionId
					? state.routeChatTargetGeneration + 1
					: state.routeChatTargetGeneration,
			};
		}),
});
