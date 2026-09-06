import { open, readdir, readFile, realpath, rm } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { getAgentDir, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
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

// Agent definition authoring only. Delegation itself is owned by the upstream
// `pi-subagent` profile (`subagent` tool): discovery at execution time, the
// child Pi process, model/thinking/tool handling, persistent child sessions,
// parallel calls and recursion guards. This extension only manages the same
// native Markdown files (`~/.pi/agent/agents/*.md`, `<project>/.pi/agents/*.md`)
// through `pi.sources.*` plus `@agent` mention discovery, so the web UI keeps
// its agent editor without duplicating any execution engine.
export default function agentsExtension(pi: ExtensionAPI, agentDir = getAgentDir()): void {
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
}
