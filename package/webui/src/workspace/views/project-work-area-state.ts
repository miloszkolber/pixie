import type { RuntimeStatusReport } from "@pixie/contracts";
import type { AppState, BrowserTab, ChatTab, ContentTab } from "../../store";
import { contentTabResourceId, INSTANCE_CONTENT_TAB_AREA_ID } from "../store/model";
import {
	isValidWorkspaceId,
	type PrimarySelection,
	type SecondarySelection,
} from "../store/selection-state";

const EMPTY_TABS: ContentTab[] = [];

export type SplitPreviewTab = Extract<
	ContentTab,
	{ kind: "file" | "diff" | "browser" | "canvas" | "design" }
>;

export type SelectionContentStatus = "none" | "available" | "missing" | "invalid";
/** Resolve the canonical primary selection through the legacy tab cache. */
export function selectPrimaryContentTab(
	tabs: readonly ContentTab[],
	selection: PrimarySelection | undefined,
	projectAreaId: string,
): ChatTab | null {
	if (selection?.kind !== "session") return null;
	if (selection.projectId !== undefined && selection.projectId !== projectAreaId) return null;
	return (
		tabs.find(
			(tab): tab is ChatTab =>
				tab.kind === "chat" &&
				tab.projectAreaId === projectAreaId &&
				tab.sessionId === selection.sessionId,
		) ?? null
	);
}

/** Resolve the canonical secondary selection through the legacy tab cache. */
export function selectSecondaryContentTab(
	tabs: readonly ContentTab[],
	selection: SecondarySelection | undefined,
	projectAreaId: string,
): SplitPreviewTab | null {
	if (!selection) return null;
	if (selection.kind === "file" || selection.kind === "diff") {
		if (selection.projectId !== projectAreaId) return null;
	} else if (selection.context.scope === "project") {
		if (selection.context.projectId !== projectAreaId) return null;
	} else if (selection.context.scope === "session") {
		if (selection.context.projectId !== undefined && selection.context.projectId !== projectAreaId)
			return null;
	}
	return (
		tabs.find((tab): tab is SplitPreviewTab => {
			if (tab.kind !== "design" && tab.projectAreaId !== projectAreaId) return false;
			if (selection.kind === "file") return tab.kind === "file" && tab.id === selection.resourceId;
			if (selection.kind === "diff") return tab.kind === "diff" && tab.id === selection.resourceId;
			if (selection.moduleId === "browser") {
				return tab.kind === "browser" && tab.panelId === selection.resourceId;
			}
			if (selection.moduleId === "canvas") {
				return (
					tab.kind === "canvas" &&
					selection.context.scope === "session" &&
					tab.sessionId === selection.context.sessionId &&
					contentTabResourceId(tab) === selection.resourceId
				);
			}
			return (
				selection.moduleId === "design" &&
				tab.kind === "design" &&
				selection.context.scope === "instance" &&
				contentTabResourceId(tab) === selection.resourceId
			);
		}) ?? null
	);
}

export function selectSplitChatTab(
	tabs: readonly ContentTab[],
	activeTab: ContentTab | null,
	primarySelection?: PrimarySelection,
	projectAreaId?: string,
): ChatTab | null {
	if (primarySelection?.kind === "session") {
		// Canonical selection owns the primary slot. A valid-but-missing chat
		// resolves to a recovery view (null), never to an unrelated tab.
		if (projectAreaId !== undefined) {
			return selectPrimaryContentTab(tabs, primarySelection, projectAreaId);
		}
		return (
			tabs.find(
				(tab): tab is ChatTab =>
					tab.kind === "chat" && tab.sessionId === primarySelection.sessionId,
			) ?? null
		);
	}
	if (activeTab?.kind === "chat") return activeTab;
	return tabs.findLast((tab): tab is ChatTab => tab.kind === "chat") ?? null;
}

