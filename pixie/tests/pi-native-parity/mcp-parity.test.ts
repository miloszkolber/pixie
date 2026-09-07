import { afterEach, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Server as McpServer } from "@modelcontextprotocol/sdk/server/index.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import {
	CallToolRequestSchema,
	ListResourcesRequestSchema,
	ListToolsRequestSchema,
	ReadResourceRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { PIXIE_BROWSER_RUNTIME_NAME } from "../../../agent/pixie-assistant/src/extensions/pi-mcp-adapter.ts";
import { piMcpAdapterWithConfig } from "./upstream.ts";

const piMcpAdapter = piMcpAdapterWithConfig();

import { startHost } from "../../../agent/pixie-assistant/src/server.ts";
import { Sessions } from "../../../agent/pixie-assistant/src/sessions.ts";

const compatibilityMcp = (pi: ExtensionAPI, dir: string) =>
	piMcpAdapterWithConfig({ agentDir: dir })(pi);

import { cleanups, fixture } from "./helpers.ts";

const savedEnv = new Map<string, string | undefined>();
function setEnv(name: string, value: string | undefined): void {
	if (!savedEnv.has(name)) savedEnv.set(name, process.env[name]);
	if (value === undefined) delete process.env[name];
	else process.env[name] = value;
}

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
	for (const [name, value] of savedEnv) {
		if (value === undefined) delete process.env[name];
		else process.env[name] = value;
	}
	savedEnv.clear();
});

const STDIO_FIXTURE = `import readline from "node:readline";
const tools = [
  { name: "echo_text", description: "Echo text back", inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] } },
  { name: "structured_tool", description: "Returns structured content", inputSchema: { type: "object", properties: {} } },
  { name: "image_tool", description: "Returns an image block", inputSchema: { type: "object", properties: {} } },
  { name: "slow_tool", description: "Replies after a delay", inputSchema: { type: "object", properties: {} } },
];
const rl = readline.createInterface({ input: process.stdin, terminal: false });
const send = (o) => process.stdout.write(JSON.stringify(o) + "\\n");
rl.on("line", (line) => {
  if (!line.trim()) return;
  const msg = JSON.parse(line);
  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: msg.params.protocolVersion, capabilities: { tools: {}, resources: {} }, serverInfo: { name: "parity-stdio", version: "1" } } });
  } else if (typeof msg.method === "string" && msg.method.startsWith("notifications/")) {
  } else if (msg.method === "tools/list") {
    send({ jsonrpc: "2.0", id: msg.id, result: { tools } });
  } else if (msg.method === "tools/call") {
    const name = msg.params.name;
    if (name === "echo_text") send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "echo:" + (msg.params.arguments?.text ?? "") }] } });
    else if (name === "structured_tool") send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "structured-ok" }], structuredContent: { value: 42 } } });
    else if (name === "image_tool") send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "image", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jM4sAAAAASUVORK5CYII=", mimeType: "image/png" }] } });
    else if (name === "slow_tool") setTimeout(() => send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "text", text: "slow-done" }] } }), 15000);
    else send({ jsonrpc: "2.0", id: msg.id, error: { code: -32602, message: "unknown tool" } });
  } else if (msg.method === "resources/list") {
    send({ jsonrpc: "2.0", id: msg.id, result: { resources: [{ uri: "parity://hello", name: "hello", mimeType: "text/plain" }] } });
  } else if (msg.method === "resources/read") {
    send({ jsonrpc: "2.0", id: msg.id, result: { contents: [{ uri: msg.params.uri, mimeType: "text/plain", text: "hello-resource" }] } });
  } else {
    send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "unknown method" } });
  }
});
`;

interface HttpFixture {
	url: string;
	seenAuth: (string | null)[];
	applyMode: (mode: "ok" | "unauthorized") => void;
	close: () => Promise<void>;
}

