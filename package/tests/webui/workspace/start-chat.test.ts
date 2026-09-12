import { afterEach, expect, spyOn, test } from "bun:test";
import { PROTOCOL_VERSION, type WsResult } from "@pixie/contracts";
import { initTransport, resetTransport } from "@/connection";
import { WsTransport } from "@/connection/transport";
import { appStoreApi, chatTabId, projectArea } from "@/store";
import { startChatSession } from "@/workspace/navigation/start-chat";

const project = {
	id: "project",
	name: "Project",
	roots: ["/project"],
	slug: "project",
	lastOpened: 1,
};
const area = projectArea(project);

function deferred<T>(): {
	promise: Promise<T>;
	resolve: (value: T) => void;
	reject: (cause: unknown) => void;
} {
	let resolve!: (value: T) => void;
	let reject!: (cause: unknown) => void;
	const promise = new Promise<T>((resolvePromise, rejectPromise) => {
		resolve = resolvePromise;
		reject = rejectPromise;
	});
	return { promise, resolve, reject };
}

function prepareStore(): void {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	appStoreApi.getState().installWelcomeSnapshot(PROTOCOL_VERSION, [project], [project]);
	appStoreApi.getState().setStatus("connected");
	appStoreApi.setState({
		projectAreas: { [project.id]: [area] },
		selectedProjectId: project.id,
		activeProjectAreaId: area.id,
	});
}

function installTransport(): { transport: WsTransport; restore: () => void } {
	const previousLocation = Object.getOwnPropertyDescriptor(globalThis, "location");
	Object.defineProperty(globalThis, "location", {
		value: new URL("http://localhost:7312"),
		configurable: true,
	});
	const connect = spyOn(WsTransport.prototype, "connect").mockImplementation(() => {});
	const transport = initTransport();
	return {
		transport,
		restore: () => {
			resetTransport();
			connect.mockRestore();
			if (previousLocation) Object.defineProperty(globalThis, "location", previousLocation);
			else Reflect.deleteProperty(globalThis, "location");
		},
	};
}

const createdSession: WsResult<"session.create"> = {
	sessionId: "created-session",
	model: null,
	thinkingLevel: "medium",
	commands: [],
};

afterEach(() => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	resetTransport();
});

test("duplicate activation shares one in-flight create and opens one selected chat", async () => {
	prepareStore();
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"session.create">>();
	const request = spyOn(transport, "request").mockImplementation(
		(() => pending.promise) as typeof transport.request,
	);
	try {
		const first = startChatSession(area.id);
		const second = startChatSession(area.id);
		expect(second).toBe(first);
		expect(request).toHaveBeenCalledTimes(1);
		pending.resolve(createdSession);
		expect(await first).toBe(true);
		const state = appStoreApi.getState();
		expect(state.sessions[createdSession.sessionId]).toBeDefined();
		expect(state.tabsByProjectArea[area.id]).toEqual([
			expect.objectContaining({ kind: "chat", sessionId: createdSession.sessionId }),
		]);
		expect(state.activeTabByProjectArea[area.id]).toBe(
			chatTabId(area.id, createdSession.sessionId),
		);
	} finally {
		request.mockRestore();
		restore();
	}
});

test("a create reply from an older connection generation is ignored", async () => {
	prepareStore();
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"session.create">>();
	const request = spyOn(transport, "request").mockImplementation(
		(() => pending.promise) as typeof transport.request,
	);
	try {
		const create = startChatSession(area.id);
		appStoreApi.getState().setStatus("disconnected");
		appStoreApi.getState().setStatus("connected");
		pending.resolve(createdSession);
		expect(await create).toBe(false);
		expect(appStoreApi.getState().sessions[createdSession.sessionId]).toBeUndefined();
		expect(appStoreApi.getState().tabsByProjectArea[area.id] ?? []).toEqual([]);
	} finally {
		request.mockRestore();
		restore();
	}
});

test("a create reply cannot replace a newer selection", async () => {
	prepareStore();
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"session.create">>();
	const request = spyOn(transport, "request").mockImplementation(
		(() => pending.promise) as typeof transport.request,
	);
	try {
		const create = startChatSession(area.id);
		appStoreApi.getState().selectMain();
		pending.resolve(createdSession);
		expect(await create).toBe(false);
		expect(appStoreApi.getState().sessions[createdSession.sessionId]).toBeUndefined();
		expect(appStoreApi.getState().tabsByProjectArea[area.id] ?? []).toEqual([]);
	} finally {
		request.mockRestore();
		restore();
	}
});

test("duplicate create failures produce one user-facing error", async () => {
	prepareStore();
	const { transport, restore } = installTransport();
	const pending = deferred<WsResult<"session.create">>();
	const request = spyOn(transport, "request").mockImplementation(
		(() => pending.promise) as typeof transport.request,
	);
	try {
		const first = startChatSession(area.id);
		const second = startChatSession(area.id);
		pending.reject(new Error("create failed"));
		expect(await first).toBe(false);
		expect(await second).toBe(false);
		expect(request).toHaveBeenCalledTimes(1);
		expect(appStoreApi.getState().toasts).toEqual([
			expect.objectContaining({ title: "Couldn't start the chat", message: "create failed" }),
		]);
	} finally {
		request.mockRestore();
		restore();
	}
});
