import { join, resolve } from "node:path";
import type { ImageContent, TextContent } from "@earendil-works/pi-ai";
import type { AgentSession } from "@earendil-works/pi-coding-agent";
import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { Type } from "typebox";
export const MCP_SERVICE_EVENT = "pi-mcp:service:v1";
export interface MCPContext {
	session: AgentSession;
	signal: AbortSignal;
	notify: (event: RecordValue) => void;
}
export interface MCPService {
	version: 1;
	operations: Record<string, (p: RecordValue, ctx: MCPContext) => unknown | Promise<unknown>>;
	close: () => Promise<void>;
}

import { JsonStore, object, type RecordValue, required, text } from "./storage.ts";

interface ConnectionConfig {
	name: string;
	type: string;
	uri?: string;
	command?: string;
	args?: string[];
	env?: Record<string, string>;
	cwd?: string;
	headers?: Record<string, string>;
	enabled: boolean;
}
interface Connection {
	client: Client;
	config: ConnectionConfig;
	tools: Map<string, RecordValue>;
}
export default function mcpExtension(pi: ExtensionAPI, agentDir = getAgentDir()): void {
	const store = new JsonStore<Record<string, ConnectionConfig>>(
		join(agentDir, "mcp.json"),
		() => ({}),
	);
	const memberships = new JsonStore<
		Record<string, { add: Record<string, ConnectionConfig>; remove: string[] }>
	>(join(agentDir, "mcp-sessions.json"), () => ({}));
	const live = new Map<string, Connection>();
	const warnings: string[] = [];
	const attached = new Set<string>();
	const disconnect = async (name: string) => {
		const connection = live.get(name);
		if (!connection) return;
		live.delete(name);
		pi.setActiveTools(
			pi
				.getActiveTools()
				.filter(
					(tool) =>
						!connection.tools.has(tool.slice(name.length + 2)) || !tool.startsWith(`${name}__`),
				),
		);
		await connection.client.close();
	};
	const config = (value: unknown): ConnectionConfig => {
		const raw = object(value);
		const c = raw.type === "mcp" ? object(raw.server) : raw;
		const name = required(c.name, "MCP name", 128);
		if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.includes("__")) throw new Error("Invalid MCP name");
		if (c.command !== undefined || c.type === "stdio") {
			const command = required(c.command, "MCP command");
			if (
				c.args !== undefined &&
				(!Array.isArray(c.args) || c.args.some((v) => typeof v !== "string"))
			)
				throw new Error("MCP arguments must be strings");
			const env: Record<string, string> = {};
			for (const [key, value] of Object.entries(object(c.env))) {
				if (typeof value !== "string") throw new Error("MCP environment values must be strings");
				env[key] = value;
			}
			return {
				name,
				type: "stdio",
				command,
				args: (c.args as string[] | undefined) ?? [],
				env,
				...(c.cwd ? { cwd: required(c.cwd, "MCP working directory") } : {}),
				enabled: c.enabled !== false,
			};
		}
		const uri = required(c.uri ?? c.url, "MCP URL");
		const url = new URL(uri);
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
			throw new Error("MCP requires HTTP(S) without URL credentials");
		if (c.type && !["http", "streamable_http", "sse"].includes(text(c.type)))
			throw new Error("Unsupported MCP transport");
		const headers: Record<string, string> = {};
		if (Array.isArray(c.headers)) {
			for (const h of c.headers) {
				const v = object(h);
				headers[required(v.name, "header")] = text(v.value);
			}
		} else for (const [k, v] of Object.entries(object(c.headers))) headers[k] = text(v);
		return {
			name,
			type: c.type === "sse" ? "sse" : "streamable_http",
			uri,
			headers,
			enabled: c.enabled !== false,
		};
	};
	const connect = async (c: ConnectionConfig) => {
		if (!c.enabled) {
			await disconnect(c.name);
			return;
		}
		const previous = live.get(c.name);
		if (previous && JSON.stringify(previous.config) === JSON.stringify(c)) return;
		await disconnect(c.name);
		const client = new Client(
			{ name: "pi-mcp", version: "0.1.0" },
			{
				capabilities: {
					experimental: {
						"io.modelcontextprotocol/ui": { mimeTypes: ["text/html;profile=mcp-app"] },
					},
				},
			},
		);
		const expand = (value: string) =>
			value.replace(/\$\{([A-Z0-9_]+)\}/g, (_, name) => {
				const value = process.env[name];
				if (value === undefined) throw new Error(`Missing MCP environment variable: ${name}`);
				return value;
			});
		const headers = Object.fromEntries(
			Object.entries(c.headers ?? {}).map(([key, value]) => [key, expand(value)]),
		);
		const transport =
			c.type === "stdio"
				? new StdioClientTransport({
						command: expand(c.command!),
						args: c.args?.map(expand),
						env: {
							...Object.fromEntries(
								Object.entries(process.env).filter(
									(entry): entry is [string, string] => entry[1] !== undefined,
								),
							),
							...Object.fromEntries(
								Object.entries(c.env ?? {}).map(([key, value]) => [key, expand(value)]),
							),
						},
						cwd: c.cwd ? resolve(agentDir, expand(c.cwd)) : undefined,
						stderr: "ignore",
					})
				: c.type === "sse"
					? new SSEClientTransport(new URL(c.uri!), {
							requestInit: { headers, redirect: "error" },
							eventSourceInit: {
								fetch: (url, init) =>
									fetch(url, {
										...init,
										headers: { ...Object.fromEntries(new Headers(init?.headers)), ...headers },
										redirect: "error",
									}),
							},
						})
					: new StreamableHTTPClientTransport(new URL(c.uri!), {
							requestInit: { headers, redirect: "error" },
						});

		const addedNames = new Set<string>();
		try {
			await client.connect(transport, { timeout: 15000 });
			const tools = new Map<string, RecordValue>();
			let cursor: string | undefined;
			let pages = 0;
			do {
				const list = client.getServerCapabilities()?.tools
					? await client.listTools({ cursor }, { timeout: 30000 })
					: { tools: [], nextCursor: undefined };
				for (const t of list.tools) {
					tools.set(t.name, t as unknown as RecordValue);
					const name = `${c.name}__${t.name}`;
					addedNames.add(name);
					pi.registerTool({
						name,
						label: t.title ?? t.name,
						description: t.description ?? t.name,
						parameters: Type.Unsafe<RecordValue>(t.inputSchema),
						execute: async (_id, args, signal) => {
							const connection = live.get(c.name);
							if (!connection) throw new Error("MCP connection is not active");
							const result = await connection.client.callTool(
								{ name: t.name, arguments: args },
								undefined,
								{
									signal,
									timeout: 120000,
								},
							);
							const content = (Array.isArray(result.content) ? result.content : []).flatMap<
								TextContent | ImageContent
							>((b) => {
								const block = object(b);
								if (block.type === "text")
									return [{ type: "text" as const, text: text(block.text) }];
								if (block.type === "image")
									return [
										{
											type: "image" as const,
											mimeType: text(block.mimeType),
											data: text(block.data),
										},
									];
								return [{ type: "text" as const, text: JSON.stringify(block) }];
							});
							if (result.isError)
								throw new Error(
									content
										.filter((b) => b.type === "text")
										.map((b) => b.text)
										.join("\n") || "MCP tool failed",
								);
							const ui = object(object(t._meta).ui);
							return {
								content,
								details: {
									mcp: {
										server: c.name,
										toolName: t.name,
										meta: result._meta,
										structuredContent: result.structuredContent,
										isError: result.isError,
										...(ui.resourceUri
											? {
													app: {
														toolName: t.name,
														extensionName: c.name,
														resourceUri: ui.resourceUri,
														toolNameIsActual: true,
													},
												}
											: {}),
									},
								},
							};
						},
					});
				}
				cursor = list.nextCursor;
				if (++pages > 20) throw new Error("MCP tool catalog exceeds limit");
			} while (cursor);
			live.set(c.name, { client, config: c, tools });
			pi.setActiveTools([
				...new Set([
					...pi.getActiveTools(),
					...[...tools.keys()].map((name) => `${c.name}__${name}`),
				]),
			]);
		} catch (error) {
			pi.setActiveTools(pi.getActiveTools().filter((name) => !addedNames.has(name)));
			await client.close().catch(() => {});
			throw error;
		}
	};
	const close = async () => {
		await Promise.allSettled([...live.keys()].map(disconnect));
	};
	const initialize = async (sessionId?: string) => {
		await close();
		warnings.length = 0;
		const saved = sessionId ? (await memberships.read())[sessionId] : undefined;
		const connections = { ...(await store.read()), ...saved?.add };
		for (const name of saved?.remove ?? []) delete connections[name];
		for (const [name, value] of Object.entries(connections)) {
			try {
				const c = config({ ...value, name });
				if (c.enabled) await connect(c);
			} catch {
				warnings.push(`MCP connection unavailable: ${name}`);
			}
		}
	};
	pi.on("session_start", (_event, ctx) =>
		initialize(ctx.sessionManager.getSessionFile() ? ctx.sessionManager.getSessionId() : undefined),
	);
	pi.on("session_shutdown", close);
	const wrap = (c: ConnectionConfig) => ({
		type: "mcp",
		server: {
			type: c.type === "streamable_http" ? "http" : c.type,
			name: c.name,
			...(c.type === "stdio"
				? { command: c.command, args: c.args, env: c.env, cwd: c.cwd }
				: { url: c.uri }),
			headers: Object.entries(c.headers ?? {}).map(([name, value]) => ({ name, value })),
		},
	});
	const list = () =>
		[...live.values()].map((c) => ({ extension: wrap(c.config), extensionKey: c.config.name }));
	const operations: MCPService["operations"] = {
		attach: async (p, ctx) => {
			const failures: string[] = [];
			const servers = (Array.isArray(p.servers) ? p.servers : []).map(config);
			const desired = new Set(servers.map((c) => c.name));
			for (const name of attached) {
				if (!desired.has(name)) {
					await disconnect(name);
					attached.delete(name);
				}
			}
			for (const raw of servers) {
				attached.add(raw.name);
				try {
					await connect(config(raw));
				} catch {
					const name = text(object(raw).name) || "MCP";
					failures.push(name);
					ctx.notify({
						type: "extension_error",
						error: `Optional connection unavailable: ${name}`,
					});
				}
			}
			return { ok: failures.length === 0, unavailable: failures };
		},
		"connections.list": async () => ({
			extensions: Object.entries(await store.read()).map(([configKey, extension]) => ({
				configKey,
				enabled: extension.enabled !== false,
				extension: wrap(config({ ...extension, name: configKey })),
			})),
			warnings: [...warnings],
		}),
		"connections.add": async (p) => {
			const c = config(p.extension);
			c.enabled = p.enabled !== false;
			await store.update((s) => {
				if (s[c.name]) throw new Error("MCP connection already exists");
				s[c.name] = c;
			});
			return { ok: true };
		},
		"connections.set-enabled": async (p) => {
			await store.update((s) => {
				const c = s[required(p.configKey, "connection")];
				if (!c) throw new Error("Unknown connection");
				if (typeof p.enabled !== "boolean") throw new Error("Enabled must be boolean");
				c.enabled = p.enabled;
			});
			return { ok: true };
		},
		"connections.remove": async (p) => {
			await store.update((s) => {
				delete s[required(p.configKey, "connection")];
			});
			return { ok: true };
		},
		"session.list": () => ({ extensions: list() }),
		"session.add": async (p, ctx) => {
			const c = config(p.extension);
			await connect(c);
			await memberships.update((state) => {
				state[ctx.session.sessionId] ??= { add: {}, remove: [] };
				const saved = state[ctx.session.sessionId];
				saved.add[c.name] = c;
				saved.remove = saved.remove.filter((name) => name !== c.name);
			});
			return { ok: true };
		},
		"session.remove": async (p, ctx) => {
			const key = required(p.extensionKey, "connection");
			attached.delete(key);
			await disconnect(key);
			await memberships.update((state) => {
				state[ctx.session.sessionId] ??= { add: {}, remove: [] };
				const saved = state[ctx.session.sessionId];
				delete saved.add[key];
				if (!saved.remove.includes(key)) saved.remove.push(key);
			});
			return { ok: true };
		},
		"resources.read": async (p, ctx) => {
			const name = required(p.extensionName ?? p.extension, "MCP connection");
			const c = live.get(name);
			if (!c) throw new Error("MCP connection is unavailable");
			return {
				result: await c.client.readResource(
					{ uri: required(p.uri, "resource") },
					{ signal: ctx.signal, timeout: 120000 },
				),
			};
		},
		"tools.call": async (p, ctx) => {
			const encoded = required(p.toolName ?? p.name, "tool");
			const separator = encoded.indexOf("__");
			const name =
				text(p.extensionName ?? p.extension) || (separator > 0 ? encoded.slice(0, separator) : "");
			const toolName = separator > 0 ? encoded.slice(separator + 2) : encoded;
			const c = live.get(name);
			if (!c || !c.tools.has(toolName)) throw new Error("Unknown MCP tool");
			const result = await c.client.callTool(
				{ name: toolName, arguments: object(p.arguments) },
				undefined,
				{ signal: ctx.signal, timeout: 120000 },
			);
			return { ...result, isError: result.isError === true };
		},
	};
	pi.events.emit(MCP_SERVICE_EVENT, { version: 1, operations, close } satisfies MCPService);
}
