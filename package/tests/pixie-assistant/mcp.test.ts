import { afterEach, expect, test } from "bun:test";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join as joinPath } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { WebStandardStreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js";
import { CallToolRequestSchema, ListToolsRequestSchema } from "@modelcontextprotocol/sdk/types.js";
import { Sessions } from "../../../assistant/src/sessions.ts";
import { piMcpAdapterWithConfig } from "../pi-native-parity/upstream.ts";

const mcpExtension = (pi: ExtensionAPI, dir: string) =>
	piMcpAdapterWithConfig({ agentDir: dir })(pi);

const priorDir = process.env.PI_CODING_AGENT_DIR;
const priorViewer = process.env.MCP_UI_VIEWER;
function isolate(dir: string) {
	process.env.PI_CODING_AGENT_DIR = dir;
	process.env.MCP_UI_VIEWER = "none";
}
afterEach(() => {
	if (priorDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
	else process.env.PI_CODING_AGENT_DIR = priorDir;
	if (priorViewer === undefined) delete process.env.MCP_UI_VIEWER;
	else process.env.MCP_UI_VIEWER = priorViewer;
});

test("MCP tools and connection removal remain scoped to the extension", async () => {
	const dir = await mkdtemp(tmpdir() + "/pixie-pi-mcp-");
	isolate(dir);
	async function serve(token = "", header = "X-Fixture") {
		let expected = token;
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
		const transport = new WebStandardStreamableHTTPServerTransport({
			sessionIdGenerator: () => crypto.randomUUID(),
			enableJsonResponse: true,
		});
		await mcp.connect(transport);
		const http = Bun.serve({
			port: 0,
			hostname: "127.0.0.1",
			fetch: (request) =>
				expected && request.headers.get(header) !== expected
					? new Response("Unauthorized", { status: 401 })
					: transport.handleRequest(request),
		});

		return { mcp, http, setToken: (value: string) => (expected = value) };
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
		expect(entry.session.getActiveToolNames()).toContain("mcp");
		expect(entry.session.getActiveToolNames()).not.toContain("fixture__show");
		await expect(
			entry.capabilities.call(
				"pi.tools.call",
				{ extensionName: "fixture", toolName: "alpha__beta" },
				ctx,
			),
		).rejects.toThrow("BRIDGE-02 blocker");
		expect(entry.session.getActiveToolNames()).toContain("bash");
		const tool = entry.session.agent.state.tools.find((t) => t.name === "mcp")!;
		const args = { server: "fixture", tool: "show", args: {} };
		const result = await tool.execute("test-call", args, new AbortController().signal);
		expect(result).toMatchObject({
			details: {
				mcpResult: { structuredContent: { ok: true } },
			},
		});
		await expect(
			entry.capabilities.call("pi.tools.call", { name: "fixture__show", arguments: {} }, ctx),
		).rejects.toThrow("BRIDGE-02 blocker");
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
			await tool.execute("after-failed-replacement", args, new AbortController().signal),
		).toMatchObject({ details: { mcpResult: { structuredContent: { ok: true } } } });
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
		// A changed attachment cannot replace an existing registration silently.
		expect(
			await tool.execute("original-registration", args, new AbortController().signal),
		).toMatchObject({
			details: { mcpResult: { structuredContent: { ok: true } } },
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

test("same-name Canvas attachment replaces revoked credentials without retaining the old client", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-pi-mcp-canvas-replace-`);
	isolate(dir);
	const canvas = await (async () => {
		const active = new Set<Server>();
		const makeServer = () => {
			const mcp = new Server(
				{ name: "canvas-fixture", version: "1.0.0" },
				{ capabilities: { tools: {}, resources: {} } },
			);
			mcp.setRequestHandler(ListToolsRequestSchema, async () => ({
				tools: [
					{ name: "show", description: "Show Canvas fixture", inputSchema: { type: "object" } },
				],
			}));
			mcp.setRequestHandler(CallToolRequestSchema, async () => ({
				content: [{ type: "text", text: "Canvas completed" }],
				structuredContent: { ok: true },
			}));
			return mcp;
		};
		let expected = "Bearer revoked";
		const seenAuth: string[] = [];
		const http = Bun.serve({
			port: 0,
			hostname: "127.0.0.1",
			fetch: async (request) => {
				const authorization = request.headers.get("Authorization") ?? "";
				seenAuth.push(authorization);
				if (authorization !== expected) return new Response("Unauthorized", { status: 401 });
				const mcp = makeServer();
				active.add(mcp);
				const transport = new WebStandardStreamableHTTPServerTransport({
					sessionIdGenerator: undefined,
					enableJsonResponse: true,
				});
				await mcp.connect(transport);
				return transport.handleRequest(request);
			},
		});
		return {
			mcp: { close: async () => Promise.allSettled([...active].map((mcp) => mcp.close())) },
			http,
			setToken: (value: string) => (expected = `Bearer ${value}`),
			seenAuth,
		};
	})();
	const sessions = new Sessions(dir, [(pi) => mcpExtension(pi, dir)], () => {});
	try {
		const entry = await sessions.create(dir);
		const context = sessions.context(entry);
		const attach = (token: string) =>
			entry.capabilities.call(
				"mcp.attach",
				{
					servers: [
						{
							name: "pixie-canvas",
							type: "http",
							url: `http://127.0.0.1:${canvas.http.port}/mcp`,
							headers: { Authorization: `Bearer ${token}` },
						},
					],
				},
				context,
			);
		const tool = entry.session.agent.state.tools.find((candidate) => candidate.name === "mcp");
		if (!tool) throw new Error("MCP tool unavailable");
		expect(await attach("revoked")).toEqual({ ok: true, unavailable: [] });
		expect(
			await tool.execute(
				"canvas-before-replacement",
				{ server: "pixie-canvas", tool: "show", args: {} },
				new AbortController().signal,
			),
		).toMatchObject({ details: { mcpResult: { structuredContent: { ok: true } } } });
		const beforeReplacement = canvas.seenAuth.length;
		canvas.setToken("fresh");
		expect(await attach("fresh")).toEqual({ ok: true, unavailable: [] });
		expect(
			await tool.execute(
				"canvas-after-replacement",
				{ server: "pixie-canvas", tool: "show", args: {} },
				new AbortController().signal,
			),
		).toMatchObject({ details: { mcpResult: { structuredContent: { ok: true } } } });
		expect(canvas.seenAuth.slice(beforeReplacement)).not.toContain("Bearer revoked");
		expect(canvas.seenAuth.slice(beforeReplacement)).toContain("Bearer fresh");
	} finally {
		await sessions.close();
		await canvas.mcp.close();
		await canvas.http.stop(true);
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
	isolate(dir);
	const models = await ModelRuntime.create({
		authPath: dir + "/auth.json",
		modelsPath: dir + "/models.json",
		allowModelNetwork: false,
	});
	const settings = SettingsManager.create(dir, dir);
	await writeFile(
		dir + "/mcp.json",
		JSON.stringify({
			mcpServers: {
				fixture: {
					type: "stdio",
					command: process.execPath,
					args: [new URL("./mcp-stdio-fixture.ts", import.meta.url).pathname],
					cwd: dir,
					env: { MCP_FIXTURE: "standalone" },
				},
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
		const tool = session.agent.state.tools.find((t) => t.name === "mcp");
		if (!tool) throw new Error("MCP tool unavailable");
		expect(
			await tool.execute(
				"stdio",
				{ server: "fixture", tool: "echo", args: {} },
				new AbortController().signal,
			),
		).toMatchObject({
			content: [{ type: "text", text: `standalone:${await realpath(dir)}` }],
		});
		expect(
			await tool.execute(
				"error",
				{ server: "fixture", tool: "echo", args: { fail: true } },
				new AbortController().signal,
			),
		).toMatchObject({ details: { error: "tool_error", mcpResult: { isError: true } } });
	} finally {
		await session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" });
		session.dispose();
		await rm(dir, { recursive: true, force: true });
	}
});

test("malformed MCP entries remain removable and do not hide valid inventory", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pi-mcp-invalid-`);
	isolate(dir);
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
	isolate(dir);
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
		expect(entry.session.getActiveToolNames()).toContain("mcp");
		// The native adapter owns execution and cancellation. Pixie's ordinary
		// extension context intentionally exposes no private direct-call route.
		await expect(
			entry.capabilities.call(
				"pi.tools.call",
				{ extensionName: "sse", toolName: "wait__here" },
				{ ...sessions.context(entry), signal: AbortSignal.timeout(50) },
			),
		).rejects.toThrow("BRIDGE-02 blocker");
		expect(cancelled).toBe(false);
	} finally {
		await sessions.close();
		await server.close();
		await new Promise<void>((resolve, reject) =>
			http.close((error) => (error ? reject(error) : resolve())),
		);
		await rm(dir, { recursive: true, force: true });
	}
});