export function selectSplitPreviewTab(
	tabs: readonly ContentTab[],
	activeTab: ContentTab | null,
	previewTabId: string | null,
	secondarySelection?: SecondarySelection,
	projectAreaId?: string,
): SplitPreviewTab | null {
	if (secondarySelection) {
		// Canonical selection owns the secondary slot. A valid-but-missing
		// resource resolves to its own unavailable view (null).
		if (projectAreaId !== undefined) {
			return selectSecondaryContentTab(tabs, secondarySelection, projectAreaId);
		}
		return (
			tabs.find((tab): tab is SplitPreviewTab => {
				if (secondarySelection.kind === "file")
					return tab.kind === "file" && secondarySelection.resourceId === tab.id;
				if (secondarySelection.kind === "diff")
					return tab.kind === "diff" && secondarySelection.resourceId === tab.id;
				if (secondarySelection.moduleId === "browser")
					return tab.kind === "browser" && secondarySelection.resourceId === tab.panelId;
				if (secondarySelection.moduleId === "canvas")
					return (
						tab.kind === "canvas" &&
						secondarySelection.context.scope === "session" &&
						tab.sessionId === secondarySelection.context.sessionId &&
						contentTabResourceId(tab) === secondarySelection.resourceId
					);
				return (
					secondarySelection.moduleId === "design" &&
					tab.kind === "design" &&
					secondarySelection.context.scope === "instance" &&
					contentTabResourceId(tab) === secondarySelection.resourceId
				);
			}) ?? null
		);
	}
	if (
		activeTab?.kind === "file" ||
		activeTab?.kind === "diff" ||
		activeTab?.kind === "browser" ||
		activeTab?.kind === "canvas" ||
		activeTab?.kind === "design"
	)
		return activeTab;
	if (previewTabId) {
		const preview = tabs.find(
			(tab): tab is SplitPreviewTab =>
				tab.id === previewTabId &&
				(tab.kind === "file" ||
					tab.kind === "diff" ||
					tab.kind === "browser" ||
					tab.kind === "canvas" ||
					tab.kind === "design"),
		);
		if (preview) return preview;
	}
	return (
		tabs.findLast(
			(tab): tab is SplitPreviewTab =>
				tab.kind === "file" ||
				tab.kind === "diff" ||
				tab.kind === "browser" ||
				tab.kind === "canvas" ||
				tab.kind === "design",
		) ?? null
	);
}

/**
 * Independent Chat+File split resolver. Both slots resolve from canonical
 * selections only; neither falls back to the other side's tab cache, so
 * opening a file never replaces chat and closing it never strands chat.
 */
export function selectSplitPair(
	tabs: readonly ContentTab[],
	projectAreaId: string,
	primarySelection: PrimarySelection | undefined,
	secondarySelection: SecondarySelection | undefined,
): { chatTab: ChatTab | null; previewTab: SplitPreviewTab | null } {
	return {
		chatTab: selectPrimaryContentTab(tabs, primarySelection, projectAreaId),
		previewTab: selectSecondaryContentTab(tabs, secondarySelection, projectAreaId),
	};
}

export function resolvePrimaryContentStatus(
	tabs: readonly ContentTab[],
	selection: PrimarySelection | undefined,
	projectAreaId: string,
): SelectionContentStatus {
	if (!selection) return "none";
	if (selection.kind === "schedule") {
		return isValidWorkspaceId(selection.scheduleId) && isValidWorkspaceId(selection.projectId)
			? "available"
			: "invalid";
	}
	if (selection.kind === "settings") {
		return isValidWorkspaceId(selection.sectionId) ? "available" : "invalid";
	}
	if (!isValidWorkspaceId(selection.sessionId)) return "invalid";
	if (selection.projectId !== undefined && !isValidWorkspaceId(selection.projectId))
		return "invalid";
	return selectPrimaryContentTab(tabs, selection, projectAreaId) ? "available" : "missing";
}

