import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionAPI, ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import {
	CallToolRequestSchema,
	ListResourcesRequestSchema,
	ListToolsRequestSchema,
	ReadResourceRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { piMcpAdapterWithConfig } from "../../../pi/host/src/extensions/pi-mcp-adapter.ts";
import { Sessions } from "../../../pi/host/src/sessions.ts";

// Resolve from the declaring workspace, exactly as the production profile does.
const upstream = createRequire(
	new URL("../../../pi/host/src/extensions/pi-mcp-adapter.ts", import.meta.url),
)("pi-mcp-adapter") as {
	readMcpResourceV1: (
		pi: ExtensionAPI,
		request: { version: number; server: string; uri: string },
		options?: { signal?: AbortSignal },
	) => Promise<unknown>;
	callMcpAppToolV1: (
		pi: ExtensionAPI,
		request: { version: number; server: string; tool: string; args?: Record<string, unknown> },
		options?: { signal?: AbortSignal },
	) => Promise<any>;
};
const cleanups: (() => Promise<unknown>)[] = [];
const originalDir = process.env.PI_CODING_AGENT_DIR;
const originalViewer = process.env.MCP_UI_VIEWER;
afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
	if (originalDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
	else process.env.PI_CODING_AGENT_DIR = originalDir;
	if (originalViewer === undefined) delete process.env.MCP_UI_VIEWER;
	else process.env.MCP_UI_VIEWER = originalViewer;
});

async function fixture() {
	const resources: string[] = [];
	const calls: string[] = [];
	const servers: Server[] = [];
	const timers = new Set<ReturnType<typeof setTimeout>>();
	const envelope = (uri: string) => ({
		_meta: { fixture: { revision: 7 } },
		contents: [
			{
				uri,
				mimeType: "text/html;profile=mcp-app",
				text: "<!doctype html><html><body>Real App</body></html>",
				_meta: { ui: { csp: { connectDomains: [] }, prefersBorder: true } },
			},
		],
	});
	const http = createServer((request, response) => {
		void (async () => {
			if (request.headers.authorization !== "Bearer resource-fixture-token") {
				response.writeHead(401).end();
				return;
			}
			const server = new Server(
				{ name: "resource-fixture", version: "1" },
				{ capabilities: { tools: {}, resources: {} } },
			);
			servers.push(server);
			server.setRequestHandler(ListToolsRequestSchema, async () => ({
				tools: [
					{
						name: "app",
						inputSchema: { type: "object" },
						_meta: { ui: { resourceUri: "ui://fixture/unlisted" } },
					},
					{
						name: "app_only",
						inputSchema: { type: "object" },
						_meta: { ui: { visibility: ["app"] } },
					},
					{
						name: "model_only",
						inputSchema: { type: "object" },
						_meta: { ui: { visibility: ["model"] } },
					},
				],
			}));
			server.setRequestHandler(CallToolRequestSchema, async ({ params }) => {
				calls.push(params.name);
				if (params.arguments?.slow)
					await new Promise<void>((resolve) => {
						const timer = setTimeout(() => {
							timers.delete(timer);
							resolve();
						}, 30000);
						timers.add(timer);
					});
				return {
					content: [
						{ type: "text", text: "app-result" },
						{
							type: "image",
							mimeType: "image/png",
							data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jM4sAAAAASUVORK5CYII=",
						},
					],
					structuredContent: { called: params.name },
					_meta: { fixture: { preserved: true } },
				};
			});
			server.setRequestHandler(ListResourcesRequestSchema, async () => ({ resources: [] }));
			server.setRequestHandler(ReadResourceRequestSchema, async ({ params }) => {
				resources.push(params.uri);
				if (params.uri === "fixture://failure") throw new Error("Fixture resource failure");
				if (params.uri === "fixture://slow")
					await new Promise<void>((resolve) => {
						const timer = setTimeout(() => {
							timers.delete(timer);
							resolve();
						}, 30000);
						timers.add(timer);
					});
				return envelope(params.uri);
			});
			const transport = new StreamableHTTPServerTransport({
				sessionIdGenerator: undefined,
				enableJsonResponse: true,
			});
			await server.connect(transport);
			await transport.handleRequest(request, response);
		})().catch(() => {
			if (!response.headersSent) response.writeHead(500);
			response.end();
		});
	});
	await new Promise<void>((resolve) => http.listen(0, "127.0.0.1", resolve));
	cleanups.push(async () => {
		for (const timer of timers) clearTimeout(timer);
		await Promise.all(servers.map((server) => server.close()));
		http.closeAllConnections();
		await new Promise<void>((resolve) => http.close(() => resolve()));
	});
	const url = `http://127.0.0.1:${(http.address() as { port: number }).port}/mcp`;
	return { url, resources, calls, envelope };
}

