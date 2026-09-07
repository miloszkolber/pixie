import { afterEach, expect, test } from "bun:test";
import { mkdir, stat, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { join } from "node:path";
import { ProjectTrustStore } from "@earendil-works/pi-coding-agent";
import { piSubagent } from "./upstream.ts";
import { cleanups, echoProvider, findTool, fixture, tempDir } from "./helpers.ts";

// Structural view of the upstream discovery result. The upstream `agents`
// module is loaded through `createRequire` (same reason as the profile
// bridge in pixie-assistant): a static import would pull its untyped JavaScript
// helpers into Pixie's strict typecheck.
interface UpstreamAgentConfig {
	name: string;
	description: string;
	tools?: string[];
	noTools?: boolean;
	model?: string;
	thinking?: string;
	inactivityTimeout?: number;
	sessionPreference?: string;
	sessionHint?: string;
	systemPrompt: string;
	source: string;
	filePath: string;
}
const { discoverAgents } = createRequire(import.meta.url)("@mjakl/pi-subagent/agents") as {
	discoverAgents: (
		cwd: string,
		scope: string,
		includeProjectAgents: boolean,
	) => { agents: UpstreamAgentConfig[]; projectAgentsDir: string | null };
};

const savedEnv = new Map<string, string | undefined>();
function setEnv(name: string, value: string | undefined): void {
	if (!savedEnv.has(name)) savedEnv.set(name, process.env[name]);
	if (value === undefined) delete process.env[name];
	else process.env[name] = value;
}

afterEach(async () => {
	for (const [name, value] of savedEnv) {
		if (value === undefined) delete process.env[name];
		else process.env[name] = value;
	}
	savedEnv.clear();
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

// Isolate upstream user-agent discovery (and its starter-file side effect)
// from the operator's real ~/.pi/agent.
async function isolatedHome(): Promise<string> {
	const home = await tempDir("pixie-subagent-home-");
	setEnv("PI_CODING_AGENT_DIR", home);
	for (const name of ["PI_SUBAGENT_DEPTH", "PI_SUBAGENT_MAX_DEPTH", "PI_SUBAGENT_STACK"]) {
		setEnv(name, undefined);
	}
	return home;
}

const REVIEWER = `---
name: reviewer
description: Reviews code changes
model: reviewer/model
thinking: high
tools: read, grep
sessionPreference: either
sessionHint: Use a topic session for follow-ups.
---

Review carefully.
`;

async function writeUserAgent(home: string, file: string, content: string): Promise<void> {
	await mkdir(join(home, "agents"), { recursive: true });
	await writeFile(join(home, "agents", file), content);
}

async function executeCalls(entry: any, calls: unknown[], signal?: AbortSignal): Promise<any> {
	return findTool(entry, "subagent").execute(
		`parity-subagent-${executeCallsCount++}`,
		{ calls },
		signal ?? new AbortController().signal,
	);
}
let executeCallsCount = 0;

test("the profile registers the upstream subagent tool and marker", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	expect(entry.session.getActiveToolNames()).toContain("subagent");
	expect(entry.session.getActiveToolNames()).toContain("subagent");
});

test("delegation depth gate removes the tool at max depth", async () => {
	await isolatedHome();
	setEnv("PI_SUBAGENT_DEPTH", "3");
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	expect(entry.session.getActiveToolNames()).not.toContain("subagent");
	expect(entry.capabilities.snapshot()["pi-subagent"]).toBeUndefined();
});

test("discovery parses full frontmatter and skips invalid files", async () => {
	const home = await isolatedHome();
	await writeUserAgent(home, "reviewer.md", REVIEWER);
	await writeUserAgent(home, "nameless.md", `---\ndescription: No name\n---\nBody.\n`);
	const { agents } = discoverAgents("/nonexistent-cwd", "both", false);
	expect(agents.map((a) => a.name)).toEqual(["reviewer"]);
	expect(agents[0]).toMatchObject({
		description: "Reviews code changes",
		model: "reviewer/model",
		thinking: "high",
		tools: ["read", "grep"],
		sessionPreference: "either",
		sessionHint: "Use a topic session for follow-ups.",
		source: "user",
	});
});

test("project definitions override user ones when included", async () => {
	const home = await isolatedHome();
	await writeUserAgent(home, "dup.md", REVIEWER);
	const cwd = await tempDir("pixie-subagent-project-");
	await mkdir(join(cwd, ".pi", "agents"), { recursive: true });
	await writeFile(
		join(cwd, ".pi", "agents", "dup.md"),
		`---\nname: reviewer\ndescription: Project reviewer\nnoTools: true\ninactivityTimeout: 30\n---\nProject review.\n`,
	);
	const excluded = discoverAgents(cwd, "both", false);
	expect(excluded.agents.map((a) => a.name)).toEqual(["reviewer"]);
	expect(excluded.agents[0].source).toBe("user");
	const included = discoverAgents(cwd, "both", true);
	expect(included.agents.map((a) => a.name)).toEqual(["reviewer"]);
	expect(included.agents[0]).toMatchObject({
		description: "Project reviewer",
		noTools: true,
		inactivityTimeout: 30,
		source: "project",
	});
	expect(included.projectAgentsDir).toContain(".pi");
});

test("unknown agents fail structured without spawning", async () => {
	const home = await isolatedHome();
	await writeUserAgent(home, "reviewer.md", REVIEWER);
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const result = await executeCalls(entry, [{ agent: "nope", prompt: "Do it" }]);
	expect(String(result.content[0].text)).toMatch(/Unknown agent: "nope"/);
	expect(String(result.content[0].text)).toContain('"reviewer"');
	expect(result.details).toMatchObject({ kind: "pi-subagent", failed: true });
	expect(result.details.results).toHaveLength(1);
	expect(result.details.results[0]).toMatchObject({
		callIndex: 0,
		agent: "nope",
		agentSource: "unknown",
		prompt: "Do it",
		initialContext: "empty",
		exitCode: 1,
		stopReason: "error",
	});
	expect(result.details.results[0].usage).toMatchObject({ input: 0, output: 0 });
	expect(typeof result.details.results[0].errorMessage).toBe("string");
});

test("project agents stay hidden until the project is trusted", async () => {
	const home = await isolatedHome();
	await writeUserAgent(home, "reviewer.md", REVIEWER);
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const cwd = await tempDir("pixie-subagent-trust-");
	await mkdir(join(cwd, ".pi", "agents"), { recursive: true });
	await writeFile(
		join(cwd, ".pi", "agents", "proj.md"),
		`---\nname: proj\ndescription: Project agent\n---\nProject.\n`,
	);
	const hidden = await sessions.create(cwd);
	const untrusted = await executeCalls(hidden, [{ agent: "nope", prompt: "hi" }]);
	expect(String(untrusted.content[0].text)).not.toContain('"proj"');
	new ProjectTrustStore(home).set(cwd, true);
	new ProjectTrustStore(dir).set(cwd, true);
	const visible = await sessions.create(cwd);
	const trusted = await executeCalls(visible, [{ agent: "nope", prompt: "hi" }]);
	expect(String(trusted.content[0].text)).toContain('"proj"');
});

test("delegation cycles are blocked before spawning", async () => {
	const home = await isolatedHome();
	await writeUserAgent(home, "reviewer.md", REVIEWER);
	setEnv("PI_SUBAGENT_STACK", JSON.stringify(["reviewer"]));
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const result = await executeCalls(entry, [{ agent: "reviewer", prompt: "Loop" }]);
	expect(String(result.content[0].text)).toMatch(/cycle/i);
	expect(result.details).toMatchObject({ kind: "pi-subagent", failed: true });
	expect(result.details.results).toHaveLength(0);
});

test("invalid calls fail validation with structured errors", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const cases: [unknown, RegExp][] = [
		[[], /at least one call/],
		[{ agent: "reviewer" }, /missing calls array/],
		[[{ agent: "reviewer", prompt: "" }], /prompt must be a non-empty string/],
		[[{ agent: "reviewer", prompt: "hi", initialContext: "bogus" }], /initialContext/],
		[[{ agent: "reviewer", prompt: "hi", model: 42 }], /model must be a string/],
		[[{ agent: "reviewer", prompt: "hi", inactivityTimeout: 0 }], /inactivityTimeout/],
		[[{ agent: "reviewer", prompt: "hi", session: "" }], /session must not be empty/],
		[[{ agent: "reviewer", prompt: "hi", session: "x".repeat(121) }], /at most 120/],
		[
			Array.from({ length: 9 }, (_, index) => ({ agent: "reviewer", prompt: `task ${index}` })),
			/Max is 8/,
		],
	];
	for (const [calls, pattern] of cases) {
		const params = Array.isArray(calls) ? { calls } : calls;
		const result = await findTool(entry, "subagent").execute(
			`parity-subagent-invalid-${executeCallsCount++}`,
			params,
			new AbortController().signal,
		);
		expect(String(result.content[0].text)).toMatch(pattern);
		expect(result.details).toMatchObject({ kind: "pi-subagent", failed: true });
	}
});

test("duplicate persistent sessions are rejected for one-at-a-time use", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const result = await executeCalls(entry, [
		{ agent: "reviewer", prompt: "first", session: "iter" },
		{ agent: "reviewer", prompt: "second", session: "iter" },
	]);
	expect(String(result.content[0].text)).toMatch(/same persistent session/);
	expect(result.details).toMatchObject({ kind: "pi-subagent", failed: true });
});

