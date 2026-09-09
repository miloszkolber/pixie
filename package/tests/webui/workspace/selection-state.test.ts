import { afterEach, beforeEach, expect, test } from "bun:test";
import { appStoreApi, type ContentTab } from "@/store";
import {
	clearSecondary,
	initialWorkspaceState,
	isSecondaryArea,
	normalizeSecondaryArea,
	normalizeWorkspaceLayout,
	type SecondarySelection,
	selectPrimary,
	selectPrimaryArea,
	selectSecondary,
	selectSecondaryArea,
	setLayout,
	WORKSPACE_PRIMARY_FRACTION_MAX,
	WORKSPACE_PRIMARY_FRACTION_MIN,
	WORKSPACE_SIDE_MAX,
	WORKSPACE_SIDE_MIN,
	workspaceReducer,
} from "@/workspace/store/selection-state";

beforeEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));
afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

function chatTab(id: string, sessionId: string): ContentTab {
	return { kind: "chat", id, projectAreaId: "project-1", name: id, sessionId };
}

function fileTab(id: string): ContentTab {
	return {
		kind: "file",
		id,
		projectAreaId: "project-1",
		root: "/work/project",
		name: id,
		path: `src/${id}`,
		content: "",
	};
}

test("primary and secondary selections are independent", () => {
	const primary = { kind: "session", sessionId: "session-1", projectId: "project-1" } as const;
	const secondary: SecondarySelection = {
		kind: "file",
		projectId: "project-1",
		resourceId: "src/main.ts",
	};
	const withPrimary = workspaceReducer(initialWorkspaceState, selectPrimary(primary));
	const withSecondary = workspaceReducer(withPrimary, selectSecondary(secondary));
	const withNewPrimary = workspaceReducer(
		withSecondary,
		selectPrimary({ kind: "session", sessionId: "session-2" }),
	);

	expect(withNewPrimary.primarySelection).toEqual({ kind: "session", sessionId: "session-2" });
	expect(withNewPrimary.secondarySelection).toEqual(secondary);
	expect(workspaceReducer(withNewPrimary, clearSecondary())).toEqual({
		...withNewPrimary,
		secondarySelection: null,
	});
});

test("clearing secondary does not clear primary context", () => {
	const state = workspaceReducer(
		workspaceReducer(
			initialWorkspaceState,
			selectPrimary({ kind: "schedule", scheduleId: "schedule-1", projectId: "project-1" }),
		),
		selectSecondary({
			kind: "module",
			moduleId: "browser",
			resourceId: "panel-1",
			context: { scope: "session", sessionId: "session-1", projectId: "project-1" },
		}),
	);

	const cleared = workspaceReducer(state, clearSecondary());
	expect(cleared.primarySelection).toEqual(state.primarySelection);
	expect(cleared.secondarySelection).toBeNull();
	expect(cleared.secondaryArea).toBe(state.secondaryArea);
});

test("area actions preserve selections on the other side", () => {
	const primary = { kind: "settings", sectionId: "appearance" } as const;
	const secondary: SecondarySelection = {
		kind: "diff",
		projectId: "project-1",
		resourceId: "src/main.ts",
		reviewId: "review-1",
	};
	let state = workspaceReducer(initialWorkspaceState, selectPrimary(primary));
	state = workspaceReducer(state, selectSecondary(secondary));
	state = workspaceReducer(state, selectPrimaryArea("settings"));
	state = workspaceReducer(state, selectSecondaryArea("git"));

	expect(state.primaryArea).toBe("settings");
	expect(state.secondaryArea).toBe("git");
	expect(state.primarySelection).toEqual(primary);
	expect(state.secondarySelection).toEqual(secondary);
});

