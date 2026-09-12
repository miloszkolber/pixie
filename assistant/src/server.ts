import { randomUUID, timingSafeEqual } from "node:crypto";
import { mkdir, realpath } from "node:fs/promises";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import type { ServerWebSocket } from "bun";
import { lock } from "proper-lockfile";
import { ADMIN_OPERATIONS } from "./admin-profiles/index.ts";
import { configureExtension } from "./extension-configuration.ts";
import { extensionInventory } from "./extension-inventory.ts";
import llama, { llamaFactory } from "./extensions/llama.ts";
import {
	createDeadline,
	DEFAULT_ADMIN_DEADLINE_MS,
	DEFAULT_AUTH_DEADLINE_MS,
	DEFAULT_HELLO_DEADLINE_MS,
	DEFAULT_SERVICE_DRAIN_DEADLINE_MS,
	type Deadline,
} from "./lifecycle.ts";
import { Providers } from "./providers.ts";
import { RuntimeWiring } from "./runtime-wiring.ts";
import { buildEventFrame, serializeFrame } from "./serialize.ts";
import type { PromptBlock } from "./session/types.ts";
import { redactSecrets } from "./session/validation.ts";
import { UncertainPromptError } from "./session-runtime.ts";
import { type ManagedSession, Sessions } from "./sessions.ts";
import {
	validateAssistantHost,
	validateAssistantRuntimePort,
	validateAssistantSecret,
} from "./startup.ts";
import { HostError, object, type RecordValue, required, serviceStore, text } from "./storage.ts";
import type { NativeJsonlTransport } from "./transport/jsonl-transport.ts";

export interface HostOptions {
	agentDir: string;
	secret: string;
	hostname?: string;
	port?: number;
	llama?: boolean;
	/** Permit `runtime.restart` to end the process for the service manager. */
	allowSelfRestart?: boolean;
	/** Notify the executable/composition root after restart admission is stopped. */
	onRestart?: () => void;
	/** Bound host construction before a partially-started host is cleaned up. */
	startupDeadlineMs?: number;
	/** Bound provider/extension administration requests. */
	adminDeadlineMs?: number;
	/** Bound the complete service drain, including construction and teardown. */
	drainDeadlineMs?: number;
	/** Optional real child-backed JSONL transport for native control handoff. */
	nativeTransport?: NativeJsonlTransport;
}

// The host reports the SDK it actually embeds; the protocol contract defines
// this as the host SDK version.
const sdkVersion = (
	createRequire(import.meta.url)("@earendil-works/pi-coding-agent/package.json") as {
		version: string;
	}
).version;

interface Peer {
	handshaken: boolean;
	handshakePending: boolean;
	sessions: Set<string>;
	attachments: Map<string, { params: RecordValue; entry: WeakRef<ManagedSession> }>;
	logins: Set<string>;
	active: Set<number>;
	loading: Map<string, { messages: RecordValue[]; bytes: number }>;
}

function operationDeadlineMs(method: string, options: HostOptions): number | undefined {
	if (method === "runtime.hello") return DEFAULT_HELLO_DEADLINE_MS;
	if (
		method === "provider.loginStart" ||
		method === "provider.loginReply" ||
		method === "provider.loginBegin" ||
		method === "provider.loginCancel"
	)
		return DEFAULT_AUTH_DEADLINE_MS;
	if (
		method.startsWith("provider.") ||
		method.startsWith("pi.providers.") ||
		method.startsWith("pi.defaults.") ||
		method.startsWith("pi.preferences.") ||
		method.startsWith("pi.extensions.") ||
		method.startsWith("pi.config.extensions.") ||
		method.startsWith("pi.session.extensions.") ||
		method === "runtime.capabilities" ||
		method === "pi.slash-commands.list" ||
		method.startsWith("pi.sources.") ||
		method === "pi.agent-mentions.list"
	)
		return options.adminDeadlineMs ?? DEFAULT_ADMIN_DEADLINE_MS;
	return undefined;
}