export function resolveSecondaryContentStatus(
	tabs: readonly ContentTab[],
	selection: SecondarySelection | undefined,
	projectAreaId: string,
): SelectionContentStatus {
	if (!selection) return "none";
	if (selection.kind === "file") {
		if (!isValidWorkspaceId(selection.projectId) || !isValidWorkspaceId(selection.resourceId))
			return "invalid";
		return selectSecondaryContentTab(tabs, selection, projectAreaId) ? "available" : "missing";
	}
	if (selection.kind === "diff") {
		if (
			!isValidWorkspaceId(selection.projectId) ||
			!isValidWorkspaceId(selection.resourceId) ||
			!isValidWorkspaceId(selection.reviewId)
		)
			return "invalid";
		return selectSecondaryContentTab(tabs, selection, projectAreaId) ? "available" : "missing";
	}
	if (!isValidWorkspaceId(selection.moduleId) || !isValidWorkspaceId(selection.resourceId))
		return "invalid";
	const context = selection.context;
	if (context.scope === "instance") {
		return isValidWorkspaceId(context.instanceId)
			? selectSecondaryContentTab(tabs, selection, projectAreaId)
				? "available"
				: "missing"
			: "invalid";
	}
	if (context.scope === "project") {
		return isValidWorkspaceId(context.projectId)
			? selectSecondaryContentTab(tabs, selection, projectAreaId)
				? "available"
				: "missing"
			: "invalid";
	}
	if (
		!isValidWorkspaceId(context.sessionId) ||
		(context.projectId !== undefined && !isValidWorkspaceId(context.projectId))
	)
		return "invalid";
	return selectSecondaryContentTab(tabs, selection, projectAreaId) ? "available" : "missing";
}

/** Stale async completions must not activate old resources. */
export function isWorkspaceSelectionStale(
	currentGeneration: number,
	capturedGeneration: number,
): boolean {
	return currentGeneration !== capturedGeneration;
}

function validScopedTabs(tabs: readonly ContentTab[], projectAreaId: string): ContentTab[] {
	return tabs.filter((tab) => {
		if (tab.kind === "design") {
			return isValidWorkspaceId(tab.id) && isValidWorkspaceId(contentTabResourceId(tab));
		}
		if (tab.projectAreaId !== projectAreaId) return false;
		if (tab.kind === "chat") return isValidWorkspaceId(tab.sessionId) && isValidWorkspaceId(tab.id);
		if (tab.kind === "file") return isValidWorkspaceId(tab.id) && isValidWorkspaceId(tab.path);
		if (tab.kind === "diff")
			return (
				isValidWorkspaceId(tab.id) &&
				isValidWorkspaceId(tab.path) &&
				isValidWorkspaceId(tab.repository)
			);
		if (tab.kind === "canvas")
			return (
				isValidWorkspaceId(tab.id) &&
				isValidWorkspaceId(tab.sessionId) &&
				isValidWorkspaceId(contentTabResourceId(tab))
			);
		return isValidWorkspaceId(tab.id) && isValidWorkspaceId(tab.panelId);
	});
}

/**
 * Old-tab upgrade recovery (X14/UI-07 slice). Translates the legacy mixed tab
 * cache into independent primary/secondary selections, ignoring invalid or
 * cross-project tabs. Callers preserve drafts/runtimes separately.
 */
