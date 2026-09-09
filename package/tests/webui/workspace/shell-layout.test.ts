import { afterEach, beforeEach, expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { appStoreApi, type ContentTab } from "@/store";
import { shortSessionAge } from "@/workspace/projects/session-age";
import {
	clampSplitPercent,
	SPLIT_LARGE_STEP,
	SPLIT_MAX,
	SPLIT_MIN,
	SPLIT_STEP,
	stepSplitPercent,
} from "@/workspace/split-range";
import {
	selectSplitChatTab,
	selectSplitPreviewTab,
} from "@/workspace/views/project-work-area-state";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

function chatTab(id: string, sessionId: string): ContentTab {
	return { kind: "chat", id, projectAreaId: "area-1", name: `Session ${id}`, sessionId };
}

function fileTab(id: string, path: string): ContentTab {
	return {
		kind: "file",
		id,
		projectAreaId: "area-1",
		root: "/work/a",
		name: path.split("/").pop() ?? path,
		path,
		content: "preview",
	};
}

beforeEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
});

afterEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
});

test("workspace layout defaults keep both sidebars open with an even primary split", () => {
	const state = appStoreApi.getState();
	expect(state.shellLeftOpen).toBeTrue();
	expect(state.shellRightOpen).toBeTrue();
	expect(state.shellRightView).toBe("files");
	expect(state.shellSplit).toBeFalse();
	expect(state.shellSplitPercent).toBe(50);
	expect(state.workspaceSelection.primaryArea).toBe("chats");
	expect(state.workspaceSelection.secondaryArea).toBe("details");
	expect(state.workspaceSelection.primarySelection).toBeNull();
	expect(state.workspaceSelection.secondarySelection).toBeNull();
	expect(state.workspaceSelection.layout).toEqual({
		leftCollapsed: false,
		rightCollapsed: false,
		focus: "none",
		leftWidth: 256,
		rightWidth: 256,
		primaryFraction: 0.5,
	});
});

test("panel collapse, right view, and split toggle round-trip", () => {
	const state = appStoreApi.getState();
	state.toggleShellLeft();
	expect(appStoreApi.getState().shellLeftOpen).toBeFalse();
	expect(appStoreApi.getState().workspaceSelection.layout.leftCollapsed).toBeTrue();
	state.setShellLeftOpen(true);
	expect(appStoreApi.getState().shellLeftOpen).toBeTrue();
	expect(appStoreApi.getState().workspaceSelection.layout.leftCollapsed).toBeFalse();

	state.toggleShellRight();
	expect(appStoreApi.getState().shellRightOpen).toBeFalse();
	expect(appStoreApi.getState().workspaceSelection.layout.rightCollapsed).toBeTrue();
	state.setShellRightOpen(true);
	expect(appStoreApi.getState().workspaceSelection.layout.rightCollapsed).toBeFalse();

	const beforeRightView = appStoreApi.getState().workspaceNavigationGeneration;
	state.setShellRightView("changes");
	expect(appStoreApi.getState().shellRightView).toBe("changes");
	expect(appStoreApi.getState().workspaceSelection.secondaryArea).toBe("git");
	expect(appStoreApi.getState().workspaceNavigationGeneration).toBe(beforeRightView + 1);

	state.toggleShellSplit();
	expect(appStoreApi.getState().shellSplit).toBeTrue();
	state.setShellSplit(false);
	expect(appStoreApi.getState().shellSplit).toBeFalse();

	state.setShellSplitPercent(65);
	expect(appStoreApi.getState().shellSplitPercent).toBe(65);
	expect(appStoreApi.getState().workspaceSelection.layout.primaryFraction).toBe(0.65);
	state.setShellSplitPercent(5);
	expect(appStoreApi.getState().shellSplitPercent).toBe(SPLIT_MIN);
	state.setShellSplitPercent(95);
	expect(appStoreApi.getState().shellSplitPercent).toBe(SPLIT_MAX);
});

