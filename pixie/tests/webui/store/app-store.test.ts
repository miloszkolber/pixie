import { beforeEach, expect, test } from "bun:test";
import type { AgentProfile, Project, SessionGoal } from "@pixie/contracts";
import { appStoreApi, EMPTY_RUNTIME, projectArea, reduceSessionEvent } from "@/store/app-store";
import { selectDiffTabTargetRef, selectSkillsStale } from "@/workspace/store/selectors";

const project: Project = {
	id: "p1",
	name: "Project",
	roots: ["/tmp/project"],
	slug: "project",
	lastOpened: 1,
};
const area = projectArea(project);
const genericProfile: AgentProfile = {
	name: "Example agent",
	version: "1.0.0",
	pi: false,
	compatible: true,
	missingRequired: [],
	operations: {
		deleteSession: false,
		forkSession: false,
		promptImage: false,
		promptEmbeddedContext: false,
		httpMcp: false,
		steer: false,
		renameSession: false,
		archiveSession: false,
		administration: false,
	},
};

test("a project area always uses the project's sole root", () => {
	expect(projectArea(project).root).toBe("/tmp/project");
});

test("content identity keeps file and repository previews isolated", () => {
	const state = appStoreApi.getState();
	state.openTab(
		{
			kind: "file",
			id: "file-a",
			projectAreaId: "p1",
			root: "/tmp/project",
			name: "README.md",
			path: "README.md",
			content: "first",
		},
		"keep",
	);
	state.openTab(
		{
			kind: "diff",
			id: "diff-a",
			projectAreaId: "p1",
			repository: "/tmp/project/repo",
			name: "file.ts",
			path: "src/file.ts",
			scope: { kind: "commit", sha: "aaaa" },
			loadedTarget: "aaaa",
			original: "before-a",
			modified: "after-a",
		},
		"keep",
	);
	state.openTab(
		{
			kind: "diff",
			id: "diff-b",
			projectAreaId: "p1",
			repository: "/tmp/project/other-repo",
			name: "file.ts",
			path: "src/file.ts",
			scope: { kind: "commit", sha: "bbbb" },
			loadedTarget: "bbbb",
			original: "before-b",
			modified: "after-b",
		},
		"keep",
	);
	for (const [id, baseRef] of [
		["diff-main", "refs/heads/main"],
		["diff-release", "refs/heads/release"],
	] as const) {
		state.openTab(
			{
				kind: "diff",
				id,
				projectAreaId: "p1",
				repository: "/tmp/project/repo",
				name: "file.ts",
				path: "src/file.ts",
				scope: { kind: "branch", baseRef },
				loadedTarget: baseRef,
				original: "before",
				modified: "after",
			},
			"keep",
		);
	}
	expect(appStoreApi.getState().tabsByProjectArea.p1).toHaveLength(5);
});

test("browser panel tabs retain distinct random panel lifecycles", () => {
	const state = appStoreApi.getState();
	state.setBrowserPanelState("b-one", { address: "https://one.example" });
	state.setBrowserPanelState("b-two", { address: "https://two.example" });
	state.openTab(
		{ kind: "browser", id: "browser-one", projectAreaId: "p1", name: "Browser", panelId: "b-one" },
		"keep",
	);
	state.openTab(
		{ kind: "browser", id: "browser-two", projectAreaId: "p1", name: "Browser", panelId: "b-two" },
		"keep",
	);
	expect(
		appStoreApi.getState().tabsByProjectArea.p1?.filter((tab) => tab.kind === "browser"),
	).toHaveLength(2);
	state.closeTab("browser-one", false, "p1");
	expect(appStoreApi.getState().tabsByProjectArea.p1?.map((tab) => tab.id)).toEqual([
		"browser-two",
	]);
	expect(appStoreApi.getState().browserPanelStateById).toEqual({
		"b-two": expect.objectContaining({ address: "https://two.example" }),
	});
});

