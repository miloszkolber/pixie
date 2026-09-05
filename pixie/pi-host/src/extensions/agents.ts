import { open, readdir, readFile, realpath, rm } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import {
	type AgentSession,
	createAgentSession,
	type ExtensionAPI,
	getAgentDir,
	ModelRuntime,
	SessionManager,
} from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { parse, stringify } from "yaml";
import { registerCapability } from "../capabilities.ts";
import { atomicWrite, object, type RecordValue, required, text } from "../storage.ts";

interface Definition {
	type: "agent";
	path: string;
	name: string;
	description: string;
	content: string;
	global: boolean;
	writable: boolean;
	properties: RecordValue;
}
export default function agentsExtension(
	pi: ExtensionAPI,
	agentDir = getAgentDir(),
	runner?: (cwd: string) => Promise<{
		session: AgentSession;
		prompt: (text: string) => Promise<unknown>;
		close: () => Promise<void>;
	}>,
): void {
	const directories = (cwd?: string) => [
		{ path: join(agentDir, "agents"), global: true },
		...(cwd ? [{ path: join(cwd, ".pi", "agents"), global: false }] : []),
	];
	const list = async (cwd?: string, warnings: string[] = []): Promise<Definition[]> => {
		const result: Definition[] = [];
		for (const dir of directories(cwd)) {
			let names: string[];
			try {
				names = await readdir(dir.path);
			} catch (e) {
				if ((e as NodeJS.ErrnoException).code === "ENOENT") continue;
				throw e;
			}
			for (const name of names.sort().filter((n) => n.endsWith(".md"))) {
				const path = join(dir.path, name);
				try {
					if (dirname(await realpath(path)) !== (await realpath(dir.path))) continue;
					const file = await open(path, "r");
					let raw: string;
					try {
						const buffer = Buffer.alloc(65537);
						let size = 0;
						while (size < buffer.length) {
							const { bytesRead } = await file.read(buffer, size, buffer.length - size, null);
							if (!bytesRead) break;
							size += bytesRead;
						}
						if (size > 65536) throw new Error("Agent exceeds 65536 bytes");
						raw = buffer.subarray(0, size).toString("utf8");
					} finally {
						await file.close();
					}
					const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(raw);
					if (!match) throw new Error("Missing agent frontmatter");
					const metadata = object(parse(match[1]));
					result.push({
						type: "agent",
						path,
						name: text(metadata.name) || basename(name, ".md"),
						description: text(metadata.description),
						content: match[2],
						global: dir.global,
						writable: true,
						properties: metadata,
					});
				} catch {
					warnings.push(`Cannot load agent: ${path}`);
				}
			}
		}
		return result;
	};
	const save = async (p: RecordValue, cwd: string): Promise<{ source: Definition }> => {
		const name = required(p.name, "agent name", 80).trim();
		if (!name || Buffer.byteLength(name) > 80 || !/^[\p{L}\p{N}_ -]+$/u.test(name))
			throw new Error("Invalid agent name");
		const target = object(p.target);
		const scope = target.scope === "projectDir" ? text(target.projectDir) : undefined;
		let path: string;
		let previous: RecordValue = {};
		if (p.path) {
			const existing = (await list(cwd)).find((a) => a.path === p.path);
			if (!existing) throw new Error("Unknown agent source");
			path = existing.path;
			previous = existing.properties;
		} else {
			path = join(
				scope ? join(await realpath(scope), ".pi", "agents") : join(agentDir, "agents"),
				`${name}.md`,
			);
			try {
				await readFile(path);
				throw new Error("Agent already exists");
			} catch (e) {
				if ((e as NodeJS.ErrnoException).code !== "ENOENT") throw e;
			}
		}
		const properties: RecordValue = {
			...previous,
			...object(p.properties),
			name,
			description: text(p.description),
		};
		for (const [key, value] of Object.entries(properties))
			if (value === null) delete properties[key];
		const document = `---\n${stringify(properties)}---\n${text(p.content)}`;
		if (Buffer.byteLength(document) > 65536)
			throw new Error("Agent must fit within 65536 bytes including frontmatter");
		await atomicWrite(path, document);
		const source = (await list(scope ?? cwd)).find((a) => a.path === path);
		if (!source) throw new Error("Saved agent could not be loaded");
		return { source };
	};
	registerCapability(pi, {
		id: "agents",
		version: 1,
		operations: {
			"pi.sources.list": async (p, ctx) => {
				const warnings: string[] = [];
				const sources = await list(text(p.projectDir) || ctx.cwd, warnings);
				for (const error of warnings) ctx.notify({ type: "extension_error", error });
				return { sources, warnings };
			},
			"pi.sources.create": (p, ctx) => save(p, ctx.cwd),
			"pi.sources.update": (p, ctx) => save(p, ctx.cwd),
			"pi.sources.delete": async (p, ctx) => {
				const source = (await list(ctx.cwd)).find((a) => a.path === p.path);
				if (!source) throw new Error("Unknown agent source");
				await rm(source.path);
				return { ok: true };
			},
			"pi.agent-mentions.list": async (p, ctx) => ({
				agents: (await list(text(p.cwd) || ctx.cwd)).map((a) => ({
					name: a.name,
					description: a.description,
					sourceType: "agent",
					mention: `@${a.name}`,
				})),
			}),
		},
	});
	pi.registerTool({
		name: "list_agents",
		label: "List agents",
		description: "List the named agents available for delegation in this project.",
		parameters: Type.Object({}),
		execute: async (_id, _params, _signal, _update, ctx) => ({
			content: [
				{
					type: "text",
					text: JSON.stringify(
						(await list(ctx.cwd)).map((a) => ({
							name: a.name,
							description: a.description,
							model: a.properties.model,
						})),
					),
				},
			],
			details: {},
		}),
	});
	pi.registerTool({
		name: "delegate",
		label: "Delegate",
		description:
			"Delegate a task to a named agent defined in ~/.pi/agent/agents or the project .pi/agents directory. Each task has an isolated context.",
		parameters: Type.Object({ agent: Type.String(), task: Type.String() }),
		execute: async (_id, p, signal, onUpdate, ctx) => {
			const definitions = await list(ctx.cwd);
			const definition = definitions.filter((a) => a.name === p.agent).at(-1);
			if (!definition) throw new Error("Unknown agent");
			let model = ctx.model;
			const execution = runner
				? await runner(ctx.cwd)
				: await (async () => {
						const models = await ModelRuntime.create({
							authPath: join(agentDir, "auth.json"),
							modelsPath: join(agentDir, "models.json"),
						});
						const { session } = await createAgentSession({
							cwd: ctx.cwd,
							agentDir,
							modelRuntime: models,
							model,
							sessionManager: SessionManager.create(
								ctx.cwd,
								join(
									agentDir,
									"sessions",
									`--${resolve(ctx.cwd)
										.replace(/^[/\\]/, "")
										.replace(/[/\\:]/g, "-")}--`,
								),
							),
						});
						await session.bindExtensions({ mode: "rpc" });
						return {
							session,
							prompt: (text: string) => session.prompt(text),
							close: async () => session.dispose(),
						};
					})();
			const { session } = execution;
			try {
				const preference = text(definition.properties.model);
				if (preference) {
					const slash = preference.indexOf("/");
					model =
						slash > 0
							? session.modelRuntime.getModel(
									preference.slice(0, slash),
									preference.slice(slash + 1),
								)
							: session.modelRuntime.getModel(model?.provider ?? "", preference);
					if (!model) throw new Error("Agent model is unavailable");
				}
				if (model) await session.setModel(model);
			} catch (error) {
				await execution.close();
				throw error;
			}
			const abort = () => void session.abort();
			if (signal?.aborted) {
				await execution.close();
				throw new Error("Delegation cancelled");
			}
			signal?.addEventListener("abort", abort, { once: true });
			const events: RecordValue[] = [];
			const unsubscribe = session.subscribe((event) => {
				if (event.type === "tool_execution_start") {
					events.push({ type: "tool", name: event.toolName });
					onUpdate?.({
						content: [{ type: "text", text: `${p.agent}: ${event.toolName}` }],
						details: {
							subagent: { agent: p.agent, sessionId: session.sessionId, events: events.slice(-20) },
							mode: "single",
							childSessionId: session.sessionId,
							status: "running",
						},
					});
				}
			});
			try {
				await execution.prompt(`${definition.content}\n\nDelegated task:\n${p.task}`);
				const last = session.messages.filter((m) => m.role === "assistant").at(-1);
				if (signal?.aborted || last?.stopReason === "aborted")
					throw new Error("Delegation cancelled");
				if (last?.stopReason === "error") throw new Error("Delegated agent failed");
				const output =
					last && "content" in last
						? last.content
								.filter((block) => block.type === "text")
								.map((block) => block.text)
								.join("\n")
						: "Agent completed.";
				return {
					content: [{ type: "text", text: output }],
					details: {
						subagent: { agent: p.agent, sessionId: session.sessionId, events: events.slice(-20) },
						mode: "single",
						childSessionId: session.sessionId,
						status: "completed",
						results: [
							{
								runId: session.sessionId,
								agent: "child",
								task: p.task,
								status: "completed",
								model: session.model,
								thinkingLevel: session.thinkingLevel,
								finalOutput: output,
								outputState: "present",
							},
						],
						usage: session.getSessionStats(),
					},
				};
			} finally {
				unsubscribe();
				signal?.removeEventListener("abort", abort);
				await execution.close();
			}
		},
	});
}