async function startHttpFixture(appMime = "text/html;profile=mcp-app"): Promise<HttpFixture> {
	const seenAuth: (string | null)[] = [];
	let mode: "ok" | "unauthorized" = "ok";
	const tools = [
		{
			name: "echo_text",
			description: "Echo text back",
			inputSchema: { type: "object", properties: { text: { type: "string" } } },
		},
		{
			name: "app_tool",
			description: "App-enabled tool",
			inputSchema: { type: "object", properties: {} },
			_meta: { ui: { resourceUri: "ui://parity/app.html" } },
		},
	];
	const server = createServer((request, response) => {
		if (request.method !== "POST" || !request.url?.startsWith("/mcp")) {
			response.writeHead(404);
			response.end();
			return;
		}
		seenAuth.push((request.headers.authorization as string) ?? null);
		let body = "";
		request.on("data", (chunk) => {
			body += chunk;
		});
		request.on("end", () => {
			const message = JSON.parse(body) as { id?: unknown; method?: string; params?: any };
			if (typeof message.method === "string" && message.method.startsWith("notifications/")) {
				response.writeHead(202);
				response.end();
				return;
			}
			if (mode === "unauthorized") {
				response.writeHead(401);
				response.end();
				return;
			}
			const ok = (result: unknown) => {
				response.writeHead(200, { "content-type": "application/json" });
				response.end(JSON.stringify({ jsonrpc: "2.0", id: message.id, result }));
			};
			if (message.method === "initialize") {
				ok({
					protocolVersion: message.params?.protocolVersion,
					capabilities: { tools: {}, resources: {} },
					serverInfo: { name: "parity-http", version: "1" },
				});
			} else if (message.method === "tools/list") {
				ok({ tools });
			} else if (message.method === "tools/call") {
				if (message.params?.name === "echo_text")
					ok({ content: [{ type: "text", text: `http:${message.params.arguments?.text ?? ""}` }] });
				else if (message.params?.name === "app_tool")
					ok({ content: [{ type: "text", text: "app-ok" }] });
				else {
					response.writeHead(200, { "content-type": "application/json" });
					response.end(
						JSON.stringify({
							jsonrpc: "2.0",
							id: message.id,
							error: { code: -32602, message: "unknown tool" },
						}),
					);
				}
			} else if (message.method === "resources/list") {
				ok({ resources: [{ uri: "parity://hello", name: "hello" }] });
			} else if (message.method === "resources/read") {
				if (message.params?.uri === "ui://parity/app.html") {
					ok({
						contents: [
							{
								uri: message.params.uri,
								mimeType: appMime,
								text: "<!doctype html><html><body>Parity App</body></html>",
								_meta: { ui: { csp: { connectDomains: [] } } },
							},
						],
					});
				} else {
					ok({
						contents: [
							{ uri: message.params.uri, mimeType: "text/plain", text: "hello-http-resource" },
						],
					});
				}
			} else {
				response.writeHead(200, { "content-type": "application/json" });
				response.end(
					JSON.stringify({
						jsonrpc: "2.0",
						id: message.id,
						error: { code: -32601, message: "unknown method" },
					}),
				);
			}
		});
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const address = server.address();
	const port = typeof address === "object" && address ? address.port : 0;
	return {
		url: `http://127.0.0.1:${port}/mcp`,
		seenAuth,
		applyMode: (next) => {
			mode = next;
		},
		close: () =>
			new Promise<void>((resolve, reject) =>
				(server as Server).close((error) => (error ? reject(error) : resolve())),
			),
	};
}

async function writeStdioFixture(dir: string): Promise<string> {
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const path = join(dir, `parity-stdio-${Date.now()}-${Math.floor(Math.random() * 1e6)}.mjs`);
	await writeFile(path, STDIO_FIXTURE);
	return path;
}

async function adapterSession(
	config: Record<string, unknown>,
	agentDir?: string,
): Promise<{
	entry: any;
	sessions: Sessions;
	call: (args: Record<string, unknown>) => Promise<any>;
}> {
	const dir = agentDir ?? (await mkdtemp(`${tmpdir()}/pixie-mcp-parity-`));
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	// Contain the adapter metadata cache, tokens, and config discovery.
	setEnv("PI_CODING_AGENT_DIR", dir);
	const factory = piMcpAdapterWithConfig({ config }) as never;
	const sessions = new Sessions(dir, [factory], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(dir);
	const tool = (entry.session.agent.state.tools as any[]).find((t: any) => t.name === "mcp");
	if (!tool) throw new Error("Adapter proxy tool unavailable: mcp");
	return {
		entry,
		sessions,
		call: (args) => tool.execute("parity-call", args, new AbortController().signal),
	};
}

function toolNames(entry: any): string[] {
	return (entry.session.agent.state.tools as any[]).map((tool) => tool.name);
}

function normalizeStatus(value: unknown): string {
	return String(value ?? "").replace(/\s+/g, "-");
}

test("native adapter starts under Bun and enables application administration", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-host-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	await writeFile(
		join(dir, "settings.json"),
		JSON.stringify({
			extensions: [createRequire(import.meta.url).resolve("pi-mcp-adapter")],
		}),
	);
	const host = await startHost({
		agentDir: dir,
		secret: "parity-mcp-adapter-secret",
		port: 0,
	});
	try {
		const base = `http://127.0.0.1:${host.server.port}`;
		const ready = (await (
			await fetch(`${base}/readyz`, {
				headers: { Authorization: "Bearer parity-mcp-adapter-secret" },
			})
		).json()) as { capabilities: Record<string, number> };
		// Bun compatibility is unknown upstream (engines node>=20); this test
		// running green under Bun is the recorded startup datum.
		expect(ready.capabilities.mcp).toBe(1);
		expect(ready.capabilities["pi-mcp-adapter"]).toBeUndefined();
	} finally {
		await host.close();
	}
	const { entry, sessions } = await adapterSession({ mcpServers: {} });
	const status = ((await sessions.context(entry)) &&
		(await entry.capabilities.call("adapter.status", {}, sessions.context(entry)))) as any;
	expect(status).toMatchObject({
		engine: "pi-mcp-adapter",
		version: null,
		testedVersion: "2.32.1",
		bunCompat: "unknown",
		proxyTool: "mcp",
		runtimeName: PIXIE_BROWSER_RUNTIME_NAME,
	});
});

test("persisted package entry and legacy configuration use the same upstream proxy without per-tool aliases", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-surface-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	const stdio = await writeStdioFixture(dir);
	await writeFile(
		join(dir, "mcp.json"),
		JSON.stringify({ parity: { type: "stdio", command: "node", args: [stdio] } }),
	);
	const compatibility = new Sessions(dir, [(pi) => compatibilityMcp(pi, dir)], () => {});
	cleanups.push(() => compatibility.close());
	const compatibilityEntry = await compatibility.create(dir);
	expect(toolNames(compatibilityEntry).filter((name) => name.startsWith("parity__"))).toEqual([]);
	expect(toolNames(compatibilityEntry)).toContain("mcp");
	expect(
		await compatibilityEntry.capabilities.call(
			"pi.tools.call",
			{ name: "parity__echo_text", arguments: { text: "legacy config" } },
			compatibility.context(compatibilityEntry),
		),
	).toMatchObject({ content: [{ text: "echo:legacy config" }] });

	const { entry } = await adapterSession({
		mcpServers: { parity: { command: "node", args: [stdio] } },
	});
	const names = toolNames(entry);
	expect(names).toContain("mcp");
	expect(names.filter((name) => name.startsWith("parity__"))).toHaveLength(0);
	expect(names.filter((name) => name.startsWith("parity_"))).toHaveLength(0);
});

test("adapter serves stdio and Streamable HTTP transports", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-transports-`);
	const stdio = await writeStdioFixture(dir);
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { call } = await adapterSession({
		mcpServers: {
			parity_stdio: { command: "node", args: [stdio] },
			parity_http: { url: http.url },
		},
	});
	expect(await call({ connect: "parity_stdio" })).toMatchObject({ details: { mode: "list" } });
	expect(await call({ connect: "parity_http" })).toMatchObject({ details: { mode: "list" } });
	const stdioCall: any = await call({ tool: "parity_stdio_echo_text", args: { text: "hi" } });
	expect(stdioCall.content[0].text).toBe("echo:hi");
	const httpCall: any = await call({
		tool: "echo_text",
		args: { text: "yo" },
		server: "parity_http",
	});
	expect(httpCall.content[0].text).toBe("http:yo");
});

test("adapter falls back to SSE transport when pinned", async () => {
	const listeners = new Map<string, (body: string) => void>();
	const server = createServer((request, response) => {
		const url = new URL(request.url ?? "/", "http://localhost");
		if (request.method === "GET" && url.pathname === "/sse") {
			response.writeHead(200, {
				"content-type": "text/event-stream",
				"cache-control": "no-cache",
				connection: "keep-alive",
			});
			response.write("event: endpoint\ndata: /messages?sessionId=s1\n\n");
			listeners.set("s1", (body: string) => {
				const message = JSON.parse(body) as { id?: unknown; method?: string; params?: any };
				const result =
					message.method === "initialize"
						? {
								protocolVersion: message.params?.protocolVersion,
								capabilities: { tools: {} },
								serverInfo: { name: "parity-sse", version: "1" },
							}
						: message.method === "tools/list"
							? [
									{
										name: "sse_tool",
										description: "SSE tool",
										inputSchema: { type: "object", properties: {} },
									},
								]
							: { content: [{ type: "text", text: "sse-ok" }] };
				const payload =
					message.method === "tools/list"
						? { tools: result }
						: message.method === "initialize"
							? result
							: result;
				response.write(
					`event: message\ndata: ${JSON.stringify({ jsonrpc: "2.0", id: message.id, result: payload })}\n\n`,
				);
			});
			return;
		}
		if (request.method === "POST" && url.pathname === "/messages") {
			let body = "";
			request.on("data", (chunk) => {
				body += chunk;
			});
			request.on("end", () => {
				response.writeHead(202);
				response.end();
				listeners.get(url.searchParams.get("sessionId") ?? "")?.(body);
			});
			return;
		}
		response.writeHead(404);
		response.end();
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	cleanups.push(() => new Promise<void>((resolve) => server.close(() => resolve())));
	const address = server.address();
	const port = typeof address === "object" && address ? address.port : 0;
	const { call } = await adapterSession({
		mcpServers: { parity_sse: { url: `http://127.0.0.1:${port}/sse`, httpTransport: "sse" } },
	});
	const result: any = await call({ tool: "parity_sse_sse_tool", args: {} });
	expect(result.content[0].text).toBe("sse-ok");
});

