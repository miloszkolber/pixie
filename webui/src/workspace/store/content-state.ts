import type { GitDiffFile, GitDiffScope, ProjectFsChangedPayload } from "@pixie/shared";
import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { omitKey } from "@/store/record";
import {
	availableContentTabId,
	type ContentOpenOptions,
	type ContentTab,
	contentSessionId,
	contentTabResourceId,
	type DiffTab,
	INSTANCE_CONTENT_TAB_AREA_ID,
	type ProjectAreaActivity,
	type TabIntent,
} from "./model";
import {
	bumpWorkspaceNavigationGeneration,
	clearPrimary,
	clearSecondary,
	selectPrimary,
	selectSecondary,
	selectSecondaryArea,
	workspaceReducer,
} from "./selection-state";
import { selectProjectAreaNavTick, selectProjectAreaSessionIds } from "./selectors";

export interface ContentWorkspaceState {
	tabsByProjectArea: Record<string, ContentTab[]>;
	activeTabByProjectArea: Record<string, string | null>;
	previewTabByProjectArea: Record<string, string>;
	navTickByProjectArea: Record<string, number>;
	activeActivityByProjectArea: Record<string, ProjectAreaActivity>;
	changesRequest: {
		projectAreaId: string;
		path: string;
		navTick: number;
	} | null;
	changesView: "list" | "tree";
	diffScopeByProjectArea: Record<string, GitDiffScope>;
	fsChangesByProjectArea: Record<
		string,
		{ tick: number; changes: ProjectFsChangedPayload["changes"]; truncated: boolean }
	>;
	skillChangeTickByProjectArea: Record<string, number>;
	openTab: (tab: ContentTab, intent: TabIntent, options?: ContentOpenOptions) => void;
	closeTab: (id: string, countNavigation?: boolean, projectAreaId?: string) => void;
	setActiveTab: (id: string, intent?: TabIntent) => void;
	noteNavigation: (projectAreaId: string) => void;
	setFileTabView: (id: string, view: "rendered" | "source") => void;
	setDiffTabIgnoreWhitespace: (id: string, ignoreWhitespace: boolean) => void;
	setChangesView: (view: "list" | "tree") => void;
	setDiffScope: (projectAreaId: string, scope: GitDiffScope) => void;
	noteDiffComparison: (
		projectAreaId: string,
		repository: string,
		scope: GitDiffScope,
		comparisonId: string,
	) => void;
	noteFsChanged: (payload: ProjectFsChangedPayload) => void;
	updateFileTabContent: (projectAreaId: string, id: string, content: string, tick: number) => void;
	updateDiffTabContent: (
		projectAreaId: string,
		id: string,
		preview: GitDiffFile,
		tick: number,
		loadedTarget: string,
	) => void;
	clearProjectAreaTabs: (projectAreaId: string) => void;
	setActiveActivity: (projectAreaId: string, activity: ProjectAreaActivity) => void;
	requestToolView: (projectAreaId: string, tool: "files" | "changes") => void;
	requestChangesView: (projectAreaId: string, path: string) => void;
	clearChangesRequest: () => void;
}

function isSessionDeleted(
	state: Pick<AppState, "deletedSessionsByProjectArea">,
	projectAreaId: string,
	sessionId: string,
): boolean {
	return state.deletedSessionsByProjectArea[projectAreaId]?.[sessionId] === true;
}

function patchDiffTab(
	state: Pick<AppState, "activeProjectAreaId" | "tabsByProjectArea">,
	id: string,
	patch: Partial<Omit<DiffTab, "kind" | "id">>,
): Partial<AppState> {
	const projectAreaId = state.activeProjectAreaId;
	if (!projectAreaId) return {};
	const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
	if (!tabs.some((tab) => tab.id === id && tab.kind === "diff")) return {};
	return {
		tabsByProjectArea: {
			...state.tabsByProjectArea,
			[projectAreaId]: tabs.map((tab) =>
				tab.id === id && tab.kind === "diff" ? { ...tab, ...patch } : tab,
			),
		},
	};
}

