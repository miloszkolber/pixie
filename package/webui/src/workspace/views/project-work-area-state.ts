import type { RuntimeStatusReport } from "@pixie/contracts";
import type { AppState, BrowserTab, ChatTab, ContentTab } from "../../store";
import type { PrimarySelection, SecondarySelection } from "../store/selection-state";

const EMPTY_TABS: ContentTab[] = [];

export type SplitPreviewTab = Extract<ContentTab, { kind: "file" | "diff" | "browser" }>;

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
): ChatTab | null {
	if (primarySelection?.kind === "session") {
		const selected =
			selectPrimaryContentTab(tabs, primarySelection, "") ??
			tabs.find(
				(tab): tab is ChatTab =>
					tab.kind === "chat" && tab.sessionId === primarySelection.sessionId,
			);
		if (selected) return selected;
	}
	if (activeTab?.kind === "chat") return activeTab;
	return tabs.findLast((tab): tab is ChatTab => tab.kind === "chat") ?? null;
}

export function selectSplitPreviewTab(
	tabs: readonly ContentTab[],
	activeTab: ContentTab | null,
	previewTabId: string | null,
	secondarySelection?: SecondarySelection,
): SplitPreviewTab | null {
	if (secondarySelection) {
		const selected =
			selectSecondaryContentTab(tabs, secondarySelection, "") ??
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
			);
		if (selected) return selected;
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
