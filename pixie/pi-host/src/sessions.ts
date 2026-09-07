import { randomUUID } from "node:crypto";
import { realpath, rm, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import {
	type AgentSession,
	createAgentSession,
	createEventBus,
	DefaultResourceLoader,
	type ExtensionFactory,
	ModelRuntime,
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

export interface ManagedSession {
	session: AgentSession;
	capabilities: Capabilities;
	modelRuntime: ModelRuntime;
	runId: string;
	inputs: { text: string; content: unknown }[];
	run?: Promise<void>;
	partialMessage?: RecordValue;
	partialTools: Map<string, RecordValue>;
	close: () => Promise<void>;
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
	private opening = new Map<string, Promise<ManagedSession>>();
	readonly catalog;
	private sequences = new Map<string, number>();
	constructor(
		readonly agentDir: string,
		readonly factories: ExtensionFactory[],
		readonly output: (id: string, event: unknown, sequence: number) => void,
	) {
		this.catalog = serviceStore<Record<string, SessionMetadata>>(agentDir, "sessions", () => ({}));
	}
	private publish(id: string, event: unknown): void {
		const sequence = (this.sequences.get(id) ?? 0) + 1;
		this.sequences.set(id, sequence);
		this.output(id, event, sequence);
	}
	async create(cwd: string, parent?: string): Promise<ManagedSession> {
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
		await this.catalog.update((c) => {
			c[id] = {
				path: entry.session.sessionFile!,
				cwd,
				createdAt: new Date().toISOString(),
				...(parent ? { parentSessionId: parent } : {}),
			};
		});
		this.entries.set(id, entry);
		return entry;
	}
	async metadata(id: string): Promise<SessionMetadata> {
		const c = await this.catalog.read();
		if (c[id]) return c[id];
		const found = (await SessionManager.listAll(join(this.agentDir, "sessions"))).filter(
			(s) => s.id === id,
		);
		if (found.length !== 1) throw new HostError("Unknown or ambiguous Pi session", -32002);
		const s = found[0];
		return { path: s.path, cwd: s.cwd, createdAt: s.created.toISOString() };
	}
	async get(id: string, cwd?: string): Promise<ManagedSession> {
		let pending = this.opening.get(id);
		if (!pending) {
			pending = (async () => {
				const existing = this.entries.get(id);
				if (existing) return existing;
				const metadata = await this.metadata(id);
				const entry = await this.build(metadata.cwd, SessionManager.open(metadata.path));
				this.entries.set(id, entry);
				return entry;
			})();
			this.opening.set(id, pending);
			void pending.finally(() => this.opening.delete(id)).catch(() => {});
		}
		const entry = await pending;
		if (cwd && (await realpath(entry.session.sessionManager.getCwd())) !== (await realpath(cwd)))
			throw new Error("Session project mismatch");
		return entry;
	}
	async control(cwd: string): Promise<ManagedSession> {
		return this.build(cwd, SessionManager.inMemory(cwd));
	}
	private async build(cwd: string, manager: SessionManager): Promise<ManagedSession> {
		const modelRuntime = await ModelRuntime.create({
			authPath: join(this.agentDir, "auth.json"),
			modelsPath: join(this.agentDir, "models.json"),
			allowModelNetwork: false,
		});
		const settings = SettingsManager.create(cwd, this.agentDir);
		const bus = createEventBus();
		const capabilities = new Capabilities();
		bus.on(CAPABILITY_EVENT, (v) => capabilities.register(v));
		bus.on(MCP_SERVICE_EVENT, (value) => {
			const service = value as MCPService;
			if (service?.version !== 1 || !service.operations) return;
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
		await loader.reload();
		const { session, extensionsResult } = await createAgentSession({
			cwd,
			agentDir: this.agentDir,
			modelRuntime,
			settingsManager: settings,
			sessionManager: manager,
			resourceLoader: loader,
		});
		for (const error of extensionsResult.errors)
			this.publish(session.sessionId, { type: "extension_error", error: error.error });
		let closing: Promise<void> | undefined;
		const entry: ManagedSession = {
			session,
			capabilities,
			modelRuntime,
			runId: "",
			inputs: [],
			partialTools: new Map(),
			close: () =>
				(closing ??= (async () => {
					await session.abort();
					try {
						await session.extensionRunner.emit({ type: "session_shutdown", reason: "quit" });
					} finally {
						await capabilities.close();
						session.dispose();
						await settings.flush();
					}
				})()),
		};
		await session.bindExtensions({
			mode: "rpc",
			onError: (e) => this.publish(session.sessionId, { type: "extension_error", error: e.error }),
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
							message: event.message,
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
			this.publish(session.sessionId, event);
		});
		return entry;
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
			if (e.type !== "message") continue;
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
	async list(cursor: string): Promise<RecordValue> {
		const all = await SessionManager.listAll(join(this.agentDir, "sessions"));
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
		for (const [id, metadata] of Object.entries(catalog)) {
			if (sessions.some((s) => s.sessionId === id)) continue;
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
		const offset = cursor ? Number(cursor) : 0;
		if (!Number.isSafeInteger(offset) || offset < 0) throw new Error("Invalid history cursor");
		return {
			sessions: sessions.slice(offset, offset + 100),
			...(offset + 100 < sessions.length ? { nextCursor: String(offset + 100) } : {}),
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
		const entry = await this.get(id, text(p.cwd) || undefined);
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
				if (!entry.runId || p.expectedRunId !== entry.runId) throw new Error("Active run changed");
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
							path: s.sessionFile!,
							cwd: s.sessionManager.getCwd(),
							createdAt: s.sessionManager.getHeader()!.timestamp,
						};
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
				await entry.close();
				await rm(s.sessionFile!, { force: true });
				this.entries.delete(id);
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
	}
	async close(): Promise<void> {
		await Promise.allSettled([...this.entries.values()].map((e) => e.close()));
		this.entries.clear();
	}
}
