import type { GitDiffScope, Project } from "@pixie/contracts";
import { isAbsolutePath, normalizePath } from "../../lib";
import type { ClosedChat, ContentTab, ProjectArea, RouteChatTarget } from "./model";

interface ActiveProjectAreaState {
	activeProjectAreaId: string | null;
	projectAreas: Record<string, ProjectArea[]>;
}

interface ProjectContextState extends ActiveProjectAreaState {
	selectedProjectId: string | null;
	projects: Project[];
}

export function selectActiveProjectArea(state: ActiveProjectAreaState): ProjectArea | null {
	return state.activeProjectAreaId ? selectProjectAreaById(state, state.activeProjectAreaId) : null;
}

export function selectProjectAreaById(
	state: ActiveProjectAreaState,
	projectAreaId: string,
): ProjectArea | null {
	for (const projectAreas of Object.values(state.projectAreas)) {
		const projectArea = projectAreas.find((candidate) => candidate.id === projectAreaId);
		if (projectArea) return projectArea;
	}
	return null;
}

export function selectActiveProjectAreaProjectId(state: ActiveProjectAreaState): string | null {
	return selectActiveProjectArea(state)?.projectId ?? null;
}

export function selectContextProject(state: ProjectContextState): Project | null {
	const projectId = selectActiveProjectArea(state)?.projectId ?? state.selectedProjectId;
	return state.projects.find((project) => project.id === projectId) ?? null;
}

export interface HistoryTarget {
	projectAreaId: string;
	tabId: string;
	sessionId: string;
}

export function selectActiveContentTab(
	state: {
		tabsByProjectArea: Record<string, ContentTab[]>;
		activeTabByProjectArea: Record<string, string | null>;
	},
	projectAreaId: string,
): ContentTab | null {
	const activeId = state.activeTabByProjectArea[projectAreaId];
	return (state.tabsByProjectArea[projectAreaId] ?? []).find((tab) => tab.id === activeId) ?? null;
}

export function selectHistoryTarget(state: {
	activeProjectAreaId: string | null;
	tabsByProjectArea: Record<string, ContentTab[]>;
	activeTabByProjectArea: Record<string, string | null>;
}): HistoryTarget | null {
	const projectAreaId = state.activeProjectAreaId;
	if (!projectAreaId) return null;
	const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
	const active = selectActiveContentTab(state, projectAreaId);
	const chat = active?.kind === "chat" ? active : tabs.findLast((t) => t.kind === "chat");
	return chat ? { projectAreaId, tabId: chat.id, sessionId: chat.sessionId } : null;
}

export interface KnownChatLocation {
	projectAreaId: string;
	title: string;
}

export function selectKnownChatLocation(
	state: {
		tabsByProjectArea: Record<string, ContentTab[]>;
		closedChatsByProjectArea: Record<string, ClosedChat[]>;
	},
	sessionId: string,
): KnownChatLocation | null {
	for (const [projectAreaId, tabs] of Object.entries(state.tabsByProjectArea)) {
		const tab = tabs.find(
			(candidate) => candidate.kind === "chat" && candidate.sessionId === sessionId,
		);
		if (tab?.kind === "chat") return { projectAreaId, title: tab.name };
	}
	for (const [projectAreaId, chats] of Object.entries(state.closedChatsByProjectArea)) {
		const chat = chats.find((candidate) => candidate.sessionId === sessionId);
		if (chat) return { projectAreaId, title: chat.title };
	}
	return null;
}

export function selectProjectAreaSessionIds(
	state: {
		tabsByProjectArea: Record<string, ContentTab[]>;
		closedChatsByProjectArea: Record<string, ClosedChat[]>;
	},
	projectAreaId: string,
): string[] {
	const sessionIds = new Set(
		(state.tabsByProjectArea[projectAreaId] ?? []).flatMap((tab) =>
			tab.kind === "chat" ? [tab.sessionId] : [],
		),
	);
	for (const chat of state.closedChatsByProjectArea[projectAreaId] ?? []) {
		sessionIds.add(chat.sessionId);
	}
	return [...sessionIds];
}

export const UNCOMMITTED_SCOPE: GitDiffScope = { kind: "uncommitted" };

export function selectDiffScope(
	state: { diffScopeByProjectArea: Record<string, GitDiffScope> },
	projectAreaId: string,
): GitDiffScope {
	return state.diffScopeByProjectArea[projectAreaId] ?? UNCOMMITTED_SCOPE;
}

export function selectDiffTabTargetRef(
	state: ActiveProjectAreaState,
	tab: { projectAreaId: string; scope: GitDiffScope; targetComparison?: string },
): string {
	return tab.scope.kind === "branch" && selectProjectAreaById(state, tab.projectAreaId)
		? (tab.targetComparison ?? "")
		: "";
}

export function matchesChangePath(reported: string, rel: string): boolean {
	const path = normalizePath(reported);
	if (path === rel) return true;
	return isAbsolutePath(path) && path.endsWith(`/${rel}`);
}

export function selectChatTitle(
	state: { tabsByProjectArea: Record<string, ContentTab[]> },
	projectAreaId: string,
	sessionId: string,
): string {
	const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
	const chatTab = tabs.find((t) => t.kind === "chat" && t.sessionId === sessionId);
	return (chatTab?.name ?? "Chat").trim() || "Chat";
}

export function selectProjectAreaTick(
	state: { fsChangesByProjectArea: Record<string, { tick: number }> },
	projectAreaId: string,
): number {
	return state.fsChangesByProjectArea[projectAreaId]?.tick ?? 0;
}

export function selectCurrentRouteChatTarget(state: {
	routeChatTarget: RouteChatTarget | null;
	activeProjectAreaId: string | null;
	navTickByProjectArea: Record<string, number>;
}): RouteChatTarget | null {
	const target = state.routeChatTarget;
	if (!target || state.activeProjectAreaId !== target.projectAreaId) return null;
	return selectProjectAreaNavTick(state, target.projectAreaId) === target.navTick ? target : null;
}

export function selectProjectAreaNavTick(
	state: { navTickByProjectArea: Record<string, number> },
	projectAreaId: string,
): number {
	return state.navTickByProjectArea[projectAreaId] ?? 0;
}

interface SkillsStaleState {
	skillChangeTickByProjectArea: Record<string, number>;
	skillsSyncedTickBySession: Record<string, number>;
}

export function selectSkillsStale(
	state: SkillsStaleState,
	projectAreaId: string,
	sessionId: string,
): boolean {
	return (
		(state.skillChangeTickByProjectArea[projectAreaId] ?? 0) >
		(state.skillsSyncedTickBySession[sessionId] ?? 0)
	);
}

export function selectLastOpenChatSession(
	state: {
		tabsByProjectArea: Record<string, { kind: string; id: string; sessionId?: string }[]>;
		activeTabByProjectArea: Record<string, string | null>;
	},
	projectAreaId: string,
): string | null {
	const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
	const activeId = state.activeTabByProjectArea[projectAreaId];
	const active = tabs.find((t) => t.id === activeId);
	if (active?.kind === "chat" && active.sessionId) return active.sessionId;
	for (let i = tabs.length - 1; i >= 0; i--) {
		const tab = tabs[i];
		if (tab?.kind === "chat" && tab.sessionId) return tab.sessionId;
	}
	return null;
}