test("browser panel state is partitioned by panel and ignores delayed completions", () => {
	const state = appStoreApi.getState();
	state.setBrowserPanelState("b-one", { address: "https://one.example" });
	state.setBrowserPanelState("b-two", { address: "https://two.example" });
	const first = state.beginBrowserPanelRequest("b-one");
	const latest = state.beginBrowserPanelRequest("b-one");
	const second = state.beginBrowserPanelRequest("b-two");
	expect(
		state.completeBrowserPanelRequest("b-one", first, { screenshot: "/stale.png" }),
	).toBeFalse();
	expect(state.completeBrowserPanelRequest("b-two", second, { snapshot: "two" })).toBeTrue();
	expect(state.completeBrowserPanelRequest("b-one", latest, { snapshot: "one" })).toBeTrue();
	expect(appStoreApi.getState().browserPanelStateById).toMatchObject({
		"b-one": { address: "https://one.example", snapshot: "one" },
		"b-two": { address: "https://two.example", snapshot: "two" },
	});
	state.openTab(
		{ kind: "browser", id: "browser-one", projectAreaId: "p1", name: "Browser", panelId: "b-one" },
		"keep",
	);
	state.openTab(
		{ kind: "browser", id: "browser-two", projectAreaId: "p1", name: "Browser", panelId: "b-two" },
		"keep",
	);
	state.clearProjectAreaTabs("p1");
	expect(appStoreApi.getState().browserPanelStateById).toEqual({});
});

test("branch diff content follows the resolved comparison instead of the branch label", () => {
	const state = appStoreApi.getState();
	const scope = { kind: "branch", baseRef: "refs/heads/main" } as const;
	const first = `${"a".repeat(40)}..${"b".repeat(40)}`;
	const second = `${"a".repeat(40)}..${"c".repeat(40)}`;
	state.openTab(
		{
			kind: "diff",
			id: "branch-diff",
			projectAreaId: "p1",
			repository: "/tmp/project/repo",
			name: "file.ts",
			path: "src/file.ts",
			scope,
			loadedTarget: first,
			targetComparison: first,
			original: "before",
			modified: "first",
		},
		"keep",
	);

	state.noteDiffComparison("p1", "/tmp/project/repo", scope, second);
	let tab = appStoreApi
		.getState()
		.tabsByProjectArea.p1?.find((candidate) => candidate.id === "branch-diff");
	expect(tab?.kind).toBe("diff");
	if (tab?.kind !== "diff") throw new Error("missing branch diff tab");
	expect(tab.loadedTarget).toBe(first);
	expect(selectDiffTabTargetRef(appStoreApi.getState(), tab)).toBe(second);

	state.updateDiffTabContent(
		"p1",
		"branch-diff",
		{ original: "before", modified: "second", comparisonId: second },
		1,
		second,
	);
	tab = appStoreApi
		.getState()
		.tabsByProjectArea.p1?.find((candidate) => candidate.id === "branch-diff");
	expect(tab?.kind === "diff" ? tab.modified : null).toBe("second");
	expect(tab?.kind === "diff" ? tab.loadedTarget : null).toBe(second);
	expect(tab?.kind === "diff" ? tab.targetComparison : null).toBe(second);
});

beforeEach(() => {
	appStoreApi.setState({
		projects: [project],
		recentProjects: [project],
		projectAreas: { p1: [area] },
		selectedProjectId: "p1",
		activeProjectAreaId: "p1",
		removedProjectAreaIds: {},
		tabsByProjectArea: {},
		activeTabByProjectArea: { p1: null },
		previewTabByProjectArea: {},
		closedChatsByProjectArea: {},
		sessionCatalogVersionByProjectArea: {},
		activeActivityByProjectArea: {},
		sessions: {},
		deletedSessionsByProjectArea: {},
		navTickByProjectArea: {},
		fsChangesByProjectArea: {},
		skillChangeTickByProjectArea: {},
		skillsSyncedTickBySession: {},
		commandCatalogGeneration: 0,
		changesRequest: null,
		browserPanelStateById: {},
		chatLocationRequest: null,
		routeChatTarget: null,
		historyOpenRequest: null,
	});
});

test("chat turn IDs work when randomUUID is unavailable on an insecure origin", () => {
	const original = crypto.randomUUID;
	Object.defineProperty(crypto, "randomUUID", { configurable: true, value: undefined });
	try {
		const state = appStoreApi.getState();
		state.openChatSession("p1", "session", null, "medium");
		state.appendUserMessage("session", "hello");
		const turn = appStoreApi.getState().sessions.session?.turns.at(-1);
		expect(turn?.kind).toBe("user");
		expect(turn?.id).toMatch(/^turn-[0-9a-f]{32}$/);
	} finally {
		Object.defineProperty(crypto, "randomUUID", { configurable: true, value: original });
	}
});

