import { afterEach, expect, test } from "bun:test";
import { PROTOCOL_VERSION } from "@pixie/contracts";
import { appStoreApi } from "@/store";
import { openChatInTab } from "@/workspace/navigation/open-chat";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

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
