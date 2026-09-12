import { afterEach, beforeEach, expect, test } from "bun:test";
import {
	appStoreApi,
	CANVAS_RESOURCE_ID,
	type ContentTab,
	canvasTabId,
	DESIGN_RESOURCE_ID,
	designTabId,
	INSTANCE_CONTENT_TAB_AREA_ID,
} from "@/store";
import {
	initialWorkspaceState,
	sanitizePrimarySelection,
	sanitizeSecondarySelection,
	selectPrimary,
	selectSecondary,
	workspaceReducer,
} from "@/workspace/store/selection-state";
import {
	migrateLegacyTabsToSelections,
	resolvePrimaryContentStatus,
	resolveSecondaryContentStatus,
	selectSecondaryContentTab,
	selectSplitChatTab,
	selectSplitPair,
	selectSplitPreviewTab,
} from "@/workspace/views/project-work-area-state";

beforeEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));
afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

function chatTab(id: string, sessionId: string, projectAreaId = "area-1"): ContentTab {
	return { kind: "chat", id, projectAreaId, name: id, sessionId };
}

function fileTab(id: string, projectAreaId = "area-1"): ContentTab {
	return {
		kind: "file",
		id,
		projectAreaId,
		root: "/work/a",
		name: id,
		path: `src/${id}`,
		content: "preview",
	};
}

function diffTab(id: string, projectAreaId = "area-1"): ContentTab {
	return {
		kind: "diff",
		id,
		projectAreaId,
		repository: "/work/a",
		name: id,
		path: `src/${id}`,
		scope: { kind: "uncommitted" },
		loadedTarget: "uncommitted",
		original: "before",
		modified: "after",
	};
}

function canvasTab(sessionId = "session-a", projectAreaId = "area-1"): ContentTab {
	return {
		kind: "canvas",
		id: canvasTabId(projectAreaId, sessionId),
		projectAreaId,
		name: "Canvas",
		sessionId,
		resourceId: CANVAS_RESOURCE_ID,
	};
}

function designTab(): ContentTab {
	return {
		kind: "design",
		id: designTabId(),
		projectAreaId: INSTANCE_CONTENT_TAB_AREA_ID,
		name: "Design",
		resourceId: DESIGN_RESOURCE_ID,
	};
}

test("six slots stay independent across primary and secondary updates", () => {
	let state = initialWorkspaceState;
	state = workspaceReducer(
		state,
		selectPrimary({ kind: "session", sessionId: "s-1", projectId: "p-1" }, "chats"),
	);
	state = workspaceReducer(
		state,
		selectSecondary({ kind: "file", projectId: "p-1", resourceId: "f-1" }, "files"),
	);
	expect(state.primarySelection).toEqual({ kind: "session", sessionId: "s-1", projectId: "p-1" });
	expect(state.secondarySelection).toEqual({ kind: "file", projectId: "p-1", resourceId: "f-1" });

	const next = workspaceReducer(
		state,
		selectPrimary({ kind: "session", sessionId: "s-2", projectId: "p-1" }, "chats"),
	);
	expect(next.primarySelection).toEqual({ kind: "session", sessionId: "s-2", projectId: "p-1" });
	expect(next.secondarySelection).toEqual(state.secondarySelection);
	expect(next.secondaryArea).toBe(state.secondaryArea);
});

test("canonical chat selection never falls back to an unrelated tab", () => {
	const chatA = chatTab("chat-a", "session-a");
	const chatB = chatTab("chat-b", "session-b");
	const file = fileTab("file-1");
	const tabs = [chatA, file, chatB];

	const selected = selectSplitChatTab(
		tabs,
		file,
		{ kind: "session", sessionId: "session-a", projectId: "area-1" },
		"area-1",
	);
	expect(selected?.id).toBe("chat-a");

	const missing = selectSplitChatTab(
		tabs,
		chatA,
		{ kind: "session", sessionId: "session-missing", projectId: "area-1" },
		"area-1",
	);
	expect(missing).toBeNull();

	const crossProject = selectSplitChatTab(
		tabs,
		chatA,
		{ kind: "session", sessionId: "session-a", projectId: "other-project" },
		"area-1",
	);
	expect(crossProject).toBeNull();
});

test("canonical preview selection never falls back across the center boundary", () => {
	const chat = chatTab("chat-a", "session-a");
	const file = fileTab("file-1");
	const diff = diffTab("diff-1");
	const tabs = [chat, file, diff];

	const selected = selectSplitPreviewTab(
		tabs,
		chat,
		null,
		{ kind: "file", projectId: "area-1", resourceId: "file-1" },
		"area-1",
	);
	expect(selected?.id).toBe("file-1");

	const missing = selectSplitPreviewTab(
		tabs,
		file,
		"file-1",
		{ kind: "file", projectId: "area-1", resourceId: "missing-file" },
		"area-1",
	);
	expect(missing).toBeNull();

	const crossProject = selectSplitPreviewTab(
		tabs,
		file,
		null,
		{ kind: "file", projectId: "other-project", resourceId: "file-1" },
		"area-1",
	);
	expect(crossProject).toBeNull();
});