test("truncated skill watches keep concurrent session command baselines independent", () => {
	const state = appStoreApi.getState();
	state.openChatSession("p1", "one", null, "medium");
	state.openChatSession("p1", "two", null, "medium");
	state.noteFsChanged({ projectId: "p1", changes: [], truncated: true });
	expect(selectSkillsStale(appStoreApi.getState(), "p1", "one")).toBe(true);
	expect(selectSkillsStale(appStoreApi.getState(), "p1", "two")).toBe(true);
	state.markSkillsSynced("one", 1);
	expect(selectSkillsStale(appStoreApi.getState(), "p1", "one")).toBe(false);
	expect(selectSkillsStale(appStoreApi.getState(), "p1", "two")).toBe(true);
	state.noteCommandCatalogChanged();
	expect(appStoreApi.getState().commandCatalogGeneration).toBe(1);
});

test("live text preserves thinking and parallel tool calls after terminal tool updates", () => {
	let runtime = reduceSessionEvent(EMPTY_RUNTIME, { type: "thinking", text: "planning" });
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "shell-call",
		toolName: "shell_execute",
		tool: { command: "false" },
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "read-call",
		toolName: "filesystem_read",
		tool: { path: "README.md" },
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-end",
		toolCallId: "shell-call",
		status: "failed",
		tool: { error: "exit 1" },
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-end",
		toolCallId: "read-call",
		status: "completed",
		tool: { text: "ok" },
	});
	runtime = reduceSessionEvent(runtime, { type: "text", text: "Finished." });

	const assistant = runtime.turns.find((turn) => turn.kind === "assistant");
	expect(assistant?.kind === "assistant" ? assistant.message.content : []).toEqual([
		{ type: "thinking", thinking: "planning" },
		{
			type: "toolCall",
			id: "shell-call",
			toolName: "shell_execute",
			name: "shell_execute",
			arguments: { command: "false" },
		},
		{
			type: "toolCall",
			id: "read-call",
			toolName: "filesystem_read",
			name: "filesystem_read",
			arguments: { path: "README.md" },
		},
		{ type: "text", text: "Finished." },
	]);
	expect(runtime.toolResults).toMatchObject({
		"shell-call": { status: "error", raw: { error: "exit 1" } },
		"read-call": { status: "done", raw: { text: "ok" } },
	});
});

test("a costless Pi context update is retained before session stats load", () => {
	const contextUsage = { tokens: 32000, contextWindow: 200000, percent: 16 };
	const runtime = reduceSessionEvent(EMPTY_RUNTIME, { type: "context", contextUsage });
	expect(runtime.stats).toMatchObject({
		tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
		cost: 0,
		contextUsage,
	});
});

test("authoritative config events reconcile the visible session model", () => {
	const model = {
		id: "reconciled-model",
		name: "Reconciled model",
		provider: "reconciled-provider",
		available: true,
		hidden: false,
	};
	const runtime = reduceSessionEvent(EMPTY_RUNTIME, {
		type: "config",
		model,
		configOptions: [{ id: "thinking", currentValue: "high" }],
	});
	expect(runtime.model).toEqual(model);
	expect(runtime.thinkingLevel).toBe("high");
});

test("a newer Pi command snapshot replaces the catalog and rejects a delayed refresh", () => {
	const state = appStoreApi.getState();
	state.openChatSession("p1", "commands", null, "medium");
	const revision = appStoreApi.getState().sessions.commands?.commandRevision;
	const command = (name: string) => ({
		name,
		inputHint: `${name} input`,
		source: "pi" as const,
		sourceInfo: {
			path: name,
			source: "Pi",
			scope: "temporary" as const,
			origin: "top-level" as const,
		},
	});
	state.handleAgentEvent({ type: "commands", commands: [command("new")] }, "commands");
	state.setCommands("commands", [command("stale")], revision);
	expect(appStoreApi.getState().sessions.commands?.commands[0]?.name).toBe("new");
	expect(appStoreApi.getState().sessions.commands?.commands[0]?.inputHint).toBe("new input");
	expect(appStoreApi.getState().sessions.commands?.commandRevision).toBe(1);
});

