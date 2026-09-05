import { afterEach, expect, test } from "bun:test";
import plans from "../../pi-host/src/extensions/plans.ts";
import rpivTodo from "../../pi-host/src/extensions/rpiv-todo.ts";
import { Sessions } from "../../pi-host/src/sessions.ts";
import { cleanups, findTool, fixture } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

test("custom update_plan writes pixie-plan entries readable via plans.read", async () => {
	const { dir, sessions } = await fixture([plans]);
	const entry = await sessions.create(dir);
	const result = await findTool(entry, "update_plan").execute(
		"parity-plan",
		{ entries: [{ content: "Research", priority: "high", status: "in_progress" }] },
		new AbortController().signal,
	);
	expect(result.details).toMatchObject({
		plan: { entries: [{ content: "Research", priority: "high", status: "in_progress" }] },
	});
	expect(await entry.capabilities.call("plans.read", {}, sessions.context(entry))).toMatchObject({
		entries: [{ content: "Research" }],
	});
	// The custom plan survives reload by replaying pixie-plan branch entries.
	const reloaded = new Sessions(dir, [plans], () => {});
	cleanups.push(() => reloaded.close());
	const loaded = await reloaded.get(entry.session.sessionId);
	expect(await loaded.capabilities.call("plans.read", {}, reloaded.context(loaded))).toMatchObject({
		entries: [{ content: "Research", status: "in_progress" }],
	});
});

test("upstream todo returns details.tasks/details.nextId with per-action envelopes", async () => {
	const { dir, sessions } = await fixture([rpivTodo]);
	const entry = await sessions.create(dir);
	const tool = findTool(entry, "todo");
	const signal = new AbortController().signal;
	const created = await tool.execute(
		"parity-todo-create",
		{ action: "create", subject: "Research existing tool" },
		signal,
	);
	expect(created.details.action).toBe("create");
	expect(created.details.nextId).toBe(2);
	expect(created.details.tasks).toMatchObject([
		{ id: 1, subject: "Research existing tool", status: "pending" },
	]);
	const updated = await tool.execute(
		"parity-todo-update",
		{ action: "update", id: 1, status: "in_progress", activeForm: "researching existing tool" },
		signal,
	);
	expect(updated.details.tasks).toMatchObject([{ id: 1, status: "in_progress" }]);
	const listed = await tool.execute("parity-todo-list", { action: "list" }, signal);
	expect(listed.details.tasks).toHaveLength(1);
	expect(listed.details.nextId).toBe(2);
});

test("upstream todo records dependencies and rejects cycles", async () => {
	const { dir, sessions } = await fixture([rpivTodo]);
	const entry = await sessions.create(dir);
	const tool = findTool(entry, "todo");
	const signal = new AbortController().signal;
	await tool.execute("parity-dep-a", { action: "create", subject: "First step" }, signal);
	await tool.execute(
		"parity-dep-b",
		{ action: "create", subject: "Second step", blockedBy: [1] },
		signal,
	);
	const listed = await tool.execute("parity-dep-list", { action: "list" }, signal);
	expect(listed.details.tasks).toMatchObject([{ id: 1 }, { id: 2, blockedBy: [1] }]);
	const cycle = await tool.execute(
		"parity-dep-cycle",
		{ action: "update", id: 1, addBlockedBy: [2] },
		signal,
	);
	expect(cycle.details.error).toBeString();
	expect((cycle.content[0] as any).text).toMatch(/cycle/i);
});

