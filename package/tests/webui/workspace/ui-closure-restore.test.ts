import { afterEach, beforeEach, expect, test } from "bun:test";
import { appStoreApi, type ContentTab } from "@/store";
import {
	constrainWorkspaceSelectionToProjects,
	createInitialWorkspaceState,
	decodeWorkspacePersist,
	encodeWorkspacePersist,
	WORKSPACE_PERSIST_VERSION,
} from "@/workspace/store/selection-state";
import {
	isWorkspaceSelectionStale,
	migrateLegacyTabsToSelections,
} from "@/workspace/views/project-work-area-state";

beforeEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));
afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

function chatTab(sessionId: string, projectAreaId = "area-1"): ContentTab {
	return { kind: "chat", id: `chat-${sessionId}`, projectAreaId, name: sessionId, sessionId };
}

function fileTab(id: string, projectAreaId = "area-1"): ContentTab {
	return {
		kind: "file",
		id,
		projectAreaId,
		root: "/work/a",
		name: id,
		path: `src/${id}`,
		content: "",
	};
}

test("persisted v2 state round-trips valid selections and layout", () => {
	const snapshot = {
		...createInitialWorkspaceState(),
		primaryArea: "chats" as const,
		secondaryArea: "files" as const,
		primarySelection: { kind: "session", sessionId: "s-1", projectId: "p-1" } as const,
		secondarySelection: { kind: "file", projectId: "p-1", resourceId: "f-1" } as const,
		layout: {
			leftCollapsed: true,
			rightCollapsed: false,
			focus: "secondary" as const,
			leftWidth: 240,
			rightWidth: 280,
			primaryFraction: 0.6,
		},
	};
	const encoded = encodeWorkspacePersist(snapshot);
	expect(encoded.version).toBe(WORKSPACE_PERSIST_VERSION);
	expect(encoded.version).toBe(2);
	expect(decodeWorkspacePersist(encoded)).toEqual(snapshot);
	expect(decodeWorkspacePersist(JSON.parse(JSON.stringify(encoded)))).toEqual(snapshot);
});

test("persisted restore drops invalid selections but keeps valid layout", () => {
	const decoded = decodeWorkspacePersist({
		version: 2,
		snapshot: {
			primaryArea: "nope",
			secondaryArea: "files",
			primarySelection: { kind: "session", sessionId: "", projectId: "p-1" },
			secondarySelection: { kind: "file", projectId: "p-1", resourceId: "f-1" },
			layout: { leftCollapsed: false, rightCollapsed: false, focus: "sideways", leftWidth: 1 },
		},
	});
	expect(decoded?.primaryArea).toBe("chats");
	expect(decoded?.primarySelection).toBeNull();
	expect(decoded?.secondarySelection).toEqual({
		kind: "file",
		projectId: "p-1",
		resourceId: "f-1",
	});
	expect(decoded?.layout.focus).toBe("none");
});

test("persisted restore fails closed on unusable shapes and future versions", () => {
	expect(decodeWorkspacePersist(null)).toBeNull();
	expect(decodeWorkspacePersist("nope")).toBeNull();
	expect(
		decodeWorkspacePersist({ version: 99, snapshot: createInitialWorkspaceState() }),
	).toBeNull();
	expect(decodeWorkspacePersist({ version: 2 })).toBeNull();
	expect(decodeWorkspacePersist({ version: 2, snapshot: null })).toBeNull();
});

test("v1 layout forward-migrates without inventing selections", () => {
	const migrated = decodeWorkspacePersist({
		version: 1,
		layout: { leftCollapsed: true, rightCollapsed: false, focus: "secondary", leftWidth: 240 },
	});
	expect(migrated?.primarySelection).toBeNull();
	expect(migrated?.secondarySelection).toBeNull();
	expect(migrated?.layout.leftCollapsed).toBe(true);
	expect(migrated?.layout.focus).toBe("secondary");

	const legacyShell = decodeWorkspacePersist({
		version: 1,
		layout: { shellLeftOpen: false, shellRightOpen: true, shellSplitPercent: 65 },
	});
	expect(legacyShell?.layout.leftCollapsed).toBe(true);
	expect(legacyShell?.layout.rightCollapsed).toBe(false);
	expect(legacyShell?.layout.primaryFraction).toBeCloseTo(0.65);
});