test("adapter discovers lazily and keeps cached schemas usable", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-lazy-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const stdio = await writeStdioFixture(dir);
	const agentDir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-lazy-agent-`);
	cleanups.push(() => rm(agentDir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", agentDir);
	const factory = () =>
		piMcpAdapterWithConfig({
			config: {
				mcpServers: { parity_lazy: { command: "node", args: [stdio], lifecycle: "lazy" } },
			},
		}) as never;
	const open = async () => {
		const sessions = new Sessions(agentDir, [factory()], () => {});
		const entry = await sessions.create(agentDir);
		const tool = (entry.session.agent.state.tools as any[]).find((t: any) => t.name === "mcp");
		return {
			sessions,
			call: (args: Record<string, unknown>) =>
				tool.execute("parity-lazy", args, new AbortController().signal) as Promise<any>,
		};
	};
	// Phase one bootstraps the metadata cache: a first run with no cache file
	// connects every server once so later runs can stay lazy.
	const first = await open();
	cleanups.push(() => first.sessions.close());
	const bootstrapped: any = await first.call({});
	expect(
		normalizeStatus(
			(bootstrapped.details.servers as any[]).find((s) => s.name === "parity_lazy").status,
		),
	).toBe("connected");
	await first.sessions.close();
	// Phase two reuses the same agent directory, so the cache file exists and
	// the lazy server stays disconnected until its first use.
	const second = await open();
	cleanups.push(() => second.sessions.close());
	const before: any = await second.call({});
	const lazyBefore = (before.details.servers as any[]).find((s) => s.name === "parity_lazy");
	expect(["cached", "not-connected"]).toContain(normalizeStatus(lazyBefore.status));
	expect(lazyBefore.toolCount).toBeGreaterThan(0);
	// Cached schemas answer describe without connecting.
	const described: any = await second.call({ describe: "parity_lazy_echo_text" });
	expect(described.details.tool).toMatchObject({ originalName: "echo_text" });
	expect(JSON.stringify(described.details.tool.inputSchema)).toContain("text");
	await second.call({ connect: "parity_lazy" });
	const after: any = await second.call({});
	const lazyAfter = (after.details.servers as any[]).find((s) => s.name === "parity_lazy");
	expect(normalizeStatus(lazyAfter.status)).toBe("connected");
});

test("adapter reconnects after an HTTP outage", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { call } = await adapterSession({ mcpServers: { parity_http: { url: http.url } } });
	await call({ connect: "parity_http" });
	http.applyMode("unauthorized");
	const failing: any = await call({ tool: "parity_http_echo_text", args: { text: "x" } });
	expect(failing.details.error).toBeString();
	http.applyMode("ok");
	const reconnected: any = await call({ connect: "parity_http" });
	expect(reconnectd(reconnected)).toBe(true);
	const recovered: any = await call({ tool: "parity_http_echo_text", args: { text: "back" } });
	expect(recovered.content[0].text).toBe("http:back");
	function reconnectd(result: any): boolean {
		return result?.details?.mode === "list" && result?.details?.server === "parity_http";
	}
});

test("adapter cancel surfaces an abort instead of hanging", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-cancel-`);
	const stdio = await writeStdioFixture(dir);
	const agentDir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-cancel-agent-`);
	cleanups.push(() => rm(agentDir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", agentDir);
	const factory = piMcpAdapterWithConfig({
		config: { mcpServers: { parity: { command: "node", args: [stdio] } } },
	}) as never;
	const sessions = new Sessions(agentDir, [factory], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(agentDir);
	const tool = (entry.session.agent.state.tools as any[]).find((t: any) => t.name === "mcp");
	const controller = new AbortController();
	const pending = tool.execute(
		"parity-cancel",
		{ tool: "parity_slow_tool", args: {} },
		controller.signal,
	);
	setTimeout(() => controller.abort(), 300);
	const result: any = await pending;
	expect(result.details.error).toBe("aborted");
});

test("adapter preserves structured and image results", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-content-`);
	const stdio = await writeStdioFixture(dir);
	const { call } = await adapterSession({
		mcpServers: { parity: { command: "node", args: [stdio] } },
	});
	await call({ connect: "parity" });
	const structured: any = await call({ tool: "parity_structured_tool", args: {} });
	expect(structured.content[0].text).toBe("structured-ok");
	expect(structured.details.mcpResult.structuredContent).toMatchObject({ value: 42 });
	const image: any = await call({ tool: "parity_image_tool", args: {} });
	expect(image.content[0]).toMatchObject({ type: "image", mimeType: "image/png" });
	expect(image.details.mcpResult.content[0]).toMatchObject({ type: "image" });
	expect(image.content[0].data).toBe(image.details.mcpResult.content[0].data);
	expect(Buffer.from(image.content[0].data, "base64").subarray(0, 8)).toEqual(
		Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
	);
});

