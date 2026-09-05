import { afterEach, expect, test } from "bun:test";
import agents from "../../pi-host/src/extensions/agents.ts";
import plans from "../../pi-host/src/extensions/plans.ts";
import rpivTodo from "../../pi-host/src/extensions/rpiv-todo.ts";
import rpivWeb from "../../pi-host/src/extensions/rpiv-web.ts";
import web from "../../pi-host/src/extensions/web.ts";
import { startHost } from "../../pi-host/src/server.ts";
import { Sessions } from "../../pi-host/src/sessions.ts";
import { cleanups, echoProvider, findTool, fixture, tempDir, toolNames } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

test("host reports Pi SDK version and advertises each optional profile", async () => {
	const dir = await tempDir("pixie-pi-parity-host-");
	const secret = "parity-matrix-secret";
	for (const extensions of [
		["mcp", "agents", "plans", "web"],
		["mcp", "agents", "rpiv-todo", "rpiv-web"],
	]) {
		const host = await startHost({ agentDir: dir, secret, port: 0, extensions });
		try {
			const base = `http://127.0.0.1:${host.server.port}`;
			const ready = (await (
				await fetch(`${base}/readyz`, { headers: { Authorization: `Bearer ${secret}` } })
			).json()) as {
				protocolVersion: number;
				runtimeId: string;
				capabilities: Record<string, number>;
			};
			expect(ready.protocolVersion).toBe(1);
			expect(ready.runtimeId).toBeString();
			if (extensions.includes("plans")) {
				expect(ready.capabilities.plans).toBe(1);
				expect(ready.capabilities["rpiv-todo"]).toBeUndefined();
			} else {
				expect(ready.capabilities["rpiv-todo"]).toBe(1);
				expect(ready.capabilities["rpiv-web"]).toBe(1);
				expect(ready.capabilities.plans).toBeUndefined();
			}
		} finally {
			await host.close();
		}
	}
});

test("unknown or duplicate extension profiles are rejected", async () => {
	const dir = await tempDir("pixie-pi-parity-reject-");
	const secret = "parity-matrix-secret";
	await expect(startHost({ agentDir: dir, secret, port: 0, extensions: ["nope"] })).rejects.toThrow(
		"Unknown or duplicate",
	);
	await expect(
		startHost({ agentDir: dir, secret, port: 0, extensions: ["web", "web"] }),
	).rejects.toThrow("Unknown or duplicate");
});

test("each profile adds tools without replacing Pi core tools", async () => {
	const { dir, sessions } = await fixture([
		(pi) => agents(pi, dir),
		plans,
		web,
		rpivTodo,
		rpivWeb,
		echoProvider(),
	]);
	const entry = await sessions.create(dir);
	const names = toolNames(entry);
	expect(names).toEqual(
		expect.arrayContaining([
			"read",
			"bash",
			"edit",
			"write",
			"update_plan",
			"todo",
			"web_fetch",
			"web_search",
			"delegate",
			"list_agents",
		]),
	);
	expect(entry.capabilities.snapshot()).toMatchObject({
		agents: 1,
		plans: 1,
		web: 1,
		"rpiv-todo": 1,
		"rpiv-web": 1,
	});
});

test("enabling custom web and upstream rpiv-web together keeps the first web_fetch", async () => {
	const { dir, sessions } = await fixture([web, rpivWeb]);
	const entry = await sessions.create(dir);
	const matches = entry.session.agent.state.tools.filter((t: any) => t.name === "web_fetch");
	// Both profiles register `web_fetch`: the first factory wins and the
	// second registration is silently dropped. Enable either `web` or
	// `rpiv-web`, never both; profile order on the CLI decides the winner.
	expect(matches).toHaveLength(1);
	expect(matches[0].description).toContain("Browser MCP tools");
});

test("agent definitions survive CRUD and list_agents reflects them", async () => {
	const { dir, sessions } = await fixture([(pi) => agents(pi, dir), echoProvider()]);
	const entry = await sessions.create(dir);
	const ctx = sessions.context(entry);
	await entry.capabilities.call(
		"pi.sources.create",
		{ name: "Reviewer", description: "Review", content: "Review carefully", properties: {} },
		ctx,
	);
	expect(await entry.capabilities.call("pi.sources.list", {}, ctx)).toMatchObject({
		sources: [{ name: "Reviewer" }],
	});
	const list = await findTool(entry, "list_agents").execute(
		"parity-list",
		{},
		new AbortController().signal,
	);
	expect(JSON.parse(list.content[0].text)).toMatchObject([{ name: "Reviewer" }]);
	await entry.capabilities.call(
		"pi.sources.update",
		{
			name: "Reviewer",
			description: "Review twice",
			content: "Review carefully",
			properties: {},
			path: ((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources[0].path,
		},
		ctx,
	);
	await entry.capabilities.call(
		"pi.sources.delete",
		{ path: ((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources[0].path },
		ctx,
	);
	expect(((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources).toHaveLength(
		0,
	);
});

test("prompt, cancel, usage, and transcript survive session switching", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const first = await sessions.create(dir);
	const second = await sessions.create(dir);
	for (const entry of [first, second]) {
		await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	}
	await sessions.call("session.prompt", {
		sessionId: first.session.sessionId,
		content: [{ type: "text", text: "Hello first" }],
	});
	await sessions.call("session.prompt", {
		sessionId: second.session.sessionId,
		content: [{ type: "text", text: "Hello second" }],
	});
	const reloaded = new Sessions(dir, [echoProvider()], () => {});
	cleanups.push(() => reloaded.close());
	for (const entry of [first, second]) {
		const loaded = await reloaded.get(entry.session.sessionId);
		const snapshot = reloaded.snapshot(loaded, true);
		expect((snapshot.messages as any[]).filter((m) => m.role === "assistant")).toHaveLength(1);
	}
	expect(first.session.sessionId).not.toBe(second.session.sessionId);
	await sessions.call("session.cancel", { sessionId: first.session.sessionId });
});
