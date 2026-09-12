import { afterEach, beforeEach, expect, test } from "bun:test";
import { appStoreApi, type ContentTab } from "@/store";
import { hasActiveSessionWork } from "@/workspace/store/session-state";

beforeEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));
afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

function chatTab(id: string, sessionId: string): ContentTab {
	return { kind: "chat", id, projectAreaId: "area-1", name: id, sessionId };
}

function fileTab(id: string): ContentTab {
	return {
		kind: "file",
		id,
		projectAreaId: "area-1",
		root: "/work/a",
		name: id,
		path: `src/${id}`,
		content: "preview",
	};
}

test("opening a file keeps chat draft, stream, and primary selection", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "session-1", null, "medium");
	state.setChatDraft("session-1", "unsent draft");
	state.handleAgentEvent({ type: "run-start" }, "session-1");
	state.openTab(fileTab("file-1"), "preview");

	const next = appStoreApi.getState();
	expect(next.sessions["session-1"]?.draft).toBe("unsent draft");
	expect(next.sessions["session-1"]?.isStreaming).toBe(true);
	expect(next.workspaceSelection.primarySelection).toEqual({
		kind: "session",
		sessionId: "session-1",
		projectId: "area-1",
	});
	expect(next.workspaceSelection.secondarySelection).toEqual({
		kind: "file",
		projectId: "area-1",
		resourceId: "file-1",
	});
});

test("closing the preview keeps chat, draft, and stream intact", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "session-1", null, "medium");
	state.setChatDraft("session-1", "keep me");
	state.openTab(fileTab("file-1"), "preview");
	appStoreApi.getState().closeTab("file-1", true, "area-1");

	const next = appStoreApi.getState();
	expect(next.workspaceSelection.primarySelection).toEqual({
		kind: "session",
		sessionId: "session-1",
		projectId: "area-1",
	});
	expect(next.workspaceSelection.secondarySelection).toBeNull();
	expect(next.sessions["session-1"]?.draft).toBe("keep me");
	expect(
		next.tabsByProjectArea["area-1"]?.some((tab) => tab.id === "chat-1" || tab.kind === "chat"),
	).toBe(true);
});

test("focus and collapse round-trip without losing canonical selections", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "session-1", null, "medium");
	state.openTab(fileTab("file-1"), "preview");
	state.setChatDraft("session-1", "focus draft");

	const before = appStoreApi.getState().workspaceSelection;
	appStoreApi
		.getState()
		.dispatchWorkspaceSelection({ type: "set-layout", layout: { focus: "secondary" } });
	expect(appStoreApi.getState().workspaceSelection.primarySelection).toEqual(
		before.primarySelection,
	);
	expect(appStoreApi.getState().workspaceSelection.secondarySelection).toEqual(
		before.secondarySelection,
	);
	expect(appStoreApi.getState().sessions["session-1"]?.draft).toBe("focus draft");

	appStoreApi.getState().dispatchWorkspaceSelection({
		type: "set-layout",
		layout: { focus: "none", leftCollapsed: true, rightCollapsed: true },
	});
	appStoreApi.getState().dispatchWorkspaceSelection({
		type: "set-layout",
		layout: { focus: "none", leftCollapsed: false, rightCollapsed: false },
	});
	expect(appStoreApi.getState().workspaceSelection.primarySelection).toEqual(
		before.primarySelection,
	);
	expect(appStoreApi.getState().workspaceSelection.secondarySelection).toEqual(
		before.secondarySelection,
	);
	expect(appStoreApi.getState().sessions["session-1"]?.draft).toBe("focus draft");
});

test("active work guard keeps drafts, streams, queues, and pending goals", () => {
	expect(
		hasActiveSessionWork({
			isStreaming: false,
			draft: "  hello  ",
			submission: null,
			queue: { steering: [], followUp: [] },
			goal: { status: "idle" },
		} as never),
	).toBe(true);
	expect(
		hasActiveSessionWork({
			isStreaming: true,
			draft: "",
			submission: null,
			queue: { steering: [], followUp: [] },
			goal: { status: "idle" },
		} as never),
	).toBe(true);
	expect(
		hasActiveSessionWork({
			isStreaming: false,
			draft: "",
			submission: { text: "x" },
			queue: { steering: [], followUp: [] },
			goal: { status: "idle" },
		} as never),
	).toBe(true);
	expect(
		hasActiveSessionWork({
			isStreaming: false,
			draft: "",
			submission: null,
			queue: { steering: ["steer"], followUp: [] },
			goal: { status: "idle" },
		} as never),
	).toBe(true);
	expect(
		hasActiveSessionWork({
			isStreaming: false,
			draft: "",
			submission: null,
			queue: { steering: [], followUp: [] },
			goal: { status: "loading" },
		} as never),
	).toBe(true);
	expect(
		hasActiveSessionWork({
			isStreaming: false,
			draft: "   ",
			submission: null,
			queue: { steering: [], followUp: [] },
			goal: { status: "idle" },
		} as never),
	).toBe(false);
});

test("stale delete reconciliation keeps a drafted runtime without tombstoning", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "drafted", null, "medium");
	state.setChatDraft("drafted", "unsent work");
	state.reconcileProjectAreaSessions("area-1", ["drafted"], []);

	const next = appStoreApi.getState();
	expect(next.sessions.drafted?.draft).toBe("unsent work");
	expect(next.deletedSessionsByProjectArea["area-1"]?.drafted).toBeUndefined();
});

test("idle delete reconciliation still tombstones and clears", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "idle", null, "medium");
	state.reconcileProjectAreaSessions("area-1", ["idle"], []);

	const next = appStoreApi.getState();
	expect(next.sessions.idle).toBeUndefined();
	expect(next.deletedSessionsByProjectArea["area-1"]?.idle).toBe(true);
	expect(next.tabsByProjectArea["area-1"]).toEqual([]);
});

test("clearing a project area keeps drafted runtimes but drops idle ones", () => {
	appStoreApi.setState({ activeProjectAreaId: "area-1" });
	const state = appStoreApi.getState();
	state.openChatSession("area-1", "drafted", null, "medium");
	state.openChatSession("area-1", "idle", null, "medium");
	state.setChatDraft("drafted", "keep");
	state.clearProjectAreaTabs("area-1");

	const next = appStoreApi.getState();
	expect(next.sessions.drafted?.draft).toBe("keep");
	expect(next.sessions.idle).toBeUndefined();
	expect(next.tabsByProjectArea["area-1"]).toBeUndefined();
});

test("split handle keeps separator semantics and never resets while dragging", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/workspace/split-view.svelte", import.meta.url),
	).text();
	for (const contract of [
		'role="separator"',
		"aria-valuemin",
		"aria-valuemax",
		"aria-valuenow",
		"onpointerdown",
		"onpointermove",
		"onpointerup",
		"onlostpointercapture",
		"resizable-group",
		"resizable-panel",
	]) {
		expect(source).toContain(contract);
	}
	expect(source).toContain("if (!dragging) current = splitPercent");
});