test("live assistant content only merges adjacent compatible blocks", () => {
	let runtime = reduceSessionEvent(EMPTY_RUNTIME, { type: "text", text: "first" });
	runtime = reduceSessionEvent(runtime, { type: "thinking", text: "plan" });
	const beforeThinkingChunk = runtime;
	runtime = reduceSessionEvent(runtime, { type: "thinking", text: "ning" });
	const priorAssistant = beforeThinkingChunk.turns.find((turn) => turn.kind === "assistant");
	expect(priorAssistant?.kind === "assistant" ? priorAssistant.message.content : []).toEqual([
		{ type: "text", text: "first" },
		{ type: "thinking", thinking: "plan" },
	]);
	expect(runtime.currentAssistantId).toBe(beforeThinkingChunk.currentAssistantId);
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "read",
		toolName: "read",
	});
	runtime = reduceSessionEvent(runtime, { type: "thinking", text: "check" });
	runtime = reduceSessionEvent(runtime, { type: "text", text: "last" });
	const assistant = runtime.turns.find((turn) => turn.kind === "assistant");
	expect(assistant?.kind === "assistant" ? assistant.message.content : []).toEqual([
		{ type: "text", text: "first" },
		{ type: "thinking", thinking: "planning" },
		{ type: "toolCall", id: "read", toolName: "read", name: "read", arguments: {} },
		{ type: "thinking", thinking: "check" },
		{ type: "text", text: "last" },
	]);
});

test("live assistant images retain their place between text blocks", () => {
	let runtime = reduceSessionEvent(EMPTY_RUNTIME, { type: "text", text: "before" });
	runtime = reduceSessionEvent(runtime, {
		type: "image",
		image: { type: "image", data: "AA==", mimeType: "image/png" },
	});
	runtime = reduceSessionEvent(runtime, { type: "text", text: "after" });
	const assistant = runtime.turns.find((turn) => turn.kind === "assistant");
	expect(assistant?.kind === "assistant" ? assistant.message.content : []).toEqual([
		{ type: "text", text: "before" },
		{ type: "image", data: "AA==", mimeType: "image/png" },
		{ type: "text", text: "after" },
	]);
});

test("welcome and profile-change snapshots replace connection-scoped agent capabilities", () => {
	appStoreApi
		.getState()
		.installWelcomeSnapshot(77, [project], [project], undefined, genericProfile);
	expect(appStoreApi.getState().agentProfile).toEqual(genericProfile);

	const changed = {
		...genericProfile,
		version: "1.1.0",
		operations: { ...genericProfile.operations, forkSession: true },
	};
	appStoreApi.getState().replaceAgentProfile(changed);
	expect(appStoreApi.getState().agentProfile).toEqual(changed);

	appStoreApi.getState().setStatus("connecting");
	expect(appStoreApi.getState().agentProfile).toBeNull();
	appStoreApi
		.getState()
		.installWelcomeSnapshot(77, [project], [project], undefined, genericProfile);
	expect(appStoreApi.getState().agentProfile).toEqual(genericProfile);
});

test("closing chats releases idle state but retains active work", () => {
	appStoreApi.getState().openChatSession("p1", "idle", null, "medium");
	appStoreApi.getState().openChatSession("p1", "running", null, "medium");
	appStoreApi.getState().handleAgentEvent({ type: "agent_start" }, "running");
	appStoreApi.getState().closeChatToHistory("idle", "p1", false);
	appStoreApi.getState().closeChatToHistory("running", "p1", false);
	const state = appStoreApi.getState();
	expect(state.closedChatsByProjectArea.p1?.map((chat) => chat.sessionId)).toEqual([
		"running",
		"idle",
	]);
	expect(state.sessions.idle).toBeUndefined();
	expect(state.sessions.running?.isStreaming).toBe(true);
});

test("Pi session lifecycle pushes rename and archive local presentation without deleting", () => {
	appStoreApi.getState().openChatSession("p1", "s1", null, "medium");
	appStoreApi
		.getState()
		.applySessionLifecycle({ projectId: "p1", sessionId: "s1", operation: "created" });
	expect(appStoreApi.getState().sessionCatalogVersionByProjectArea.p1).toBe(1);
	appStoreApi.getState().applySessionLifecycle({
		projectId: "p1",
		sessionId: "s1",
		operation: "renamed",
		title: "Focused work",
	});
	expect(appStoreApi.getState().tabsByProjectArea.p1?.[0]?.name).toBe("Focused work");
	appStoreApi.getState().applySessionLifecycle({
		projectId: "p1",
		sessionId: "s1",
		operation: "archived",
	});
	const archived = appStoreApi.getState();
	expect(archived.tabsByProjectArea.p1).toEqual([]);
	expect(archived.sessions.s1).toBeUndefined();
	expect(archived.deletedSessionsByProjectArea.p1?.s1).toBeUndefined();
	archived.applySessionLifecycle({ projectId: "p1", sessionId: "s1", operation: "unarchived" });
	expect(appStoreApi.getState().sessionCatalogVersionByProjectArea.p1).toBe(4);
});

