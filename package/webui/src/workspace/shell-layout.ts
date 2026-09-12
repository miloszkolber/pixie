import { getTransport } from "../connection";
import { STORAGE_PREFIX } from "../constants/branding";
import { appStoreApi } from "../store";
import { SPLIT_DEFAULT } from "./split-range";
import {
	normalizeWorkspaceLayout,
	setLayout as setWorkspaceLayout,
	type WorkspaceLayout,
} from "./store/selection-state";
import type { ShellLayoutSnapshot } from "./store/shell-layout-state";
import { SHELL_LAYOUT_DEFAULTS } from "./store/shell-layout-state";

const WORKSPACE_LAYOUT_VERSION = 1;

function storageKey(suffix = "workspace-layout"): string {
	return `${STORAGE_PREFIX}${suffix}:${getTransport().httpBase()}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null;
}

function workspaceLayoutFromRecord(value: Record<string, unknown>): Partial<WorkspaceLayout> {
	const layout: Partial<WorkspaceLayout> = {};
	if (typeof value.leftCollapsed === "boolean") layout.leftCollapsed = value.leftCollapsed;
	if (typeof value.rightCollapsed === "boolean") layout.rightCollapsed = value.rightCollapsed;
	if (value.focus === "none" || value.focus === "primary" || value.focus === "secondary")
		layout.focus = value.focus;
	if (typeof value.leftWidth === "number") layout.leftWidth = value.leftWidth;
	if (typeof value.rightWidth === "number") layout.rightWidth = value.rightWidth;
	if (typeof value.primaryFraction === "number") layout.primaryFraction = value.primaryFraction;
	return layout;
}

function legacyLayoutFromRecord(value: Record<string, unknown>): Partial<WorkspaceLayout> {
	const layout: Partial<WorkspaceLayout> = {};
	if (typeof value.shellLeftOpen === "boolean") layout.leftCollapsed = !value.shellLeftOpen;
	if (typeof value.shellRightOpen === "boolean") layout.rightCollapsed = !value.shellRightOpen;
	if (typeof value.shellSplitPercent === "number")
		layout.primaryFraction = value.shellSplitPercent / 100;
	return layout;
}

export function readPersistedShellLayout(): Partial<WorkspaceLayout> {
	try {
		for (const key of [storageKey(), storageKey("shell-layout")]) {
			const raw = localStorage.getItem(key);
			if (!raw) continue;
			const parsed = JSON.parse(raw) as unknown;
			if (!isRecord(parsed)) continue;
			if (parsed.version === WORKSPACE_LAYOUT_VERSION && isRecord(parsed.layout))
				return workspaceLayoutFromRecord(parsed.layout);
			const legacy = legacyLayoutFromRecord(parsed);
			if (Object.keys(legacy).length > 0) return legacy;
		}
		return {};
	} catch {
		return {};
	}
}

function persistWorkspaceLayout(snapshot: WorkspaceLayout): void {
	try {
		localStorage.setItem(
			storageKey(),
			JSON.stringify({ version: WORKSPACE_LAYOUT_VERSION, layout: snapshot }),
		);
	} catch {}
}

/** Compatibility projection for callers that still understand the old shell fields. */
export function selectShellLayoutSnapshot(state: ShellLayoutSnapshot): ShellLayoutSnapshot {
	return {
		shellLeftOpen: state.shellLeftOpen ?? SHELL_LAYOUT_DEFAULTS.shellLeftOpen,
		shellRightOpen: state.shellRightOpen ?? SHELL_LAYOUT_DEFAULTS.shellRightOpen,
		shellRightView: state.shellRightView ?? SHELL_LAYOUT_DEFAULTS.shellRightView,
		shellSplit: state.shellSplit ?? SHELL_LAYOUT_DEFAULTS.shellSplit,
		shellSplitPercent: state.shellSplitPercent ?? SPLIT_DEFAULT,
	};
}

export function selectWorkspaceLayoutSnapshot(state: {
	workspaceSelection: { layout: WorkspaceLayout };
}): WorkspaceLayout {
	return normalizeWorkspaceLayout(state.workspaceSelection.layout);
}

export function initShellLayoutPersistence(): () => void {
	appStoreApi.getState().dispatchWorkspaceSelection(setWorkspaceLayout(readPersistedShellLayout()));
	let previous = selectWorkspaceLayoutSnapshot(appStoreApi.getState());
	return appStoreApi.subscribe((state) => {
		const next = selectWorkspaceLayoutSnapshot(state);
		if (
			next.leftCollapsed === previous.leftCollapsed &&
			next.rightCollapsed === previous.rightCollapsed &&
			next.focus === previous.focus &&
			next.leftWidth === previous.leftWidth &&
			next.rightWidth === previous.rightWidth &&
			next.primaryFraction === previous.primaryFraction
		) {
			return;
		}
		previous = next;
		persistWorkspaceLayout(next);
	});
}
