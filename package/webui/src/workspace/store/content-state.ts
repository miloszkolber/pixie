import type { GitDiffFile, GitDiffScope, ProjectFsChangedPayload } from "@pixie/contracts";
import type { AppState } from "@/store/app-store";
import type { StateCreator } from "@/store/external-store";
import { omitKey } from "@/store/record";
import {
	availableContentTabId,
	type BrowserPanelViewState,
	type ContentOpenOptions,
	type ContentTab,
	contentSessionId,
	type DiffTab,
	type ProjectAreaActivity,
	type TabIntent,
} from "./model";
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
	browserPanelStateById: Record<string, BrowserPanelViewState>;
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
	setBrowserPanelState: (panelId: string, patch: Partial<BrowserPanelViewState>) => void;
	beginBrowserPanelRequest: (panelId: string) => number;
	completeBrowserPanelRequest: (
		panelId: string,
		generation: number,
		patch: Partial<BrowserPanelViewState>,
	) => boolean;
	removeBrowserPanelState: (panelId: string) => void;
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
	browserPanelStateById: {},
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
			const currentProjectAreaId = projectAreaId ?? state.activeProjectAreaId;
			if (!currentProjectAreaId || state.removedProjectAreaIds[currentProjectAreaId]) return {};
			const currentTabs = state.tabsByProjectArea[currentProjectAreaId] ?? [];
			const closedBrowser = currentTabs.find((tab) => tab.id === id && tab.kind === "browser");
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
				...(state.previewTabByProjectArea[currentProjectAreaId] === id
					? {
							previewTabByProjectArea: omitKey(state.previewTabByProjectArea, currentProjectAreaId),
						}
					: {}),
				...(closedBrowser?.kind === "browser"
					? { browserPanelStateById: omitKey(state.browserPanelStateById, closedBrowser.panelId) }
					: {}),
			};
		}),
	setActiveTab: (id, intent) =>
		set((state) => {
			const projectAreaId = state.activeProjectAreaId;
			if (!projectAreaId) return {};
			return {
				activeTabByProjectArea: { ...state.activeTabByProjectArea, [projectAreaId]: id },
				navTickByProjectArea: bumpProjectAreaNavigation(state, projectAreaId),
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
				: { navTickByProjectArea: bumpProjectAreaNavigation(state, projectAreaId) },
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
			const browserPanelStateById = { ...state.browserPanelStateById };
			for (const tab of state.tabsByProjectArea[projectAreaId] ?? []) {
				if (tab.kind === "browser") delete browserPanelStateById[tab.panelId];
			}
			const sessions = { ...state.sessions };
			const skillsSyncedTickBySession = { ...state.skillsSyncedTickBySession };
			for (const sessionId of selectProjectAreaSessionIds(state, projectAreaId)) {
				delete sessions[sessionId];
				delete skillsSyncedTickBySession[sessionId];
			}
			return {
				tabsByProjectArea: omitKey(state.tabsByProjectArea, projectAreaId),
				activeTabByProjectArea: omitKey(state.activeTabByProjectArea, projectAreaId),
				previewTabByProjectArea: omitKey(state.previewTabByProjectArea, projectAreaId),
				navTickByProjectArea: omitKey(state.navTickByProjectArea, projectAreaId),
				closedChatsByProjectArea: omitKey(state.closedChatsByProjectArea, projectAreaId),
				sessionCatalogVersionByProjectArea: omitKey(
					state.sessionCatalogVersionByProjectArea,
					projectAreaId,
				),
				activeActivityByProjectArea: omitKey(state.activeActivityByProjectArea, projectAreaId),
				browserPanelStateById,
				sessions,
				skillsSyncedTickBySession,
			};
		}),
	setActiveActivity: (projectAreaId, activity) =>
		set((state) =>
			state.removedProjectAreaIds[projectAreaId]
				? {}
				: {
						activeActivityByProjectArea: {
							...state.activeActivityByProjectArea,
							[projectAreaId]: activity,
						},
					},
		),
	requestToolView: (projectAreaId, tool) =>
		set((state) =>
			state.removedProjectAreaIds[projectAreaId]
				? {}
				: {
						activeActivityByProjectArea: {
							...state.activeActivityByProjectArea,
							[projectAreaId]: tool === "changes" ? "changes" : "files",
						},
					},
		),
	requestChangesView: (projectAreaId, path) =>
		set((state) => {
			if (state.removedProjectAreaIds[projectAreaId]) return {};
			return {
				activeActivityByProjectArea: {
					...state.activeActivityByProjectArea,
					[projectAreaId]: "changes",
				},
				changesRequest: {
					projectAreaId,
					path,
					navTick: selectProjectAreaNavTick(state, projectAreaId) + 1,
				},
			};
		}),
	clearChangesRequest: () => set({ changesRequest: null }),
	setBrowserPanelState: (panelId, patch) =>
		set((state) => {
			const current = state.browserPanelStateById[panelId] ?? {
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
			return {
				browserPanelStateById: {
					...state.browserPanelStateById,
					[panelId]: { ...current, ...patch },
				},
			};
		}),
	beginBrowserPanelRequest: (panelId) => {
		let generation = 0;
		set((state) => {
			const current = state.browserPanelStateById[panelId] ?? {
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
			generation = current.requestGeneration + 1;
			return {
				browserPanelStateById: {
					...state.browserPanelStateById,
					[panelId]: { ...current, requestGeneration: generation, loading: true, error: null },
				},
			};
		});
		return generation;
	},
	completeBrowserPanelRequest: (panelId, generation, patch) => {
		let completed = false;
		set((state) => {
			const current = state.browserPanelStateById[panelId];
			if (!current || current.requestGeneration !== generation) return {};
			completed = true;
			return {
				browserPanelStateById: {
					...state.browserPanelStateById,
					[panelId]: { ...current, ...patch, loading: false },
				},
			};
		});
		return completed;
	},
	removeBrowserPanelState: (panelId) =>
		set((state) => ({ browserPanelStateById: omitKey(state.browserPanelStateById, panelId) })),
});