test("authoritative session reconciliation repairs missed chat title pushes", () => {
	appStoreApi.getState().openChatSession("p1", "open", null, "medium");
	appStoreApi.getState().openChatSession("p1", "closed", null, "medium");
	appStoreApi.getState().closeChatToHistory("closed", "p1", false);
	appStoreApi.getState().reconcileProjectAreaSessions(
		"p1",
		["open", "closed"],
		[
			{ sessionId: "open", title: "Renamed open chat", archived: false },
			{ sessionId: "closed", title: "Renamed closed chat", archived: false },
		],
	);
	const state = appStoreApi.getState();
	expect(state.tabsByProjectArea.p1?.find((tab) => tab.kind === "chat")?.name).toBe(
		"Renamed open chat",
	);
	expect(state.closedChatsByProjectArea.p1?.[0]?.title).toBe("Renamed closed chat");
});

test("identical session reconciliation is a no-op", () => {
	appStoreApi.getState().openChatSession("p1", "stable", null, "medium");
	appStoreApi.getState().setCommands("stable", []);
	const queue = { revision: "queue-1", steering: ["one"], followUp: [] };
	appStoreApi
		.getState()
		.reconcileProjectAreaSessions(
			"p1",
			["stable"],
			[{ sessionId: "stable", title: "Chat", archived: false, queue }],
		);
	let notifications = 0;
	const unsubscribe = appStoreApi.subscribe(() => {
		notifications += 1;
	});
	appStoreApi.getState().reconcileProjectAreaSessions(
		"p1",
		["stable"],
		[
			{
				sessionId: "stable",
				title: "Chat",
				archived: false,
				queue: { revision: "queue-1", steering: ["one"], followUp: [] },
			},
		],
	);
	unsubscribe();
	expect(notifications).toBe(0);
});

test("missed archive reconciliation does not tombstone a restorable chat", () => {
	appStoreApi.getState().openChatSession("p1", "archived", null, "medium");
	appStoreApi
		.getState()
		.reconcileProjectAreaSessions(
			"p1",
			["archived"],
			[{ sessionId: "archived", title: "Archived chat", archived: true }],
		);
	expect(appStoreApi.getState().tabsByProjectArea.p1).toEqual([]);
	expect(appStoreApi.getState().deletedSessionsByProjectArea.p1?.archived).toBeUndefined();
	appStoreApi
		.getState()
		.applySessionLifecycle({ projectId: "p1", sessionId: "archived", operation: "unarchived" });
	appStoreApi
		.getState()
		.noteClosedChats("p1", [{ sessionId: "archived", title: "Restored chat", closedAt: 42 }]);
	appStoreApi.getState().reopenChat("p1", "archived");
	expect(appStoreApi.getState().tabsByProjectArea.p1?.[0]).toMatchObject({
		kind: "chat",
		sessionId: "archived",
		name: "Restored chat",
	});
});

test("missed deletion reconciliation tombstones late hydration", () => {
	appStoreApi.getState().openChatSession("p1", "deleted", null, "medium");
	appStoreApi.getState().reconcileProjectAreaSessions("p1", ["deleted"], []);
	expect(appStoreApi.getState().deletedSessionsByProjectArea.p1?.deleted).toBe(true);
	appStoreApi.getState().hydrateSession(
		{
			sessionId: "deleted",
			projectId: "p1",
			cwd: "/workspace",
			title: "Stale chat",
			model: null,
			thinkingLevel: "off",
			isStreaming: false,
			messageCount: 0,
			updatedAt: 42,
			live: false,
			archived: false,
		},
		{
			turns: [],
			toolResults: {},
			askAnswers: {},
			turnIdByMessageIndex: {},
			currentAssistantId: null,
			transcript: null,
			messageCount: 0,
		},
		null,
	);
	expect(appStoreApi.getState().sessions.deleted).toBeUndefined();
	expect(appStoreApi.getState().tabsByProjectArea.p1).toEqual([]);
});