test("shell layout hydrates persisted snapshots and normalizes unknown views", () => {
	appStoreApi.getState().hydrateShellLayout({ shellLeftOpen: false, shellSplit: true });
	const collapsed = appStoreApi.getState();
	expect(collapsed.shellLeftOpen).toBeFalse();
	expect(collapsed.shellSplit).toBeTrue();
	expect(collapsed.shellRightOpen).toBeTrue();
	expect(collapsed.workspaceSelection.layout.leftCollapsed).toBeTrue();
	expect(collapsed.workspaceSelection.layout.rightCollapsed).toBeFalse();
	expect(collapsed.workspaceSelection.layout.primaryFraction).toBe(0.5);
	appStoreApi.getState().hydrateShellLayout({ shellRightView: "notes" as "files" });
	expect(appStoreApi.getState().shellRightView).toBe("files");
	expect(appStoreApi.getState().workspaceSelection.secondaryArea).toBe("files");
	appStoreApi.getState().hydrateShellLayout({ shellSplitPercent: 5 });
	expect(appStoreApi.getState().shellSplitPercent).toBe(SPLIT_MIN);
	expect(appStoreApi.getState().workspaceSelection.layout.primaryFraction).toBe(SPLIT_MIN / 100);
	appStoreApi.getState().hydrateShellLayout({});
	expect(appStoreApi.getState().shellSplitPercent).toBe(50);
});

test("split state survives content tab switches", () => {
	const chat = chatTab("chat-1", "session-1");
	const file = fileTab("file-1", "File1.ext");
	appStoreApi.setState({
		activeProjectAreaId: "area-1",
		tabsByProjectArea: { "area-1": [chat, file] },
		activeTabByProjectArea: { "area-1": "chat-1" },
		shellSplit: true,
	});
	appStoreApi.getState().setActiveTab("file-1", "keep");
	expect(appStoreApi.getState().activeTabByProjectArea["area-1"]).toBe("file-1");
	expect(appStoreApi.getState().shellSplit).toBeTrue();
});

test("split panes prefer the active tab and fall back to the most recent match", () => {
	const chatA = chatTab("chat-a", "session-a");
	const chatB = chatTab("chat-b", "session-b");
	const file = fileTab("file-1", "File1.ext");
	const tabs = [chatA, file, chatB];
	expect(selectSplitChatTab(tabs, file)?.id).toBe("chat-b");
	expect(selectSplitChatTab(tabs, chatA)?.id).toBe("chat-a");
	expect(selectSplitChatTab([file], file)).toBeNull();
	expect(selectSplitPreviewTab(tabs, chatB, "file-1")?.id).toBe("file-1");
	expect(selectSplitPreviewTab(tabs, file, null)?.id).toBe("file-1");
	expect(selectSplitPreviewTab([chatA], chatA, null)).toBeNull();
});

test("session timestamps stay short and muted", () => {
	const now = Date.now();
	expect(shortSessionAge(now - 10_000, now)).toBe("now");
	expect(shortSessionAge(now - 3 * 3_600_000, now)).toBe("3h");
	expect(shortSessionAge(now - 4 * 3_600_000, now)).toBe("4h");
	expect(shortSessionAge(now - 26 * 3_600_000, now)).toBe("1d");
	expect(shortSessionAge(now - 3 * 86_400_000, now)).toBe("3d");
	expect(shortSessionAge(now - 8 * 86_400_000, now)).toBe("1w");
	expect(shortSessionAge(now - 15 * 86_400_000, now)).toBe("2w");
});

test("session timestamps guard invalid and future input", () => {
	const now = Date.now();
	expect(shortSessionAge(Number.NaN, now)).toBe("now");
	expect(shortSessionAge(0, now)).toBe("now");
	expect(shortSessionAge(-100, now)).toBe("now");
	expect(shortSessionAge(now + 60_000, now)).toBe("now");
	expect(shortSessionAge(now - 5_000, Number.NaN)).toBe("now");
});

test("split percent clamps to the operable 20-80 range", () => {
	expect(clampSplitPercent(50)).toBe(50);
	expect(clampSplitPercent(5)).toBe(SPLIT_MIN);
	expect(clampSplitPercent(95)).toBe(SPLIT_MAX);
	expect(clampSplitPercent(Number.NaN)).toBe(50);
	expect(SPLIT_MIN).toBe(20);
	expect(SPLIT_MAX).toBe(80);
});

test("split keyboard steps stay inside the range", () => {
	expect(stepSplitPercent(50, -1)).toBe(50 - SPLIT_STEP);
	expect(stepSplitPercent(50, 1)).toBe(50 + SPLIT_STEP);
	expect(stepSplitPercent(50, -1, true)).toBe(50 - SPLIT_LARGE_STEP);
	expect(stepSplitPercent(22, -1)).toBe(SPLIT_MIN);
	expect(stepSplitPercent(78, 1)).toBe(SPLIT_MAX);
});

