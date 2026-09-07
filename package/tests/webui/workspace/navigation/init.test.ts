import { expect, test } from "bun:test";
import type { Project } from "@pixie/contracts";
import { appStoreApi } from "@/store";
import type { NavigationDriver } from "@/workspace/navigation/driver";
import { initNavigation } from "@/workspace/navigation/init";

test("navigation cleanup cancels pending route application and permits a fresh subscription", async () => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	const project: Project = {
		id: "p1",
		name: "Project",
		roots: ["/tmp/project"],
		slug: "project",
		lastOpened: 1,
	};
	appStoreApi.getState().setStatus("connected");
	appStoreApi.getState().installWelcomeSnapshot(1, [project], [project]);
	const handlers = new Set<(fragment: string) => void>();
	const writes: string[] = [];
	const driver: NavigationDriver = {
		read: () => "#/v1/projects/p1/projectAreas/p1/chats/chat-a",
		replace: (fragment) => writes.push(fragment),
		push: (fragment) => writes.push(fragment),
		onIncoming: (handler) => {
			handlers.add(handler);
			return () => handlers.delete(handler);
		},
	};

	const stopFirst = initNavigation(driver);
	expect(handlers.size).toBe(1);
	stopFirst();
	await Promise.resolve();
	expect(handlers.size).toBe(0);
	expect(appStoreApi.getState().activeProjectAreaId).toBeNull();
	expect(appStoreApi.getState().routeChatTarget).toBeNull();
	expect(appStoreApi.getState().projectAreas).toEqual({});

	const stopSecond = initNavigation(driver);
	try {
		expect(handlers.size).toBe(1);
		await Promise.resolve();
		expect(appStoreApi.getState().activeProjectAreaId).toBe("p1");
		expect(appStoreApi.getState().routeChatTarget).toMatchObject({
			projectAreaId: "p1",
			sessionId: "chat-a",
		});
		stopSecond();
		const writesAfterCleanup = writes.length;
		appStoreApi.getState().selectMain();
		expect(handlers.size).toBe(0);
		expect(writes).toHaveLength(writesAfterCleanup);
	} finally {
		stopSecond();
		appStoreApi.setState(appStoreApi.getInitialState(), true);
	}
});

test("a reconnect route waits for the current welcome before rejecting an unknown project", async () => {
	appStoreApi.setState(appStoreApi.getInitialState(), true);
	const oldProject: Project = {
		id: "p1",
		name: "Old project",
		roots: ["/tmp/old-project"],
		slug: "old-project",
		lastOpened: 1,
	};
	const newProject: Project = {
		id: "p2",
		name: "New project",
		roots: ["/tmp/new-project"],
		slug: "new-project",
		lastOpened: 2,
	};
	appStoreApi.getState().setStatus("connected");
	appStoreApi.getState().installWelcomeSnapshot(1, [oldProject], [oldProject]);
	appStoreApi.getState().setStatus("disconnected");
	appStoreApi.getState().setStatus("connecting");
	appStoreApi.getState().setStatus("connected");

	const writes: string[] = [];
	const driver: NavigationDriver = {
		read: () => "#/v1/projects/p2",
		replace: (fragment) => writes.push(fragment),
		push: (fragment) => writes.push(fragment),
		onIncoming: () => () => {},
	};
	const stop = initNavigation(driver);
	try {
		await Promise.resolve();
		expect(appStoreApi.getState().selectedProjectId).toBeNull();
		expect(writes).toEqual([]);

		appStoreApi
			.getState()
			.installWelcomeSnapshot(1, [oldProject, newProject], [newProject, oldProject]);
		await Promise.resolve();
		expect(appStoreApi.getState().selectedProjectId).toBe("p2");
		expect(writes).toEqual([]);
	} finally {
		stop();
		appStoreApi.setState(appStoreApi.getInitialState(), true);
	}
});