test("session goal state keeps loading, ready, and error transitions isolated to one runtime", () => {
	appStoreApi.getState().openChatSession("p1", "s1", null, "medium");
	appStoreApi.getState().openChatSession("p1", "s2", null, "medium");
	appStoreApi.getState().setSessionGoalLoading("s1", "p1");
	appStoreApi.getState().setSessionGoal("s1", {
		projectId: "p1",
		sessionId: "s1",
		goal: "Ship the focused chat",
		tasks: [],
		updatedAt: 42,
	} satisfies SessionGoal);
	appStoreApi.getState().setSessionGoalError("s2", "p1", "not found");

	expect(appStoreApi.getState().sessions.s1?.goal).toMatchObject({
		projectAreaId: "p1",
		status: "ready",
		goal: "Ship the focused chat",
		updatedAt: 42,
		error: null,
	});
	expect(appStoreApi.getState().sessions.s2?.goal).toMatchObject({
		projectAreaId: "p1",
		status: "error",
		goal: null,
		error: "not found",
	});
});

test("delayed goal and config responses cannot replace newer push events", () => {
	appStoreApi.getState().openChatSession("p1", "ordered", null, "medium");
	const initial = appStoreApi.getState().sessions.ordered;
	expect(initial).toBeDefined();
	const goalRevision = initial?.goalRevision ?? 0;
	const configRevision = initial?.configRevision ?? 0;

	appStoreApi.getState().setSessionGoal("ordered", {
		projectId: "p1",
		sessionId: "ordered",
		goal: "Newer broadcast goal",
		tasks: [],
		updatedAt: 20,
	});
	appStoreApi.getState().handleAgentEvent(
		{
			type: "config",
			model: {
				provider: "new-provider",
				id: "new-model",
				name: "New model",
				available: true,
				hidden: false,
			},
			configOptions: [{ id: "thinking", currentValue: "high" }],
		},
		"ordered",
	);

	appStoreApi.getState().setSessionGoal(
		"ordered",
		{
			projectId: "p1",
			sessionId: "ordered",
			goal: "Stale response goal",
			tasks: [],
			updatedAt: 10,
		},
		goalRevision,
	);
	appStoreApi.getState().setCurrentModel(
		"ordered",
		{
			provider: "old-provider",
			id: "old-model",
			name: "Old model",
			available: true,
			hidden: false,
		},
		configRevision,
	);
	appStoreApi.getState().setThinkingLevel("ordered", "low", configRevision);

	const current = appStoreApi.getState().sessions.ordered;
	expect(current?.goal.goal).toBe("Newer broadcast goal");
	expect(current?.model?.id).toBe("new-model");
	expect(current?.thinkingLevel).toBe("high");
});

test("mutation responses remain a fallback when their push event was missed", () => {
	appStoreApi.getState().openChatSession("p1", "fallback", null, "medium");
	const initial = appStoreApi.getState().sessions.fallback;
	expect(initial).toBeDefined();
	appStoreApi.getState().setSessionGoal(
		"fallback",
		{
			projectId: "p1",
			sessionId: "fallback",
			goal: "Recovered from the response",
			tasks: [],
			updatedAt: 30,
		},
		initial?.goalRevision,
	);
	appStoreApi.getState().setThinkingLevel("fallback", "high", initial?.configRevision);
	expect(appStoreApi.getState().sessions.fallback?.goal.goal).toBe("Recovered from the response");
	expect(appStoreApi.getState().sessions.fallback?.thinkingLevel).toBe("high");
	expect(appStoreApi.getState().sessions.fallback?.goalRevision).toBe(1);
	expect(appStoreApi.getState().sessions.fallback?.configRevision).toBe(1);
});

test("config and plan events update live state while a replay replaces them authoritatively", () => {
	appStoreApi.getState().openChatSession("p1", "mode-plan", null, "medium");
	appStoreApi.getState().handleAgentEvent({ type: "run-start" }, "mode-plan");
	appStoreApi.getState().handleAgentEvent(
		{
			type: "plan",
			planState: {
				entries: [{ content: "Inspect", priority: "high", status: "in_progress" }],
			},
		},
		"mode-plan",
	);
	appStoreApi
		.getState()
		.handleAgentEvent(
			{ type: "config", configOptions: [{ id: "old-setting", currentValue: "old" }] },
			"mode-plan",
		);
	expect(appStoreApi.getState().sessions["mode-plan"]?.planState?.entries[0]?.content).toBe(
		"Inspect",
	);

	appStoreApi.getState().replaceTranscriptSnapshot(
		"mode-plan",
		{
			sessionId: "mode-plan",
			projectId: "p1",
			cwd: "/workspace",
			title: "Mode and plan",
			model: null,
			thinkingLevel: "off",
			isStreaming: false,
			messageCount: 0,
			updatedAt: 42,
			live: true,
			archived: false,
			configOptions: [{ id: "current-setting", currentValue: "current" }],
		},
		{
			turns: [],
			toolResults: {},
			askAnswers: {},
			turnIdByMessageIndex: {},
			currentAssistantId: null,
			transcript: null,
			messageCount: 0,
		},
		null,
	);
	expect(appStoreApi.getState().sessions["mode-plan"]?.planState).toBeNull();
	expect(appStoreApi.getState().sessions["mode-plan"]?.configOptions).toEqual([
		{ id: "current-setting", currentValue: "current" },
	]);
});

