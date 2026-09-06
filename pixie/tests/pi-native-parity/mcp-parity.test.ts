import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer, type Server } from "node:http";
import customMcp from "../../../pi/mcp/src/index.ts";
import piMcpAdapter, {
	PIXIE_BROWSER_RUNTIME_NAME,
	piMcpAdapterWithConfig,
} from "../../../pi/host/src/extensions/pi-mcp-adapter.ts";
import signet from "../../../pi/host/src/extensions/signet.ts";
import { startHost } from "../../../pi/host/src/server.ts";
import { Sessions } from "../../../pi/host/src/sessions.ts";
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
    else if (name === "image_tool") send({ jsonrpc: "2.0", id: msg.id, result: { content: [{ type: "image", data: "aGk=", mimeType: "image/png" }] } });
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

async function startHttpFixture(): Promise<HttpFixture> {
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
					// Deliberately not HTML: the headless parity run asserts the
					// fail-closed inline fallback instead of opening a UI session.
					ok({
						contents: [{ uri: message.params.uri, mimeType: "text/plain", text: "not html" }],
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
	const path = join(dir, `parity-stdio-${Date.now()}-${Math.floor(Math.random() * 1e6)}.mjs`);
	await writeFile(path, STDIO_FIXTURE);
	return path;
}

async function adapterSession(
	config: Record<string, unknown>,
	agentDir?: string,
): Promise<{ entry: any; sessions: Sessions; call: (args: Record<string, unknown>) => Promise<any> }> {
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

test("adapter profile starts under Bun and advertises its marker", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-host-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	const host = await startHost({
		agentDir: dir,
		secret: "parity-mcp-adapter-secret",
		port: 0,
		extensions: ["pi-mcp-adapter"],
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
		expect(ready.capabilities["pi-mcp-adapter"]).toBe(1);
	} finally {
		await host.close();
	}
	const { entry, sessions } = await adapterSession({ mcpServers: {} });
	const status = (await sessions.context(entry) &&
		(await entry.capabilities.call(
			"adapter.status",
			{},
			sessions.context(entry),
		))) as any;
	expect(status).toMatchObject({
		engine: "pi-mcp-adapter",
		version: "2.32.1",
		bunCompat: "unknown",
		proxyTool: "mcp",
		runtimeName: PIXIE_BROWSER_RUNTIME_NAME,
	});
});

test("adapter exposes one proxy tool where the custom client exposes one tool per remote tool", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-parity-surface-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	const stdio = await writeStdioFixture(dir);
	await writeFile(
		join(dir, "mcp.json"),
		JSON.stringify({ parity: { type: "stdio", command: "node", args: [stdio] } }),
	);
	const custom = new Sessions(dir, [(pi: any) => (customMcp as any)(pi, dir)], () => {});
	cleanups.push(() => custom.close());
	const customEntry = await custom.create(dir);
	const customTools = toolNames(customEntry).filter((name) => name.startsWith("parity__"));
	// The custom client eagerly connects at session start and registers one
	// model-visible tool per remote tool.
	expect(customTools.length).toBeGreaterThan(1);

	const { entry } = await adapterSession({
		mcpServers: { parity: { command: "node", args: [stdio] } },
	});
	const names = toolNames(entry);
	expect(names).toContain("mcp");
	expect(names.filter((name) => name.startsWith("parity__"))).toHaveLength(0);
	expect(names.filter((name) => name.startsWith("parity_"))).toHaveLength(0);
	// Qualitative overhead comparison only: one proxy tool against one tool
	// per remote tool. No token, latency, or memory numbers are claimed here.
	expect(1).toBeLessThan(customTools.length);
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
			config: { mcpServers: { parity_lazy: { command: "node", args: [stdio], lifecycle: "lazy" } } },
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
	const pending = tool.execute("parity-cancel", { tool: "parity_slow_tool", args: {} }, controller.signal);
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

test("adapter passes OAuth and bearer credentials through without crashing init", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	setEnv("PARITY_PROBE_TOKEN", "parity-token-123");
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
	expect(authStart.details.error).toBeString();
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

test("signet marker needs no MCP connection", async () => {
	const { sessions } = await fixture([signet]);
	const entry = await sessions.create((await mkdtemp(`${tmpdir()}/pixie-mcp-parity-signet-`)));
	expect(entry.capabilities.snapshot()).toMatchObject({ signet: 1 });
	expect(entry.capabilities.snapshot().mcp).toBeUndefined();
	const names = (entry.session.agent.state.tools as any[]).map((tool) => tool.name);
	expect(names.some((name) => name.includes("__"))).toBe(false);
});

test("adapter surfaces App metadata and falls back inline when no UI is available", async () => {
	const http = await startHttpFixture();
	cleanups.push(() => http.close());
	const { call } = await adapterSession({ mcpServers: { parity_http: { url: http.url } } });
	await call({ connect: "parity_http" });
	const described: any = await call({ describe: "parity_http_app_tool" });
	expect(described.details.tool.uiResourceUri).toBe("ui://parity/app.html");
	const result: any = await call({ tool: "parity_http_app_tool", args: {} });
	expect(result.details.mcpResult.content[0].text).toBe("app-ok");
	expect(result.details.uiOpen).toBe(false);
	expect(result.content[0].text).toContain("app-ok");
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