test("activity switches keep the global right view in sync", () => {
	const state = appStoreApi.getState();
	state.setActiveActivity("area-1", "changes");
	expect(appStoreApi.getState().activeActivityByProjectArea["area-1"]).toBe("changes");
	expect(appStoreApi.getState().shellRightView).toBe("changes");
	state.requestToolView("area-1", "files");
	expect(appStoreApi.getState().activeActivityByProjectArea["area-1"]).toBe("files");
	expect(appStoreApi.getState().shellRightView).toBe("files");
	state.requestChangesView("area-1", "src/app.ts");
	expect(appStoreApi.getState().activeActivityByProjectArea["area-1"]).toBe("changes");
	expect(appStoreApi.getState().shellRightView).toBe("changes");
	expect(appStoreApi.getState().changesRequest?.path).toBe("src/app.ts");
});

test("activating a preview tab without keep does not pin it", () => {
	const file = fileTab("file-1", "File1.ext");
	appStoreApi.getState().openTab(file, "preview");
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	expect(appStoreApi.getState().previewTabByProjectArea["area-1"]).toBe("file-1");
	// Single click in the tab bar activates without a keep intent.
	appStoreApi.getState().setActiveTab("file-1");
	expect(appStoreApi.getState().activeTabByProjectArea["area-1"]).toBe("file-1");
	expect(appStoreApi.getState().previewTabByProjectArea["area-1"]).toBe("file-1");
	// Double click (or another explicit keep) promotes the preview.
	appStoreApi.getState().setActiveTab("file-1", "keep");
	expect(appStoreApi.getState().previewTabByProjectArea["area-1"]).toBeUndefined();
});

test("the shell keeps one main landmark behind a skip link on the edge chrome", async () => {
	const shell = await source("workspace/shell.svelte");
	expect(shell).toContain("app-shell app-shell-edge");
	expect(shell).toContain('class="skip-link" href="#main-content"');
	expect(shell).toContain('data-testid="project-shell"');
});

test("the work area keeps the renderer and accessibility contracts inside six stable slots", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	expect(
		compile(workArea, {
			filename: "project-work-area.svelte",
			generate: false,
		}).warnings,
	).toEqual([]);
	for (const contract of [
		'data-testid="project-work-area"',
		'data-testid="mobile-pane-navigation"',
		'data-testid="mobile-primary-area-navigation"',
		'data-testid="mobile-area-settings"',
		'data-testid="workspace-grid"',
		'data-slot="primary-rail"',
		'data-slot="primary-sidebar"',
		'data-slot="primary-view"',
		'data-slot="secondary-view"',
		'data-slot="secondary-sidebar"',
		'data-slot="secondary-rail"',
		'data-secondary-selection={hasSecondarySelection ? "true" : "false"}',
		"data-layout-focus={layout.focus}",
		'aria-label="Primary rail"',
		'aria-label="Primary sidebar"',
		"aria-label={primaryTitle}",
		"aria-label={secondaryTitle}",
		'aria-label="Secondary sidebar"',
		'aria-label="Secondary rail"',
		'data-testid="primary-view"',
		'data-testid="secondary-view"',
		'data-testid="secondary-unavailable"',
		'data-testid="project-ready"',
		'data-testid="start-chat"',
		'id="activity-panel"',
		'id="main-content"',
		'data-testid="toggle-left-panel"',
		'data-testid="toggle-right-panel"',
		'data-testid="focus-primary"',
		'data-testid="focus-secondary"',
		'data-testid="restore-primary"',
		'data-testid="restore-secondary"',
		'data-testid="close-secondary"',
		'data-testid="collapse-left-panel"',
		'data-testid="collapse-right-panel"',
		'data-testid="expand-left-panel"',
		'data-testid="expand-right-panel"',
		'data-testid="rail-details"',
		'data-testid="rail-files"',
		'data-testid="rail-changes"',
		'data-testid="rail-browser"',
		"<PanelHeader",
		"<ShellRail",
		"<ChatView",
		"<FilePane",
		"<DiffPane",
		"<BrowserPanel",
		"<ErrorBoundary",
		"selectPrimaryContentTab",
		"selectSecondaryContentTab",
		'primaryArea === "chats"',
		"hasSecondarySelection",
		"secondaryViewVisible",
		"sessionDetailsVisible",
		'data-testid="toggle-files-filter"',
		"<ShellResizer",
		'kind="left"',
		'kind="primary"',
		'kind="right"',
		"showProjects",
		"showSecondarySurface",
		'hasSelection ? "view" : "sidebar"',
		"appStoreApi.getState().workspaceSelection.secondarySelection",
		"mobile-secondary-view",
		"dispatchWorkspaceSelection",
		"initialMobilePane",
		"initialMobileSecondarySurface",
	]) {
		expect(workArea).toContain(contract);
	}
	expect(workArea.match(/id="activity-panel"/g)).toHaveLength(1);
	expect(workArea).not.toContain("five-column");
});