export async function startHost(options: HostOptions) {
	const hostname = validateAssistantHost(options.hostname);
	const port = validateAssistantRuntimePort(options.port);
	const secret = validateAssistantSecret(options.secret);
	const startup = createDeadline(
		options.startupDeadlineMs ?? DEFAULT_SERVICE_DRAIN_DEADLINE_MS,
		"Assistant startup",
	);
	let release: (() => Promise<void>) | undefined;
	let starting: Promise<Awaited<ReturnType<typeof startUnlockedHost>>> | undefined;
	try {
		await startup.race(
			mkdir(options.agentDir, { recursive: true, mode: 0o700 }),
			"Assistant state setup",
		);
		const agentDir = await startup.race(realpath(options.agentDir), "Assistant state identity");
		await startup.race(
			mkdir(join(agentDir, "pixie"), { recursive: true, mode: 0o700 }),
			"Assistant metadata setup",
		);
		const lockPromise = lock(join(agentDir, "pixie", "host"), { realpath: false });
		void lockPromise.then(
			(candidate) => {
				if (startup.signal.aborted) void candidate().catch(() => {});
			},
			() => {},
		);
		release = await startup.race(lockPromise, "Assistant host lock");
		starting = startUnlockedHost({ ...options, agentDir, hostname, port, secret }, startup);
		const host = await startup.race(starting, "Assistant startup");
		const unlock = release;
		if (!unlock) throw new Error("Assistant host lock was not acquired");
		startup.dispose();
		let closing: Promise<void> | undefined;
		return {
			...host,
			close: () =>
				(closing ??= (async () => {
					const deadline = createDeadline(
						options.drainDeadlineMs ?? DEFAULT_SERVICE_DRAIN_DEADLINE_MS,
						"Assistant shutdown",
					);
					try {
						await host.close(deadline);
					} finally {
						try {
							await deadline.race(unlock(), "Assistant host lock release");
						} finally {
							deadline.dispose();
						}
					}
				})()),
		};
	} catch (error) {
		startup.abort(new Error("Assistant startup cancelled"));
		startup.dispose();
		if (release) await startup.race(release(), "Assistant host lock release").catch(() => {});
		throw error;
	}
}
async function startUnlockedHost(options: HostOptions, startup?: Deadline) {
	const agentDir = resolve(options.agentDir);
	await mkdir(agentDir, { recursive: true, mode: 0o700 });
	if (options.secret.length < 16)
		throw new Error("Pi host secret must contain at least 16 characters");
	const identity = serviceStore<{ id: string }>(agentDir, "identity", () => ({ id: randomUUID() }));
	const runtimeId = await identity.update((s) => s.id);
	// Unlike runtimeId, this is intentionally fresh for every engine start. It
	// lets reconnecting clients distinguish a new process from a new transport.
	const bootId = randomUUID();
	const peers = new Set<ServerWebSocket<Peer>>();
	let stopping = false;
	let restartPending = false;
	const shutdown = new AbortController();
	const inflight = new Set<Promise<unknown>>();
	const sendData = (peer: ServerWebSocket<Peer>, data: string) => {
		if (Buffer.byteLength(data) > 32 * 1024 * 1024) {
			peer.close(1009, "Response exceeds limit");
			return;
		}
		if (peer.send(data) === -1 && peer.getBufferedAmount() > 32 * 1024 * 1024)
			peer.close(1013, "Consumer is too slow");
	};
	const send = (peer: ServerWebSocket<Peer>, value: unknown) =>
		sendData(peer, serializeFrame(value));
	// Fail loudly before the service starts when --llama is requested but the
	// pinned SDK does not expose its built-in factory through the public export.
	if (options.llama) llamaFactory();
	let runtimeWiring: RuntimeWiring;
	const sessions = new Sessions(
		agentDir,
		options.llama ? [llama] : [],
		(sessionId, event, sequence) => {
			const safeEvent = redactSecrets(event) as RecordValue;
			runtimeWiring.onEvent(sessionId, safeEvent);
			const frame = buildEventFrame(sessionId, safeEvent, sequence);
			for (const peer of peers) {
				const loading = peer.data.loading.get(sessionId);
				if (loading) {
					loading.bytes += Buffer.byteLength(frame.data);
					if (loading.bytes > 32 * 1024 * 1024) {
						peer.data.loading.delete(sessionId);
						peer.close(1013, "Session attachment exceeds buffer limit");
					} else loading.messages.push(frame.message);
				} else if (peer.data.sessions.has(sessionId)) sendData(peer, frame.data);
			}
		},
	);
	runtimeWiring = new RuntimeWiring({ agentDir, bootId, sessions, nativeTransport: options.nativeTransport });
	let control: ManagedSession;
	try {
		if (startup) await startup.race(runtimeWiring.ready(), "Assistant runtime wiring startup");
		else await runtimeWiring.ready();
		const pending = sessions.control(agentDir);
		control = startup ? await startup.race(pending, "Pi control session startup") : await pending;
	} catch (error) {
		await sessions.close(startup).catch(() => {});
		await runtimeWiring.close().catch(() => {});
		throw error;
	}
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
	/**
	 * Advertise operation-level support alongside the coarse extension
	 * capabilities.  The latter are useful for discovery, but they are not an
	 * authorization boundary: a Pi installation can expose MCP while the
	 * adapter still has no supported programmatic tool executor, for example.
	 * Keep this list in the host so the Go client can reject an unsupported
	 * operation before sending it through the generic extension channel.
	 */
	const operationSet = (entry = control): Record<string, boolean> => {
		const caps = entry.capabilities.snapshot();
		const mcp = caps.mcp === 1;
		const agents = caps.agents === 1;
		const plans = caps.plans === 1;
		return {
			// Start from the canonical retained-operation catalog so every operation
			// has an explicit negotiated value, including newly added bridge routes.
			...Object.fromEntries(ADMIN_OPERATIONS.map(({ id }) => [id, false])),
			// Core methods are explicit too. Controllers reject absent/partial maps;
			// this keeps the retained Bun compatibility host fail-closed without an
			// optimistic legacy fallback.
			"session.list": true,
			"session.create": true,
			"session.load": true,
			"session.prompt": true,
			"session.cancel": true,
			"session.configure": true,
			"session.release": true,
			"runtime.release": true,
			"runtime.releaseToTui": true,
			// Session administration still travels through the native RPC-backed
			// session owner, but is listed explicitly so Go does not need a broad
			// Administration assumption.
			"session.delete": true,
			"session.fork": true,
			"session.steer": true,
			"session.rename": true,
			"session.archive": true,
			"session.prompt.image": true,
			"session.prompt.resource": true,
			"session.uiResponse": true,
			"session.uiCancel": true,
			// Restart is executable-owned and is available only when both admission
			// and the termination hook were wired by the composition root.
			"runtime.restart": options.allowSelfRestart === true && typeof options.onRestart === "function",
			"pi.session.info": true,
			"pi.session.rename": true,
			"pi.session.archive": true,
			"pi.session.unarchive": true,
			"pi.session.steer": true,
			"pi.tools.list": mcp,
			"runtime.capabilities": true,
			"pi.slash-commands.list": true,
			// Provider/auth/default operations are delegated to the selected native
			// ModelRuntime. They remain optional from the vanilla profile's point of
			// view, but are available when this host has a runtime context.
			"pi.providers.list": true,
			"pi.providers.canonical-model-info": true,
			"pi.providers.inventory.refresh": true,
			"pi.providers.readiness.check": true,
			"provider.loginStart": true,
			"provider.loginBegin": true,
			"provider.loginReply": true,
			"provider.loginCancel": true,
			"pi.providers.config.read": true,
			"pi.providers.config.delete": true,
			"pi.defaults.read": true,
			"pi.defaults.save": true,
			"pi.defaults.clear": true,
			"pi.preferences.read": true,
			"pi.preferences.save": true,
			"pi.preferences.reset": true,
			"pi.extensions.list": true,
			"pi.extensions.configure": true,
			// The authoring capability is installed for every managed session. Its
			// operations are deliberately separate from native execution eligibility.
			"pi.sources.list": agents,
			"pi.sources.create": agents,
			"pi.sources.update": agents,
			"pi.sources.delete": agents,
			"pi.agent-mentions.list": agents,
			// Whole-host restart is owned by the executable composition. The
			// assistant host exposes runtime.restart, not a session-level pi.reload.
			"pi.reload": false,
			"pi.subagent.execute": false,
			"pi.todo.plan": plans,
			"pixie.goals.questions": false,
			// A caller must use the selected adapter's public event surface for
			// MCP registration. There is intentionally no private tool-array route.
			"mcp.attach": mcp,
			"pi.config.extensions.list": mcp,
			"pi.config.extensions.add": mcp,
			"pi.config.extensions.set-enabled": mcp,
			"pi.config.extensions.remove": mcp,
			"pi.session.extensions.list": mcp,
			"pi.session.extensions.add": mcp,
			"pi.session.extensions.remove": mcp,
			"pi.tools.call": false,
			"adapter.status": mcp,
			"adapter.registerBrowser": mcp,
			"adapter.session.forget": mcp,
			"pi.llama": options.llama === true,
			"pi.native-extensions": false,
		};
	};
	const capabilitySnapshot = (entry = control) => ({
		sessions: 1,
		providers: 1,
		...entry.capabilities.snapshot(),
	});
	const attach = async (entry: ManagedSession, p: RecordValue, signal?: AbortSignal) => {
		const supported = entry.capabilities.snapshot();
		if (supported.mcp === 1 && Array.isArray(p.mcpServers))
			await entry.capabilities.call(
				"mcp.attach",
				{ servers: p.mcpServers },
				sessions.context(entry, signal),
			);
	};
	const runtimeSnapshot = (entry: ManagedSession, p: RecordValue) =>
		runtimeWiring.snapshot(entry.session.sessionId, text(p.clientId));
	const dispatch = async (
		method: string,
		p: RecordValue,
		peer: ServerWebSocket<Peer>,
		signal?: AbortSignal,
	): Promise<unknown> => {
		if (method === "runtime.hello")
			return {
				protocolVersion: 1,
				runtimeId,
				bootId,
				version: sdkVersion,
				capabilities: capabilitySnapshot(),
				operationSet: operationSet(),
			};
		if (method === "runtime.restart") {
			if (!options.allowSelfRestart)
				throw new Error("Service self restart is not enabled for this deployment");
			const onRestart = options.onRestart;
			if (!onRestart) throw new Error("Service self restart has no executable termination hook");
			if (!restartPending) {
				restartPending = true;
				// Stop admitting new work as soon as the request is accepted. The
				// restart reply and duplicate restart requests remain available until
				// the executable has had a chance to drain and exit.
				stopping = true;
				shutdown.abort(new Error("Service restarting"));
				void (async () => {
					// Reply first, then end the process so the
					// service manager brings a fresh host up. Runs interrupt
					// with the documented restart semantics; session
					// transcripts stay durable on disk.
					await Bun.sleep(250);
					try {
						onRestart();
					} catch (error) {
						console.error("Service self restart hook failed", error);
					}
				})();
			}
			return { ok: true };
		}
		if (["pi.extensions.list", "pi.extensions.configure"].includes(method)) {
			const id = text(p.sessionId);
			const metadata = id ? await sessions.metadata(id) : undefined;
			const cwd = await realpath(text(p.cwd) || metadata?.cwd || agentDir);
			if (metadata && cwd !== (await realpath(metadata.cwd)))
				throw new Error("Session project mismatch");
			const entry = id ? sessions.entries.get(id) : !text(p.cwd) ? control : undefined;
			if (method === "pi.extensions.configure") {
				if (p.scope !== "user" && p.scope !== "project")
					throw new Error("Select a native settings scope");
				if (typeof p.enabled !== "boolean" || p.confirmed !== true)
					throw new Error("Confirm the native configuration change");
				return configureExtension(agentDir, cwd, {
					scope: p.scope,
					resourceKey: required(p.resourceKey, "native resource"),
					expectedRevision: required(p.expectedRevision, "configuration revision"),
					enabled: p.enabled,
					confirmed: true,
				});
			}
			return extensionInventory(
				agentDir,
				cwd,
				entry?.session,
				id ? (entry ? "session" : "not-resident") : entry ? "service" : "configured-only",
				id || null,
			);
		}
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
			const capacity = runtimeWiring.canCreate();
			if (!capacity.ok) throw new HostError(capacity.error.message, -32000);
			const entry = await sessions.create(
				required(p.cwd, "project"),
				method === "session.fork" ? required(p.sessionId, "session") : undefined,
			);
			entry.refs++;
			try {
				await attach(entry, p, signal);
			} finally {
				entry.refs--;
			}
			await runtimeWiring.bind(entry);
			peer.data.attachments.set(entry.session.sessionId, { params: p, entry: new WeakRef(entry) });
			peer.data.sessions.add(entry.session.sessionId);
			return { ...sessions.snapshot(entry), runtime: runtimeSnapshot(entry, p) };
		}
		if (method === "runtime.release" || method === "session.release" || method === "runtime.releaseToTui") {
			const id = required(p.sessionId, "session");
			const entry = await sessions.get(id, text(p.cwd) || undefined);
			const released = await runtimeWiring.release(
				entry,
				method === "runtime.releaseToTui",
				text(p.instruction) || (method === "runtime.releaseToTui" ? `Resume ${id} in the native TUI` : ""),
			);
			if (!released.ok) throw new HostError(released.error.message, -32000);
			return { ok: true };
		}
		if (text(p.sessionId)) {
			return sessions.use(text(p.sessionId), text(p.cwd) || undefined, async (entry) => {
				if (method === "session.load") {
					await attach(entry, p, signal);
					await runtimeWiring.bind(entry);
					peer.data.attachments.set(entry.session.sessionId, {
						params: p,
						entry: new WeakRef(entry),
					});
					peer.data.sessions.add(entry.session.sessionId);
					return { ...sessions.snapshot(entry, true), runtime: runtimeSnapshot(entry, p) };
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
					await attach(entry, attachment.params, signal);
					attachment.entry = new WeakRef(entry);
				}
				if (method === "session.prompt") {
					if (!Array.isArray(p.content)) throw new HostError("Prompt content must be an array", -32000);
					return runtimeWiring.prompt(
						entry,
						{
							content: p.content as PromptBlock[],
							...(typeof p.mutationId === "string" ? { mutationId: p.mutationId } : {}),
							...(typeof p.deliveryId === "string" ? { deliveryId: p.deliveryId } : {}),
							...(typeof p.runId === "string" ? { runId: p.runId } : {}),
						},
						(request) => sessions.call("session.prompt", { ...p, content: request.content }, signal),
					);
				}
				if (method === "session.cancel") {
					return runtimeWiring.cancel(
						entry,
						text(p.requestId) || randomUUID(),
						() => sessions.call(method, p, signal),
					);
				}
				if (method === "session.configure") {
					const result = await sessions.call(method, p, signal);
					const configId = text(p.configId);
					const configured =
						configId === "thinking"
							? await runtimeWiring.configureThinking(entry, text(p.value))
							: await runtimeWiring.configureModel(entry, entry.session.model as never);
					if (!configured.ok) throw new HostError(configured.error.message, -32000);
					return result;
				}
				return sessions.call(method, p, signal);
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
			let entry: ManagedSession | undefined;
			try {
				entry = cwd ? await sessions.control(cwd) : control;
				const current = entry;
				if (!current) throw new Error("Missing Pi capability context");
				if (method === "runtime.capabilities") return capabilitySnapshot(current);
				if (method === "pi.slash-commands.list")
					return { availableCommands: sessions.commands(current) };
				return await current.capabilities.call(method, p, sessions.context(current, signal));
			} finally {
				if (entry && entry !== control) await entry.close();
			}
		}
		return control.capabilities.call(method, p, sessions.context(control, signal));
	};
	let server: ReturnType<typeof Bun.serve<Peer>>;
	try {
		server = Bun.serve<Peer>({
			hostname: options.hostname,
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
						bootId,
						capabilities: capabilitySnapshot(),
						operationSet: operationSet(),
					});
				if (
					url.pathname === "/pi" &&
					server.upgrade(request, {
						data: {
							handshaken: false,
							handshakePending: false,
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
					let value: unknown;
					try {
						value = JSON.parse(String(raw));
					} catch {
						peer.close(1007, "Invalid JSON");
						return;
					}
					if (!value || typeof value !== "object" || Array.isArray(value)) {
						peer.close(1008, "Invalid request envelope");
						return;
					}
					const request = value as RecordValue;
					const id = request.id;
					if (typeof id !== "number" || !Number.isSafeInteger(id) || id <= 0) {
						peer.close(1008, "Invalid request ID");
						return;
					}
					const method = request.method;
					if (typeof method !== "string" || method.length === 0) {
						peer.close(1008, "Invalid request method");
						return;
					}
					const params = request.params;
					if (!params || typeof params !== "object" || Array.isArray(params)) {
						peer.close(1008, "Invalid request params");
						return;
					}
					// A reused in-flight id is a protocol mistake, not broken
					// framing: answer with an error frame and keep the connection.
					if (peer.data.active.has(id)) {
						send(peer, { id, error: { code: -32000, message: "Request id is already in flight" } });
						return;
					}
					if (peer.data.active.size >= 128) {
						send(peer, { id, error: { code: -32000, message: "Too many pending requests" } });
						return;
					}
					peer.data.active.add(id);
					const envelopeParams = params as RecordValue,
						sessionId = text(envelopeParams.sessionId);
					const handshaking = method === "runtime.hello" && !peer.data.handshaken;
					if (!peer.data.handshaken) {
						if (
							peer.data.handshakePending ||
							method !== "runtime.hello" ||
							envelopeParams.protocolVersion !== 1
						) {
							peer.data.active.delete(id);
							peer.close(1008, "Invalid or missing runtime hello");
							return;
						}
						peer.data.handshakePending = true;
					}
					if (stopping && method !== "runtime.restart") {
						peer.close(1001, "Service stopping");
						return;
					}
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
					const timeoutMs = operationDeadlineMs(method, options);
					const operationDeadline = timeoutMs
						? createDeadline(timeoutMs, `Pi ${method}`)
						: undefined;
					const signal = operationDeadline
						? AbortSignal.any([shutdown.signal, operationDeadline.signal])
						: shutdown.signal;
					const dispatched = dispatch(method, envelopeParams, peer, signal);
					inflight.add(dispatched);
					void dispatched.finally(() => inflight.delete(dispatched)).catch(() => {});
					const operation = (
						operationDeadline ? operationDeadline.race(dispatched, `Pi ${method}`) : dispatched
					)
						.then(
							(result) => {
								if (handshaking) {
									peer.data.handshakePending = false;
									peer.data.handshaken = true;
								}
								let safeResult = redactSecrets(result);
								const snapshot = object(safeResult);
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
									safeResult = { ...snapshot, messages: [] };
								}
								send(peer, { id, result: safeResult ?? null });
								flush(Number(object(safeResult).eventSequence ?? -1));
							},
							(error) => {
								if (handshaking) peer.data.handshakePending = false;
								const uncertain = error instanceof UncertainPromptError;
								send(peer, {
									id,
									error: {
										code: uncertain ? -32003 : error instanceof HostError ? error.code : -32000,
										message: uncertain
											? error.message
											: error instanceof Error ? error.message : "Pi request failed",
									},
								});
								flush();
							},
						)
						.finally(() => operationDeadline?.dispose())
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
		stopping = true;
		shutdown.abort(new Error("Service startup failed"));
		providers.close();
		const cleanupDeadline =
			startup ??
			createDeadline(
				options.drainDeadlineMs ?? DEFAULT_SERVICE_DRAIN_DEADLINE_MS,
				"Assistant startup cleanup",
			);
		await Promise.allSettled([sessions.close(cleanupDeadline), control.close(cleanupDeadline), runtimeWiring.close()]);
		if (!startup) cleanupDeadline.dispose();
		throw error;
	}
	return {
		server,
		bootId,
		nativeTransport: options.nativeTransport,
		sessions,
		control,
		capabilities: capabilitySnapshot(),
		close: async (providedDeadline?: Deadline) => {
			const deadline =
				providedDeadline ??
				createDeadline(
					options.drainDeadlineMs ?? DEFAULT_SERVICE_DRAIN_DEADLINE_MS,
					"Assistant shutdown",
				);
			const ownedDeadline = !providedDeadline;
			stopping = true;
			shutdown.abort(new Error("Service stopping"));
			let stopped: PromiseLike<void>;
			try {
				stopped = server.stop(true);
			} catch (error) {
				stopped = Promise.reject(error);
			}
			for (const peer of peers) peer.close(1001, "Service stopping");
			providers.close();
			const cleanup = Promise.allSettled([
				sessions.close(deadline),
				control.close(deadline),
				runtimeWiring.close(),
				stopped,
				...inflight,
			]);
			try {
				const results = await deadline.race(cleanup, "Assistant service drain");
				const failures = results.filter(
					(result): result is PromiseRejectedResult => result.status === "rejected",
				);
				if (failures.length)
					throw new AggregateError(
						failures.map((failure) => failure.reason),
						"Assistant service shutdown failed",
					);
			} finally {
				if (ownedDeadline) deadline.dispose();
			}
		},
	};
}
