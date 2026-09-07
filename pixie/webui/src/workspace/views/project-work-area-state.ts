import type { RuntimeStatusReport } from "@pixie/contracts";
import type { AppState, BrowserTab, ContentTab } from "../../store";

const EMPTY_TABS: ContentTab[] = [];

export function browserPanelAvailable(report: RuntimeStatusReport | null): boolean {
	return report?.browser.state === "ready";
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
