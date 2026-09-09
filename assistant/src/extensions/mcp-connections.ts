import { join, resolve } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { Capability, CapabilityContext } from "../capabilities.ts";
import { JsonStore, object, type RecordValue, required, text } from "../storage.ts";
import { blockedPiToolsCall, emitRuntimeRegister } from "./adapter-mcp.ts";

interface Registration {
	dispose(): Promise<void>;
}
interface Connection {
	definition: RecordValue;
	registration: Registration;
	source: RecordValue;
}
type Membership = { add: Record<string, RecordValue>; remove: string[] };

// Compatibility for persisted Pixie connection records only. Registration and
// all execution belong to the upstream adapter, not an MCP client here.
function connection(
	value: unknown,
	agentDir: string,
): { name: string; definition: RecordValue; source: RecordValue } {
	const raw = object(value);
	const source = raw.type === "mcp" ? object(raw.server) : raw;
	const name = required(source.name, "MCP name", 128);
	if (!/^[a-zA-Z0-9_-]+$/.test(name) || name.includes("__")) throw new Error("Invalid MCP name");
	const definition: RecordValue = {};
	if (source.command !== undefined || source.type === "stdio") {
		definition.command = required(source.command, "MCP command");
		if (
			source.args !== undefined &&
			(!Array.isArray(source.args) || source.args.some((v) => typeof v !== "string"))
		)
			throw new Error("MCP arguments must be strings");
		definition.args = source.args ?? [];
		definition.env = Object.fromEntries(
			Object.entries(object(source.env)).map(([key, value]) => {
				if (typeof value !== "string") throw new Error("MCP environment values must be strings");
				return [key, value];
			}),
		);
		if (source.cwd)
			definition.cwd = resolve(
				agentDir,
				required(source.cwd, "MCP working directory").replace(
					/\$\{([A-Z0-9_]+)\}/g,
					(_match, key: string) => {
						const value = process.env[key];
						if (value === undefined) throw new Error(`Missing MCP environment variable: ${key}`);
						return value;
					},
				),
			);
	} else {
		const url = new URL(required(source.uri ?? source.url, "MCP URL"));
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
			throw new Error("MCP requires HTTP(S) without URL credentials");
		if (source.type && !["http", "streamable_http", "sse"].includes(text(source.type)))
			throw new Error("Unsupported MCP transport");
		definition.url = url.href;
		if (source.type === "sse") definition.httpTransport = "sse";
		definition.headers = Array.isArray(source.headers)
			? Object.fromEntries(
					source.headers.map((value) => {
						const h = object(value);
						return [required(h.name, "header"), text(h.value)];
					}),
				)
			: Object.fromEntries(
					Object.entries(object(source.headers)).map(([key, value]) => [key, text(value)]),
				);
	}
	return {
		name,
		definition,
		source: {
			...source,
			name,
			type: definition.command ? "stdio" : source.type === "sse" ? "sse" : "http",
		},
	};
}