test("adapter exposes resources without extra configuration", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-resources-`);
	const stdio = await writeStdioFixture(dir);
	const { call } = await adapterSession({
		mcpServers: { parity: { command: "node", args: [stdio] } },
	});
	const listed: any = await call({ connect: "parity" });
	expect(listed.details.tools).toContain("parity_read_hello");
	const read: any = await call({ tool: "parity_read_hello", args: {} });
	expect(JSON.stringify(read)).toContain("hello-resource");
});

test("adapter passes bearer credentials and reports an unavailable OAuth credential store", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	setEnv("PARITY_PROBE_TOKEN", "parity-token-123");
	setEnv("PI_MCP_ADAPTER_TEST_AUTH_STORE", "unavailable");
	const { call } = await adapterSession({
		mcpServers: {
			parity_env: { url: http.url, headers: { Authorization: "Bearer ${PARITY_PROBE_TOKEN}" } },
			parity_bearer: { url: http.url, auth: "bearer", bearerToken: "static-bearer-xyz" },
			parity_oauth: { url: http.url, auth: "oauth" },
		},
	});
	await call({ connect: "parity_env" });
	expect(http.seenAuth).toContain("Bearer parity-token-123");
	await call({ connect: "parity_bearer" });
	expect(http.seenAuth).toContain("Bearer static-bearer-xyz");
	const status: any = await call({});
	const oauth = (status.details.servers as any[]).find((s) => s.name === "parity_oauth");
	// No OAuth issuer is reachable in this fixture: init stays fail-open and
	// reports a canonical non-connected status instead of crashing.
	expect(["not-connected", "failed", "needs-auth"]).toContain(normalizeStatus(oauth.status));
	const authStart: any = await call({ action: "auth-start", server: "parity_oauth" });
	expect(authStart.details.error).toBe("auth_start_failed");
	// Upstream currently masks the storage cause with its cleanup error.
	expect(authStart.details.message).toBe("OAuth startup cleanup failed");
});

test("Browser registers through the adapter runtime API first-wins fail-closed", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const agentDir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-browser-`);
	cleanups.push(() => rm(agentDir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", agentDir);
	const sessions = new Sessions(agentDir, [piMcpAdapter as never], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(agentDir);
	const context = sessions.context(entry);
	const first = (await entry.capabilities.call(
		"adapter.registerBrowser",
		{ url: http.url },
		context,
	)) as any;
	expect(first).toMatchObject({ ok: true });
	await expect(
		entry.capabilities.call("adapter.registerBrowser", { url: http.url }, context),
	).rejects.toThrow();
	// The Browser-named runtime server is registered proxy-only; adapter
	// status keeps projecting it through the bridge snapshot channel.
	const status = (await entry.capabilities.call("adapter.status", {}, context)) as any;
	expect(status.runtimeName).toBe(PIXIE_BROWSER_RUNTIME_NAME);
	expect(status.snapshot === null || typeof status.snapshot === "object").toBe(true);
});

test.each([
	"capability",
	"session-start",
])("runtime Browser registration via %s executes through the upstream proxy over authenticated SDK Streamable HTTP", async (registration) => {
	const seenAuth: (string | undefined)[] = [];
	const methods: string[] = [];
	const calledTools: string[] = [];
	const fixtureErrors: unknown[] = [];
	const servers: McpServer[] = [];
	const http = createServer((request, response) => {
		void (async () => {
			seenAuth.push(request.headers.authorization);
			if (request.headers.authorization !== "Bearer runtime-browser-token") {
				response.writeHead(401).end();
				return;
			}
			const server = new McpServer(
				{ name: "browser-fixture", version: "1" },
				{ capabilities: { tools: {}, resources: {} } },
			);
			servers.push(server);
			server.setRequestHandler(ListToolsRequestSchema, async () => {
				methods.push("tools/list");
				return {
					tools: [
						{
							name: "echo_text",
							description: "Echo Browser text",
							inputSchema: {
								type: "object",
								properties: { text: { type: "string" } },
								required: ["text"],
							},
						},
					],
				};
			});
			server.setRequestHandler(CallToolRequestSchema, async ({ params }) => {
				methods.push("tools/call");
				calledTools.push(params.name);
				return {
					content: [{ type: "text", text: `browser:${params.arguments?.text}` }],
					structuredContent: { echoed: params.arguments?.text },
				};
			});
			server.setRequestHandler(ListResourcesRequestSchema, async () => ({
				resources: [
					{ uri: "browser://instructions", name: "instructions", mimeType: "text/plain" },
				],
			}));
			server.setRequestHandler(ReadResourceRequestSchema, async ({ params }) => ({
				contents: [
					{ uri: params.uri, mimeType: "text/plain", text: "Browser fixture instructions" },
				],
			}));
			const transport = new StreamableHTTPServerTransport({
				sessionIdGenerator: undefined,
				enableJsonResponse: true,
			});
			await server.connect(transport);
			await transport.handleRequest(request, response);
		})().catch((error: unknown) => {
			fixtureErrors.push(error);
			response.writeHead(500).end();
		});
	});
	await new Promise<void>((resolve) => http.listen(0, "127.0.0.1", resolve));
	cleanups.push(async () => {
		await Promise.all(servers.map((server) => server.close()));
		http.closeAllConnections();
		await new Promise<void>((resolve) => http.close(() => resolve()));
	});
	const address = http.address() as { port: number };
	const url = `http://127.0.0.1:${address.port}/mcp/browser`;
	setEnv("PIXIE_MCP_ADAPTER_BROWSER_URL", registration === "session-start" ? url : undefined);
	setEnv("PIXIE_MCP_ADAPTER_BROWSER_TOKEN", "runtime-browser-token");
	const { entry, sessions, call } = await adapterSession({ mcpServers: {} });
	// Finish upstream initialization before testing late, lazy registration.
	await call({});
	const context = sessions.context(entry);
	if (registration === "capability") {
		expect(
			await entry.capabilities.call(
				"adapter.registerBrowser",
				{
					url,
					token: "runtime-browser-token",
				},
				context,
			),
		).toEqual({ ok: true });
	}
	await expect(
		entry.capabilities.call(
			"adapter.registerBrowser",
			{
				url,
				token: "replacement-must-not-win",
			},
			context,
		),
	).rejects.toThrow("already registered");
	expect(seenAuth).toHaveLength(0);
	const connected = await call({ connect: PIXIE_BROWSER_RUNTIME_NAME });
	expect(connected.details).toMatchObject({ mode: "list", server: PIXIE_BROWSER_RUNTIME_NAME });
	expect(connected.details.tools).toContain("pixie-browser_echo_text");
	// Describe requires the catalog name, not { server, describe: originalName }.
	const described = await call({ describe: "pixie-browser_echo_text" });
	expect(described).toMatchObject({ details: { tool: { originalName: "echo_text" } } });
	const result = await call({
		server: PIXIE_BROWSER_RUNTIME_NAME,
		tool: "echo_text",
		args: { text: "hello" },
	});
	expect(result.content[0].text).toBe("browser:hello");
	expect(result.details.mcpResult.structuredContent).toEqual({ echoed: "hello" });
	const prefixed = await call({
		tool: "pixie-browser_echo_text",
		args: JSON.stringify({ text: "prefixed" }),
	});
	expect(prefixed.details.mcpResult.structuredContent).toEqual({ echoed: "prefixed" });
	expect(connected.details.tools).toContain("pixie-browser_read_instructions");
	const resource = await call({ tool: "pixie-browser_read_instructions", args: {} });
	expect(resource.content[0].text).toContain("Browser fixture instructions");
	expect(methods).toEqual(["tools/list", "tools/call", "tools/call"]);
	expect(calledTools).toEqual(["echo_text", "echo_text"]);
	expect(toolNames(entry).filter((name) => name.startsWith("pixie-browser"))).toEqual([]);
	const status = await entry.capabilities.call("adapter.status", {}, context);
	expect(JSON.stringify(status)).not.toContain("runtime-browser-token");
	expect(seenAuth.length).toBeGreaterThan(0);
	expect(seenAuth.every((value) => value === "Bearer runtime-browser-token")).toBe(true);
	expect(fixtureErrors).toEqual([]);
});

test("baseline advertises neither absent Signet nor MCP", async () => {
	const { sessions, dir } = await fixture();
	const entry = await sessions.create(dir);
	expect(entry.capabilities.snapshot().signet).toBeUndefined();
	expect(entry.capabilities.snapshot().mcp).toBeUndefined();
	const names = (entry.session.agent.state.tools as any[]).map((tool) => tool.name);
	expect(names.some((name) => name.includes("__"))).toBe(false);
});

test("adapter accepts valid App MIME and starts its own UI server with browser launch suppressed", async () => {
	setEnv("MCP_UI_VIEWER", "none");
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { call } = await adapterSession({ mcpServers: { parity_http: { url: http.url } } });
	await call({ connect: "parity_http" });
	const described: any = await call({ describe: "parity_http_app_tool" });
	expect(described.details.tool.uiResourceUri).toBe("ui://parity/app.html");
	const result: any = await call({ tool: "parity_http_app_tool", args: {} });
	expect(result.details.mcpResult.content[0].text).toBe("app-ok");
	expect(result.details.uiViewer).toBe("suppressed");
	expect(result.details.uiUrl).toBeString();
	const page = await fetch(result.details.uiUrl);
	expect(page.status).toBe(200);
	expect(result.content[0].text).toContain("app-ok");
});

test("adapter rejects invalid App MIME and preserves inline tool output", async () => {
	setEnv("MCP_UI_VIEWER", "none");
	const http = await startHttpFixture("text/plain");
	cleanups.push(() => http.close());
	const { call } = await adapterSession({ mcpServers: { parity: { url: http.url } } });
	const result = await call({ server: "parity", tool: "app_tool", args: {} });
	expect(result.details.uiOpen).toBe(false);
	expect(result.details.mcpResult.content[0].text).toBe("app-ok");
});

test("adapter runtime bridge scopes attachment, tools, persisted membership and removal", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { entry, sessions, call } = await adapterSession({
		mcpServers: {},
		settings: { scriptMode: false },
	});
	const context = sessions.context(entry);
	const operation = (method: string, params: Record<string, unknown> = {}) =>
		entry.capabilities.call(method, params, context);
	await call({});
	const servers = [
		{
			name: "objective",
			url: http.url,
			headers: { Authorization: "Bearer session-objective-token" },
		},
	];
	expect(await operation("mcp.attach", { servers })).toEqual({ ok: true, unavailable: [] });
	expect(await operation("mcp.attach", { servers })).toEqual({ ok: true, unavailable: [] });
	expect(http.seenAuth).toHaveLength(0);
	expect(
		await operation("pi.tools.call", {
			name: "objective__echo_text",
			arguments: { text: "scoped" },
		}),
	).toMatchObject({ content: [{ text: "http:scoped" }], isError: false });
	expect(http.seenAuth.every((v) => v === "Bearer session-objective-token")).toBe(true);
	const other = await sessions.create(context.cwd);
	await expect(
		other.capabilities.call(
			"pi.tools.call",
			{ name: "objective__echo_text", arguments: {} },
			sessions.context(other),
		),
	).rejects.toThrow("Unknown MCP connection");
	const changed = [{ ...servers[0], headers: { Authorization: "Bearer must-not-replace" } }];
	expect(await operation("mcp.attach", { servers: changed })).toMatchObject({ ok: false });
	expect(
		await operation("pi.tools.call", {
			name: "objective__echo_text",
			arguments: { text: "original" },
		}),
	).toMatchObject({ content: [{ text: "http:original" }] });
	expect(http.seenAuth).not.toContain("Bearer must-not-replace");
	await operation("pi.session.extensions.remove", { extensionKey: "objective" });
	await operation("mcp.attach", { servers });
	await expect(operation("pi.tools.call", { name: "objective__echo_text" })).rejects.toThrow(
		"Unknown MCP connection",
	);
	await operation("pi.session.extensions.add", { extension: { type: "mcp", server: servers[0] } });
	expect(
		await operation("pi.tools.call", {
			name: "objective__echo_text",
			arguments: { text: "restored" },
		}),
	).toMatchObject({ content: [{ text: "http:restored" }] });
	await operation("mcp.attach", { servers: [] });
	await expect(operation("pi.tools.call", { name: "objective__echo_text" })).resolves.toMatchObject(
		{ isError: false },
	);
	// Only owned runtime handles are disposed. The model surface has no old aliases.
	expect(toolNames(entry).filter((n) => n.startsWith("objective"))).toEqual([]);
	await entry.close();
	await expect(operation("pi.tools.call", { name: "objective__echo_text" })).rejects.toThrow();
});

