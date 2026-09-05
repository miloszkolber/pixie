import { randomUUID } from "node:crypto";
import { readdir, realpath, rm, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import {
	type AgentSession,
	createAgentSession,
	createEventBus,
	DefaultResourceLoader,
	type ExtensionFactory,
	ModelRuntime,
	type SessionEntry,
	SessionManager,
	SettingsManager,
} from "@earendil-works/pi-coding-agent";
import { MCP_SERVICE_EVENT, type MCPService } from "@pixie/pi-mcp";
import { CAPABILITY_EVENT, Capabilities, type CapabilityContext } from "./capabilities.ts";
import {
	atomicWrite,
	HostError,
	object,
	type RecordValue,
	required,
	serviceStore,
	text,
} from "./storage.ts";

function presentationEntry(e: SessionEntry): RecordValue | undefined {
	if (e.type === "compaction" || e.type === "branch_summary")
		return {
			role: "summary",
			messageId: e.id,
			summaryKind: e.type,
			summary: e.summary,
			tokensBefore: e.type === "compaction" ? e.tokensBefore : undefined,
		};
	if (e.type === "custom_message" && e.display)
		return { role: "custom", messageId: e.id, content: e.content, display: true };
	if (e.type === "custom" && e.customType === "pixie-plan")
		return { role: "plan", messageId: e.id, ...object(e.data) };
}

export interface ManagedSession {
	session: AgentSession;
	capabilities: Capabilities;
	modelRuntime: ModelRuntime;
	runId: string;
	refs: number;
	lastUsed: number;
	inputs: { text: string; content: unknown }[];
	run?: Promise<void>;
	partialMessage?: RecordValue;
	partialTools: Map<string, RecordValue>;
	close: () => Promise<void>;
	forgetMcp?: () => Promise<unknown>;
}
interface SessionMetadata {
	path: string;
	cwd: string;
	createdAt: string;
	archivedAt?: string;
	parentSessionId?: string;
}
export class Sessions {
	readonly entries = new Map<string, ManagedSession>();
	private closed = false;
	private building = new Set<Promise<ManagedSession>>();
	private creating = new Set<Promise<ManagedSession>>();
	private timer: ReturnType<typeof setInterval>;
	private evicting = new Map<string, Promise<void>>();
	private opening = new Map<string, Promise<ManagedSession>>();
	readonly catalog;
	private sequences = new Map<string, number>();
	private listing?: { expires: number; rows: Promise<RecordValue[]> };
	private pages = new Map<string, { expires: number; rows: RecordValue[] }>();
	constructor(
		readonly agentDir: string,
		readonly factories: ExtensionFactory[],
		readonly output: (id: string, event: unknown, sequence: number) => void,
		readonly limits = { maxIdle: 32, idleMs: 300000 },
	) {
		this.catalog = serviceStore<Record<string, SessionMetadata>>(agentDir, "sessions", () => ({}));
		this.timer = setInterval(() => void this.sweep().catch(() => {}), 5000);
		this.timer.unref();
	}
	private publish(id: string, event: unknown): void {
		const sequence = (this.sequences.get(id) ?? 0) + 1;
		this.sequences.set(id, sequence);
		this.output(id, event, sequence);
		if (["message_end", "session_info_changed"].includes(text(object(event).type)))
			this.listing = undefined;
	}
	create(cwd: string, parent?: string): Promise<ManagedSession> {
		const pending = this.createSession(cwd, parent);
		this.creating.add(pending);
		void pending.finally(() => this.creating.delete(pending)).catch(() => {});
		return pending;
	}
	private async createSession(cwd: string, parent?: string): Promise<ManagedSession> {
		if (this.closed) throw new Error("Pi host is stopping");
		cwd = await realpath(required(cwd, "project directory"));
		if (!(await stat(cwd)).isDirectory()) throw new Error("Project is not a directory");
		let manager: SessionManager;
		if (parent) {
			const source = await this.metadata(parent);
			manager = SessionManager.forkFrom(
				source.path,
				cwd,
				join(
					this.agentDir,
					"sessions",
					`--${resolve(cwd)
						.replace(/^[/\\]/, "")
						.replace(/[/\\:]/g, "-")}--`,
				),
			);
		} else {
			manager = SessionManager.create(
				cwd,
				join(
					this.agentDir,
					"sessions",
					`--${resolve(cwd)
						.replace(/^[/\\]/, "")
						.replace(/[/\\:]/g, "-")}--`,
				),
			);
			const path = manager.getSessionFile();
			if (!path) throw new Error("Missing Pi session file");
			await atomicWrite(path, `${JSON.stringify(manager.getHeader())}\n`);
			manager = SessionManager.open(path, undefined, cwd);
		}
		const entry = await this.build(cwd, manager);
		const id = entry.session.sessionId;
		try {
			await this.catalog.update((c) => {
				c[id] = {
					path: required(entry.session.sessionFile, "session file"),
					cwd,
					createdAt: new Date().toISOString(),
					...(parent ? { parentSessionId: parent } : {}),
				};
			});
			if (this.closed) throw new Error("Pi host is stopping");
			this.entries.set(id, entry);
			this.listing = undefined;
			return entry;
		} catch (error) {
			await entry.close();
			throw error;
		}
	}
	async metadata(id: string): Promise<SessionMetadata> {
		const c = await this.catalog.read();
		if (c[id]) return c[id];
		const found = (await this.nativeSessions()).filter((s) => s.id === id);
		if (found.length !== 1) throw new HostError("Unknown or ambiguous Pi session", -32002);
		const s = found[0];
		return { path: s.path, cwd: s.cwd, createdAt: s.created.toISOString() };
	}
	async get(id: string, cwd?: string): Promise<ManagedSession> {
		if (this.closed) throw new Error("Pi host is stopping");
		await this.evicting.get(id);
		let pending = this.opening.get(id);
		if (!pending) {
			pending = (async () => {
				const existing = this.entries.get(id);
				if (existing) return existing;
				const metadata = await this.metadata(id);
				const entry = await this.build(metadata.cwd, SessionManager.open(metadata.path));
				if (this.closed) {
					await entry.close();
					throw new Error("Pi host is stopping");
				}
				this.entries.set(id, entry);
				return entry;
			})();
			this.opening.set(id, pending);
			void pending.finally(() => this.opening.delete(id)).catch(() => {});
		}
		const entry = await pending;
		if (cwd && (await realpath(entry.session.sessionManager.getCwd())) !== (await realpath(cwd)))
			throw new Error("Session project mismatch");
		entry.lastUsed = Date.now();
		return entry;
	}
	async control(cwd: string): Promise<ManagedSession> {
		return this.build(cwd, SessionManager.inMemory(cwd));
	}
	private build(cwd: string, manager: SessionManager): Promise<ManagedSession> {
		if (this.closed) return Promise.reject(new Error("Pi host is stopping"));
		const pending = this.construct(cwd, manager);
		this.building.add(pending);
		void pending.finally(() => this.building.delete(pending)).catch(() => {});
		return pending;
	}
	private async construct(cwd: string, manager: SessionManager): Promise<ManagedSession> {
		const modelRuntime = await ModelRuntime.create({
			authPath: join(this.agentDir, "auth.json"),
			modelsPath: join(this.agentDir, "models.json"),
			allowModelNetwork: false,
		});
		const settings = SettingsManager.create(cwd, this.agentDir);
		const bus = createEventBus();
		const capabilities = new Capabilities();
		let forgetMcp: ((ctx: CapabilityContext) => Promise<unknown>) | undefined;
		bus.on(CAPABILITY_EVENT, (v) => capabilities.register(v));
		bus.on(MCP_SERVICE_EVENT, (value) => {
			const service = value as MCPService;
			if (service?.version !== 1 || !service.operations) return;
			if (!manager.getSessionFile()) service.prepare?.({ connectOnStart: false });
			if (service.operations["session.forget"])
				forgetMcp = async (ctx) => service.operations["session.forget"]({}, ctx);
			const names: Record<string, string> = {
				"mcp.attach": "attach",
				"pi.config.extensions.list": "connections.list",
				"pi.config.extensions.add": "connections.add",
				"pi.config.extensions.set-enabled": "connections.set-enabled",
				"pi.config.extensions.remove": "connections.remove",
				"pi.session.extensions.list": "session.list",
				"pi.session.extensions.add": "session.add",
				"pi.session.extensions.remove": "session.remove",
				"pi.resources.read": "resources.read",
				"pi.tools.call": "tools.call",
			};
			const operations = Object.fromEntries(
				Object.entries(names).map(([wire, native]) => [wire, service.operations[native]]),
			);
			if (Object.values(operations).some((operation) => typeof operation !== "function")) return;
			capabilities.register({ id: "mcp", version: 1, operations, close: service.close });
			capabilities.register({ id: "mcp-apps", version: 1, operations: {} });
		});

		const loader = new DefaultResourceLoader({
			cwd,
			agentDir: this.agentDir,
			settingsManager: settings,
			eventBus: bus,
			extensionFactories: this.factories,
		});
		let built: AgentSession | undefined;
		try {
			await loader.reload();
			const { session, extensionsResult } = await createAgentSession({
				cwd,
				agentDir: this.agentDir,
				modelRuntime,
				settingsManager: settings,
				sessionManager: manager,
				resourceLoader: loader,
			});
			built = session;
			for (const error of extensionsResult.errors)
				this.publish(session.sessionId, { type: "extension_error", error: error.error });
			let closing: Promise<void> | undefined;
			const entry: ManagedSession = {
				session,
				capabilities,
				modelRuntime,
				runId: "",
				refs: 0,
				lastUsed: Date.now(),
				inputs: [],
				partialTools: new Map(),
				close: () =>
					(closing ??= (async () => {
						await session.abort();
						try {
							await session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" });
						} finally {
							try {
								await capabilities.close();
							} finally {
								session.dispose();
								await settings.flush();
							}
						}
					})()),
			};
			const forget = forgetMcp;
			entry.forgetMcp = forget ? () => forget(this.context(entry)) : undefined;
			await session.bindExtensions({
				mode: "rpc",
				onError: (e) =>
					this.publish(session.sessionId, { type: "extension_error", error: e.error }),
			});
			const streamed = new Map<number, number>();
			session.subscribe((event) => {
				if (event.type === "message_start" && event.message.role === "assistant") streamed.clear();
				if (event.type === "message_update") {
					const delta = event.assistantMessageEvent;
					if (delta.type === "text_delta" || delta.type === "thinking_delta")
						streamed.set(
							delta.contentIndex,
							(streamed.get(delta.contentIndex) ?? 0) + delta.delta.length,
						);
				}
				// Some providers deliver only a final message, or finish with an unstreamed tail.
				if (event.type === "message_end" && event.message.role === "assistant") {
					event.message.content.forEach((block, index) => {
						const value =
							block.type === "text" ? block.text : block.type === "thinking" ? block.thinking : "";
						const tail = value.slice(streamed.get(index) ?? 0);
						if (tail)
							this.publish(session.sessionId, {
								type: "message_update",
								message: { role: event.message.role, timestamp: event.message.timestamp },
								assistantMessageEvent: {
									type: block.type === "text" ? "text_delta" : "thinking_delta",
									contentIndex: index,
									delta: tail,
								},
							});
					});
				}

				if (
					(event.type === "message_start" || event.type === "message_update") &&
					event.message.role === "assistant"
				)
					entry.partialMessage = event.message as unknown as RecordValue;
				if (event.type === "message_end" && event.message.role === "assistant")
					entry.partialMessage = undefined;
				if (event.type === "tool_execution_update")
					entry.partialTools.set(event.toolCallId, object(event.partialResult));
				if (event.type === "tool_execution_end") entry.partialTools.delete(event.toolCallId);
				if (event.type === "message_start" && event.message.role === "user") {
					const value = event.message.content;
					const prompt =
						typeof value === "string"
							? value
							: value
									.filter((b) => b.type === "text")
									.map((b) => b.text)
									.join("");
					const index = entry.inputs.findIndex((input) => input.text === prompt);
					if (index >= 0) {
						const [input] = entry.inputs.splice(index, 1);
						this.publish(session.sessionId, {
							...event,
							message: { ...event.message, displayContent: input.content },
						});
						return;
					}
				}
				if (event.type === "entry_appended") {
					const message = presentationEntry(event.entry);
					if (message) this.publish(session.sessionId, { type: "replay_message", message });
					if (event.entry.type === "model_change")
						this.publish(session.sessionId, {
							type: "configuration_changed",
							configOptions: this.config(entry),
						});
					return;
				}
				if (event.type === "thinking_level_changed")
					this.publish(session.sessionId, {
						type: "configuration_changed",
						configOptions: this.config(entry),
					});
				if (event.type === "message_update") {
					const delta = event.assistantMessageEvent;
					if (delta.type !== "text_delta" && delta.type !== "thinking_delta") return;
					this.publish(session.sessionId, {
						type: event.type,
						message: { role: event.message.role, timestamp: event.message.timestamp },
						assistantMessageEvent: {
							type: delta.type,
							delta: delta.delta,
							contentIndex: delta.contentIndex,
						},
					});
					return;
				}
				this.publish(session.sessionId, event);
			});
			return entry;
		} catch (error) {
			try {
				await capabilities.close();
			} finally {
				built?.dispose();
				await settings.flush();
			}
			throw error;
		}
	}
	async use<T>(
		id: string,
		cwd: string | undefined,
		operation: (entry: ManagedSession) => Promise<T>,
	): Promise<T> {
		const entry = await this.get(id, cwd);
		entry.refs++;
		try {
			return await operation(entry);
		} finally {
			entry.refs--;
			entry.lastUsed = Date.now();
		}
	}
	async release(id: string): Promise<void> {
		const entry = this.entries.get(id);
		if (!entry || entry.refs || entry.run || entry.session.isStreaming) return;
		this.entries.delete(id);
		const closing = entry.close();
		this.evicting.set(id, closing);
		try {
			await closing;
		} finally {
			this.evicting.delete(id);
		}
	}
	async sweep(now = Date.now()): Promise<void> {
		const idle = [...this.entries.entries()]
			.filter(([, e]) => !e.refs && !e.run && !e.session.isStreaming)
			.sort((a, b) => a[1].lastUsed - b[1].lastUsed);
		const excess = Math.max(0, idle.length - this.limits.maxIdle);
		await Promise.all(
			idle
				.filter(([, e], i) => i < excess || now - e.lastUsed >= this.limits.idleMs)
				.map(([id]) => this.release(id)),
		);
	}
	context(entry: ManagedSession, signal = AbortSignal.timeout(120000)): CapabilityContext {
		return {
			cwd: entry.session.sessionManager.getCwd(),
			agentDir: this.agentDir,
			session: entry.session,
			signal,
			notify: (event) => this.publish(entry.session.sessionId, event),
		};
	}
	commands(entry: ManagedSession): RecordValue[] {
		const s = entry.session;
		return [
			{ name: "compact", description: "Compact the conversation", source: "builtin" },
			...s.extensionRunner
				.getRegisteredCommands()
				.map((c) => ({ name: c.name, description: c.description, source: "extension" })),
			...s.promptTemplates.map((p) => ({
				name: p.name,
				description: p.description,
				source: "prompt",
			})),
			...s.resourceLoader.getSkills().skills.map((s) => ({
				name: `skill:${s.name}`,
				description: s.description,
				source: "skill",
			})),
		];
	}
	config(entry: ManagedSession): RecordValue[] {
		const model = entry.session.model,
			provider = model?.provider ?? "";
		const options = (
			id: string,
			name: string,
			currentValue: string,
			choices: { value: string; name: string }[],
		) => ({ id, name, category: id, type: "select", currentValue, options: choices });
		return [
			options(
				"provider",
				"Provider",
				provider,
				entry.modelRuntime.getProviders().map((p) => ({ value: p.id, name: p.name ?? p.id })),
			),
			options(
				"model",
				"Model",
				model?.id ?? "",
				entry.modelRuntime
					.getModels(provider || undefined)
					.map((m) => ({ value: m.id, name: m.name })),
			),
			options(
				"thinking",
				"Thinking",
				entry.session.thinkingLevel,
				entry.session.getAvailableThinkingLevels().map((v) => ({ value: v, name: v })),
			),
		];
	}
	snapshot(entry: ManagedSession, history = false): RecordValue {
		return {
			sessionId: entry.session.sessionId,
			eventSequence: this.sequences.get(entry.session.sessionId) ?? 0,
			configOptions: this.config(entry),
			metadata: { model: entry.session.model },
			commands: this.commands(entry),
			runId: entry.runId,
			capabilities: { sessions: 1, providers: 1, ...entry.capabilities.snapshot() },
			...(history ? { messages: this.history(entry) } : {}),
		};
	}
	private history(entry: ManagedSession): RecordValue[] {
		const inputs: { text: string; content: unknown }[] = [];
		const messages: RecordValue[] = [];
		for (const e of entry.session.sessionManager.getBranch()) {
			if (e.type === "custom" && e.customType === "pixie-input") {
				const data = object(e.data);
				inputs.push({ text: text(data.text), content: data.content });
			}
			const presentation = presentationEntry(e);
			if (presentation) messages.push(presentation);
			if (e.type !== "message") continue;
			if (e.message.role === "custom" && !e.message.display) continue;
			const message = { ...e.message, messageId: e.id } as unknown as RecordValue;
			if (e.message.role === "user") {
				const content = e.message.content;
				const prompt =
					typeof content === "string"
						? content
						: content
								.filter((b) => b.type === "text")
								.map((b) => b.text)
								.join("");
				const index = inputs.findIndex((input) => input.text === prompt);
				if (index >= 0) message.displayContent = inputs.splice(index, 1)[0].content;
			}
			messages.push(message);
		}
		if (entry.partialMessage) {
			const partial = structuredClone(entry.partialMessage);
			// Incomplete tool arguments are not a tool execution yet.
			partial.content = (Array.isArray(partial.content) ? partial.content : []).filter(
				(block) => object(block).type !== "toolCall",
			);
			messages.push(partial);
		}
		for (const [toolCallId, result] of entry.partialTools)
			messages.push({ ...structuredClone(result), role: "toolResult", toolCallId, partial: true });
		return messages;
	}
	private recordInput(entry: ManagedSession, prompt: string, content: unknown): void {
		const input = { text: prompt, content };
		entry.session.sessionManager.appendCustomEntry("pixie-input", input);
		entry.inputs.push(input);
	}
	private async nativeSessions() {
		const root = join(this.agentDir, "sessions");
		let directories: string[];
		try {
			directories = [
				root,
				...(await readdir(root, { withFileTypes: true }))
					.filter((entry) => entry.isDirectory() || entry.isSymbolicLink())
					.map((entry) => join(root, entry.name)),
			];
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
			throw error;
		}
		const results: Awaited<ReturnType<typeof SessionManager.listAll>> = [];
		await Promise.all(
			Array.from({ length: Math.min(4, directories.length) }, async () => {
				for (let directory = directories.shift(); directory; directory = directories.shift())
					results.push(...(await SessionManager.listAll(directory)));
			}),
		);
		return results;
	}
	private async scan(): Promise<RecordValue[]> {
		const all = await this.nativeSessions();
		const catalog = await this.catalog.read();
		const sessions = all.map((s) => ({
			sessionId: s.id,
			cwd: s.cwd,
			title: s.name ?? s.firstMessage?.slice(0, 120) ?? "Chat",
			updatedAt: s.modified.toISOString(),
			_meta: {
				createdAt: s.created.toISOString(),
				messageCount: s.messageCount,
				archivedAt: catalog[s.id]?.archivedAt,
				parentSessionId: catalog[s.id]?.parentSessionId,
			},
		}));
		const found = new Set(sessions.map((s) => s.sessionId));
		for (const [id, metadata] of Object.entries(catalog)) {
			if (found.has(id)) continue;
			try {
				const file = await stat(metadata.path);
				const manager = SessionManager.open(metadata.path);
				sessions.push({
					sessionId: id,
					cwd: metadata.cwd,
					title: manager.getSessionName() ?? "Chat",
					updatedAt: file.mtime.toISOString(),
					_meta: {
						createdAt: metadata.createdAt,
						messageCount: 0,
						archivedAt: metadata.archivedAt,
						parentSessionId: metadata.parentSessionId,
					},
				});
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
		}
		sessions.sort(
			(a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.sessionId.localeCompare(b.sessionId),
		);
		return sessions;
	}
	async list(cursor: string): Promise<RecordValue> {
		const now = Date.now();
		for (const [key, page] of this.pages) if (page.expires < now) this.pages.delete(key);
		let rows: RecordValue[],
			key: string,
			offset = 0;
		if (cursor) {
			const parts = cursor.split(":");
			key = parts[0];
			offset = Number(parts[1]);
			const page = this.pages.get(key);
			if (
				parts.length !== 2 ||
				!Number.isSafeInteger(offset) ||
				offset < 0 ||
				!page ||
				offset >= page.rows.length
			)
				throw new Error("History cursor expired or invalid; restart the listing");
			rows = page.rows;
		} else {
			if (!this.listing || this.listing.expires < now) {
				const rows = this.scan();
				this.listing = { expires: now + 1000, rows };
				void rows.catch(() => {
					if (this.listing?.rows === rows) this.listing = undefined;
				});
			}
			rows = await this.listing.rows;
			key = randomUUID();
			if (rows.length > 100) {
				if (Buffer.byteLength(JSON.stringify(rows)) > 16 * 1024 * 1024)
					throw new Error("Session catalog exceeds 16 MiB");
				while (this.pages.size >= 4) {
					const oldest = this.pages.keys().next().value;
					if (oldest === undefined) break;
					this.pages.delete(oldest);
				}
				this.pages.set(key, { rows, expires: now + 60000 });
			}
		}
		return {
			sessions: rows.slice(offset, offset + 100),
			...(offset + 100 < rows.length ? { nextCursor: `${key}:${offset + 100}` } : {}),
		};
	}

	private promptContent(value: unknown): {
		text: string;
		images: { type: "image"; mimeType: string; data: string }[];
	} {
		if (!Array.isArray(value)) throw new Error("Prompt content must be an array");
		let prompt = "";
		const images: { type: "image"; mimeType: string; data: string }[] = [];
		for (const block of value) {
			const b = object(block);
			if (b.type === "text") prompt += text(b.text);
			else if (b.type === "image")
				images.push({
					type: "image",
					mimeType: required(b.mimeType, "image MIME type", 100),
					data: required(b.data, "image", 24 * 1024 * 1024),
				});
			else if (b.type === "resource") {
				const r = object(b.resource);
				prompt += `\n\n<attached-file name=${JSON.stringify(text(object(r._meta).name) || text(r.uri))}>\n${text(r.text)}\n</attached-file>`;
			} else throw new Error("Unsupported prompt content");
		}
		if (images.length > 8 || prompt.length > 4 * 1024 * 1024)
			throw new Error("Prompt exceeds limits");
		return { text: prompt, images };
	}
	async call(method: string, p: RecordValue): Promise<unknown> {
		if (method === "session.list") return this.list(text(p.cursor));
		if (method === "session.create")
			return this.snapshot(await this.create(required(p.cwd, "project")));
		if (method === "session.fork")
			return this.snapshot(
				await this.create(required(p.cwd, "project"), required(p.sessionId, "session")),
			);
		const id = required(p.sessionId, "session");
		return this.use(id, text(p.cwd) || undefined, async (entry) => {
			const s = entry.session;
			switch (method) {
				case "session.load":
					return this.snapshot(entry, true);
				case "session.prompt": {
					if (entry.run || s.isStreaming) throw new Error("Session is already running");
					const content = this.promptContent(p.content);
					const previousAssistant = s.messages.filter((m) => m.role === "assistant").at(-1);
					const runId = randomUUID();
					entry.runId = runId;
					this.recordInput(entry, content.text, p.content);
					this.publish(id, { type: "run_start", runId });
					entry.run = (async () => {
						if (content.text === "/compact") {
							await s.compact();
							return;
						}
						await s.prompt(content.text, { images: content.images });
					})();
					let stopReason = "error";
					try {
						await entry.run;
						const last = s.messages.filter((m) => m.role === "assistant").at(-1);
						stopReason =
							last && last !== previousAssistant && "stopReason" in last
								? last.stopReason
								: "end_turn";
						if (stopReason === "error")
							throw new Error(
								"Pi could not complete this turn. Check the provider connection and model configuration.",
							);
						return { stopReason };
					} finally {
						entry.run = undefined;
						entry.runId = "";
						this.publish(id, { type: "run_end", stopReason });
					}
				}
				case "pi.session.steer": {
					if (!entry.runId || p.expectedRunId !== entry.runId)
						throw new Error("Active run changed");
					const content = this.promptContent(p.prompt);
					this.recordInput(entry, content.text, p.prompt);
					await s.steer(content.text, content.images);
					return { accepted: true };
				}
				case "session.cancel":
					await s.abort();
					return { ok: true };
				case "session.configure": {
					const key = text(p.configId),
						value = required(p.value, "configuration value");
					if (key === "thinking") {
						if (!s.getAvailableThinkingLevels().includes(value as typeof s.thinkingLevel))
							throw new Error("Unsupported thinking level");
						s.setThinkingLevel(value as typeof s.thinkingLevel);
					} else if (key === "provider" || key === "model") {
						const provider = key === "provider" ? value : s.model?.provider;
						if (!provider) throw new Error("Select a provider");
						const model =
							key === "provider"
								? (await entry.modelRuntime.getAvailable(provider))[0]
								: entry.modelRuntime.getModel(provider, value);
						if (!model) throw new Error("Model is unavailable");
						await s.setModel(model);
					} else throw new Error("Unknown session configuration");
					return { configOptions: this.config(entry) };
				}
				case "pi.session.rename":
					s.setSessionName(required(p.title, "title", 1000));
					return { ok: true };
				case "pi.session.archive":
				case "pi.session.unarchive":
					if (entry.run) throw new Error("Stop the running session first");
					await this.catalog.update((c) => {
						if (!c[id])
							c[id] = {
								path: required(s.sessionFile, "session file"),
								cwd: s.sessionManager.getCwd(),
								createdAt: s.sessionManager.getHeader()!.timestamp,
							};
						this.listing = undefined;
						if (method.endsWith(".archive")) c[id].archivedAt = new Date().toISOString();
						else delete c[id].archivedAt;
					});
					return { ok: true };
				case "pi.session.info": {
					const metadata = await this.metadata(id);
					return {
						session: {
							sessionId: id,
							title: s.sessionName ?? "Chat",
							updatedAt: new Date().toISOString(),
							archived: !!metadata.archivedAt,
							...metadata,
						},
					};
				}
				case "session.delete":
					if (entry.run) throw new Error("Stop the running session first");
					await entry.forgetMcp?.();
					await entry.close();
					await rm(required(s.sessionFile, "session file"), { force: true });
					this.entries.delete(id);
					this.listing = undefined;
					await this.catalog.update((c) => {
						delete c[id];
					});
					return { ok: true };
				case "pi.slash-commands.list":
					return { commands: this.commands(entry) };
				case "pi.tools.list":
					return {
						tools: s
							.getAllTools()
							.filter((t) => s.getActiveToolNames().includes(t.name))
							.map((t) => ({
								name: t.name,
								description: t.description,
								parameters: Object.keys(object(object(t.parameters).properties)),
								extensionName:
									t.sourceInfo?.source === "builtin"
										? "builtin"
										: (t.sourceInfo?.path ?? "extension"),
							})),
					};
				default:
					return entry.capabilities.call(method, p, this.context(entry));
			}
		});
	}
	async close(): Promise<void> {
		this.closed = true;
		clearInterval(this.timer);
		await Promise.allSettled([
			...this.creating,
			...this.building,
			...this.opening.values(),
			...this.evicting.values(),
		]);
		await Promise.allSettled([...this.entries.values()].map((e) => e.close()));
		this.entries.clear();
		this.pages.clear();
		this.listing = undefined;
	}
}