test("shell grid reserves six stable tracks and releases the secondary view until selected", async () => {
	const shell = await source("styles/shell.css");
	expect(shell).toContain("grid-template-columns:");
	expect(shell).toContain("grid-template-rows: minmax(0, 1fr);");
	expect(shell).toContain(
		'grid-template-areas: "primary-rail primary-sidebar primary-view secondary-view secondary-sidebar secondary-rail"',
	);
	for (const placement of [
		".pixie-slot-primary-rail",
		".pixie-slot-primary-sidebar",
		".pixie-slot-primary-view",
		".pixie-slot-secondary-view",
		".pixie-slot-secondary-sidebar",
		".pixie-slot-secondary-rail",
	]) {
		expect(shell).toContain(placement);
	}
	expect(shell).toContain("var(--pixie-secondary-view-track, 0px)");
	expect(shell).toContain(
		"minmax(var(--pixie-secondary-content-min, 0px), var(--pixie-secondary-view-track, 0px))",
	);
	expect(shell).not.toContain("max-width: 64rem");
	expect(shell).toContain("@media (width < 64rem)");
	expect(shell).toContain("overflow: hidden;");
	expect(shell).toContain(".pixie-resizer-left");
	expect(shell).toContain(".pixie-resizer-primary");
	expect(shell).toContain(".pixie-resizer-right");
	expect(shell).not.toContain("pixie-header-cell");
});

test("the split handle is keyboard-operable with correct separator semantics", async () => {
	const split = await source("workspace/split-view.svelte");
	expect(
		compile(split, {
			filename: "split-view.svelte",
			generate: false,
		}).warnings,
	).toEqual([]);
	for (const contract of [
		'role="separator"',
		'tabindex="0"',
		"aria-valuemin",
		"aria-valuemax",
		"aria-valuenow",
		"aria-valuetext",
		"ArrowLeft",
		"ArrowRight",
		'"Home"',
		'"End"',
		"onpointerdown",
		"onpointerup",
		'role="region"',
	]) {
		expect(split).toContain(contract);
	}
});

test("shell resizers expose bounded pointer and keyboard controls for every desktop track", async () => {
	const resizer = await source("workspace/shell-resizer.svelte");
	expect(
		compile(resizer, {
			filename: "shell-resizer.svelte",
			generate: false,
		}).warnings,
	).toEqual([]);
	for (const contract of [
		'role="separator"',
		"aria-valuemin",
		"aria-valuemax",
		"aria-valuenow",
		"ArrowLeft",
		"ArrowRight",
		'"Home"',
		'"End"',
		"onpointerdown",
		"onpointermove",
		"onpointerup",
		"WORKSPACE_SIDE_MIN",
		"WORKSPACE_PRIMARY_FRACTION_MIN",
		"onChange",
	]) {
		expect(resizer).toContain(contract);
	}
});

test("panel collapse recovers focus without losing canonical selections", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	for (const contract of [
		"collapseLeftPanel",
		"collapseRightPanel",
		"queueMicrotask",
		'[data-testid="toggle-left-panel"]',
		'[data-testid="toggle-right-panel"]',
		'[data-testid="expand-left-panel"]',
		'[data-testid="expand-right-panel"]',
		'data-testid="focus-primary"',
		'data-testid="focus-secondary"',
		"restoreLayout",
		"panelHasFocusableContent",
		"dispatchLayout({ leftCollapsed: true })",
		"dispatchLayout({ rightCollapsed: true })",
	]) {
		expect(workArea).toContain(contract);
	}
});

