import { expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ProjectTrustStore, SessionManager } from "@earendil-works/pi-coding-agent";
import { type ManagedSession, Sessions } from "../../../assistant/src/sessions.ts";

const require = createRequire(import.meta.url);
const { runAgent } = require("@mjakl/pi-subagent/runner");
const { discoverAgents } = require("@mjakl/pi-subagent/agents");

test("real known child and assistant execute the same unknown user/project native resources", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-native-child-"));
	const previous = process.env.PI_CODING_AGENT_DIR;
	let sessions: Sessions | undefined;
	try {
		const agentDir = join(root, "agent"),
			cwd = join(root, "project");
		await mkdir(join(agentDir, "agents"), { recursive: true });
		await mkdir(join(agentDir, "extensions"));
		await mkdir(join(cwd, ".pi", "extensions"), { recursive: true });
		process.env.PI_CODING_AGENT_DIR = agentDir;
		// The RPC child honors native project trust. Explicitly trust this
		// disposable fixture, just as an operator must trust project extensions.
		new ProjectTrustStore(agentDir).set(cwd, true);
		await writeFile(
			join(agentDir, "settings.json"),
			JSON.stringify({
				extensions: [join(import.meta.dir, "lifecycle-provider.ts")],
				defaultProvider: "lifecycle-fixture",
				defaultModel: "echo",
			}),
		);
		await writeFile(
			join(agentDir, "agents", "known.md"),
			"---\nname: known\ndescription: Native child parity\nmodel: lifecycle-fixture/echo\n---\n",
		);
		for (const scope of ["user", "project"]) {
			const dir = scope === "user" ? join(agentDir, "extensions") : join(cwd, ".pi", "extensions");
			await writeFile(
				join(dir, "unknown.js"),
				`import { appendFileSync } from "node:fs";
				export default pi => {
					pi.on("session_start", (_event, ctx) => appendFileSync(${JSON.stringify(join(cwd, "resources.jsonl"))}, JSON.stringify({scope: ${JSON.stringify(scope)}, pid: process.pid, cwd: ctx.cwd}) + "\\n"));
					pi.on("before_agent_start", event => ({ systemPrompt: event.systemPrompt + "\\nUNKNOWN_${scope.toUpperCase()}_PROMPT" }));
					pi.registerTool({name: "unknown_${scope}", label: "Unknown", description: "Unknown native ${scope} resource", parameters: {type: "object", properties: {}}, execute: async (_id, _params, _signal, _update, ctx) => ({ content: [{type: "text", text: "native-${scope}-result"}], details: {scope: "${scope}", cwd: ctx.cwd, mode: ctx.mode} })});
				};`,
			);
		}
		sessions = new Sessions(agentDir, [], () => {});
		const parent = await sessions.create(cwd);
		expect(sessions.inventory(parent).errors).toEqual([]);
		await sessions.call("session.prompt", {
			sessionId: parent.session.sessionId,
			content: [{ type: "text", text: "probe-native" }],
		});
		const { agents } = discoverAgents(cwd, "user", false);
		const childId = crypto.randomUUID();
		const childDir = join(root, "child-sessions");
		const child = await runAgent({
			cwd,
			agents,
			callIndex: 0,
			agentName: "known",
			prompt: "probe-native",
			initialContext: "empty",
			parentDepth: 0,
			parentAgentStack: [],
			maxDepth: 3,
			preventCycles: true,
			timeoutMs: 15000,
			session: {
				handle: "parity",
				id: childId,
				name: "parity",
				cwd,
				created: true,
				initialContextApplied: "empty",
			},
			persistentSessionDir: childDir,
			makeDetails: (results: unknown[]) => ({
				kind: "pi-subagent",
				projectAgentsDir: null,
				results,
			}),
		});
		expect(child.exitCode).toBe(0);
		expect(child.stderr).not.toMatch(/error/i);
		const results = (messages: ManagedSession["session"]["messages"]) =>
			messages
				.filter((message) => message.role === "toolResult")
				.filter((message) => message.toolName.startsWith("unknown_"))
				.map(({ toolName, content, details, isError }) => ({
					toolName,
					content,
					details,
					isError,
				}));
		const parentResults = results(parent.session.messages);
		expect(parentResults).toHaveLength(2);
		const nativeChildren = await SessionManager.listAll(childDir);
		expect(nativeChildren).toHaveLength(1);
		expect(nativeChildren[0].id).toBe(childId);
		const childMessages = SessionManager.open(nativeChildren[0].path)
			.getBranch()
			.flatMap((entry) => (entry.type === "message" ? [entry.message] : []));
		expect(results(childMessages)).toEqual(parentResults);
		const loaded = (await readFile(join(cwd, "resources.jsonl"), "utf8"))
			.trim()
			.split("\n")
			.map((line) => JSON.parse(line));
		for (const scope of ["user", "project"]) {
			expect(
				loaded.filter((event) => event.scope === scope && event.pid === process.pid),
			).toHaveLength(1);
			expect(
				loaded.filter((event) => event.scope === scope && event.pid !== process.pid),
			).toHaveLength(1);
		}
		const streams = (await readFile(join(cwd, "lifecycle-audit.jsonl"), "utf8"))
			.trim()
			.split("\n")
			.map((line) => JSON.parse(line))
			.filter((event) => event.event === "stream");
		expect(streams).toHaveLength(4);
		for (const stream of streams) {
			expect(stream.system).toContain("UNKNOWN_USER_PROMPT");
			expect(stream.system).toContain("UNKNOWN_PROJECT_PROMPT");
			expect(stream.tools).toContain("unknown_user");
			expect(stream.tools).toContain("unknown_project");
		}
	} finally {
		await sessions?.close();
		if (previous === undefined) delete process.env.PI_CODING_AGENT_DIR;
		else process.env.PI_CODING_AGENT_DIR = previous;
		await rm(root, { recursive: true, force: true });
	}
}, 30000);
