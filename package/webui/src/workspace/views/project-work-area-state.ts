import type { RuntimeStatusReport } from "@pixie/contracts";
import type { AppState, BrowserTab, ChatTab, ContentTab } from "../../store";
import {
	isValidWorkspaceId,
	type PrimarySelection,
	type SecondarySelection,
} from "../store/selection-state";

const EMPTY_TABS: ContentTab[] = [];

export type SplitPreviewTab = Extract<ContentTab, { kind: "file" | "diff" | "browser" }>;

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
		if (
			selection.context.projectId !== undefined &&
			selection.context.projectId !== projectAreaId
		)
			return null;
	}
	return (
		tabs.find((tab): tab is SplitPreviewTab => {
			if (tab.projectAreaId !== projectAreaId) return false;
			if (selection.kind === "file") return tab.kind === "file" && tab.id === selection.resourceId;
			if (selection.kind === "diff") return tab.kind === "diff" && tab.id === selection.resourceId;
			return (
				selection.moduleId === "browser" &&
				tab.kind === "browser" &&
				tab.panelId === selection.resourceId
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
			tabs.find(
				(tab): tab is SplitPreviewTab =>
					(secondarySelection.kind === "file" &&
						tab.kind === "file" &&
						secondarySelection.resourceId === tab.id) ||
					(secondarySelection.kind === "diff" &&
						tab.kind === "diff" &&
						secondarySelection.resourceId === tab.id) ||
					(secondarySelection.kind === "module" &&
						tab.kind === "browser" &&
						secondarySelection.moduleId === "browser" &&
						secondarySelection.resourceId === tab.panelId),
			) ?? null
		);
	}
	if (activeTab?.kind === "file" || activeTab?.kind === "diff" || activeTab?.kind === "browser")
		return activeTab;
	if (previewTabId) {
		const preview = tabs.find(
			(tab): tab is SplitPreviewTab =>
				tab.id === previewTabId &&
				(tab.kind === "file" || tab.kind === "diff" || tab.kind === "browser"),
		);
		if (preview) return preview;
	}
	return (
		tabs.findLast(
			(tab): tab is SplitPreviewTab =>
				tab.kind === "file" || tab.kind === "diff" || tab.kind === "browser",
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
	if (
		selection.projectId !== undefined &&
		!isValidWorkspaceId(selection.projectId)
	)
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

function validScopedTabs(
	tabs: readonly ContentTab[],
	projectAreaId: string,
): ContentTab[] {
	return tabs.filter((tab) => {
		if (tab.projectAreaId !== projectAreaId) return false;
		if (tab.kind === "chat") return isValidWorkspaceId(tab.sessionId) && isValidWorkspaceId(tab.id);
		if (tab.kind === "file")
			return isValidWorkspaceId(tab.id) && isValidWorkspaceId(tab.path);
		if (tab.kind === "diff")
			return (
				isValidWorkspaceId(tab.id) &&
				isValidWorkspaceId(tab.path) &&
				isValidWorkspaceId(tab.repository)
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
	const previews = scoped.filter((tab): tab is SplitPreviewTab =>
		tab.kind === "file" || tab.kind === "diff" || tab.kind === "browser" ? true : false,
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
		(active?.kind === "file" || active?.kind === "diff" || active?.kind === "browser"
			? active
			: null) ??
		(preview?.kind === "file" || preview?.kind === "diff" || preview?.kind === "browser"
			? preview
			: null) ??
		previews.at(-1) ??
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
	}
	return { primarySelection, secondarySelection };
}

export function browserPanelAvailable(report: RuntimeStatusReport | null): boolean {
	return report?.browser?.state === "ready";
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
			if (tab.kind !== "chat") return [];
			const runtime = state.sessions[tab.sessionId];
			return runtime ? [[tab.sessionId, runtime.isStreaming]] : [];
		}),
	);
}