test("upstream todo carries priority in task metadata and tombstones deletes", async () => {
	const { dir, sessions } = await fixture([rpivTodo]);
	const entry = await sessions.create(dir);
	const tool = findTool(entry, "todo");
	const signal = new AbortController().signal;
	await tool.execute(
		"parity-meta",
		{ action: "create", subject: "Urgent step", metadata: { priority: "high" } },
		signal,
	);
	await tool.execute("parity-meta-plain", { action: "create", subject: "Normal step" }, signal);
	const listed = await tool.execute("parity-meta-list", { action: "list" }, signal);
	expect(listed.details.tasks).toMatchObject([
		{ id: 1, metadata: { priority: "high" } },
		{ id: 2 },
	]);
	await tool.execute("parity-meta-delete", { action: "delete", id: 2 }, signal);
	// The tombstone stays in details.tasks (the persistence snapshot) while the
	// rendered list hides it by default. The Go projection must skip deleted.
	const visible = await tool.execute("parity-meta-visible", { action: "list" }, signal);
	expect(visible.details.tasks).toMatchObject([{ id: 1 }, { id: 2, status: "deleted" }]);
	expect((visible.content[0] as any).text).toContain("#1");
	expect((visible.content[0] as any).text).not.toContain("#2");
	const withDeleted = await tool.execute(
		"parity-meta-all",
		{ action: "list", includeDeleted: true },
		signal,
	);
	expect((withDeleted.content[0] as any).text).toContain("#2");
});

test("upstream todo replays from the session branch after reload", async () => {
	const { dir, sessions } = await fixture([rpivTodo]);
	const entry = await sessions.create(dir);
	const id = entry.session.sessionId;
	const tool = findTool(entry, "todo");
	const signal = new AbortController().signal;
	await tool.execute("parity-replay-a", { action: "create", subject: "First step" }, signal);
	const second = await tool.execute(
		"parity-replay-b",
		{ action: "create", subject: "Second step", blockedBy: [1] },
		signal,
	);
	// Direct tool.execute bypasses the agent loop, so persist a toolResult in
	// the exact shape the loop writes (agent-loop createToolResultMessage).
	// Reload replays the branch through the same session_start path as
	// compaction and /reload.
	entry.session.sessionManager.appendMessage({
		role: "toolResult",
		toolCallId: "parity-replay-b",
		toolName: "todo",
		content: second.content,
		details: second.details,
		isError: false,
		timestamp: Date.now(),
	});
	await sessions.close();
	const reloaded = new Sessions(dir, [rpivTodo], () => {});
	cleanups.push(() => reloaded.close());
	const loaded = await reloaded.get(id);
	const listed = await findTool(loaded, "todo").execute(
		"parity-replay-list",
		{ action: "list" },
		signal,
	);
	expect(listed.details.nextId).toBe(3);
	expect(listed.details.tasks).toMatchObject([
		{ id: 1, subject: "First step" },
		{ id: 2, subject: "Second step", blockedBy: [1] },
	]);
});

test("upstream todo keeps per-session state isolated", async () => {
	const { dir, sessions } = await fixture([rpivTodo]);
	const first = await sessions.create(dir);
	const second = await sessions.create(dir);
	const signal = new AbortController().signal;
	await findTool(first, "todo").execute(
		"parity-iso",
		{ action: "create", subject: "First only" },
		signal,
	);
	const listed = await findTool(second, "todo").execute(
		"parity-iso-list",
		{ action: "list" },
		signal,
	);
	expect(listed.details.tasks).toHaveLength(0);
	expect(listed.details.nextId).toBe(1);
});

test("custom plans and upstream todo coexist for dual-read migration", async () => {
	const { dir, sessions } = await fixture([plans, rpivTodo]);
	const entry = await sessions.create(dir);
	const signal = new AbortController().signal;
	await findTool(entry, "update_plan").execute(
		"parity-dual-plan",
		{ entries: [{ content: "Legacy plan", priority: "medium", status: "pending" }] },
		signal,
	);
	const created = await findTool(entry, "todo").execute(
		"parity-dual-todo",
		{ action: "create", subject: "Upstream task" },
		signal,
	);
	expect(created.details.nextId).toBe(2);
	expect(await entry.capabilities.call("plans.read", {}, sessions.context(entry))).toMatchObject({
		entries: [{ content: "Legacy plan" }],
	});
});
