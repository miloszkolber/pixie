import { expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

// Resolve the host's locked dependency, not stale application-local installs.
const require = createRequire(new URL("../../../agent/pixie-assistant/package.json", import.meta.url));
const { runAgent } = require("@mjakl/pi-subagent/runner");
const { discoverAgents } = require("@mjakl/pi-subagent/agents");
const { getFinalOutput } = require("@mjakl/pi-subagent/types");

interface ChildAudit {
	pid: number;
	argv: string[];
	execPath: string;
	bun: string;
	agentDir: string;
	dismissed: boolean;
	prompts: string[];
}

async function waitFor(check: () => Promise<boolean> | boolean, timeout = 10000): Promise<void> {
	const deadline = Date.now() + timeout;
	while (!(await check())) {
		if (Date.now() >= deadline) throw new Error("Child process check timed out");
		await Bun.sleep(20);
	}
}

test("Bun runs native known-agent children with settings, named continuation and cancellation", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-subagent-child-"));
	const saved = process.env.PI_CODING_AGENT_DIR;
	const controller = new AbortController();
	let pending: Promise<unknown> | undefined;
	try {
		const agentDir = join(root, "agent");
		const cwd = join(root, "project");
		await mkdir(join(agentDir, "agents"), { recursive: true });
		await mkdir(cwd);
		process.env.PI_CODING_AGENT_DIR = agentDir;
		await writeFile(
			join(agentDir, "settings.json"),
			JSON.stringify({
				extensions: [join(import.meta.dir, "subagent-child-provider.ts")],
				defaultProvider: "child-fixture",
				defaultModel: "echo",
			}),
		);
		await writeFile(
			join(agentDir, "agents", "known.md"),
			"---\nname: known\ndescription: Deterministic child\nmodel: child-fixture/echo\nnoTools: true\n---\n",
		);
		const { agents } = discoverAgents(cwd, "user", false);
		expect(agents.map((agent: { name: string }) => agent.name)).toEqual(["known"]);
		const options = {
			cwd,
			agents,
			callIndex: 0,
			agentName: "known",
			initialContext: "empty",
			parentDepth: 0,
			parentAgentStack: [],
			maxDepth: 3,
			preventCycles: true,
			timeoutMs: 15000,
			makeDetails: (results: unknown[]) => ({
				kind: "pi-subagent",
				projectAgentsDir: null,
				results,
			}),
		};
		const audit = async (): Promise<ChildAudit[]> => {
			try {
				return (await readFile(join(agentDir, "child-audit.jsonl"), "utf8"))
					.trim()
					.split("\n")
					.map((line) => JSON.parse(line));
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
				throw error;
			}
		};
		const fresh = await runAgent({ ...options, prompt: "fresh" });
		expect(fresh.stderr).not.toMatch(/error/i);
		expect(fresh.exitCode).toBe(0);
		expect(getFinalOutput(fresh.messages)).toBe('["fresh"]');
		expect(fresh.usage).toMatchObject({ input: 5, output: 4, turns: 1 });
		const [launch] = await audit();
		expect(launch).toMatchObject({
			execPath: process.execPath,
			bun: Bun.version,
			agentDir,
			dismissed: true,
		});
		expect(launch.pid).not.toBe(process.pid);
		expect(launch.argv).toEqual([
			process.execPath,
			fileURLToPath(
				import.meta.resolve(
					"@earendil-works/pi-coding-agent/rpc-entry",
					require.resolve("@mjakl/pi-subagent/runner"),
				),
			),
			"--mode",
			"rpc",
			"--no-session",
			"--model",
			"child-fixture/echo",
			"--no-tools",
		]);
		const session = {
			handle: "topic",
			id: crypto.randomUUID(),
			name: "topic",
			cwd,
			created: true,
			initialContextApplied: "empty",
		};
		const named = await runAgent({
			...options,
			prompt: "remember",
			session,
			persistentSessionDir: join(agentDir, "sessions"),
		});
		expect(named.stderr).not.toMatch(/error/i);
		expect(named.exitCode).toBe(0);
		const continued = await runAgent({
			...options,
			prompt: "continue",
			session: { ...session, created: false, initialContextApplied: null },
			persistentSessionDir: join(agentDir, "sessions"),
		});
		expect(continued.stderr).not.toMatch(/error/i);
		expect(continued.exitCode).toBe(0);
		expect(getFinalOutput(continued.messages)).toBe('["remember","continue"]');
		expect(continued.session.id).toBe(session.id);
		pending = runAgent({ ...options, prompt: "hold", signal: controller.signal });
		await waitFor(async () => (await audit()).length === 4);
		const child = (await audit())[3];
		process.kill(child.pid, 0);
		controller.abort();
		expect(await pending).toMatchObject({ exitCode: 130, stopReason: "aborted" });
		await waitFor(() => {
			try {
				process.kill(child.pid, 0);
				return false;
			} catch (error) {
				return (error as NodeJS.ErrnoException).code === "ESRCH";
			}
		}, 3000);
	} finally {
		controller.abort();
		await pending;
		if (saved === undefined) delete process.env.PI_CODING_AGENT_DIR;
		else process.env.PI_CODING_AGENT_DIR = saved;
		await rm(root, { recursive: true, force: true });
	}
}, 60000);
