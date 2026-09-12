import { afterEach, expect, spyOn, test } from "bun:test";
import { PROTOCOL_VERSION, type WsResult } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { appStoreApi, chatTabId, projectArea } from "@/store";
import { openChatInTab } from "@/workspace/navigation/open-chat";

afterEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	resetTransport();
});

const project = {
	id: "project",
	name: "Project",
	roots: ["/project"],
	slug: "project",
	lastOpened: 1,
};

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}

function snapshot(sessionId: string): WsResult<"session.getMessages"> {
	return {
		kind: "snapshot",
		summary: {
			sessionId,
			projectId: project.id,
			cwd: "/project",
			title: sessionId,
			model: null,
			thinkingLevel: "off",
			isStreaming: false,
			messageCount: 0,
			updatedAt: 1,
			live: true,
			archived: false,
		},
		messages: [],
		pendingTools: [],
		commands: [],
		planState: null,
		page: { projectionId: sessionId, start: 0, total: 0 },
	};
}

function installConnection(): { restore: () => void; request: ReturnType<typeof spyOn> } {
	const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	const request = spyOn(transport, "request");
	return {
		request,
		restore: () => {
			resetTransport();
			request.mockRestore();
			connect.mockRestore();
			if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
			else Reflect.deleteProperty(globalThis, "location");
		},
	};
}

function prepareStore(): void {
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		projectAreas: { [project.id]: [projectArea(project)] },
		selectedProjectId: project.id,
		activeProjectAreaId: project.id,
	});
}

test("reopening a retained session preserves its persisted title", async () => {
	const project = {
		id: "project",
		name: "Project",
		roots: ["/project"],
		slug: "project",
		lastOpened: 1,
	};
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().openChatSession(project.id, "session", null, "medium");
	appStoreApi.getState().applySessionLifecycle({
		projectId: project.id,
		sessionId: "session",
		operation: "renamed",
		title: "Response Instruction Test",
	});
	appStoreApi.getState().handleAgentEvent({ type: "agent_start" }, "session");
	const runtime = appStoreApi.getState().sessions.session;
	appStoreApi.getState().closeChatToHistory("session", project.id, false);

	expect(appStoreApi.getState().closedChatsByProjectArea.project?.[0]?.title).toBe(
		"Response Instruction Test",
	);
	expect(appStoreApi.getState().sessions.session).toBe(runtime);

	await openChatInTab(project.id, "session");

	const state = appStoreApi.getState();
	expect(state.tabsByProjectArea.project?.[0]?.name).toBe("Response Instruction Test");
	expect(state.closedChatsByProjectArea.project).toEqual([]);
	expect(state.sessions.session).toBe(runtime);
});

test("a chat snapshot after project navigation hydrates without stealing the new project", async () => {
	prepareStore();
	const other = { ...project, id: "other", name: "Other", slug: "other" };
	appStoreApi.setState({
		projects: [project, other],
		projectAreas: { [project.id]: [projectArea(project)], [other.id]: [projectArea(other)] },
	});
	const { request, restore } = installConnection();
	const pending = deferred<WsResult<"session.getMessages">>();
	request.mockReturnValue(pending.promise);
	try {
		const opening = openChatInTab(project.id, "late-chat");
		appStoreApi.getState().selectProject(other.id);
		pending.resolve(snapshot("late-chat"));
		await opening;
		const state = appStoreApi.getState();
		expect(state.selectedProjectId).toBe(other.id);
		expect(state.activeProjectAreaId).toBeNull();
		expect(state.workspaceSelection.primarySelection).toBeNull();
		expect(state.tabsByProjectArea[project.id]).toEqual([
			expect.objectContaining({ sessionId: "late-chat" }),
		]);
	} finally {
		restore();
	}
});

test("a chat snapshot after a same-project selection switch stays in the background", async () => {
	prepareStore();
	appStoreApi.getState().openChatSession(project.id, "current", null, "off");
	const { request, restore } = installConnection();
	const pending = deferred<WsResult<"session.getMessages">>();
	request.mockReturnValue(pending.promise);
	try {
		const opening = openChatInTab(project.id, "late-chat");
		appStoreApi.getState().openChatSession(project.id, "newer", null, "off");
		pending.resolve(snapshot("late-chat"));
		await opening;
		const state = appStoreApi.getState();
		expect(state.workspaceSelection.primarySelection).toEqual({
			kind: "session",
			sessionId: "newer",
			projectId: project.id,
		});
		expect(state.activeTabByProjectArea[project.id]).toBe(chatTabId(project.id, "newer"));
		expect(state.tabsByProjectArea[project.id]).toEqual([
			expect.objectContaining({ sessionId: "current" }),
			expect.objectContaining({ sessionId: "newer" }),
			expect.objectContaining({ sessionId: "late-chat" }),
		]);
	} finally {
		restore();
	}
});