async function sessionsFixture(
	extra: ExtensionFactory[] = [],
	settings: Record<string, unknown> = {},
	servers: Record<string, unknown> = {},
) {
	const dir = await mkdtemp(`${tmpdir()}/pixie-resource-api-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	process.env.PI_CODING_AGENT_DIR = dir;
	process.env.MCP_UI_VIEWER = "none";
	const instances: ExtensionAPI[] = [];
	const factory = piMcpAdapterWithConfig({
		agentDir: dir,
		config: { mcpServers: servers, settings: { scriptMode: false, ...settings } },
	});
	const events: any[] = [];
	const sessions = new Sessions(
		dir,
		[
			...extra,
			(pi) => {
				instances.push(pi);
				factory(pi);
			},
		],
		(_id, event) => events.push(event),
	);
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(dir);
	return { dir, sessions, entry, instances, events };
}

test("native App tool results project trusted attachment metadata live and after reload without modifying model results", async () => {
	const http = await fixture();
	const provider: ExtensionFactory = (pi) =>
		pi.registerProvider("app-fixture", {
			baseUrl: "http://localhost/unused",
			apiKey: "fixture-only",
			api: "app-fixture-api",
			models: [
				{
					id: "app",
					name: "App",
					reasoning: false,
					input: ["text", "image"],
					cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
					contextWindow: 64000,
					maxTokens: 1024,
				},
			],
			streamSimple: (_model, context) => {
				const stream = createAssistantMessageEventStream();
				const finished = context.messages.some((message) => message.role === "toolResult");
				const message: AssistantMessage = {
					role: "assistant",
					api: "app-fixture-api",
					provider: "app-fixture",
					model: "app",
					timestamp: Date.now(),
					stopReason: finished ? "stop" : "toolUse",
					content: finished
						? [{ type: "text", text: "App done" }]
						: [
								{
									type: "toolCall",
									id: "fixture-app-call",
									name: "mcp",
									arguments: { server: "appserver", tool: "app", args: {} },
								},
							],
					usage: {
						input: 1,
						output: 1,
						cacheRead: 0,
						cacheWrite: 0,
						totalTokens: 2,
						cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
					},
				};
				queueMicrotask(() => {
					stream.push({ type: "done", reason: message.stopReason as "stop" | "toolUse", message });
					stream.end(message);
				});
				return stream;
			},
		});
	const { dir, sessions, entry, events } = await sessionsFixture([provider]);
	await entry.capabilities.call(
		"mcp.attach",
		{
			servers: [
				{
					name: "appserver",
					url: http.url,
					headers: { Authorization: "Bearer resource-fixture-token" },
				},
			],
		},
		sessions.context(entry),
	);
	const model = entry.modelRuntime.getModel("app-fixture", "app");
	if (!model) throw new Error("Fixture model missing");
	await entry.session.setModel(model);
	await sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content: [{ type: "text", text: "Show App" }],
	});
	const attachment = {
		toolName: "app",
		extensionName: "appserver",
		resourceUri: "ui://fixture/unlisted",
		toolNameIsActual: true,
	};
	expect(
		events.find((event) => event.type === "tool_execution_end")?.result.details.mcp.app,
	).toEqual(attachment);
	const raw = entry.session.sessionManager
		.getBranch()
		.find((e) => e.type === "message" && e.message.role === "toolResult");
	expect((raw as any).message.details.mcp).toBeUndefined();
	const id = entry.session.sessionId;
	entry.lastUsed = 0;
	await sessions.sweep();
	const restored = await sessions.get(id, dir);
	const snapshot: any = sessions.snapshot(restored, true);
	expect(
		snapshot.messages.find((message: any) => message.role === "toolResult").details.mcp.app,
	).toEqual(attachment);
});

test("patched raw resource API preserves unlisted valid App MIME and metadata with exact instance authority", async () => {
	const http = await fixture();
	const { dir, sessions, entry, instances } = await sessionsFixture();
	const context = sessions.context(entry);
	await entry.capabilities.call(
		"mcp.attach",
		{
			servers: [
				{
					name: "appserver",
					url: http.url,
					headers: { Authorization: "Bearer resource-fixture-token" },
				},
			],
		},
		context,
	);
	const request = { version: 1, server: "appserver", uri: "ui://fixture/unlisted" };
	expect(
		await upstream.readMcpResourceV1(instances[0], JSON.parse(JSON.stringify(request))),
	).toEqual(http.envelope(request.uri));
	expect(
		await entry.capabilities.call(
			"pi.resources.read",
			{ extensionName: "appserver", uri: request.uri },
			context,
		),
	).toEqual({ result: http.envelope(request.uri) });
	const other = await sessions.create(dir);
	await expect(upstream.readMcpResourceV1(instances[1], request)).rejects.toThrow(
		"unavailable in this session",
	);
	await other.capabilities.call(
		"mcp.attach",
		{
			servers: [
				{
					name: "appserver",
					url: http.url,
					headers: { Authorization: "Bearer wrong-session-token" },
				},
			],
		},
		sessions.context(other),
	);
	await expect(upstream.readMcpResourceV1(instances[1], request)).rejects.toThrow();
	await expect(
		upstream.readMcpResourceV1(instances[0], { ...request, server: "__proto__" }),
	).rejects.toThrow("unavailable in this session");
	await expect(upstream.readMcpResourceV1({} as ExtensionAPI, request)).rejects.toThrow(
		"not installed",
	);
	await expect(
		entry.capabilities.call(
			"pi.resources.read",
			{ extensionName: "appserver", uri: request.uri },
			sessions.context(other),
		),
	).rejects.toThrow("another session");
	await expect(
		upstream.readMcpResourceV1(instances[0], { ...request, version: 2 }),
	).rejects.toThrow("Invalid MCP resource");
	await expect(
		upstream.readMcpResourceV1(instances[0], { ...request, uri: "fixture://failure" }),
	).rejects.toThrow("Fixture resource failure");
	expect(http.resources).toEqual([request.uri, request.uri, "fixture://failure"]);
	await entry.close();
	await expect(upstream.readMcpResourceV1(instances[0], request)).rejects.toThrow("not active");
});

test("patched raw resource API rejects bad bearer and aborts in-flight reads and shutdown", async () => {
	const http = await fixture();
	const { sessions, entry, instances } = await sessionsFixture();
	await entry.capabilities.call(
		"mcp.attach",
		{
			servers: [
				{
					name: "allowed",
					url: http.url,
					headers: { Authorization: "Bearer resource-fixture-token" },
				},
				{ name: "denied", url: http.url, headers: { Authorization: "Bearer wrong" } },
			],
		},
		sessions.context(entry),
	);
	await expect(
		upstream.readMcpResourceV1(instances[0], {
			version: 1,
			server: "denied",
			uri: "ui://fixture/unlisted",
		}),
	).rejects.toThrow();
	expect(http.resources).toEqual([]);
	const request = { version: 1, server: "allowed", uri: "fixture://slow" };
	const aborted = new AbortController();
	aborted.abort(new Error("cancelled before dispatch"));
	await expect(
		upstream.readMcpResourceV1(instances[0], request, { signal: aborted.signal }),
	).rejects.toThrow("cancelled before dispatch");
	const controller = new AbortController();
	const pending = upstream.readMcpResourceV1(instances[0], request, { signal: controller.signal });
	const rejected = pending.then(
		() => null,
		(error) => error as Error,
	);
	for (let n = 0; http.resources.length === 0 && n < 100; n++) await Bun.sleep(10);
	expect(http.resources).toContain(request.uri);
	controller.abort(new Error("fixture cancelled"));
	expect((await rejected)?.message).toBe("fixture cancelled");
	const closingRead = upstream.readMcpResourceV1(instances[0], request);
	const closed = closingRead.then(
		() => null,
		(error) => error as Error,
	);
	await entry.close();
	expect(await closed).toBeInstanceOf(Error);
});

test("App-origin calls preserve visibility, same-server authority, raw content and cancellation", async () => {
	const http = await fixture();
	const { dir, sessions, entry, instances } = await sessionsFixture();
	const server = {
		name: "appserver",
		url: http.url,
		headers: { Authorization: "Bearer resource-fixture-token" },
	};
	await entry.capabilities.call("mcp.attach", { servers: [server] }, sessions.context(entry));
	const proxy = entry.session.agent.state.tools.find((tool) => tool.name === "mcp");
	if (!proxy) throw new Error("Missing upstream proxy");
	const result = await proxy.execute(
		"app-result",
		{ server: "appserver", tool: "app", args: {} },
		new AbortController().signal,
	);
	expect(result.details).toMatchObject({ uiViewer: "suppressed" });
	expect(http.resources).toContain("ui://fixture/unlisted");
	const catalog: any = await proxy.execute(
		"catalog",
		{ server: "appserver" },
		new AbortController().signal,
	);
	expect(catalog.details.tools).not.toContain("appserver_app_only");
	const search: any = await proxy.execute(
		"search",
		{ search: "app_only", server: "appserver" },
		new AbortController().signal,
	);
	expect(search.details.matches.some((match: any) => match.tool === "appserver_app_only")).toBe(
		false,
	);
	await expect(
		entry.capabilities.call(
			"pi.tools.call",
			{ name: "appserver__app_only", arguments: {} },
			sessions.context(entry),
		),
	).rejects.toThrow("tool_not_found");
	const request = { version: 1, server: "appserver", tool: "app_only", args: {} };
	const raw = await upstream.callMcpAppToolV1(instances[0], request);
	expect(raw).toMatchObject({
		structuredContent: { called: "app_only" },
		_meta: { fixture: { preserved: true } },
	});
	expect(raw.content[1]).toMatchObject({ type: "image", mimeType: "image/png" });
	expect(await upstream.callMcpAppToolV1(instances[0], { ...request, tool: "app" })).toMatchObject({
		structuredContent: { called: "app" },
	});
	expect(
		await entry.capabilities.call(
			"pi.apps.tools.call",
			{ extensionName: "appserver", toolName: "app_only", arguments: {} },
			sessions.context(entry),
		),
	).toMatchObject({ ...raw, isError: false });
	await expect(
		upstream.callMcpAppToolV1(instances[0], { ...request, tool: "model_only" }),
	).rejects.toThrow("not callable by Apps");
	await expect(
		upstream.callMcpAppToolV1(instances[0], { ...request, server: "other" }),
	).rejects.toThrow("unavailable in this session");
	await expect(
		entry.capabilities.call(
			"pi.apps.tools.call",
			{ extensionName: "appserver", toolName: "other__app_only" },
			sessions.context(entry),
		),
	).rejects.toThrow("not callable by Apps");
	const other = await sessions.create(dir);
	await expect(upstream.callMcpAppToolV1(instances[1], request)).rejects.toThrow(
		"unavailable in this session",
	);
	await expect(
		entry.capabilities.call(
			"pi.apps.tools.call",
			{ extensionName: "appserver", toolName: "app_only" },
			sessions.context(other),
		),
	).rejects.toThrow("another session");
	const abort = new AbortController();
	const pending = upstream
		.callMcpAppToolV1(instances[0], { ...request, args: { slow: true } }, { signal: abort.signal })
		.then(
			() => null,
			(error) => error as Error,
		);
	await Bun.sleep(50);
	abort.abort(new Error("App cancelled"));
	expect((await pending)?.message).toBe("App cancelled");
	expect(http.calls.filter((name) => name === "model_only")).toEqual([]);
	expect(entry.capabilities.snapshot()).toMatchObject({
		mcp: 1,
		"mcp-apps": 1,
		"mcp-app-tools": 1,
	});
});

test("App-origin API honors upstream configured tool approval without publishing App-only tools", async () => {
	const http = await fixture();
	const origins: string[] = [];
	const { sessions, entry, instances } = await sessionsFixture(
		[
			(pi) => {
				pi.events.on("pi-mcp-adapter:tool-approval-request", (request: any) => {
					origins.push(request.origin);
					request.claim(() => "deny");
				});
			},
		],
		{ approveTools: true },
	);
	await entry.capabilities.call(
		"mcp.attach",
		{
			servers: [
				{
					name: "appserver",
					url: http.url,
					headers: { Authorization: "Bearer resource-fixture-token" },
				},
			],
		},
		sessions.context(entry),
	);
	await expect(
		upstream.callMcpAppToolV1(instances[0], {
			version: 1,
			server: "appserver",
			tool: "app_only",
			args: {},
		}),
	).rejects.toThrow("approval: denied");
	expect(http.calls).toEqual([]);
	expect(origins).toEqual(["iframe"]);
});

test("native upstream-owned server configuration supports host App APIs without a second registration", async () => {
	const http = await fixture();
	const { sessions, entry } = await sessionsFixture(
		[],
		{},
		{ native: { url: http.url, headers: { Authorization: "Bearer resource-fixture-token" } } },
	);
	const ctx = sessions.context(entry);
	expect(
		await entry.capabilities.call(
			"pi.resources.read",
			{ extensionName: "native", uri: "ui://fixture/unlisted" },
			ctx,
		),
	).toEqual({ result: http.envelope("ui://fixture/unlisted") });
	expect(
		await entry.capabilities.call(
			"pi.apps.tools.call",
			{ extensionName: "native", toolName: "app_only", arguments: {} },
			ctx,
		),
	).toMatchObject({ structuredContent: { called: "app_only" }, isError: false });
	expect(
		await entry.capabilities.call("adapter.describeApp", { server: "native", tool: "app" }, ctx),
	).toMatchObject({ resourceUri: "ui://fixture/unlisted", extensionName: "native" });
});
