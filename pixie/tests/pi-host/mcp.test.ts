import { expect, test } from "bun:test";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join as joinPath } from "node:path";
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
					name: "alpha__beta",
					description: "Separator tool",
					inputSchema: { type: "object" },
				},
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
		expect(
			await entry.capabilities.call(
				"pi.tools.call",
				{ extensionName: "fixture", toolName: "alpha__beta" },
				ctx,
			),
		).toMatchObject({ isError: false });
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
		await expect(
			entry.capabilities.call(
				"pi.session.extensions.add",
				{
					extension: {
						name: "fixture",
						url: `http://127.0.0.1:${rotated.http.port}/mcp`,
						headers: { "X-Fixture": "wrong" },
					},
				},
				ctx,
			),
		).rejects.toThrow();
		expect(
			await tool.execute("after-failed-replacement", {}, new AbortController().signal),
		).toMatchObject({ content: [{ type: "text", text: "Tool completed" }] });
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

test("malformed MCP entries remain removable and do not hide valid inventory", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pi-mcp-invalid-`);
	await writeFile(
		joinPath(dir, "mcp.json"),
		JSON.stringify({
			broken: { url: "invalid" },
			disabled: { url: "http://localhost:9/mcp", enabled: false },
		}),
	);
	const sessions = new Sessions(dir, [(pi) => mcpExtension(pi, dir)], () => {});
	try {
		const entry = await sessions.create(dir);
		const ctx = sessions.context(entry);
		const inventory = (await entry.capabilities.call("pi.config.extensions.list", {}, ctx)) as {
			extensions: { configKey: string; invalid?: boolean }[];
		};
		expect(inventory.extensions).toHaveLength(2);
		expect(inventory.extensions.find((e) => e.configKey === "broken")?.invalid).toBe(true);
		await entry.capabilities.call("pi.config.extensions.remove", { configKey: "broken" }, ctx);
		expect(
			(
				(await entry.capabilities.call("pi.config.extensions.list", {}, ctx)) as {
					extensions: unknown[];
				}
			).extensions,
		).toHaveLength(1);
		expect(entry.session.getActiveToolNames()).toContain("bash");
	} finally {
		await sessions.close();
		await rm(dir, { recursive: true, force: true });
	}
});

test("standalone MCP supports authenticated SSE and cancels a blocked tool", async () => {
	const { createServer } = await import("node:http");
	const { SSEServerTransport } = await import("@modelcontextprotocol/sdk/server/sse.js");
	const dir = await mkdtemp(tmpdir() + "/pixie-sse-");
	const server = new Server({ name: "sse-fixture", version: "1" }, { capabilities: { tools: {} } });
	server.setRequestHandler(ListToolsRequestSchema, async () => ({
		tools: [{ name: "wait__here", inputSchema: { type: "object" } }],
	}));
	let cancelled = false;
	server.setRequestHandler(CallToolRequestSchema, async (_request, extra) => {
		await new Promise<void>((resolve) =>
			extra.signal.addEventListener(
				"abort",
				() => {
					cancelled = true;
					resolve();
				},
				{ once: true },
			),
		);
		return { content: [{ type: "text", text: "Cancelled" }] };
	});
	let transport: InstanceType<typeof SSEServerTransport> | undefined;
	const http = createServer(async (request, response) => {
		if (request.headers.authorization !== "Bearer fixture") {
			response.writeHead(401).end();
			return;
		}
		if (request.method === "GET") {
			transport = new SSEServerTransport("/messages", response);
			await server.connect(transport);
		} else if (transport) await transport.handlePostMessage(request, response);
		else response.writeHead(404).end();
	});
	await new Promise<void>((resolve) => http.listen(0, "127.0.0.1", resolve));
	const address = http.address();
	if (!address || typeof address === "string") throw new Error("No listener");
	await writeFile(
		joinPath(dir, "mcp.json"),
		JSON.stringify({
			sse: {
				type: "sse",
				url: `http://127.0.0.1:${address.port}/sse`,
				headers: { Authorization: "Bearer fixture" },
			},
		}),
	);
	const sessions = new Sessions(dir, [(pi) => mcpExtension(pi, dir)], () => {});
	try {
		const entry = await sessions.create(dir);
		expect(entry.session.getActiveToolNames()).toContain("sse__wait__here");
		const abort = new AbortController();
		const call = entry.capabilities.call(
			"pi.tools.call",
			{ extensionName: "sse", toolName: "wait__here" },
			{ ...sessions.context(entry), signal: abort.signal },
		);
		setTimeout(() => abort.abort(), 50);
		await expect(call).rejects.toThrow();
		for (let i = 0; i < 20 && !cancelled; i++) await Bun.sleep(10);
		expect(cancelled).toBe(true);
	} finally {
		await sessions.close();
		await server.close();
		await new Promise<void>((resolve, reject) =>
			http.close((error) => (error ? reject(error) : resolve())),
		);
		await rm(dir, { recursive: true, force: true });
	}
});