export function migrateLegacyTabsToSelections(
	tabs: readonly ContentTab[],
	projectAreaId: string,
	activeTabId?: string | null,
	previewTabId?: string | null,
): { primarySelection: PrimarySelection; secondarySelection: SecondarySelection } {
	const scoped = validScopedTabs(tabs, projectAreaId);
	const chats = scoped.filter((tab): tab is ChatTab => tab.kind === "chat");
	const previews = scoped.filter(
		(tab): tab is SplitPreviewTab =>
			tab.kind === "file" ||
			tab.kind === "diff" ||
			tab.kind === "browser" ||
			tab.kind === "canvas" ||
			tab.kind === "design",
	);
	const active = scoped.find((tab) => tab.id === activeTabId) ?? null;
	const preview = scoped.find((tab) => tab.id === previewTabId) ?? null;
	const projectId = isValidWorkspaceId(projectAreaId) ? projectAreaId : undefined;

	let primarySelection: PrimarySelection = null;
	const activeChat = active?.kind === "chat" ? active : null;
	const primaryChat = activeChat ?? chats.at(-1) ?? null;
	if (primaryChat) {
		primarySelection = {
			kind: "session",
			sessionId: primaryChat.sessionId,
			...(projectId === undefined ? {} : { projectId }),
		};
	}

	let secondarySelection: SecondarySelection = null;
	const candidate =
		(active?.kind === "file" ||
		active?.kind === "diff" ||
		active?.kind === "browser" ||
		active?.kind === "canvas" ||
		active?.kind === "design"
			? active
			: null) ??
		(preview?.kind === "file" ||
		preview?.kind === "diff" ||
		preview?.kind === "browser" ||
		preview?.kind === "canvas" ||
		preview?.kind === "design"
			? preview
			: null) ??
		// Instance-wide Design must not become the default preview while
		// migrating a project area's legacy tabs.
		previews.findLast((tab) => tab.kind !== "design") ??
		null;
	if (candidate?.kind === "file" && projectId !== undefined) {
		secondarySelection = { kind: "file", projectId, resourceId: candidate.id };
	} else if (candidate?.kind === "diff" && projectId !== undefined) {
		const reviewId =
			candidate.targetComparison && isValidWorkspaceId(candidate.targetComparison)
				? candidate.targetComparison
				: candidate.id;
		secondarySelection = { kind: "diff", projectId, resourceId: candidate.id, reviewId };
	} else if (candidate?.kind === "browser" && projectId !== undefined) {
		secondarySelection = {
			kind: "module",
			moduleId: "browser",
			resourceId: candidate.panelId,
			context: { scope: "project", projectId },
		};
	} else if (candidate?.kind === "canvas") {
		secondarySelection = {
			kind: "module",
			moduleId: "canvas",
			resourceId: contentTabResourceId(candidate),
			context: {
				scope: "session",
				sessionId: candidate.sessionId,
				...(projectId === undefined ? {} : { projectId }),
			},
		};
	} else if (candidate?.kind === "design") {
		secondarySelection = {
			kind: "module",
			moduleId: "design",
			resourceId: contentTabResourceId(candidate),
			context: { scope: "instance", instanceId: INSTANCE_CONTENT_TAB_AREA_ID },
		};
	}
	return { primarySelection, secondarySelection };
}

export function browserPanelAvailable(report: RuntimeStatusReport | null): boolean {
	return report?.browser?.state === "ready";
}

export function canvasModuleAvailable(report: RuntimeStatusReport | null): boolean {
	return report?.canvas?.state === "ready";
}

export function designModuleAvailable(report: RuntimeStatusReport | null): boolean {
	return report?.design?.state === "ready";
}

export function claimBrowserRestart(inFlight: Set<string>, tabId: string): boolean {
	if (inFlight.has(tabId)) return false;
	inFlight.add(tabId);
	return true;
}

export function browserRestartTargetOpen(
	tabs: readonly ContentTab[] | undefined,
	target: BrowserTab,
): boolean {
	return (
		tabs?.some(
			(tab) => tab.kind === "browser" && tab.id === target.id && tab.panelId === target.panelId,
		) === true
	);
}

export function selectTabSessionStreaming(
	state: AppState,
	projectAreaId: string,
): Record<string, boolean> {
	return Object.fromEntries(
		(state.tabsByProjectArea[projectAreaId] ?? EMPTY_TABS).flatMap((tab): [string, boolean][] => {
			if (tab.kind !== "chat" && tab.kind !== "canvas") return [];
			const runtime = state.sessions[tab.sessionId];
			return runtime ? [[tab.sessionId, runtime.isStreaming]] : [];
		}),
	);
}