test("parallel unknown agents return one error result per call", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const result = await executeCalls(entry, [
		{ agent: "ghost-a", prompt: "first" },
		{ agent: "ghost-b", prompt: "second" },
	]);
	expect(result.details.results).toHaveLength(2);
	expect(result.details.results.map((r: any) => r.callIndex)).toEqual([0, 1]);
	for (const single of result.details.results) {
		expect(single.exitCode).toBe(1);
		expect(single.errorMessage).toMatch(/Unknown agent/);
	}
	expect(result.details).toMatchObject({ failed: true });
});

test("per-call model override and parent context pass validation", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	// Unknown-agent (not validation) errors prove the override fields parsed.
	const overridden = await executeCalls(entry, [
		{ agent: "nope", prompt: "hi", model: "custom/model", initialContext: "parent" },
	]);
	expect(String(overridden.content[0].text)).toMatch(/Unknown agent: "nope"/);
});

test("named sessions attach deterministic child metadata", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const first = await executeCalls(entry, [{ agent: "nope", prompt: "hi", session: "iter-1" }]);
	const single = first.details.results[0];
	expect(single.session).toMatchObject({ handle: "iter-1" });
	expect(String(single.session.id)).toMatch(/^subagent\./);
	const second = await executeCalls(entry, [{ agent: "nope", prompt: "hi", session: "iter-1" }]);
	expect(second.details.results[0].session.id).toBe(single.session.id);
});

test("an aborted signal returns promptly without spawning", async () => {
	await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	const entry = await sessions.create(dir);
	const controller = new AbortController();
	controller.abort();
	const result = await executeCalls(entry, [{ agent: "nope", prompt: "hi" }], controller.signal);
	expect(String(result.content[0].text)).toMatch(/Unknown agent: "nope"/);
	expect(result.details).toMatchObject({ kind: "pi-subagent", failed: true });
});

test("an empty user directory gains the upstream starter agent", async () => {
	const home = await isolatedHome();
	const { dir, sessions } = await fixture([piSubagent, echoProvider()]);
	await sessions.create(dir);
	// Upstream discovery creates `explore.md` when no agents exist; existing
	// files are never overwritten. The side effect stays under the isolated
	// agent directory.
	await stat(join(home, "agents", "explore.md"));
	const { agents } = discoverAgents(dir, "both", false);
	expect(agents.map((a) => a.name)).toContain("explore");
});