test("adapter bridge preserves legacy admin configuration and restores session membership", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-admin-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const factory = piMcpAdapterWithConfig({ config: { mcpServers: {} }, agentDir: dir });
	const open = () => {
		const sessions = new Sessions(dir, [factory], () => {});
		cleanups.push(() => sessions.close());
		return sessions;
	};
	const sessions = open();
	const control = await sessions.control(dir);
	cleanups.push(() => control.close());
	const admin = (method: string, params: Record<string, unknown>) =>
		control.capabilities.call(method, params, sessions.context(control));
	await admin("pi.config.extensions.add", {
		extension: { type: "mcp", server: { name: "global", type: "http", url: http.url } },
	});
	await expect(
		admin("pi.config.extensions.add", { extension: { name: "global", url: http.url } }),
	).rejects.toThrow("already exists");
	expect(http.seenAuth).toHaveLength(0);
	const entry = await sessions.create(dir);
	const invoke = (method: string, params: Record<string, unknown>) =>
		entry.capabilities.call(method, params, sessions.context(entry));
	await invoke("pi.session.extensions.remove", { extensionKey: "global" });
	await invoke("pi.session.extensions.add", { extension: { name: "local", url: http.url } });
	const id = entry.session.sessionId;
	await sessions.close();
	const next = open();
	const restored = await next.get(id, dir);
	const list: any = await restored.capabilities.call(
		"pi.session.extensions.list",
		{},
		next.context(restored),
	);
	expect(list.extensions.map((e: any) => e.extensionKey)).toEqual(["local"]);
	expect(
		await restored.capabilities.call(
			"pi.tools.call",
			{ name: "local__echo_text", arguments: { text: "reopened" } },
			next.context(restored),
		),
	).toMatchObject({ content: [{ text: "http:reopened" }] });
	await restored.forgetMcp?.();
	const { readFile } = await import("node:fs/promises");
	expect(JSON.parse(await readFile(join(dir, "mcp-sessions.json"), "utf8"))[id]).toBeUndefined();
	const native = JSON.stringify({
		mcpServers: { native: { url: http.url } },
		settings: { scriptMode: false },
	});
	await writeFile(join(dir, "mcp.json"), native);
	await expect(
		admin("pi.config.extensions.add", { extension: { name: "must-not-write", url: http.url } }),
	).rejects.toThrow("Native MCP configuration");
	expect(await readFile(join(dir, "mcp.json"), "utf8")).toBe(native);
});

