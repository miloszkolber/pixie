import { randomUUID, timingSafeEqual } from "node:crypto";
import { mkdir, realpath } from "node:fs/promises";
import { join, resolve } from "node:path";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import mcp from "@pixie/pi-mcp";
import type { ServerWebSocket } from "bun";
import { lock } from "proper-lockfile";
import agents from "./extensions/agents.ts";
import plans from "./extensions/plans.ts";
import web from "./extensions/web.ts";
import { Providers } from "./providers.ts";
import { type ManagedSession, Sessions } from "./sessions.ts";
import { HostError, object, type RecordValue, required, serviceStore, text } from "./storage.ts";

export interface HostOptions {
	agentDir: string;
	secret: string;
	hostname?: string;
	port?: number;
	extensions?: string[];
}
interface Peer {
	sessions: Set<string>;
	attachments: Map<string, { params: RecordValue; entry: WeakRef<ManagedSession> }>;
	logins: Set<string>;
	active: Set<number>;
	loading: Map<string, { messages: RecordValue[]; bytes: number }>;
}
export async function startHost(options: HostOptions) {
	await mkdir(options.agentDir, { recursive: true, mode: 0o700 });
	const agentDir = await realpath(options.agentDir);
	await mkdir(join(agentDir, "pixie"), { recursive: true, mode: 0o700 });
	const release = await lock(join(agentDir, "pixie", "host"), { realpath: false });
	try {
		const host = await startUnlockedHost({ ...options, agentDir });
		let closing: Promise<void> | undefined;
		return { ...host, close: () => (closing ??= host.close().finally(release)) };
	} catch (error) {
		await release();
		throw error;
	}
}
async function startUnlockedHost(options: HostOptions) {
	const agentDir = resolve(options.agentDir);
	await mkdir(agentDir, { recursive: true, mode: 0o700 });
	if (options.secret.length < 16)
		throw new Error("Pi host secret must contain at least 16 characters");
	const profiles: Record<string, ExtensionFactory> = {
		mcp: (pi) => mcp(pi, agentDir),
		agents: (pi) =>
			agents(pi, agentDir, async (cwd) => {
				const entry = await sessions.create(cwd);
				return {
					session: entry.session,
					prompt: async (text) =>
						sessions.call("session.prompt", {
							sessionId: entry.session.sessionId,
							content: [{ type: "text", text }],
						}),
					close: () => sessions.release(entry.session.sessionId),
				};
			}),
		plans,
		web,
	};
	const names = options.extensions ?? [];
	if (new Set(names).size !== names.length || names.some((n) => !profiles[n]))
		throw new Error("Unknown or duplicate bundled extension");
	const identity = serviceStore<{ id: string }>(agentDir, "identity", () => ({ id: randomUUID() }));
	const runtimeId = await identity.update((s) => s.id);
	const peers = new Set<ServerWebSocket<Peer>>();
	let stopping = false;
	const inflight = new Set<Promise<unknown>>();
	const send = (peer: ServerWebSocket<Peer>, value: unknown) => {
		const data = JSON.stringify(value);
		if (Buffer.byteLength(data) > 32 * 1024 * 1024) {
			peer.close(1009, "Response exceeds limit");
			return;
		}
		if (peer.send(data) === -1 && peer.getBufferedAmount() > 32 * 1024 * 1024)
			peer.close(1013, "Consumer is too slow");
	};
	const sessions = new Sessions(
		agentDir,
		names.map((n) => profiles[n]),
		(sessionId, event, sequence) => {
			for (const peer of peers) {
				const message = { method: "session.event", params: { sessionId, event, sequence } };
				const loading = peer.data.loading.get(sessionId);
				if (loading) {
					loading.bytes += Buffer.byteLength(JSON.stringify(message));
					if (loading.bytes > 32 * 1024 * 1024) {
						peer.data.loading.delete(sessionId);
						peer.close(1013, "Session attachment exceeds buffer limit");
					} else loading.messages.push(message);
				} else if (peer.data.sessions.has(sessionId)) send(peer, message);
			}
		},
	);
	const control = await sessions.control(agentDir);
	const providers = new Providers(
		control.modelRuntime,
		control.session.settingsManager,
		(frame) => {
			for (const peer of peers)
				if (peer.data.logins.has(text(frame.loginId)))
					send(peer, { method: "provider.login", params: frame });
		},
		agentDir,
	);
	const capabilitySnapshot = (entry = control) => ({
		sessions: 1,
		providers: 1,
		...entry.capabilities.snapshot(),
	});
	const attach = async (entry: ManagedSession, p: RecordValue) => {
		if (entry.capabilities.snapshot().mcp === 1 && Array.isArray(p.mcpServers))
			await entry.capabilities.call(
				"mcp.attach",
				{ servers: p.mcpServers },
				sessions.context(entry),
			);
	};
	const dispatch = async (
		method: string,
		p: RecordValue,
		peer: ServerWebSocket<Peer>,
	): Promise<unknown> => {
		if (method === "runtime.hello")
			return {
				protocolVersion: 1,
				runtimeId,
				version: "0.85.1",
				capabilities: capabilitySnapshot(),
			};
		if (method === "provider.loginStart") {
			const result = (await providers.call(method, p)) as RecordValue;
			peer.data.logins.add(text(result.loginId));
			return result;
		}
		if (
			method === "provider.loginReply" ||
			method === "provider.loginCancel" ||
			method === "provider.loginBegin"
		) {
			if (!peer.data.logins.has(text(p.loginId)))
				throw new Error("Authentication belongs to another connection");
			return providers.call(method, p);
		}
		if (
			method.startsWith("pi.providers.") ||
			method.startsWith("pi.defaults.") ||
			method.startsWith("pi.preferences.")
		)
			return providers.call(method, p);
		if (method === "session.create" || method === "session.fork") {
			const entry = await sessions.create(
				required(p.cwd, "project"),
				method === "session.fork" ? required(p.sessionId, "session") : undefined,
			);
			entry.refs++;
			try {
				await attach(entry, p);
			} finally {
				entry.refs--;
			}
			peer.data.attachments.set(entry.session.sessionId, { params: p, entry: new WeakRef(entry) });
			peer.data.sessions.add(entry.session.sessionId);
			return sessions.snapshot(entry);
		}
		if (text(p.sessionId)) {
			return sessions.use(text(p.sessionId), text(p.cwd) || undefined, async (entry) => {
				if (method === "session.load") {
					await attach(entry, p);
					peer.data.attachments.set(entry.session.sessionId, {
						params: p,
						entry: new WeakRef(entry),
					});
					peer.data.sessions.add(entry.session.sessionId);
					return sessions.snapshot(entry, true);
				}
				if (
					!peer.data.sessions.has(entry.session.sessionId) &&
					method !== "pi.session.info" &&
					method !== "session.delete"
				)
					throw new Error("Attach the session before using it");
				if (method === "pi.slash-commands.list")
					return { availableCommands: sessions.commands(entry) };
				const attachment = peer.data.attachments.get(entry.session.sessionId);
				if (attachment && attachment.entry.deref() !== entry) {
					await attach(entry, attachment.params);
					attachment.entry = new WeakRef(entry);
				}
				return sessions.call(method, p);
			});
		}
		if (method === "session.list") return sessions.list(text(p.cursor));
		if (
			method === "runtime.capabilities" ||
			method === "pi.slash-commands.list" ||
			method.startsWith("pi.sources.") ||
			method === "pi.agent-mentions.list"
		) {
			const cwd = text(p.cwd) || text(p.projectDir) || text(object(p.target).projectDir);
			const entry = cwd ? await sessions.control(cwd) : control;
			try {
				if (method === "runtime.capabilities") return capabilitySnapshot(entry);
				if (method === "pi.slash-commands.list")
					return { availableCommands: sessions.commands(entry) };
				return await entry.capabilities.call(method, p, sessions.context(entry));
			} finally {
				if (entry !== control) await entry.close();
			}
		}
		return control.capabilities.call(method, p, sessions.context(control));
	};
	let server: ReturnType<typeof Bun.serve<Peer>>;
	try {
		server = Bun.serve<Peer>({
			hostname: options.hostname ?? "127.0.0.1",
			port: options.port ?? 3284,
			maxRequestBodySize: 32 * 1024 * 1024,
			fetch(request, server) {
				const url = new URL(request.url);
				if (url.pathname === "/livez") return Response.json({ ok: true });
				const provided = Buffer.from(request.headers.get("authorization") ?? ""),
					expected = Buffer.from(`Bearer ${options.secret}`);
				if (provided.length !== expected.length || !timingSafeEqual(provided, expected))
					return new Response("Unauthorized", { status: 401 });
				if (request.headers.has("origin"))
					return new Response("Browser access is not allowed", { status: 403 });
				if (url.pathname === "/readyz")
					return Response.json({
						protocolVersion: 1,
						runtimeId,
						capabilities: capabilitySnapshot(),
					});
				if (
					url.pathname === "/pi" &&
					server.upgrade(request, {
						data: {
							sessions: new Set(),
							attachments: new Map(),
							logins: new Set(),
							active: new Set(),
							loading: new Map(),
						},
					})
				)
					return;
				return new Response("Not found", { status: 404 });
			},
			websocket: {
				maxPayloadLength: 32 * 1024 * 1024,
				backpressureLimit: 32 * 1024 * 1024,
				closeOnBackpressureLimit: true,
				open(peer) {
					peers.add(peer);
				},
				message(peer, raw) {
					if (stopping) {
						peer.close(1001, "Service stopping");
						return;
					}
					let request: RecordValue;
					try {
						request = object(JSON.parse(String(raw)));
					} catch {
						peer.close(1007, "Invalid JSON");
						return;
					}
					const id = Number(request.id);
					if (!Number.isSafeInteger(id) || id <= 0 || peer.data.active.has(id)) {
						peer.close(1008, "Invalid request ID");
						return;
					}
					if (peer.data.active.size >= 128) {
						send(peer, { id, error: { code: -32000, message: "Too many pending requests" } });
						return;
					}
					peer.data.active.add(id);
					const method = text(request.method),
						params = object(request.params),
						sessionId = text(params.sessionId);
					const loading = method === "session.load" && sessionId !== "";
					if (loading && peer.data.loading.has(sessionId)) {
						peer.data.active.delete(id);
						send(peer, { id, error: { code: -32000, message: "Session is already loading" } });
						return;
					}
					if (loading) peer.data.loading.set(sessionId, { messages: [], bytes: 0 });
					const flush = (sequence = -1) => {
						if (!loading) return;
						const buffered = peer.data.loading.get(sessionId)?.messages ?? [];
						peer.data.loading.delete(sessionId);
						for (const message of buffered)
							if (Number(object(message.params).sequence) > sequence) send(peer, message);
					};
					const operation = dispatch(method, params, peer)
						.then(
							(result) => {
								const snapshot = object(result);
								if (
									loading &&
									Array.isArray(snapshot.messages) &&
									Buffer.byteLength(JSON.stringify(snapshot.messages)) > 8 * 1024 * 1024
								) {
									let chunk: unknown[] = [],
										bytes = 0;
									for (const message of snapshot.messages) {
										const size = Buffer.byteLength(JSON.stringify(message));
										if (chunk.length && bytes + size > 8 * 1024 * 1024) {
											send(peer, {
												method: "session.history",
												params: { sessionId, messages: chunk },
											});
											chunk = [];
											bytes = 0;
										}
										chunk.push(message);
										bytes += size;
									}
									if (chunk.length)
										send(peer, {
											method: "session.history",
											params: { sessionId, messages: chunk },
										});
									result = { ...snapshot, messages: [] };
								}
								send(peer, { id, result: result ?? null });
								flush(Number(object(result).eventSequence ?? -1));
							},
							(error) => {
								send(peer, {
									id,
									error: {
										code: error instanceof HostError ? error.code : -32000,
										message: error instanceof Error ? error.message : "Pi request failed",
									},
								});
								flush();
							},
						)
						.finally(() => peer.data.active.delete(id));
					inflight.add(operation);
					void operation.finally(() => inflight.delete(operation)).catch(() => {});
				},
				close(peer) {
					peers.delete(peer);
					for (const id of peer.data.logins) providers.cancel(id);
				},
			},
		});
	} catch (error) {
		providers.close();
		await sessions.close();
		await control.close();
		throw error;
	}
	return {
		server,
		sessions,
		control,
		capabilities: capabilitySnapshot(),
		close: async () => {
			stopping = true;
			const stopped = server.stop(true);
			for (const peer of peers) peer.close(1001, "Service stopping");
			providers.close();
			await sessions.close();
			await control.close();
			await Promise.allSettled(inflight);
			await stopped;
		},
	};
}