export function mcpConnectionsBridge(
	pi: ExtensionAPI,
	agentDir: string,
): {
	operations: Capability["operations"];
	close: () => Promise<void>;
} {
	const store = new JsonStore<Record<string, RecordValue>>(join(agentDir, "mcp.json"), () => ({}));
	const memberships = new JsonStore<Record<string, Membership>>(
		join(agentDir, "mcp-sessions.json"),
		() => ({}),
	);
	const legacy = (value: Record<string, RecordValue>) => {
		if (
			!value ||
			Array.isArray(value) ||
			typeof value !== "object" ||
			Object.hasOwn(value, "mcpServers")
		)
			throw new Error(
				"Native MCP configuration is managed by pi-mcp-adapter, not legacy connection administration",
			);
		return value;
	};
	const live = new Map<string, Connection>();
	const attached = new Set<string>();
	let closed = false;
	let sessionId: string | undefined;
	let ownerSession: CapabilityContext["session"] | undefined;
	const authorize = (ctx: CapabilityContext) => {
		if (closed) throw new Error("MCP bridge is closed");
		if (
			!sessionId ||
			ctx.session.sessionId !== sessionId ||
			(ownerSession && ctx.session !== ownerSession)
		)
			throw new Error("MCP operation belongs to another session");
		ownerSession ??= ctx.session;
		ctx.signal.throwIfAborted();
	};
	let tail: Promise<unknown> = Promise.resolve();
	const serial = <T>(fn: () => Promise<T>): Promise<T> => {
		const result = tail
			.catch(() => {})
			.then(() => {
				if (closed) throw new Error("MCP bridge is closed");
				return fn();
			});
		tail = result;
		return result;
	};
	const remove = async (name: string) => {
		const previous = live.get(name);
		if (!previous) return;
		await previous.registration.dispose();
		live.delete(name);
	};
	const register = (value: unknown) => {
		const c = connection(value, agentDir);
		const previous = live.get(c.name);
		if (previous) {
			if (JSON.stringify(previous.definition) !== JSON.stringify(c.definition))
				throw new Error(`MCP connection already registered: ${c.name}`);
			return;
		}
		if (c.source.enabled === false) return;
		const registration = emitRuntimeRegister(
			(channel, data) => pi.events.emit(channel, data),
			c.name,
			c.definition,
		);
		live.set(c.name, { ...c, registration });
	};
	const close = async () => {
		closed = true;
		await tail.catch(() => {});
		await Promise.allSettled([...live.keys()].map(remove));
	};
	pi.on("session_start", async (_event, ctx) => {
		sessionId = ctx.sessionManager.getSessionId();
		// Runtime registration is lazy, including standalone in-memory Pi sessions.
		// Merely opening administration never executes a remote tool or subprocess.
		await serial(async () => {
			const saved = (await memberships.read())[ctx.sessionManager.getSessionId()];
			const stored = await store.read();
			const all = { ...(Object.hasOwn(stored, "mcpServers") ? {} : legacy(stored)), ...saved?.add };
			for (const name of saved?.remove ?? []) delete all[name];
			for (const [name, value] of Object.entries(all)) {
				try {
					register({ ...value, name });
				} catch {
					ctx.ui.notify(`MCP connection unavailable: ${name}`, "warning");
				}
			}
		});
	});
	pi.on("session_shutdown", close);
	const wrap = (source: RecordValue) => ({
		type: "mcp",
		server: {
			...source,
			url: source.url ?? source.uri,
			type: source.type === "streamable_http" ? "http" : source.type,
		},
	});
	return {
		close,
		operations: {
			"mcp.attach": (p, ctx) =>
				serial(async () => {
					authorize(ctx);
					const removed = (await memberships.read())[ctx.session.sessionId]?.remove ?? [];
					const servers = (Array.isArray(p.servers) ? p.servers : [])
						.map((v) => connection(v, agentDir))
						.filter((c) => !removed.includes(c.name));
					const desired = new Set(servers.map((c) => c.name));
					for (const name of attached)
						if (!desired.has(name)) {
							await remove(name);
							attached.delete(name);
						}
					const unavailable: string[] = [];
					for (const c of servers) {
						ctx.signal.throwIfAborted();
						try {
							register(c.source);
							attached.add(c.name);
						} catch {
							unavailable.push(`MCP connection unavailable: ${c.name}`);
						}
					}
					return { ok: unavailable.length === 0, unavailable };
				}),
			"pi.config.extensions.list": async () => {
				const warnings: string[] = [];
				const extensions = Object.entries(legacy(await store.read())).map(([configKey, source]) => {
					try {
						connection({ ...source, name: configKey }, agentDir);
					} catch {
						warnings.push(`Invalid MCP configuration: ${configKey}`);
						return {
							configKey,
							enabled: source.enabled !== false,
							invalid: true,
							extension: { type: "mcp", server: { name: configKey, type: "invalid" } },
						};
					}
					return {
						configKey,
						enabled: source.enabled !== false,
						extension: wrap({ ...source, name: configKey }),
					};
				});
				return { extensions, warnings };
			},
			"pi.config.extensions.add": async (p) => {
				const c = connection(p.extension, agentDir);
				await store.update((s) => {
					legacy(s);
					if (s[c.name]) throw new Error("MCP connection already exists");
					s[c.name] = { ...c.source, enabled: p.enabled !== false };
				});
				return { ok: true };
			},
			"pi.config.extensions.remove": async (p) => {
				await store.update((s) => {
					legacy(s);
					delete s[required(p.configKey, "connection")];
				});
				return { ok: true };
			},
			"pi.config.extensions.set-enabled": async (p) => {
				if (typeof p.enabled !== "boolean") throw new Error("Enabled must be boolean");
				await store.update((s) => {
					legacy(s);
					const c = s[required(p.configKey, "connection")];
					if (!c) throw new Error("Unknown connection");
					c.enabled = p.enabled;
				});
				return { ok: true };
			},
			"pi.session.extensions.list": () => ({
				extensions: [...live].map(([extensionKey, c]) => ({
					extensionKey,
					extension: wrap(c.source),
				})),
				warnings: [],
			}),
			"pi.session.extensions.add": (p, ctx) =>
				serial(async () => {
					const c = connection(p.extension, agentDir);
					const existed = live.has(c.name);
					register(c.source);
					try {
						await memberships.update((s) => {
							s[ctx.session.sessionId] ??= { add: {}, remove: [] };
							const m = s[ctx.session.sessionId];
							m.add[c.name] = c.source;
							m.remove = m.remove.filter((n) => n !== c.name);
						});
					} catch (error) {
						if (!existed) await remove(c.name);
						throw error;
					}
					return { ok: true };
				}),
			"pi.session.extensions.remove": (p, ctx) =>
				serial(async () => {
					const name = required(p.extensionKey, "connection");
					await memberships.update((s) => {
						s[ctx.session.sessionId] ??= { add: {}, remove: [] };
						const m = s[ctx.session.sessionId];
						delete m.add[name];
						if (!m.remove.includes(name)) m.remove.push(name);
					});
					await remove(name);
					attached.delete(name);
					return { ok: true };
				}),
			"adapter.session.forget": async (_p, ctx) => {
				await memberships.update((s) => {
					delete s[ctx.session.sessionId];
				});
				return { ok: true };
			},
			"pi.tools.call": () => blockedPiToolsCall(),
		},
	};
}