function selectionActionForTab(tab: ContentTab) {
	if (tab.kind === "chat") {
		return selectPrimary(
			{ kind: "session", sessionId: tab.sessionId, projectId: tab.projectAreaId },
			"chats",
		);
	}
	if (tab.kind === "file") {
		return selectSecondary(
			{ kind: "file", projectId: tab.projectAreaId, resourceId: tab.id },
			"files",
		);
	}
	if (tab.kind === "diff") {
		return selectSecondary(
			{
				kind: "diff",
				projectId: tab.projectAreaId,
				resourceId: tab.id,
				reviewId: tab.targetComparison ?? tab.id,
			},
			"git",
		);
	}
	if (tab.kind === "canvas") {
		return selectSecondary(
			{
				kind: "module",
				moduleId: "canvas",
				resourceId: contentTabResourceId(tab),
				context: { scope: "session", sessionId: tab.sessionId, projectId: tab.projectAreaId },
			},
			"module:canvas",
		);
	}
	return selectSecondary(
		{
			kind: "module",
			moduleId: "design",
			resourceId: contentTabResourceId(tab),
			context: { scope: "instance", instanceId: INSTANCE_CONTENT_TAB_AREA_ID },
		},
		"module:design",
	);
}

function selectionMatchesTab(
	selection: ReturnType<typeof workspaceReducer>,
	tab: ContentTab,
): "primary" | "secondary" | null {
	if (tab.kind === "chat") {
		return selection.primarySelection?.kind === "session" &&
			selection.primarySelection.sessionId === tab.sessionId &&
			(selection.primarySelection.projectId === undefined ||
				selection.primarySelection.projectId === tab.projectAreaId)
			? "primary"
			: null;
	}
	if (tab.kind === "file") {
		return selection.secondarySelection?.kind === "file" &&
			selection.secondarySelection.projectId === tab.projectAreaId &&
			selection.secondarySelection.resourceId === tab.id
			? "secondary"
			: null;
	}
	if (tab.kind === "diff") {
		return selection.secondarySelection?.kind === "diff" &&
			selection.secondarySelection.projectId === tab.projectAreaId &&
			selection.secondarySelection.resourceId === tab.id
			? "secondary"
			: null;
	}
	if (tab.kind === "canvas") {
		return selection.secondarySelection?.kind === "module" &&
			selection.secondarySelection.moduleId === "canvas" &&
			selection.secondarySelection.resourceId === contentTabResourceId(tab) &&
			selection.secondarySelection.context.scope === "session" &&
			selection.secondarySelection.context.sessionId === tab.sessionId &&
			(selection.secondarySelection.context.projectId === undefined ||
				selection.secondarySelection.context.projectId === tab.projectAreaId)
			? "secondary"
			: null;
	}
	return selection.secondarySelection?.kind === "module" &&
		selection.secondarySelection.moduleId === "design" &&
		selection.secondarySelection.resourceId === contentTabResourceId(tab) &&
		selection.secondarySelection.context.scope === "instance"
		? "secondary"
		: null;
}

function selectContentTab(state: AppState, tab: ContentTab): Partial<AppState> {
	const workspaceSelection = workspaceReducer(state.workspaceSelection, selectionActionForTab(tab));
	return workspaceSelectionPatch(state, workspaceSelection);
}

function clearContentTabSelection(state: AppState, tab: ContentTab): Partial<AppState> {
	const side = selectionMatchesTab(state.workspaceSelection, tab);
	if (!side) return {};
	const workspaceSelection = workspaceReducer(
		state.workspaceSelection,
		side === "primary" ? clearPrimary() : clearSecondary(),
	);
	return workspaceSelectionPatch(state, workspaceSelection);
}

