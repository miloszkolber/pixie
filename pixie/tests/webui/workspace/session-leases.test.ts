import { afterEach, expect, spyOn, test } from "bun:test";
import { PROTOCOL_VERSION, type WsParams, type WsResult } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { appStoreApi, projectArea } from "@/store";
import {
	hydrateChatResource,
	initProjectAreaChatReconciliation,
} from "@/workspace/navigation/chat-reconciliation";
import { initSessionLeases } from "@/workspace/navigation/session-leases";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

test("leases derive from open tabs, restore after reconnect and stop with their subscription", async () => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	const project = {
		id: "project",
		name: "Project",
		roots: ["/project"],
		slug: "project",
		lastOpened: 1,
	};
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		projectAreas: { project: [projectArea(project), { ...projectArea(project), id: "alias" }] },
	});
	const calls: WsParams<"session.setLeases">[] = [];
	const transport = {
		request: async (method: string, params: WsParams<"session.setLeases">) => {
			expect(method).toBe("session.setLeases");
			calls.push(params);
			return { ok: true };
		},
	} as Pick<WsTransport, "request">;
	const stop = initSessionLeases(transport);
	try {
		await Promise.resolve();
		expect(calls.at(-1)?.sessions).toEqual([]);
		const tab = {
			kind: "chat" as const,
			id: "chat-tab",
			projectAreaId: "project",
			sessionId: "chat",
			name: "Chat",
		};
		appStoreApi.setState({
			tabsByProjectArea: { project: [tab], alias: [{ ...tab, projectAreaId: "alias" }] },
		});
		await Promise.resolve();
		expect(calls.at(-1)?.sessions).toEqual([{ projectId: "project", sessionId: "chat" }]);
		const before = calls.length;
		appStoreApi.setState({ tabsByProjectArea: { alias: [{ ...tab, projectAreaId: "alias" }] } });
		appStoreApi.setState({ toasts: [] });
		await Promise.resolve();
		expect(calls).toHaveLength(before);
		appStoreApi.getState().setStatus("disconnected");
		await Promise.resolve();
		appStoreApi.getState().setStatus("connected");
		await Promise.resolve();
		expect(calls).toHaveLength(before + 1);
		expect(calls.at(-1)?.revision).toBeGreaterThan(calls.at(-2)?.revision ?? 0);
		appStoreApi.getState().applyProjectUpdated({ ...project, closed: true });
		await Promise.resolve();
		expect(calls.at(-1)?.sessions).toEqual([]);
		const completed = calls.length;
		appStoreApi.getState().applyProjectUpdated(project);
		stop();
		await Promise.resolve();
		expect(calls).toHaveLength(completed);
	} finally {
		stop();
	}
});

test("an older failed snapshot cannot report an error for the current open tabs", async () => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [], []);
	appStoreApi.getState().setStatus("connected");
	const failures: ((error: Error) => void)[] = [];
	const transport = {
		request: () => new Promise((_resolve, reject) => failures.push(reject)),
	} as Pick<WsTransport, "request">;
	const stop = initSessionLeases(transport);
	try {
		await Promise.resolve();
		appStoreApi.getState().setStatus("disconnected");
		await Promise.resolve();
		appStoreApi.getState().setStatus("connected");
		await Promise.resolve();
		failures[0]?.(new Error("old connection failed"));
		await Promise.resolve();
		expect(appStoreApi.getState().toasts).toHaveLength(0);
		failures[1]?.(new Error("current snapshot failed"));
		await Promise.resolve();
		expect(appStoreApi.getState().toasts.at(-1)?.message).toBe("current snapshot failed");
	} finally {
		stop();
	}
});

test("a reconnect refresh completes every chat batch after its project view unsubscribes", async () => {
	const location = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	const project = {
		id: "batch-refresh-project",
		name: "Batch refresh",
		roots: ["/batch-refresh"],
		slug: "batch-refresh",
		lastOpened: 1,
	};
	const replies: Array<{
		sessionId: string;
		resolve: (reply: WsResult<"session.getMessages">) => void;
	}> = [];
	const requested: string[] = [];
	const request = spyOn(transport, "request").mockImplementation(((
		method: string,
		params: unknown,
	) => {
		if (method === "session.getMessages") {
			const { sessionId } = params as WsParams<"session.getMessages">;
			requested.push(sessionId);
			return new Promise((resolve) => replies.push({ sessionId, resolve }));
		}
		if (method === "session.list") return new Promise(() => {});
		throw new Error(`unexpected request: ${method}`);
	}) as typeof transport.request);
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		tabsByProjectArea: {
			[project.id]: Array.from({ length: 6 }, (_, index) => ({
				kind: "chat" as const,
				id: `chat-tab-${index}`,
				projectAreaId: project.id,
				sessionId: `chat-${index}`,
				name: `Chat ${index}`,
			})),
		},
	});
	const stop = initProjectAreaChatReconciliation(project.id);
	try {
		await Promise.resolve();
		expect(requested).toEqual(["chat-0", "chat-1", "chat-2", "chat-3"]);
		stop();
		for (const { sessionId, resolve } of replies.slice(0, 4)) {
			resolve({
				kind: "snapshot",
				summary: {
					sessionId,
					projectId: project.id,
					cwd: project.roots[0] ?? "",
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
				modes: null,
				planState: null,
				page: { projectionId: sessionId, start: 0, total: 0 },
			});
		}
		await Bun.sleep(0);
		expect(requested).toEqual(["chat-0", "chat-1", "chat-2", "chat-3", "chat-4", "chat-5"]);
	} finally {
		stop();
		request.mockRestore();
		resetTransport();
		connect.mockRestore();
		if (location) Object.defineProperty(globalThis, "location", location);
		else Reflect.deleteProperty(globalThis, "location");
	}
});