test("project reveal and the global hotkey never strand focus in an empty panel", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	expect(workArea).toContain("focusFirstVisible");
	const shell = await source("workspace/shell.svelte");
	expect(shell).toContain("queueMicrotask");
	expect(shell).toContain("panelHasFocusableContent");
	expect(shell).toContain("focusFirstVisible");
	const rail = await source("workspace/shell-rail.svelte");
	expect(rail).toContain('role="toolbar"');
	expect(rail).toContain("aria-label={label}");
	expect(rail).not.toContain("<nav");
});

test("focus recovery skips hidden and disabled controls", async () => {
	const focus = await source("workspace/focus-control.ts");
	for (const contract of [
		"isFocusableControl",
		":disabled",
		"aria-disabled",
		"aria-hidden",
		"getComputedStyle",
		"getClientRects",
		"panelHasFocusableContent",
	]) {
		expect(focus).toContain(contract);
	}
	// There is no hidden-candidate fallback: a failed lookup must leave focus alone.
	expect(focus).not.toContain("const fallback = candidates[0]");
});

test("settings does not derive session-specific capability context outside Chats", async () => {
	const settings = await source("settings/settings-dialog.svelte");
	expect(settings).toContain('$appStore.workspaceSelection.primaryArea === "chats"');
	expect(settings).toContain("sessionCapabilities");
});

test("shell chrome stays scoped to six slots and stacks split panes narrow", async () => {
	const shell = await source("styles/shell.css");
	for (const slot of [
		".pixie-slot-primary-rail",
		".pixie-slot-primary-sidebar",
		".pixie-slot-primary-view",
		".pixie-slot-secondary-view",
		".pixie-slot-secondary-sidebar",
		".pixie-slot-secondary-rail",
	]) {
		expect(shell).toContain(slot);
	}
	expect(shell).not.toContain(".pixie-header-cell");
	expect(shell).not.toContain(".pixie-panel-cell");
	expect(shell).toContain("flex-direction: column;");
	expect(shell).toMatch(/\.pixie-panel-box\s*{[^}]*min-width:\s*0/s);
});

test("the shell fills the viewport with flex, never percentage heights", async () => {
	const shell = await source("styles/shell.css");
	expect(shell).toContain("min-height: 100dvh;");
	// Percentage heights collapse when the app-shell only guarantees a minimum
	// height; every level below it must grow with flex instead.
	expect(shell).not.toMatch(/height:\s*100%/);
	// Grid slots stretch and their inner boxes fill, without breaking the mobile
	// single-pane switcher.
	expect(shell).toContain(".pixie-slot-primary-view");
	expect(shell).toContain(".pixie-slot-secondary-view");
	// The center canvas bleeds to the grid tracks instead of Mewa's centered
	// reading column.
	expect(shell).toContain(".pixie-shell-grid .app-content");
	for (const [path, root] of [
		["chat/chat-view.svelte", "flex min-h-0 flex-1 flex-col bg-container-project-bg"],
		["files/tabs/file-pane.svelte", "app-content flex min-h-0 flex-1 flex-col"],
		["files/changes/diff-pane.svelte", "app-content flex min-h-0 flex-1 flex-col"],
		["workspace/browser/browser-panel.svelte", "app-content flex min-h-0 flex-1 flex-col"],
	] as const) {
		expect(await source(path)).toContain(root);
	}
	const toaster = await source("components/toaster.svelte");
	expect(toaster).not.toContain("top: var(--space-400)");
});

test("selected sessions and files keep a highlight hook with guide borders", async () => {
	const sessions = await source("workspace/projects/project-sessions.svelte");
	expect(sessions).toContain('data-testid="project-session-row"');
	expect(sessions).toContain("data-active");
	expect(sessions).toContain("bg-control-bg-selected");
	expect(sessions).toContain("loader-circle");
	expect(sessions).toContain("Expand");
	const tree = await source("workspace/projects/project-tree.svelte");
	expect(tree).toContain("pixie-guide");
	expect(tree).toContain('data-testid="project-row"');
	const fileRow = await source("files/tree/file-node-row.svelte");
	expect(fileRow).toContain("pixie-guide");
	expect(fileRow).toContain("{active}");
	const treeRow = await source("files/tree/tree-row.svelte");
	expect(treeRow).toContain("data-active");
	const split = await source("workspace/split-view.svelte");
	expect(split).toContain("resizable-group");
	expect(split).toContain("resizable-panel");
	expect(split).toContain('role="separator"');
});