function clearProjectAreaContentSelection(
	state: AppState,
	projectAreaId: string,
	tabs: readonly ContentTab[],
): Partial<AppState> {
	let workspaceSelection = state.workspaceSelection;
	for (const tab of tabs) {
		// Design is instance-scoped. A project-area teardown must not clear its
		// selection even if an old cache placed the tab in that area's bucket.
		if (tab.kind === "design") continue;
		const side = selectionMatchesTab(workspaceSelection, tab);
		if (!side) continue;
		workspaceSelection = workspaceReducer(
			workspaceSelection,
			side === "primary" ? clearPrimary() : clearSecondary(),
		);
	}
	const secondary = workspaceSelection.secondarySelection;
	const belongsToProject =
		secondary?.kind === "file" || secondary?.kind === "diff"
			? secondary.projectId === projectAreaId
			: secondary?.kind === "module" &&
				(secondary.context.scope === "project"
					? secondary.context.projectId === projectAreaId
					: false);
	if (belongsToProject) workspaceSelection = workspaceReducer(workspaceSelection, clearSecondary());
	return workspaceSelectionPatch(state, workspaceSelection);
}

function secondaryAreaForActivity(activity: ProjectAreaActivity): "files" | "git" {
	return activity === "changes" ? "git" : "files";
}

function navigationPatch(state: Pick<AppState, "workspaceNavigationGeneration">) {
	return { workspaceNavigationGeneration: bumpWorkspaceNavigationGeneration(state) };
}

function workspaceSelectionPatch(
	state: AppState,
	workspaceSelection: ReturnType<typeof workspaceReducer>,
): Partial<AppState> {
	return workspaceSelection === state.workspaceSelection
		? {}
		: { workspaceSelection, ...navigationPatch(state) };
}

export function bumpProjectAreaNavigation(
	state: AppState,
	projectAreaId: string,
): Record<string, number> {
	return {
		...state.navTickByProjectArea,
		[projectAreaId]: selectProjectAreaNavTick(state, projectAreaId) + 1,
	};
}

