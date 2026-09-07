import { expect, test } from "bun:test";
import { mkdtemp, realpath, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js";
import {
	CallToolRequestSchema,
	ListToolsRequestSchema,
	ReadResourceRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { Sessions } from "../../pi-host/src/sessions.ts";
import mcpExtension from "../../pi-mcp/src/index.ts";

test("MCP tools, App resources and connection removal remain scoped to the extension", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-pi-mcp-");
	async function serve(token = "") {
		const mcp = new Server(
			{ name: "fixture", version: "1.0.0" },
			{ capabilities: { tools: {}, resources: {} } },
		);
		mcp.setRequestHandler(ListToolsRequestSchema, async () => ({
			tools: [
				{
					name: "show",
					description: "Show fixture",
					inputSchema: { type: "object" },
					_meta: { ui: { resourceUri: "ui://fixture/app" } },
				},
			],
		}));
		mcp.setRequestHandler(CallToolRequestSchema, async () => ({
			content: [{ type: "text", text: "Tool completed" }],
			structuredContent: { ok: true },
		}));
		mcp.setRequestHandler(ReadResourceRequestSchema, async () => ({
			contents: [
				{
					uri: "ui://fixture/app",
					mimeType: "text/html;profile=mcp-app",
					text: "<p>Fixture App</p>",
				},
			],
		}));
		const transport = new WebStandardStreamableHTTPServerTransport({
			sessionIdGenerator: () => crypto.randomUUID(),
			enableJsonResponse: true,
		});
		await mcp.connect(transport);
		const http = Bun.serve({
			port: 0,
			hostname: "127.0.0.1",
			fetch: (request) =>
				token && request.headers.get("X-Fixture") !== token
					? new Response("Unauthorized", { status: 401 })
					: transport.handleRequest(request),
		});

		return { mcp, http };
	}
	const { mcp, http } = await serve();
	const rotated = await serve("rotated");
	const sessions = new Sessions(dir, [(pi) => mcpExtension(pi, dir)], () => {});
	try {
		const entry = await sessions.create(dir),
			ctx = sessions.context(entry);
		await entry.capabilities.call(
			"mcp.attach",
			{
				servers: [
					{ name: "fixture", type: "http", url: `http://127.0.0.1:${http.port}/mcp`, headers: [] },
				],
			},
			ctx,
		);
		expect(entry.session.getActiveToolNames()).toContain("fixture__show");
		expect(entry.session.getActiveToolNames()).toContain("bash");
		const tool = entry.session.agent.state.tools.find((t) => t.name === "fixture__show")!;
		const result = await tool.execute("test-call", {}, new AbortController().signal);
		expect(result).toMatchObject({
			content: [{ type: "text", text: "Tool completed" }],
			details: {
				mcp: {
					app: { extensionName: "fixture", toolName: "show", resourceUri: "ui://fixture/app" },
				},
			},
		});
		expect(
			await entry.capabilities.call(
				"pi.resources.read",
				{ extensionName: "fixture", uri: "ui://fixture/app" },
				ctx,
			),
		).toMatchObject({ result: { contents: [{ text: "<p>Fixture App</p>" }] } });
		expect(
			await entry.capabilities.call("pi.tools.call", { name: "fixture__show", arguments: {} }, ctx),
		).toMatchObject({ isError: false, structuredContent: { ok: true } });
		expect(await entry.capabilities.call("pi.session.extensions.list", {}, ctx)).toMatchObject({
			extensions: [{ extensionKey: "fixture", extension: { type: "mcp" } }],
		});
		await entry.capabilities.call(
			"mcp.attach",
			{
				servers: [
					{
						name: "fixture",
						url: `http://127.0.0.1:${rotated.http.port}/mcp`,
						headers: { "X-Fixture": "rotated" },
					},
				],
			},
			ctx,
		);
		expect(entry.session.getActiveToolNames()).toContain("fixture__show");
		expect(await tool.execute("rotated-call", {}, new AbortController().signal)).toMatchObject({
			content: [{ type: "text", text: "Tool completed" }],
		});
		await entry.capabilities.call(
			"pi.config.extensions.add",
			{
				extension: {
					name: "fixture",
					url: `http://127.0.0.1:${rotated.http.port}/mcp`,
					headers: { "X-Fixture": "rotated" },
				},
			},
			ctx,
		);
		await entry.capabilities.call("pi.session.extensions.remove", { extensionKey: "fixture" }, ctx);
		expect(entry.session.getActiveToolNames()).not.toContain("fixture__show");
		expect(entry.session.getActiveToolNames()).toContain("bash");
		await sessions.close();
		const restored = new Sessions(dir, [(pi) => mcpExtension(pi, dir)], () => {});
		try {
			const reopened = await restored.get(entry.session.sessionId);
			expect(reopened.session.getActiveToolNames()).not.toContain("fixture__show");
			expect(reopened.session.getActiveToolNames()).toContain("bash");
		} finally {
			await restored.close();
		}
	} finally {
		await sessions.close();
		await rotated.mcp.close();
		await rotated.http.stop(true);
		await mcp.close();
		await http.stop(true);
		await rm(dir, { recursive: true, force: true });
	}
});

test("the MCP extension loads standalone in vanilla Pi and honors stdio cwd, environment and tool failures", async () => {
	const { writeFile } = await import("node:fs/promises");
	const {
		createAgentSession,
		DefaultResourceLoader,
		SessionManager,
		SettingsManager,
		ModelRuntime,
	} = await import("@earendil-works/pi-coding-agent");
	const dir = await mkdtemp(tmpdir() + "/pi-mcp-stdio-");
	const models = await ModelRuntime.create({
		authPath: dir + "/auth.json",
		modelsPath: dir + "/models.json",
		allowModelNetwork: false,
	});
	const settings = SettingsManager.create(dir, dir);
	await writeFile(
		dir + "/mcp.json",
		JSON.stringify({
			"invalid:name": { type: "stdio", command: "unused" },
			fixture: {
				type: "stdio",
				command: process.execPath,
				args: [new URL("./mcp-stdio-fixture.ts", import.meta.url).pathname],
				cwd: dir,
				env: { MCP_FIXTURE: "standalone" },
			},
		}),
	);
	const loader = new DefaultResourceLoader({
		cwd: dir,
		agentDir: dir,
		settingsManager: settings,
		extensionFactories: [(pi) => mcpExtension(pi, dir)],
	});
	await loader.reload();
	const { session } = await createAgentSession({
		cwd: dir,
		agentDir: dir,
		modelRuntime: models,
		settingsManager: settings,
		resourceLoader: loader,
		sessionManager: SessionManager.inMemory(dir),
	});
	try {
		await session.bindExtensions({ mode: "rpc" });
		expect(session.getActiveToolNames()).toContain("bash");
		const tool = session.agent.state.tools.find((t) => t.name === "fixture__echo");
		if (!tool) throw new Error("MCP tool unavailable");
		expect(await tool.execute("stdio", {}, new AbortController().signal)).toMatchObject({
			content: [{ type: "text", text: `standalone:${await realpath(dir)}` }],
		});
		await expect(
			tool.execute("error", { fail: true }, new AbortController().signal),
		).rejects.toThrow("Expected fixture error");
	} finally {
		await session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" });
		session.dispose();
		await rm(dir, { recursive: true, force: true });
	}
});