test("late hydration respects chat and project closure while allowing an explicit reopen", async () => {
	const location = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	const replies: ((reply: WsResult<"session.getMessages">) => void)[] = [];
	const request = spyOn(transport, "request").mockImplementation(
		() => new Promise((resolve) => replies.push(resolve)),
	);
	const project = {
		id: "project",
		name: "Project",
		roots: ["/project"],
		slug: "project",
		lastOpened: 1,
	};
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi
		.getState()
		.openTab(
			{ kind: "chat", id: "chat-tab", projectAreaId: "project", sessionId: "chat", name: "Chat" },
			"keep",
		);
	const reply: WsResult<"session.getMessages"> = {
		kind: "snapshot",
		summary: {
			sessionId: "chat",
			projectId: "project",
			cwd: "/project",
			title: "Chat",
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
		modes: null,
		planState: null,
		page: { projectionId: "projection", start: 0, total: 0 },
	};
	try {
		const previous = hydrateChatResource("project", "chat");
		appStoreApi.getState().closeChatToHistory("chat", "project");
		const reopened = hydrateChatResource("project", "chat");
		expect(reopened).not.toBe(previous);
		expect(replies).toHaveLength(2);
		replies[0]?.(reply);
		expect(await previous).toBe(false);
		expect(appStoreApi.getState().tabsByProjectArea.project).toEqual([]);
		expect(hydrateChatResource("project", "chat")).toBe(reopened);
		replies[1]?.(reply);
		expect(await reopened).toBe(true);
		expect(appStoreApi.getState().tabsByProjectArea.project).toHaveLength(1);
		appStoreApi.getState().setChatDraft("chat", "keep this draft");
		appStoreApi.getState().appendUserMessage("chat", "pending admission");
		const refresh = hydrateChatResource("project", "chat", true);
		expect(replies).toHaveLength(3);
		replies[2]?.({ ...reply, page: { projectionId: "refreshed", start: 0, total: 0 } });
		expect(await refresh).toBe(true);
		expect(appStoreApi.getState().sessions.chat?.transcript?.projectionId).toBe("refreshed");
		expect(appStoreApi.getState().sessions.chat?.draft).toBe("keep this draft");
		expect(
			appStoreApi
				.getState()
				.sessions.chat?.turns.some(
					(turn) => turn.kind === "user" && turn.message.content === "pending admission",
				),
		).toBe(true);
		const admittedRefresh = hydrateChatResource("project", "chat", true);
		expect(replies).toHaveLength(4);
		replies[3]?.({
			...reply,
			summary: { ...reply.summary, messageCount: 1 },
			messages: [{ role: "user", content: [{ type: "text", text: "pending admission" }] }],
			page: { projectionId: "admitted", start: 0, total: 1 },
		});
		expect(await admittedRefresh).toBe(true);
		expect(
			appStoreApi.getState().sessions.chat?.turns.filter((turn) => turn.kind === "user"),
		).toHaveLength(1);

		const staleRefresh = hydrateChatResource("project", "chat", true);
		expect(replies).toHaveLength(5);
		appStoreApi.getState().setStatus("disconnected");
		appStoreApi.getState().setStatus("connected");
		replies[4]?.({ ...reply, page: { projectionId: "stale", start: 0, total: 0 } });
		expect(await staleRefresh).toBe(false);
		expect(appStoreApi.getState().sessions.chat?.transcript?.projectionId).toBe("admitted");

		const runtime = appStoreApi.getState().sessions.chat;
		if (!runtime) throw new Error("chat runtime missing");
		appStoreApi.setState({ sessions: { chat: { ...runtime, isStreaming: true } } });
		appStoreApi.getState().closeChatToHistory("chat", "project");
		expect(await hydrateChatResource("project", "chat")).toBe(true);
		expect(replies).toHaveLength(5);
		expect(appStoreApi.getState().tabsByProjectArea.project).toHaveLength(1);

		appStoreApi.getState().closeChatRuntime("chat");
		const closingProject = hydrateChatResource("project", "chat");
		appStoreApi.getState().applyProjectUpdated({ ...project, closed: true });
		replies[5]?.(reply);
		expect(await closingProject).toBe(false);
		expect(appStoreApi.getState().sessions.chat).toBeUndefined();
	} finally {
		request.mockRestore();
		resetTransport();
		connect.mockRestore();
		if (location) Object.defineProperty(globalThis, "location", location);
		else Reflect.deleteProperty(globalThis, "location");
	}
});
