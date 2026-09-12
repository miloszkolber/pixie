import { afterEach, expect, spyOn, test } from "bun:test";
import { PROTOCOL_VERSION, type WsResult } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { appStoreApi, projectArea, selectPrimary } from "@/store";
import { initProjectAreaChatReconciliation } from "@/workspace/navigation/chat-reconciliation";

afterEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	resetTransport();
});

const project = {
	id: "reconciliation-project",
	name: "Reconciliation",
	roots: ["/reconciliation"],
	slug: "reconciliation",
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
		planState: null,
		page: { projectionId: sessionId, start: 0, total: 0 },
	};
}

test("catalog hydration does not steal a same-project primary selection", async () => {
	const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	const hydration = deferred<WsResult<"session.getMessages">>();
	const request = spyOn(transport, "request").mockImplementation(((method: string) => {
		if (method === "session.list")
			return Promise.resolve([
				{
					sessionId: "late-chat",
					projectId: project.id,
					cwd: project.roots[0] ?? "",
					title: "Late chat",
					model: null,
					thinkingLevel: "off",
					isStreaming: false,
					messageCount: 0,
					updatedAt: 2,
					live: true,
					archived: false,
				},
			]);
		if (method === "session.getMessages") return hydration.promise;
		throw new Error(`unexpected request: ${method}`);
	}) as typeof transport.request);

	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		projectAreas: { [project.id]: [projectArea(project)] },
		selectedProjectId: project.id,
		activeProjectAreaId: project.id,
	});
	const stop = initProjectAreaChatReconciliation(project.id);
	try {
		await Bun.sleep(0);
		appStoreApi
			.getState()
			.dispatchWorkspaceSelection(
				selectPrimary({ kind: "session", sessionId: "newer", projectId: project.id }),
			);
		hydration.resolve(snapshot("late-chat"));
		await Bun.sleep(0);
		const state = appStoreApi.getState();
		expect(state.workspaceSelection.primarySelection).toEqual({
			kind: "session",
			sessionId: "newer",
			projectId: project.id,
		});
		expect(state.activeTabByProjectArea[project.id]).toBeUndefined();
		expect(state.tabsByProjectArea[project.id]).toEqual([
			expect.objectContaining({ sessionId: "late-chat" }),
		]);
	} finally {
		stop();
		request.mockRestore();
		resetTransport();
		connect.mockRestore();
		if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
		else Reflect.deleteProperty(globalThis, "location");
	}
});
