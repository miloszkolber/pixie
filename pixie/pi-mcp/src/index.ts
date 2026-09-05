import { join, resolve } from "node:path";
import type { ImageContent, TextContent } from "@earendil-works/pi-ai";
import type { AgentSession } from "@earendil-works/pi-coding-agent";
import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { ToolListChangedNotificationSchema } from "@modelcontextprotocol/sdk/types.js";
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
	prepare?: (options: { connectOnStart: boolean }) => void;
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
	let connectOnStart = true;
	const warnings: string[] = [];
	const attached = new Set<string>();
	const desiredConfigs = new Map<string, ConnectionConfig>();
	const pending = new Map<string, Promise<unknown>>();
	let lifetime = new AbortController();
	const serial = <T>(name: string, operation: () => Promise<T>): Promise<T> => {
		const result = (pending.get(name) ?? Promise.resolve()).catch(() => {}).then(operation);
		pending.set(name, result);
		void result
			.finally(() => {
				if (pending.get(name) === result) pending.delete(name);
			})
			.catch(() => {});
		return result;
	};
	const disconnectNow = async (name: string) => {
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
	const establish = async (c: ConnectionConfig, signal: AbortSignal, refresh = false) => {
		signal.throwIfAborted();
		if (!c.enabled) {
			await disconnectNow(c.name);
			return;
		}
		const previous = live.get(c.name);
		if (!refresh && previous && JSON.stringify(previous.config) === JSON.stringify(c)) return;
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
								fetch: (url, init) => {
									const merged = new Headers(init?.headers);
									for (const [name, value] of Object.entries(headers)) merged.set(name, value);
									return fetch(url, { ...init, headers: merged, redirect: "error" });
								},
							},
						})
					: new StreamableHTTPClientTransport(new URL(c.uri!), {
							requestInit: { headers, redirect: "error" },
						});

		const addedNames = new Set<string>();
		try {
			const abort = () => {
				void client.close().catch(() => {});
			};
			signal.addEventListener("abort", abort, { once: true });
			try {
				await client.connect(transport, { timeout: 10000, signal });
			} finally {
				signal.removeEventListener("abort", abort);
			}
			const tools = new Map<string, RecordValue>();
			const definitions: import("@modelcontextprotocol/sdk/types.js").Tool[] = [];
			let cursor: string | undefined;
			let pages = 0;
			do {
				const list = client.getServerCapabilities()?.tools
					? await client.listTools({ cursor }, { timeout: 10000, signal })
					: { tools: [], nextCursor: undefined };
				definitions.push(...list.tools);
				cursor = list.nextCursor;
				if (++pages > 20 || definitions.length > 2000)
					throw new Error("MCP tool catalog exceeds limit");
			} while (cursor);
			signal.throwIfAborted();
			for (const t of definitions) {
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
							if (block.type === "text") return [{ type: "text" as const, text: text(block.text) }];
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
			await disconnectNow(c.name);
			live.set(c.name, { client, config: c, tools });
			desiredConfigs.set(c.name, c);
			client.onclose = () => {
				if (live.get(c.name)?.client !== client) return;
				live.delete(c.name);
				pi.setActiveTools(pi.getActiveTools().filter((name) => !addedNames.has(name)));
			};
			client.setNotificationHandler(ToolListChangedNotificationSchema, async () => {
				try {
					await connect(c, undefined, true);
				} catch {
					warnings.push(`MCP tool refresh unavailable: ${c.name}`);
				}
			});
			pi.setActiveTools([
				...new Set([
					...pi.getActiveTools(),
					...[...tools.keys()].map((name) => `${c.name}__${name}`),
				]),
			]);
		} catch (error) {
			pi.setActiveTools(
				pi
					.getActiveTools()
					.filter(
						(name) => !addedNames.has(name) || previous?.tools.has(name.slice(c.name.length + 2)),
					),
			);
			await client.close().catch(() => {});
			throw error;
		}
	};
	const connect = (c: ConnectionConfig, signal?: AbortSignal, refresh = false) => {
		const budget = AbortSignal.any([
			lifetime.signal,
			AbortSignal.timeout(10000),
			...(signal ? [signal] : []),
		]);
		return serial(c.name, () => establish(c, budget, refresh));
	};
	const disconnect = (name: string) => serial(name, () => disconnectNow(name));
	const close = async () => {
		lifetime.abort();
		await Promise.allSettled([...pending.values()]);
		await Promise.allSettled([...live.keys()].map(disconnectNow));
	};
	const connectAll = async (
		values: [string, unknown][],
		signal: AbortSignal,
		notify?: MCPContext["notify"],
	) => {
		const queue = [...values];
		await Promise.all(
			Array.from({ length: Math.min(4, queue.length) }, async () => {
				for (let item = queue.shift(); item; item = queue.shift()) {
					const [name, value] = item;
					try {
						const c = config({ ...object(value), name });
						desiredConfigs.set(name, c);
						await connect(c, signal);
					} catch {
						const error = `MCP connection unavailable: ${name}`;
						warnings.push(error);
						if (warnings.length > 100) warnings.shift();
						notify?.({ type: "extension_error", error });
					}
				}
			}),
		);
	};
	const initialize = async (sessionId?: string) => {
		await close();
		lifetime = new AbortController();
		desiredConfigs.clear();
		warnings.length = 0;
		const saved = sessionId ? (await memberships.read())[sessionId] : undefined;
		const connections = { ...(await store.read()), ...saved?.add };
		for (const name of saved?.remove ?? []) delete connections[name];
		await connectAll(Object.entries(connections), AbortSignal.timeout(10000));
	};
	pi.on("session_start", (_event, ctx) =>
		connectOnStart
			? initialize(
					ctx.sessionManager.getSessionFile() ? ctx.sessionManager.getSessionId() : undefined,
				)
			: undefined,
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
		attach: (p, ctx) =>
			serial("$attach", async () => {
				const removed = (await memberships.read())[ctx.session.sessionId]?.remove ?? [];
				const servers = (Array.isArray(p.servers) ? p.servers : []).filter(
					(value) => !removed.includes(text(object(value).name)),
				);
				const desired = new Set(servers.map((value) => text(object(value).name)));
				for (const name of attached) {
					if (!desired.has(name)) {
						await disconnect(name);
						attached.delete(name);
						desiredConfigs.delete(name);
					}
				}
				const failures: string[] = [];
				await connectAll(
					servers.map((value) => [text(object(value).name), value]),
					AbortSignal.any([ctx.signal, AbortSignal.timeout(10000)]),
					(event) => {
						failures.push(text(event.error));
						ctx.notify(event);
					},
				);
				for (const name of desired) attached.add(name);
				return { ok: failures.length === 0, unavailable: failures };
			}),
		"connections.list": async () => {
			const diagnostics = [...warnings];
			const extensions = Object.entries(await store.read()).map(([configKey, extension]) => {
				try {
					return {
						configKey,
						enabled: object(extension).enabled !== false,
						extension: wrap(config({ ...object(extension), name: configKey })),
					};
				} catch {
					diagnostics.push(`Invalid MCP configuration: ${configKey}`);
					return {
						configKey,
						enabled: object(extension).enabled !== false,
						invalid: true,
						extension: { type: "mcp", server: { name: configKey, type: "invalid" } },
					};
				}
			});
			return { extensions, warnings: [...new Set(diagnostics)] };
		},
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
		"session.list": async (_p, ctx) => {
			await connectAll(
				[...desiredConfigs].filter(([name]) => !live.has(name)),
				AbortSignal.any([ctx.signal, AbortSignal.timeout(10000)]),
				ctx.notify,
			);
			return { extensions: list(), warnings: [...new Set(warnings)] };
		},
		"session.add": async (p, ctx) => {
			const c = config(p.extension);
			return serial(c.name, async () => {
				const previous = await memberships.update((state) => {
					const previous = structuredClone(state[ctx.session.sessionId]);
					state[ctx.session.sessionId] ??= { add: {}, remove: [] };
					const saved = state[ctx.session.sessionId];
					saved.add[c.name] = c;
					saved.remove = saved.remove.filter((name) => name !== c.name);
					return previous;
				});
				try {
					await establish(
						c,
						AbortSignal.any([ctx.signal, lifetime.signal, AbortSignal.timeout(10000)]),
					);
				} catch (error) {
					await memberships.update((state) => {
						const saved = state[ctx.session.sessionId];
						if (!saved) return;
						if (previous?.add[c.name]) saved.add[c.name] = previous.add[c.name];
						else delete saved.add[c.name];
						saved.remove = saved.remove.filter((name) => name !== c.name);
						if (previous?.remove.includes(c.name)) saved.remove.push(c.name);
					});
					throw error;
				}
				return { ok: true };
			});
		},
		"session.remove": async (p, ctx) => {
			const key = required(p.extensionKey, "connection");
			return serial(key, async () => {
				await memberships.update((state) => {
					state[ctx.session.sessionId] ??= { add: {}, remove: [] };
					const saved = state[ctx.session.sessionId];
					delete saved.add[key];
					if (!saved.remove.includes(key)) saved.remove.push(key);
				});
				attached.delete(key);
				desiredConfigs.delete(key);
				await disconnectNow(key);
				return { ok: true };
			});
		},
		"session.forget": async (_p, ctx) => {
			await memberships.update((state) => {
				delete state[ctx.session.sessionId];
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
			const toolName =
				!text(p.extensionName ?? p.extension) && separator > 0
					? encoded.slice(separator + 2)
					: encoded;
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
	pi.events.emit(MCP_SERVICE_EVENT, {
		version: 1,
		operations,
		close,
		prepare: (options) => {
			connectOnStart = options.connectOnStart;
		},
	} satisfies MCPService);
}