test("public proxy resource output stays rendered content without raw resource fields", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { entry, call } = await adapterSession({ mcpServers: { parity: { url: http.url } } });
	const listed = await call({ connect: "parity" });
	expect(listed.details.tools).toContain("parity_read_hello");
	const read = await call({ server: "parity", tool: "read_hello", args: {} });
	expect(read.content[0].text).toContain("hello-http-resource");
	// The public output is rendered content, not ReadResourceResult. In particular
	// it carries no raw contents, MIME or _meta fields.
	expect(read.details.mcpResult).toBeUndefined();
	expect(read.contents).toBeUndefined();
	expect(listed.details.tools).not.toContain("parity_read_app");
	const unlisted = await call({ server: "parity", tool: "ui://parity/app.html", args: {} });
	expect(unlisted.details.error).toBe("tool_not_found");
	// The bridge exposes the upstream runtime and administration only.
	expect(entry.capabilities.snapshot()).toMatchObject({
		mcp: 1,
	});
	expect(entry.capabilities.snapshot()).not.toHaveProperty("mcp-apps");
	expect(entry.capabilities.snapshot()).not.toHaveProperty("mcp-app-tools");
});

test("adapter OAuth uses local discovery, PKCE, state validation, token exchange and refresh", async () => {
	// Upstream's explicit test store exercises its credential mechanics without
	// reading/writing the operator keyring. OS keyring availability is a separate gate.
	setEnv("PI_MCP_ADAPTER_TEST_AUTH_STORE", "memory");
	const mcp = await startHttpFixture();
	cleanups.push(() => mcp.close());
	const grants: string[] = [];
	let challenge = "";
	let origin = "";
	let accessExpired = false;
	const issuer = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		async fetch(request) {
			const url = new URL(request.url);
			if (url.pathname.startsWith("/.well-known/oauth-protected-resource"))
				return Response.json({ resource: `${origin}/mcp`, authorization_servers: [origin] });
			if (url.pathname === "/.well-known/oauth-authorization-server")
				return Response.json({
					issuer: origin,
					authorization_endpoint: `${origin}/authorize`,
					token_endpoint: `${origin}/token`,
					response_types_supported: ["code"],
					grant_types_supported: ["authorization_code", "refresh_token"],
					code_challenge_methods_supported: ["S256"],
					token_endpoint_auth_methods_supported: ["none"],
				});
			if (url.pathname === "/token") {
				const body = new URLSearchParams(await request.text());
				const grant = body.get("grant_type") ?? "";
				grants.push(grant);
				if (grant === "authorization_code") {
					if (
						body.get("code") !== "fixture-code" ||
						createHash("sha256")
							.update(body.get("code_verifier") ?? "")
							.digest("base64url") !== challenge
					)
						return Response.json({ error: "invalid_grant" }, { status: 400 });
					return Response.json({
						token_type: "Bearer",
						access_token: "fixture-access",
						refresh_token: "fixture-refresh",
						expires_in: 1,
					});
				}
				if (grant === "refresh_token" && body.get("refresh_token") === "fixture-refresh")
					return Response.json({
						token_type: "Bearer",
						access_token: "fixture-refreshed",
						refresh_token: "fixture-refresh",
						expires_in: 3600,
					});
				return Response.json({ error: "invalid_grant" }, { status: 400 });
			}
			if (url.pathname === "/mcp") {
				if (
					!(
						accessExpired
							? ["Bearer fixture-refreshed"]
							: ["Bearer fixture-access", "Bearer fixture-refreshed"]
					).includes(request.headers.get("authorization") ?? "")
				)
					return new Response(null, {
						status: 401,
						headers: {
							"WWW-Authenticate": `Bearer resource_metadata="${origin}/.well-known/oauth-protected-resource"`,
						},
					});
				return fetch(mcp.url, {
					method: request.method,
					headers: request.headers,
					body: request.method === "POST" ? await request.text() : undefined,
				});
			}
			return new Response(null, { status: 404 });
		},
	});
	origin = `http://127.0.0.1:${issuer.port}`;
	cleanups.push(async () => {
		issuer.stop(true);
	});
	const { call } = await adapterSession({
		mcpServers: {
			oauth_fixture: {
				url: `${origin}/mcp`,
				auth: "oauth",
				oauth: { clientId: "fixture-client", redirectUri: "https://client.example/callback" },
			},
		},
	});
	const started = await call({ action: "auth-start", server: "oauth_fixture" });
	expect(started.details.error).toBeUndefined();
	const authorization = new URL(started.details.authorizationUrl);
	expect(authorization.origin).toBe(origin);
	expect(authorization.searchParams.get("client_id")).toBe("fixture-client");
	expect(authorization.searchParams.get("code_challenge_method")).toBe("S256");
	challenge = authorization.searchParams.get("code_challenge")!;
	expect(challenge.length).toBeGreaterThan(32);
	const state = authorization.searchParams.get("state");
	expect(state).toBeString();
	const rejected = await call({
		action: "auth-complete",
		server: "oauth_fixture",
		args: { redirectUrl: "https://client.example/callback?code=fixture-code&state=wrong" },
	});
	expect(rejected.details.error).toBe("auth_complete_failed");
	expect(grants).toEqual([]);
	const completed = await call({
		action: "auth-complete",
		server: "oauth_fixture",
		args: { redirectUrl: `https://client.example/callback?code=fixture-code&state=${state}` },
	});
	expect(completed.details).toMatchObject({ authenticated: true });
	await Bun.sleep(1100);
	accessExpired = true;
	expect((await call({ connect: "oauth_fixture" })).details.error).toBeUndefined();
	expect(
		(await call({ server: "oauth_fixture", tool: "echo_text", args: { text: "authenticated" } }))
			.content[0].text,
	).toBe("http:authenticated");
	expect(grants).toEqual(["authorization_code", "refresh_token"]);
	expect(mcp.seenAuth).toContain("Bearer fixture-refreshed");
});

test("adapter status projects the canonical server states", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-status-`);
	const stdio = await writeStdioFixture(dir);
	const { entry, sessions, call } = await adapterSession({
		mcpServers: {
			parity_on: { command: "node", args: [stdio] },
			parity_off: { command: "node", args: [stdio], disabled: true },
		},
	});
	await call({ connect: "parity_on" });
	const context = sessions.context(entry);
	const status = (await entry.capabilities.call("adapter.status", {}, context)) as any;
	expect(status.engine).toBe("pi-mcp-adapter");
	const servers = (status.snapshot?.servers ?? (await call({})).details.servers) as any[];
	const canonical = new Set([
		"connected",
		"cached",
		"failed",
		"needs-auth",
		"not-connected",
		"disabled",
	]);
	for (const server of servers) {
		expect(canonical.has(normalizeStatus(server.status))).toBe(true);
	}
	const names = new Map(servers.map((server) => [server.name, normalizeStatus(server.status)]));
	expect(names.get("parity_on")).toBe("connected");
	expect(names.get("parity_off")).toBe("disabled");
});