test("session hydration restores controller queues and question replies", () => {
	appStoreApi.getState().hydrateSession(
		{
			sessionId: "s1",
			projectId: "p1",
			cwd: "/workspace",
			title: "Queued chat",
			model: null,
			thinkingLevel: "off",
			isStreaming: true,
			messageCount: 0,
			updatedAt: 42,
			live: true,
			archived: false,
			parentSessionId: "parent-session",
			queue: {
				revision: "loaded",
				steering: [],
				followUp: ["continue after refresh"],
				blocked: { lane: "followUp", index: 0, reason: "delivery-uncertain" },
			},
		},
		{
			turns: [],
			toolResults: {},
			askAnswers: {},
			turnIdByMessageIndex: {},
			currentAssistantId: null,
			transcript: null,
			messageCount: 0,
		},
		null,
	);
	const result = { answers: [], cancelled: true };
	appStoreApi.getState().setAskAnswer("s1", "question-1", result);
	expect(appStoreApi.getState().sessions.s1?.queue.followUp).toEqual(["continue after refresh"]);
	expect(appStoreApi.getState().sessions.s1?.queue.revision).toBe("loaded");
	expect(appStoreApi.getState().sessions.s1?.queue.blocked?.reason).toBe("delivery-uncertain");
	appStoreApi.getState().handleAgentEvent(
		{
			type: "queue_update",
			revision: "changed",
			steering: [],
			followUp: ["continue after refresh"],
		},
		"s1",
	);
	expect(appStoreApi.getState().sessions.s1?.queue.revision).toBe("changed");
	expect(appStoreApi.getState().sessions.s1?.queue.blocked).toBeUndefined();
	expect(appStoreApi.getState().sessions.s1?.parentSessionId).toBe("parent-session");
	expect(appStoreApi.getState().sessions.s1?.askAnswers["question-1"]).toEqual(result);
	appStoreApi.getState().reconcileProjectAreaSessions(
		"p1",
		["s1"],
		[
			{
				sessionId: "s1",
				title: "Queued chat",
				archived: false,
				queue: { revision: "reconciled", steering: [], followUp: [] },
			},
		],
	);
	expect(appStoreApi.getState().sessions.s1?.queue.followUp).toEqual([]);
	expect(appStoreApi.getState().sessions.s1?.queue.revision).toBe("reconciled");
	expect(appStoreApi.getState().sessions.s1?.parentSessionId).toBe("parent-session");
});

test("failed submissions retain payload and live work across chat closure", () => {
	const state = appStoreApi.getState();
	state.openChatSession("p1", "retained", null, "off");
	state.handleAgentEvent({ type: "run-start" }, "retained");
	state.appendUserMessage("retained", "Rejected message", []);
	const optimistic = appStoreApi.getState().sessions.retained?.turns.at(-1)?.id;
	if (!optimistic) throw new Error("missing optimistic message");
	const submission = {
		text: "Rejected message",
		attachments: [
			{
				kind: "image" as const,
				name: "image.png",
				content: { type: "image" as const, data: "AA==", mimeType: "image/png" },
			},
		],
		behavior: "queue" as const,
		busy: false,
		error: "Unavailable",
		optimisticTurnId: optimistic,
	};
	state.setSubmission("retained", submission);
	expect(appStoreApi.getState().sessions.retained?.isStreaming).toBe(true);
	expect(
		appStoreApi.getState().sessions.retained?.turns.some((turn) => turn.id === optimistic),
	).toBe(false);
	state.closeChatToHistory("retained", "p1");
	expect(appStoreApi.getState().sessions.retained?.submission).toEqual(submission);
});
