import { createHash } from "node:crypto";
import { mkdir, open, readdir, realpath, rm } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { parse, stringify } from "yaml";
import type { Capability } from "./capabilities.ts";
import { atomicCreate, atomicWrite, object, type RecordValue, required, text } from "./storage.ts";

interface Definition {
	type: "agent";
	path: string;
	name: string;
	description: string;
	content: string;
	revision: string;
	global: boolean;
	writable: boolean;
	executionEligibility: "unknown";
	properties: RecordValue;
}

// Agent definition authoring only. Delegation itself is owned by the upstream
// native subagent extension: discovery at execution time, the
// child Pi process, model/thinking/tool handling, persistent child sessions,
// parallel calls and recursion guards. This application API only manages the same
// native Markdown files (`~/.pi/agent/agents/*.md`, `<project>/.pi/agents/*.md`)
// through `pi.sources.*` plus `@agent` mention discovery, so the web UI keeps
// its agent editor without duplicating any execution engine.
export default function agentAuthoring(agentDir: string): Capability {
	const directories = (cwd?: string) => [
		{ path: join(agentDir, "agents"), global: true },
		...(cwd ? [{ path: join(cwd, ".pi", "agents"), global: false }] : []),
	];
	const list = async (cwd?: string, warnings: string[] = []): Promise<Definition[]> => {
		const result: Definition[] = [];
		for (const dir of directories(cwd)) {
			let root: string;
			let names: string[];
			try {
				root = await realpath(dir.path);
				if (root !== dir.path) continue;
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
					if ((await realpath(path)) !== path || dirname(await realpath(path)) !== root)
						throw new Error("Agent path changed while reading");
					const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(raw);
					if (!match) throw new Error("Missing agent frontmatter");
					const metadata = object(parse(match[1]));
					const revision = `sha256:${createHash("sha256").update(raw).digest("hex")}`;
					result.push({
						type: "agent",
						path,
						name: text(metadata.name) || basename(name, ".md"),
						description: text(metadata.description),
						content: match[2],
						revision,
						global: dir.global,
						writable: true,
						executionEligibility: "unknown",
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
		let expectedRevision = "";
		if (p.path) {
			const existing = (await list(cwd)).find((a) => a.path === p.path);
			if (!existing) throw new Error("Unknown agent source");
			expectedRevision = required(p.expectedRevision, "agent revision", 128);
			if (existing.revision !== expectedRevision)
				throw new Error("Agent changed on disk; reload before editing");
			path = existing.path;
			previous = existing.properties;
		} else {
			if (p.expectedRevision !== undefined)
				throw new Error("Agent revision is not valid for create");
			path = join(
				scope ? join(await realpath(scope), ".pi", "agents") : join(agentDir, "agents"),
				`${name}.md`,
			);
			await mkdir(dirname(path), { recursive: true, mode: 0o700 });
			const root = await realpath(dirname(path)).catch(() => "");
			if (root !== dirname(path)) throw new Error("Agent directory is not rooted");
			try {
				await realpath(path);
				throw new Error("Agent already exists");
			} catch (e) {
				if ((e as NodeJS.ErrnoException).code !== "ENOENT") {
					if ((e as Error).message === "Agent already exists") throw e;
					throw new Error("Agent already exists");
				}
			}
		}
		const current = (await list(scope ?? cwd)).find((a) => a.path === path);
		if (p.path) {
			if (!current || current.revision !== expectedRevision)
				throw new Error("Agent changed on disk; reload before editing");
			previous = current.properties;
		} else if (current) {
			throw new Error("Agent already exists");
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
		try {
			if (p.path) await atomicWrite(path, document);
			else await atomicCreate(path, document);
		} catch (error) {
			if (!p.path && (error as NodeJS.ErrnoException).code === "EEXIST")
				throw new Error("Agent already exists");
			throw error;
		}
		const source = (await list(scope ?? cwd)).find((a) => a.path === path);
		if (!source) throw new Error("Saved agent could not be loaded");
		return { source };
	};
	return {
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
				const expectedRevision = required(p.expectedRevision, "agent revision", 128);
				if (source.revision !== expectedRevision)
					throw new Error("Agent changed on disk; reload before deleting");
				const current = (await list(ctx.cwd)).find((a) => a.path === source.path);
				if (!current || current.revision !== expectedRevision)
					throw new Error("Agent changed on disk; reload before deleting");
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
	};
}
