import type { GitDiffFile, GitDiffScope, Project } from "@pixie/contracts";
import { randomId, tupleKey } from "../../lib";

/** Transitional view identity: one UI work area per directory-based project. */
export interface ProjectArea {
	id: string;
	projectId: string;
	name: string;
	root: string;
	kind: "project";
}

export function projectArea(project: Project): ProjectArea {
	const root = project.roots[0] ?? "";
	return {
		id: project.id,
		projectId: project.id,
		name: project.name,
		root,
		kind: "project",
	};
}

export interface FileTab {
	kind: "file";
	id: string;
	projectAreaId: string;
	root: string;
	name: string;
	path: string;
	content: string;
	view?: "rendered" | "source";
	loadedTick?: number;
}
export interface ChatTab {
	kind: "chat";
	id: string;
	projectAreaId: string;
	name: string;
	sessionId: string;
}
export interface DiffTab extends GitDiffFile {
	kind: "diff";
	id: string;
	projectAreaId: string;
	repository: string;
	name: string;
	path: string;
	scope: GitDiffScope;
	loadedTarget: string;
	targetComparison?: string;
	ignoreWhitespace?: boolean;
	loadedTick?: number;
}
export interface BrowserTab {
	kind: "browser";
	id: string;
	projectAreaId: string;
	name: string;
	panelId: string;
}

export interface BrowserPanelViewState {
	address: string;
	snapshot: string;
	screenshot: string | null;
	reference: string;
	fillText: string;
	viewport: { width: number; height: number };
	error: string | null;
	loading: boolean;
	requestGeneration: number;
}

export function newBrowserPanelViewState(): BrowserPanelViewState {
	return {
		address: "",
		snapshot: "",
		screenshot: null,
		reference: "",
		fillText: "",
		viewport: { width: 1280, height: 800 },
		error: null,
		loading: false,
		requestGeneration: 0,
	};
}
export type ContentTab = FileTab | ChatTab | DiffTab | BrowserTab;
export type ProjectAreaActivity = "files" | "changes";

export function chatTabId(projectAreaId: string, sessionId: string): string {
	return tupleKey("chat", projectAreaId, sessionId);
}

function contentResourceIdentity(tab: ContentTab): string {
	if (tab.kind === "file")
		return tupleKey("content-resource", tab.projectAreaId, "file", tab.root, tab.path);
	if (tab.kind === "diff") {
		const reference =
			tab.scope.kind === "commit"
				? tab.scope.sha
				: tab.scope.kind === "pinned" || tab.scope.kind === "branch"
					? tab.scope.baseRef
					: "";
		return tupleKey(
			"content-resource",
			tab.projectAreaId,
			"diff",
			tab.repository,
			tab.path,
			tab.scope.kind,
			reference,
		);
	}
	return tab.kind === "chat"
		? tupleKey("content-resource", tab.projectAreaId, "chat", tab.sessionId)
		: tupleKey("content-resource", tab.projectAreaId, "browser", tab.panelId);
}

export function contentSessionId(tab: ContentTab): string | null {
	return tab.kind === "chat" ? tab.sessionId : null;
}

export function availableContentTabId(tabs: readonly ContentTab[], tab: ContentTab): string {
	const identity = contentResourceIdentity(tab);
	const existing = tabs.find((candidate) => contentResourceIdentity(candidate) === identity);
	if (existing) return existing.id;
	if (!tabs.some((candidate) => candidate.id === tab.id)) return tab.id;
	let fallback = randomId("content-cache");
	while (tabs.some((candidate) => candidate.id === fallback)) fallback = randomId("content-cache");
	return fallback;
}

export type TabIntent = "preview" | "keep";

export interface RouteChatTarget {
	projectAreaId: string;
	sessionId: string;
	navTick: number;
	validated: boolean;
}

export interface ContentOpenOptions {
	activate?: boolean;
	claimPreview?: boolean;
}

export interface ClosedChat {
	sessionId: string;
	title: string;
	closedAt: number;
}

export interface ChatLocationRequest {
	projectAreaId: string;
	projectId: string;
	sessionId: string;
	messageIndex: number;
	anchorText: string;
}
