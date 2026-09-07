import { afterEach, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";
import { Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";

const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
	for (const close of cleanup.splice(0).reverse()) await close();
});

test("unfamiliar native user/project extensions load once and execute UI and tools without markers", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-loading-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"),
		cwd = join(root, "project");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(join(cwd, ".pi", "extensions"), { recursive: true });
	const userPath = join(agentDir, "extensions", "unfamiliar.js");
	const projectPath = join(cwd, ".pi", "extensions", "project.js");
	await writeFile(
		userPath,
		`export default pi => {
		pi.registerTool({ name: "unfamiliar_dialog", label: "Unfamiliar", description: "Native fixture",
			parameters: { type: "object", properties: {} },
			execute: async (_id, _params, _signal, onUpdate, ctx) => {
				onUpdate?.({ content: [{ type: "text", text: "waiting" }], details: {} });
				const answer = await ctx.ui.select("Unfamiliar choice", ["alpha", "beta"]);
				return { content: [{ type: "text", text: answer }], details: { answer, mode: ctx.mode } };
			}
		});
		pi.on("session_start", (_event, ctx) => ctx.ui.notify("user loaded"));
	};`,
	);
	await writeFile(
		projectPath,
		`export default pi => {
		pi.registerCommand("unfamiliar-project", { description: "Project fixture", handler: async () => {} });
		pi.on("session_start", (_event, ctx) => ctx.ui.notify("project loaded"));
	};`,
	);
	// The same user file is both discovered and explicitly configured. Native
	// path deduplication, rather than a Pixie package-name table, owns this case.
	const settings = JSON.stringify({ extensions: [userPath], defaultThinkingLevel: "high" });
	await writeFile(join(agentDir, "settings.json"), settings);
	// Project extensions need an explicit stored trust decision, exactly as
	// native Pi requires before executing project-local code.
	new ProjectTrustStore(agentDir).set(cwd, true);
	const events: any[] = [];
	const sessions = new Sessions(agentDir, [], (_id, event) => events.push(event));
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const inventory = sessions.inventory(entry);
	expect(inventory.errors).toEqual([]);
	expect(
		inventory.extensions.filter((extension) => extension.resolvedPath === userPath),
	).toHaveLength(1);
	expect(
		inventory.extensions.find((extension) => extension.resolvedPath === userPath)?.source.scope,
	).toBe("user");
	expect(
		inventory.extensions.find((extension) => extension.resolvedPath === projectPath)?.source.scope,
	).toBe("project");
	for (const message of ["user loaded", "project loaded"]) {
		expect(
			events.filter((event) => event.type === "pixie:ui:notify" && event.message === message),
		).toHaveLength(1);
	}
	expect(entry.capabilities.snapshot()).toEqual({ agents: 1 });
	const tool = entry.session.agent.state.tools.find((tool) => tool.name === "unfamiliar_dialog")!;
	const updates: unknown[] = [];
	const pending = tool.execute("unfamiliar-call", {}, new AbortController().signal, (update) =>
		updates.push(update),
	);
	for (let i = 0; i < 100 && !events.some((event) => event.type === "pixie:ui:request"); i++)
		await Bun.sleep(5);
	const request = events.find((event) => event.type === "pixie:ui:request");
	expect(request).toMatchObject({ primitive: "select", options: ["alpha", "beta"] });
	await sessions.call("session.uiResponse", {
		sessionId: entry.session.sessionId,
		requestId: request.requestId,
		value: "beta",
	});
	expect(await pending).toMatchObject({
		content: [{ text: "beta" }],
		details: { answer: "beta", mode: "rpc" },
	});
	expect(updates).toHaveLength(1);
	expect(await readFile(join(agentDir, "settings.json"), "utf8")).toBe(settings);
});

test("untrusted project extensions never execute while user extensions load", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-trust-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	const agentDir = join(root, "agent"),
		cwd = join(root, "project"),
		marker = join(root, "project-factory-ran");
	await mkdir(join(agentDir, "extensions"), { recursive: true });
	await mkdir(join(cwd, ".pi", "extensions"), { recursive: true });
	await writeFile(
		join(agentDir, "extensions", "user.js"),
		`export default pi => {
			pi.registerTool({ name: "user_probe", description: "User fixture",
				parameters: { type: "object", properties: {} },
				execute: async () => ({ content: [{ type: "text", text: "user" }] }) });
		};`,
	);
	await writeFile(
		join(cwd, ".pi", "extensions", "project.js"),
		`import { appendFileSync } from "node:fs";
		export default pi => {
			appendFileSync(${JSON.stringify(marker)}, "ran\\n");
			pi.registerTool({ name: "project_probe", description: "Project fixture",
				parameters: { type: "object", properties: {} },
				execute: async () => ({ content: [{ type: "text", text: "project" }] }) });
		};`,
	);
	const events: any[] = [];
	const sessions = new Sessions(agentDir, [], (_id, event) => events.push(event));
	cleanup.push(() => sessions.close());
	// No stored trust decision: native Pi would skip project-local code, and
	// so must the assistant. The user extension is unaffected.
	const entry = await sessions.create(cwd);
	expect(entry.session.getActiveToolNames()).toContain("user_probe");
	expect(entry.session.getActiveToolNames()).not.toContain("project_probe");
	expect(existsSync(marker)).toBe(false);
	expect(
		sessions.inventory(entry).extensions.some((extension) => extension.resolvedPath.endsWith("project.js")),
	).toBe(false);
	// Recording trust through the native store enables project extensions on
	// the next session, with no other configuration change.
	new ProjectTrustStore(agentDir).set(cwd, true);
	const trusted = await sessions.create(cwd);
	expect(trusted.session.getActiveToolNames()).toContain("project_probe");
});

test("native load failures remain visible instead of becoming an empty successful inventory", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-errors-"));
	cleanup.push(() => rm(root, { recursive: true, force: true }));
	await mkdir(join(root, "extensions"));
	await writeFile(
		join(root, "extensions", "broken.js"),
		'export default () => { throw new Error("fixture initialization failed"); };',
	);
	const sessions = new Sessions(root, [], () => {});
	cleanup.push(() => sessions.close());
	const entry = await sessions.create(root);
	expect(sessions.inventory(entry).errors).toEqual([
		expect.objectContaining({ error: expect.stringContaining("fixture initialization failed") }),
	]);
	expect(entry.session.getActiveToolNames()).toContain("read");
});