test("new-server recovery clears unknown projects but keeps instance scope and layout", () => {
	const snapshot = {
		...createInitialWorkspaceState(),
		primarySelection: { kind: "session", sessionId: "s-1", projectId: "gone" } as const,
		secondarySelection: {
			kind: "module",
			moduleId: "browser",
			resourceId: "panel-1",
			context: { scope: "instance", instanceId: "instance-1" },
		} as const,
		layout: { ...createInitialWorkspaceState().layout, leftCollapsed: true },
	};
	const constrained = constrainWorkspaceSelectionToProjects(snapshot, new Set(["p-1"]));
	expect(constrained.primarySelection).toBeNull();
	expect(constrained.secondarySelection).toEqual(snapshot.secondarySelection);
	expect(constrained.layout.leftCollapsed).toBe(true);

	const fileSnapshot = {
		...createInitialWorkspaceState(),
		secondarySelection: { kind: "file", projectId: "gone", resourceId: "f" } as const,
	};
	expect(
		constrainWorkspaceSelectionToProjects(fileSnapshot, ["p-1"]).secondarySelection,
	).toBeNull();
});

test("stale generations never win over current navigation", () => {
	expect(isWorkspaceSelectionStale(3, 3)).toBe(false);
	expect(isWorkspaceSelectionStale(4, 3)).toBe(true);
});

test("old-tab upgrade prefers active entries and ignores invalid tabs", () => {
	const tabs: ContentTab[] = [
		chatTab("s-1"),
		fileTab("file-1"),
		{ kind: "chat", id: "", projectAreaId: "area-1", name: "", sessionId: "" },
		chatTab("other", "area-2"),
	];
	const migrated = migrateLegacyTabsToSelections(tabs, "area-1", "file-1", "file-1");
	expect(migrated.primarySelection).toEqual({
		kind: "session",
		sessionId: "s-1",
		projectId: "area-1",
	});
	expect(migrated.secondarySelection).toEqual({
		kind: "file",
		projectId: "area-1",
		resourceId: "file-1",
	});

	const empty = migrateLegacyTabsToSelections([], "area-1", null, null);
	expect(empty.primarySelection).toBeNull();
	expect(empty.secondarySelection).toBeNull();
});

test("old-tab upgrade maps diff review and browser panel without draft loss", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	appStoreApi.getState().openChatSession("area-1", "s-1", null, "medium");
	appStoreApi.getState().setChatDraft("s-1", "upgrade draft");
	const tabs: ContentTab[] = [
		chatTab("s-1"),
		{
			kind: "diff",
			id: "diff-1",
			projectAreaId: "area-1",
			repository: "/work/a",
			name: "diff-1",
			path: "src/a.ts",
			scope: { kind: "branch", baseRef: "refs/heads/main" },
			loadedTarget: "refs/heads/main",
			targetComparison: "abc..def",
			original: "a",
			modified: "b",
		},
		{
			kind: "browser",
			id: "browser-1",
			projectAreaId: "area-1",
			name: "Browser",
			panelId: "panel-1",
		},
	];
	const migrated = migrateLegacyTabsToSelections(tabs, "area-1", "diff-1", "diff-1");
	expect(migrated.secondarySelection).toEqual({
		kind: "diff",
		projectId: "area-1",
		resourceId: "diff-1",
		reviewId: "abc..def",
	});
	expect(appStoreApi.getState().sessions["s-1"]?.draft).toBe("upgrade draft");

	const browser = migrateLegacyTabsToSelections(tabs, "area-1", "browser-1", "browser-1");
	expect(browser.secondarySelection).toEqual({
		kind: "module",
		moduleId: "browser",
		resourceId: "panel-1",
		context: { scope: "project", projectId: "area-1" },
	});
	expect(appStoreApi.getState().sessions["s-1"]?.draft).toBe("upgrade draft");
});
