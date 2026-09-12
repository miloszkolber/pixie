import { afterEach, expect, test } from "bun:test";
import type { Project } from "@pixie/contracts";
import { appStoreApi } from "@/store";
import type { NavigationDriver } from "@/workspace/navigation/driver";
import { initNavigation } from "@/workspace/navigation/init";
import { type NavigationLocationV2, serializeLocation } from "@/workspace/navigation/location";
import { deriveLocation } from "@/workspace/navigation/restore";
import {
	initialWorkspaceState,
	selectPrimary,
	selectSecondary,
	selectSecondaryArea,
} from "@/workspace/store/selection-state";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

const project: Project = {
	id: "project-1",
	name: "Project",
	roots: ["/tmp/project"],
	slug: "project",
	lastOpened: 1,
};

test("deriveLocation prefers canonical chat and secondary selections over the active legacy tab", () => {
	const location = deriveLocation({
		activeProjectAreaId: "area-1",
		selectedProjectId: "project-1",
		projectAreas: {
			[project.id]: [
				{
					id: "area-1",
					projectId: project.id,
					name: "Project",
					root: "/tmp/project",
					kind: "project",
				},
			],
		},
		tabsByProjectArea: {
			"area-1": [{ id: "file-tab", kind: "file" }],
		},
		activeTabByProjectArea: { "area-1": "file-tab" },
		workspaceSelection: {
			...initialWorkspaceState,
			primarySelection: { kind: "session", sessionId: "session-1", projectId: project.id },
			secondaryArea: "files",
			secondarySelection: {
				kind: "file",
				projectId: project.id,
				resourceId: "file-tab",
			},
		},
	});

	expect(location).toEqual({
		version: 2,
		primaryArea: "chats",
		primarySelection: { kind: "session", sessionId: "session-1", projectId: project.id },
		secondaryArea: "files",
		secondarySelection: { kind: "file", projectId: project.id, resourceId: "file-tab" },
		projectId: project.id,
		projectAreaId: "area-1",
	});
});

test("a valid v2 route keeps a missing secondary resource in its own selection state", async () => {
	appStoreApi.getState().setStatus("connected");
	appStoreApi.getState().installWelcomeSnapshot(1, [project], [project]);
	const route: NavigationLocationV2 = {
		version: 2,
		primaryArea: "chats",
		primarySelection: null,
		secondaryArea: "files",
		secondarySelection: { kind: "file", projectId: project.id, resourceId: "missing-file" },
		projectId: project.id,
		projectAreaId: project.id,
	};
	const driver: NavigationDriver = {
		read: () => serializeLocation(route),
		replace: () => undefined,
		push: () => undefined,
		onIncoming: () => () => undefined,
	};
	const stop = initNavigation(driver);
	try {
		await Promise.resolve();
		await Promise.resolve();
		const state = appStoreApi.getState();
		expect(state.activeProjectAreaId).toBe(project.id);
		expect(state.workspaceSelection.primarySelection).toBeNull();
		expect(state.workspaceSelection.secondarySelection).toEqual(route.secondarySelection);
		expect(state.tabsByProjectArea[project.id] ?? []).toEqual([]);
	} finally {
		stop();
	}
});

test("a v2 schedule route activates its project area instead of dropping to project home", async () => {
	appStoreApi.getState().setStatus("connected");
	appStoreApi.getState().installWelcomeSnapshot(1, [project], [project]);
	const route: NavigationLocationV2 = {
		version: 2,
		primaryArea: "schedules",
		primarySelection: { kind: "schedule", scheduleId: "schedule-1", projectId: project.id },
		secondaryArea: "details",
		secondarySelection: null,
		projectId: project.id,
		projectAreaId: project.id,
	};
	const driver: NavigationDriver = {
		read: () => serializeLocation(route),
		replace: () => undefined,
		push: () => undefined,
		onIncoming: () => () => undefined,
	};
	const stop = initNavigation(driver);
	try {
		await Promise.resolve();
		await Promise.resolve();
		const state = appStoreApi.getState();
		expect(state.activeProjectAreaId).toBe(project.id);
		expect(state.workspaceSelection.primaryArea).toBe("schedules");
		expect(state.workspaceSelection.primarySelection).toEqual(route.primarySelection);
	} finally {
		stop();
	}
});

test("canonical primary and secondary changes share one v2 history entry model", async () => {
	appStoreApi.getState().setStatus("connected");
	appStoreApi.getState().installWelcomeSnapshot(1, [project], [project]);
	const initialRoute: NavigationLocationV2 = {
		version: 2,
		primaryArea: "chats",
		primarySelection: null,
		secondaryArea: "details",
		secondarySelection: null,
		projectId: project.id,
		projectAreaId: project.id,
	};
	const incoming = new Set<(fragment: string) => void>();
	const writes: string[] = [];
	const driver: NavigationDriver = {
		read: () => serializeLocation(initialRoute),
		replace: (fragment) => writes.push(`replace:${fragment}`),
		push: (fragment) => writes.push(`push:${fragment}`),
		onIncoming: (handler) => {
			incoming.add(handler);
			return () => incoming.delete(handler);
		},
	};
	const stop = initNavigation(driver);
	try {
		await Promise.resolve();
		await Promise.resolve();
		const state = appStoreApi.getState();
		state.dispatchWorkspaceSelection(
			selectPrimary({ kind: "session", sessionId: "session-1", projectId: project.id }, "chats"),
		);
		state.dispatchWorkspaceSelection(selectSecondaryArea("files"));
		state.dispatchWorkspaceSelection(
			selectSecondary({ kind: "file", projectId: project.id, resourceId: "file-1" }, "files"),
		);
		const route = writes.at(-1);
		expect(route).toStartWith("push:#/v2/chats/session-1?");
		const fragment = route?.slice("push:".length);
		expect(fragment).toBe(
			"#/v2/chats/session-1?project=project-1&area=project-1&secondaryArea=files&secondary=%7B%22kind%22%3A%22file%22%2C%22projectId%22%3A%22project-1%22%2C%22resourceId%22%3A%22file-1%22%7D",
		);
		for (const handler of incoming) handler(fragment ?? "");
		await Promise.resolve();
		await Promise.resolve();
		expect(appStoreApi.getState().workspaceSelection.primarySelection).toEqual({
			kind: "session",
			sessionId: "session-1",
			projectId: project.id,
		});
		expect(appStoreApi.getState().workspaceSelection.secondarySelection).toEqual({
			kind: "file",
			projectId: project.id,
			resourceId: "file-1",
		});
	} finally {
		stop();
	}
});