test("split pair resolves chat and file independently from canonical state", () => {
	const chat = chatTab("chat-1", "session-1");
	const file = fileTab("file-1");
	const tabs = [chat, file];
	const pair = selectSplitPair(
		tabs,
		"area-1",
		{ kind: "session", sessionId: "session-1", projectId: "area-1" },
		{ kind: "file", projectId: "area-1", resourceId: "file-1" },
	);
	expect(pair.chatTab?.id).toBe("chat-1");
	expect(pair.previewTab?.id).toBe("file-1");

	const missing = selectSplitPair(
		tabs,
		"area-1",
		{ kind: "session", sessionId: "gone", projectId: "area-1" },
		{ kind: "file", projectId: "area-1", resourceId: "gone" },
	);
	expect(missing.chatTab).toBeNull();
	expect(missing.previewTab).toBeNull();
});

test("invalid restored ids sanitize to empty selections", () => {
	expect(sanitizePrimarySelection({ kind: "session", sessionId: "", projectId: "p" })).toBeNull();
	expect(sanitizePrimarySelection({ kind: "session", sessionId: "badid" })).toBeNull();
	expect(sanitizeSecondarySelection({ kind: "file", projectId: "p", resourceId: "" })).toBeNull();
	expect(
		sanitizeSecondarySelection({
			kind: "diff",
			projectId: "p",
			resourceId: "f",
			reviewId: "",
		}),
	).toBeNull();
	expect(
		sanitizeSecondarySelection({
			kind: "module",
			moduleId: "browser",
			resourceId: "panel",
			context: { scope: "project", projectId: "" },
		}),
	).toBeNull();
});

test("content status distinguishes none, available, missing, and invalid", () => {
	const tabs = [chatTab("chat-1", "s-1"), fileTab("file-1")];
	expect(resolvePrimaryContentStatus(tabs, null, "area-1")).toBe("none");
	expect(
		resolvePrimaryContentStatus(
			tabs,
			{ kind: "session", sessionId: "s-1", projectId: "area-1" },
			"area-1",
		),
	).toBe("available");
	expect(
		resolvePrimaryContentStatus(
			tabs,
			{ kind: "session", sessionId: "gone", projectId: "area-1" },
			"area-1",
		),
	).toBe("missing");
	expect(resolvePrimaryContentStatus(tabs, { kind: "session", sessionId: "" }, "area-1")).toBe(
		"invalid",
	);
	expect(resolveSecondaryContentStatus(tabs, null, "area-1")).toBe("none");
	expect(
		resolveSecondaryContentStatus(
			tabs,
			{ kind: "file", projectId: "area-1", resourceId: "file-1" },
			"area-1",
		),
	).toBe("available");
	expect(
		resolveSecondaryContentStatus(
			tabs,
			{ kind: "file", projectId: "area-1", resourceId: "gone" },
			"area-1",
		),
	).toBe("missing");
	expect(
		resolveSecondaryContentStatus(
			tabs,
			{ kind: "file", projectId: "area-1", resourceId: "" },
			"area-1",
		),
	).toBe("invalid");
});

test("legacy tabs migrate to independent selections without cross-project bleed", () => {
	const tabs: ContentTab[] = [
		chatTab("chat-a", "session-a", "area-1"),
		fileTab("file-a", "area-1"),
		chatTab("chat-other", "session-other", "area-2"),
		fileTab("file-other", "area-2"),
	];
	const migrated = migrateLegacyTabsToSelections(tabs, "area-1", "file-a", "file-a");
	expect(migrated.primarySelection).toEqual({
		kind: "session",
		sessionId: "session-a",
		projectId: "area-1",
	});
	expect(migrated.secondarySelection).toEqual({
		kind: "file",
		projectId: "area-1",
		resourceId: "file-a",
	});
});

test("secondary selectors resolve session Canvas and instance Design resources", () => {
	const canvas = canvasTab();
	const design = designTab();
	const tabs = [canvas, design];
	const canvasSelection = {
		kind: "module",
		moduleId: "canvas",
		resourceId: CANVAS_RESOURCE_ID,
		context: { scope: "session", sessionId: "session-a", projectId: "area-1" },
	} as const;
	const designSelection = {
		kind: "module",
		moduleId: "design",
		resourceId: DESIGN_RESOURCE_ID,
		context: { scope: "instance", instanceId: INSTANCE_CONTENT_TAB_AREA_ID },
	} as const;

	expect(selectSecondaryContentTab(tabs, canvasSelection, "area-1")?.kind).toBe("canvas");
	expect(selectSecondaryContentTab(tabs, designSelection, "area-2")?.kind).toBe("design");
	expect(resolveSecondaryContentStatus(tabs, canvasSelection, "area-1")).toBe("available");
	expect(resolveSecondaryContentStatus(tabs, designSelection, "area-2")).toBe("available");
	expect(
		selectSecondaryContentTab(
			tabs,
			{ ...canvasSelection, context: { scope: "session", sessionId: "session-b" } },
			"area-1",
		),
	).toBeNull();
});

test("legacy module tabs migrate to their declared scope", () => {
	const migratedCanvas = migrateLegacyTabsToSelections([canvasTab()], "area-1");
	expect(migratedCanvas.secondarySelection).toEqual({
		kind: "module",
		moduleId: "canvas",
		resourceId: CANVAS_RESOURCE_ID,
		context: { scope: "session", sessionId: "session-a", projectId: "area-1" },
	});

	const migratedDesign = migrateLegacyTabsToSelections([designTab()], "area-1", designTab().id);
	expect(migratedDesign.secondarySelection).toEqual({
		kind: "module",
		moduleId: "design",
		resourceId: DESIGN_RESOURCE_ID,
		context: { scope: "instance", instanceId: INSTANCE_CONTENT_TAB_AREA_ID },
	});
});