test("layout normalization clamps bounds and invalid focus", () => {
	const normalized = normalizeWorkspaceLayout({
		leftWidth: 1,
		rightWidth: 999,
		primaryFraction: Number.NaN,
		focus: "sideways" as "none",
	});
	expect(normalized.leftWidth).toBe(WORKSPACE_SIDE_MIN);
	expect(normalized.rightWidth).toBe(WORKSPACE_SIDE_MAX);
	expect(normalized.primaryFraction).toBe(0.5);
	expect(normalized.focus).toBe("none");

	const state = workspaceReducer(
		initialWorkspaceState,
		setLayout({
			leftWidth: Number.NEGATIVE_INFINITY,
			rightWidth: Number.POSITIVE_INFINITY,
			primaryFraction: 2,
			focus: "secondary",
		}),
	);
	expect(state.layout).toMatchObject({
		leftWidth: WORKSPACE_SIDE_MIN,
		rightWidth: WORKSPACE_SIDE_MAX,
		primaryFraction: WORKSPACE_PRIMARY_FRACTION_MAX,
		focus: "secondary",
	});
	expect(WORKSPACE_PRIMARY_FRACTION_MIN).toBe(0.2);
});

test("secondary module areas require a bounded module identity", () => {
	expect(isSecondaryArea("details")).toBeTrue();
	expect(isSecondaryArea("module:browser")).toBeTrue();
	expect(isSecondaryArea("module:")).toBeFalse();
	expect(isSecondaryArea(`module:${"b".repeat(513)}`)).toBeFalse();
	expect(isSecondaryArea("module:bad\u0001id")).toBeFalse();
	expect(normalizeSecondaryArea("module:")).toBe("details");
	expect(normalizeSecondaryArea("module:browser")).toBe("module:browser");
});

test("content activation bridges to independent primary and secondary selections", () => {
	appStoreApi.setState({ activeProjectAreaId: "project-1" });
	const chat = chatTab("chat-1", "session-1");
	const file = fileTab("file-1");
	const state = appStoreApi.getState();

	state.openTab(chat, "keep");
	const withChat = appStoreApi.getState();
	state.openTab(file, "preview");
	const withBoth = appStoreApi.getState();

	expect(withChat.workspaceSelection.primarySelection).toEqual({
		kind: "session",
		sessionId: "session-1",
		projectId: "project-1",
	});
	expect(withBoth.workspaceSelection.primarySelection).toEqual(
		withChat.workspaceSelection.primarySelection,
	);
	expect(withBoth.workspaceSelection.secondarySelection).toEqual({
		kind: "file",
		projectId: "project-1",
		resourceId: "file-1",
	});

	appStoreApi.getState().setActiveTab(file.id);
	expect(appStoreApi.getState().workspaceSelection.primarySelection).toEqual(
		withChat.workspaceSelection.primarySelection,
	);
	appStoreApi.getState().closeTab(file.id, false, "project-1");
	expect(appStoreApi.getState().workspaceSelection.primarySelection).toEqual(
		withChat.workspaceSelection.primarySelection,
	);
	expect(appStoreApi.getState().workspaceSelection.secondarySelection).toBeNull();
	appStoreApi.getState().closeTab(chat.id, false, "project-1");
	expect(appStoreApi.getState().workspaceSelection.primarySelection).toBeNull();
});

test("background chat hydration does not steal primary focus", () => {
	appStoreApi.setState({ activeProjectAreaId: "project-1" });
	const state = appStoreApi.getState();
	state.openTab(chatTab("chat-1", "session-1"), "keep");
	state.openChatSession("project-1", "session-2", null, "medium", undefined, {
		activate: false,
	});

	const next = appStoreApi.getState();
	expect(next.workspaceSelection.primarySelection).toEqual({
		kind: "session",
		sessionId: "session-1",
		projectId: "project-1",
	});
	expect(next.tabsByProjectArea["project-1"]).toHaveLength(2);
});

test("project navigation clears only incompatible scoped selections", () => {
	appStoreApi.setState({ activeProjectAreaId: "project-1" });
	const state = appStoreApi.getState();
	state.openTab(chatTab("chat-1", "session-1"), "keep");
	state.openTab(fileTab("file-1"), "preview");
	state.selectProject("project-2");

	const next = appStoreApi.getState();
	expect(next.workspaceSelection.primarySelection).toBeNull();
	expect(next.workspaceSelection.secondarySelection).toBeNull();
});