export const createContentWorkspaceState: StateCreator<AppState, [], [], ContentWorkspaceState> = (
	set,
) => ({
	tabsByProjectArea: {},
	activeTabByProjectArea: {},
	previewTabByProjectArea: {},
	navTickByProjectArea: {},
	activeActivityByProjectArea: {},
	changesRequest: null,
	changesView: "list",
	diffScopeByProjectArea: {},
	fsChangesByProjectArea: {},
	skillChangeTickByProjectArea: {},
	openTab: (tab, intent, options = {}) =>
		set((state) => {
			const projectAreaId = tab.projectAreaId;
			const sessionId = contentSessionId(tab);
			if (
				state.removedProjectAreaIds[projectAreaId] ||
				(sessionId !== null && isSessionDeleted(state, projectAreaId, sessionId))
			) {
				return {};
			}
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const resolvedId = availableContentTabId(tabs, tab);
			const resolvedTab = resolvedId === tab.id ? tab : { ...tab, id: resolvedId };
			const previewCompatible = resolvedTab.kind === "file" || resolvedTab.kind === "diff";
			const effectiveIntent = previewCompatible ? intent : "keep";
			const claimPreview = previewCompatible && options.claimPreview === true;
			const preview = state.previewTabByProjectArea[projectAreaId];
			const selection = options.activate === false ? {} : selectContentTab(state, resolvedTab);
			const navigation = options.activate === false ? {} : navigationPatch(state);
			const activeTabByProjectArea =
				options.activate === false
					? state.activeTabByProjectArea
					: { ...state.activeTabByProjectArea, [projectAreaId]: resolvedTab.id };
			const existingIndex = tabs.findIndex((candidate) => candidate.id === resolvedTab.id);
			if (existingIndex >= 0) {
				const existing = tabs[existingIndex];
				return {
					tabsByProjectArea:
						existing === resolvedTab
							? state.tabsByProjectArea
							: {
									...state.tabsByProjectArea,
									[projectAreaId]: tabs.with(existingIndex, resolvedTab),
								},
					activeTabByProjectArea,
					...selection,
					...navigation,
					previewTabByProjectArea:
						effectiveIntent === "keep" &&
						(preview === resolvedTab.id || (claimPreview && preview !== undefined))
							? omitKey(state.previewTabByProjectArea, projectAreaId)
							: state.previewTabByProjectArea,
				};
			}
			const at =
				(effectiveIntent === "preview" || claimPreview) && preview
					? tabs.findIndex((candidate) => candidate.id === preview)
					: -1;
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[projectAreaId]: at === -1 ? [...tabs, resolvedTab] : tabs.with(at, resolvedTab),
				},
				activeTabByProjectArea,
				...selection,
				...navigation,
				previewTabByProjectArea:
					effectiveIntent === "preview"
						? { ...state.previewTabByProjectArea, [projectAreaId]: resolvedTab.id }
						: claimPreview && preview
							? omitKey(state.previewTabByProjectArea, projectAreaId)
							: state.previewTabByProjectArea,
			};
		}),
	closeTab: (id, countNavigation = true, projectAreaId) =>
		set((state) => {
			const currentProjectAreaId =
				projectAreaId ??
				(state.activeProjectAreaId &&
				(state.tabsByProjectArea[state.activeProjectAreaId] ?? []).some((tab) => tab.id === id)
					? state.activeProjectAreaId
					: Object.entries(state.tabsByProjectArea).find(([, tabs]) =>
							tabs.some((tab) => tab.id === id),
						)?.[0]);
			if (!currentProjectAreaId || state.removedProjectAreaIds[currentProjectAreaId]) return {};
			const currentTabs = state.tabsByProjectArea[currentProjectAreaId] ?? [];
			const closedTab = currentTabs.find((tab) => tab.id === id);
			if (!closedTab) return {};
			const tabs = currentTabs.filter((tab) => tab.id !== id);
			const wasActive = state.activeTabByProjectArea[currentProjectAreaId] === id;
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[currentProjectAreaId]: tabs,
				},
				activeTabByProjectArea: {
					...state.activeTabByProjectArea,
					[currentProjectAreaId]: wasActive
						? (tabs.at(-1)?.id ?? null)
						: (state.activeTabByProjectArea[currentProjectAreaId] ?? null),
				},
				navTickByProjectArea:
					wasActive && countNavigation
						? bumpProjectAreaNavigation(state, currentProjectAreaId)
						: state.navTickByProjectArea,
				...(wasActive && countNavigation ? navigationPatch(state) : {}),
				...(closedTab ? clearContentTabSelection(state, closedTab) : {}),
				...(state.previewTabByProjectArea[currentProjectAreaId] === id
					? {
							previewTabByProjectArea: omitKey(state.previewTabByProjectArea, currentProjectAreaId),
						}
					: {}),
			};
		}),
	setActiveTab: (id, intent) =>
		set((state) => {
			const projectAreaId =
				state.activeProjectAreaId &&
				(state.tabsByProjectArea[state.activeProjectAreaId] ?? []).some((tab) => tab.id === id)
					? state.activeProjectAreaId
					: Object.entries(state.tabsByProjectArea).find(([, tabs]) =>
							tabs.some((tab) => tab.id === id),
						)?.[0];
			if (!projectAreaId) return {};
			const tab = (state.tabsByProjectArea[projectAreaId] ?? []).find(
				(candidate) => candidate.id === id,
			);
			if (!tab) return {};
			return {
				activeTabByProjectArea: { ...state.activeTabByProjectArea, [projectAreaId]: id },
				navTickByProjectArea: bumpProjectAreaNavigation(state, projectAreaId),
				...navigationPatch(state),
				...(tab ? selectContentTab(state, tab) : {}),
				...(intent === "keep" && state.previewTabByProjectArea[projectAreaId] === id
					? {
							previewTabByProjectArea: omitKey(state.previewTabByProjectArea, projectAreaId),
						}
					: {}),
			};
		}),
	noteNavigation: (projectAreaId) =>
		set((state) =>
			state.removedProjectAreaIds[projectAreaId]
				? {}
				: {
						navTickByProjectArea: bumpProjectAreaNavigation(state, projectAreaId),
						...navigationPatch(state),
					},
		),
	setFileTabView: (id, view) =>
		set((state) => {
			const projectAreaId = state.activeProjectAreaId;
			if (!projectAreaId) return {};
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			if (!tabs.some((tab) => tab.id === id && tab.kind === "file")) return {};
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[projectAreaId]: tabs.map((tab) =>
						tab.id === id && tab.kind === "file" ? { ...tab, view } : tab,
					),
				},
			};
		}),
	setDiffTabIgnoreWhitespace: (id, ignoreWhitespace) =>
		set((state) => patchDiffTab(state, id, { ignoreWhitespace })),
	setChangesView: (view) => set({ changesView: view }),
	setDiffScope: (projectAreaId, scope) =>
		set((state) =>
			state.removedProjectAreaIds[projectAreaId]
				? {}
				: {
						diffScopeByProjectArea: {
							...state.diffScopeByProjectArea,
							[projectAreaId]: scope,
						},
					},
		),
	noteDiffComparison: (projectAreaId, repository, scope, comparisonId) =>
		set((state) => {
			if (
				state.removedProjectAreaIds[projectAreaId] ||
				scope.kind !== "branch" ||
				comparisonId === ""
			) {
				return {};
			}
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			if (
				!tabs.some(
					(tab) =>
						tab.kind === "diff" &&
						tab.repository === repository &&
						tab.scope.kind === "branch" &&
						tab.scope.baseRef === scope.baseRef &&
						tab.targetComparison !== comparisonId,
				)
			) {
				return {};
			}
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[projectAreaId]: tabs.map((tab) =>
						tab.kind === "diff" &&
						tab.repository === repository &&
						tab.scope.kind === "branch" &&
						tab.scope.baseRef === scope.baseRef
							? { ...tab, targetComparison: comparisonId }
							: tab,
					),
				},
			};
		}),
	noteFsChanged: (payload) =>
		set((state) => {
			if (state.removedProjectAreaIds[payload.projectId]) return {};
			const previous = state.fsChangesByProjectArea[payload.projectId];
			const tick = (previous?.tick ?? 0) + 1;
			const skillChanged =
				payload.truncated || payload.changes.some(({ path }) => /(^|\/)SKILL\.md$/.test(path));
			return {
				fsChangesByProjectArea: {
					...state.fsChangesByProjectArea,
					[payload.projectId]: {
						tick,
						changes: payload.changes,
						truncated: payload.truncated,
					},
				},
				...(skillChanged
					? {
							skillChangeTickByProjectArea: {
								...state.skillChangeTickByProjectArea,
								[payload.projectId]: tick,
							},
						}
					: {}),
			};
		}),
	updateFileTabContent: (projectAreaId, id, content, tick) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			if (!tabs.some((tab) => tab.id === id && tab.kind === "file")) return {};
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[projectAreaId]: tabs.map((tab) =>
						tab.id === id && tab.kind === "file" ? { ...tab, content, loadedTick: tick } : tab,
					),
				},
			};
		}),
	updateDiffTabContent: (projectAreaId, id, preview, tick, loadedTarget) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			if (!tabs.some((tab) => tab.id === id && tab.kind === "diff")) return {};
			return {
				tabsByProjectArea: {
					...state.tabsByProjectArea,
					[projectAreaId]: tabs.map((tab) => {
						if (tab.id !== id || tab.kind !== "diff") return tab;
						const next: DiffTab = {
							...tab,
							original: preview.original,
							modified: preview.modified,
							loadedTick: tick,
							loadedTarget: preview.comparisonId ?? loadedTarget,
							...(tab.scope.kind === "branch" && preview.comparisonId
								? { targetComparison: preview.comparisonId }
								: {}),
						};
						for (const key of [
							"originalPath",
							"comparisonId",
							"unavailable",
							"binary",
							"tooLarge",
							"message",
						] as const) {
							if (preview[key] === undefined) delete next[key];
							else Object.assign(next, { [key]: preview[key] });
						}
						return next;
					}),
				},
			};
		}),
	clearProjectAreaTabs: (projectAreaId) =>
		set((state) => {
			const tabs = state.tabsByProjectArea[projectAreaId] ?? [];
			const sessions = { ...state.sessions };
			const skillsSyncedTickBySession = { ...state.skillsSyncedTickBySession };
			for (const sessionId of selectProjectAreaSessionIds(state, projectAreaId)) {
				const runtime = (state.sessions as unknown as Record<string, unknown>)[sessionId] as
					| {
							isStreaming?: unknown;
							draft?: unknown;
							submission?: unknown;
							queue?: { steering?: unknown; followUp?: unknown };
							goal?: { status?: unknown };
					  }
					| undefined;
				const queue = runtime?.queue as { steering?: unknown[]; followUp?: unknown[] } | undefined;
				const hasActiveWork =
					runtime !== undefined &&
					(runtime.isStreaming === true ||
						(runtime.submission !== null && runtime.submission !== undefined) ||
						(typeof runtime.draft === "string" && runtime.draft.trim() !== "") ||
						(Array.isArray(queue?.steering) && queue.steering.length > 0) ||
						(Array.isArray(queue?.followUp) && queue.followUp.length > 0) ||
						runtime.goal?.status === "loading" ||
						runtime.goal?.status === "saving");
				const tabbedElsewhere = Object.entries(state.tabsByProjectArea).some(
					([areaId, areaTabs]) =>
						areaId !== projectAreaId &&
						areaTabs.some((candidate) => contentSessionId(candidate) === sessionId),
				);
				if (hasActiveWork || tabbedElsewhere) continue;
				delete sessions[sessionId];
				delete skillsSyncedTickBySession[sessionId];
			}
			const retainedInstanceTabs = tabs
				.filter((tab) => tab.kind === "design")
				.map((tab) => ({ ...tab, projectAreaId: INSTANCE_CONTENT_TAB_AREA_ID }));
			const tabsByProjectArea = omitKey(state.tabsByProjectArea, projectAreaId);
			if (retainedInstanceTabs.length > 0) {
				const existing = tabsByProjectArea[INSTANCE_CONTENT_TAB_AREA_ID] ?? [];
				const existingIds = new Set(existing.map((tab) => tab.id));
				tabsByProjectArea[INSTANCE_CONTENT_TAB_AREA_ID] = [
					...existing,
					...retainedInstanceTabs.filter((tab) => !existingIds.has(tab.id)),
				];
			}
			return {
				tabsByProjectArea,
				activeTabByProjectArea: omitKey(state.activeTabByProjectArea, projectAreaId),
				previewTabByProjectArea: omitKey(state.previewTabByProjectArea, projectAreaId),
				navTickByProjectArea: omitKey(state.navTickByProjectArea, projectAreaId),
				closedChatsByProjectArea: omitKey(state.closedChatsByProjectArea, projectAreaId),
				sessionCatalogVersionByProjectArea: omitKey(
					state.sessionCatalogVersionByProjectArea,
					projectAreaId,
				),
				activeActivityByProjectArea: omitKey(state.activeActivityByProjectArea, projectAreaId),
				...clearProjectAreaContentSelection(state, projectAreaId, tabs),
				sessions,
				skillsSyncedTickBySession,
			};
		}),
	setActiveActivity: (projectAreaId, activity) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				selectSecondaryArea(secondaryAreaForActivity(activity)),
			);
			return {
				activeActivityByProjectArea: {
					...state.activeActivityByProjectArea,
					[projectAreaId]: activity,
				},
				shellRightView: activity,
				...workspaceSelectionPatch(state, workspaceSelection),
			};
		}),
	requestToolView: (projectAreaId, tool) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				selectSecondaryArea(tool === "changes" ? "git" : "files"),
			);
			return {
				activeActivityByProjectArea: {
					...state.activeActivityByProjectArea,
					[projectAreaId]: tool === "changes" ? "changes" : "files",
				},
				shellRightView: tool === "changes" ? "changes" : "files",
				...workspaceSelectionPatch(state, workspaceSelection),
			};
		}),
	requestChangesView: (projectAreaId, path) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			const workspaceSelection = workspaceReducer(
				state.workspaceSelection,
				selectSecondaryArea("git"),
			);
			return {
				activeActivityByProjectArea: {
					...state.activeActivityByProjectArea,
					[projectAreaId]: "changes",
				},
				shellRightView: "changes",
				...workspaceSelectionPatch(state, workspaceSelection),
				changesRequest: {
					projectAreaId,
					path,
					navTick: selectProjectAreaNavTick(state, projectAreaId) + 1,
				},
			};
		}),
	clearChangesRequest: () => set({ changesRequest: null }),
});
