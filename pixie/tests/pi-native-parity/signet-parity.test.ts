import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type AssistantMessage, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { PiConnector } from "@signetai/connector-pi";
import { Sessions } from "../../../pi/pixie-assistant/src/sessions.ts";
import { cleanups, echoProvider, findTool, fixture, tempDir } from "./helpers.ts";

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

// A definitely-closed loopback port: bind, read, and release it so the
// offline tests never depend on ambient port state.
async function deadDaemonUrl(): Promise<string> {
	const server = Bun.serve({ port: 0, hostname: "127.0.0.1", fetch: () => new Response("gone") });
	const port = server.port;
	server.stop();
	return `http://127.0.0.1:${port}`;
}

async function freshAgentDir(): Promise<{ dir: string; configHome: string; cwd: string }> {
	const dir = await tempDir("pixie-signet-agent-");
	const configHome = await tempDir("pixie-signet-config-");
	const cwd = await tempDir("pixie-signet-cwd-");
	return { dir, configHome, cwd };
}

// Install the upstream-managed file extension into a temp agent directory.
// `PiConnector.install` is upstream code used unchanged; XDG_CONFIG_HOME and
// PI_CODING_AGENT_DIR containment keeps every side effect under temp dirs.
async function installManaged(dir: string, configHome: string): Promise<void> {
	setEnv("PI_CODING_AGENT_DIR", dir);
	setEnv("XDG_CONFIG_HOME", configHome);
	await new PiConnector().install("");
}

function silenceWarnings(): () => void {
	const warn = console.warn;
	console.warn = () => {};
	return () => {
		console.warn = warn;
	};
}

const SIGNET_TOOLS = [
	"signet_recall",
	"signet_source_search",
	"signet_session_search",
	"signet_remember",
];

test("absent Signet has no marker, tools or MCP", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const entry = await sessions.create(dir);
	expect(entry.capabilities.snapshot().signet).toBeUndefined();
	expect(entry.capabilities.snapshot().mcp).toBeUndefined();
	for (const name of SIGNET_TOOLS) {
		expect(entry.session.getActiveToolNames()).not.toContain(name);
	}
});

test("installed Signet registers the four memory tools without MCP", async () => {
	const { dir, configHome, cwd } = await freshAgentDir();
	setEnv("SIGNET_DAEMON_URL", await deadDaemonUrl());
	const restore = silenceWarnings();
	try {
		await installManaged(dir, configHome);
		const sessions = new Sessions(dir, [echoProvider()], () => {});
		cleanups.push(() => sessions.close());
		const entry = await sessions.create(cwd);
		for (const name of SIGNET_TOOLS) {
			expect(entry.session.getActiveToolNames()).toContain(name);
		}
		expect(
			sessions
				.inventory(entry)
				.extensions.some((extension) => extension.tools.includes("signet_recall")),
		).toBe(true);
		expect(entry.capabilities.snapshot().mcp).toBeUndefined();
		const commands = sessions.commands(entry).map((c) => String(c.name));
		expect(commands).toEqual(expect.arrayContaining(["recall", "remember", "signet-status"]));
	} finally {
		restore();
	}
});

test("an unreachable daemon stays fail-open on attach and every tool", async () => {
	const { dir, configHome, cwd } = await freshAgentDir();
	setEnv("SIGNET_DAEMON_URL", await deadDaemonUrl());
	const restore = silenceWarnings();
	try {
		await installManaged(dir, configHome);
		const sessions = new Sessions(dir, [echoProvider()], () => {});
		cleanups.push(() => sessions.close());
		// Session attach succeeds even though the daemon refuses connections.
		const entry = await sessions.create(cwd);
		const echo = entry.modelRuntime.getModel("fixture", "echo");
		if (!echo) throw new Error("Echo model unavailable");
		await entry.session.setModel(echo);
		for (const [tool, params] of [
			["signet_recall", { query: "past decisions" }],
			["signet_source_search", { query: "imported notes" }],
			["signet_session_search", { query: "prior sessions" }],
			["signet_remember", { content: "remember this" }],
		] as const) {
			const found = findTool(entry, tool);
			const result = await found.execute(
				`parity-signet-offline-${tool}`,
				params,
				new AbortController().signal,
			);
			expect(String((result.content[0] as { text: string }).text)).toMatch(/daemon not running/i);
			expect(result.details).toMatchObject({ error: "daemon_offline" });
		}
		// Ordinary prompting still works while memory is offline.
		await sessions.call("session.prompt", {
			sessionId: entry.session.sessionId,
			content: [{ type: "text", text: "Hello offline" }],
		});
		const snapshot = sessions.snapshot(entry, true);
		expect(JSON.stringify(snapshot.messages)).not.toContain("signet-memory");
	} finally {
		restore();
	}
});

const SESSION_MARKER = "parity-session-context-aurora";
const RECALL_MARKER = "parity-recall-inject-borealis";
const MEMORY_MARKER = "parity-recalled-memory-cassiopeia";

function startFakeDaemon(): { url: string; seen: string[] } {
	const seen: string[] = [];
	const server = Bun.serve({
		port: 0,
		hostname: "127.0.0.1",
		async fetch(request) {
			const path = new URL(request.url).pathname.replaceAll("//", "/");
			seen.push(`${request.method} ${path}`);
			if (request.method === "GET" && path === "/health") return new Response("ok");
			if (request.method === "POST" && path === "/api/hooks/session-start")
				return Response.json({ inject: SESSION_MARKER });
			if (request.method === "POST" && path === "/api/hooks/user-prompt-submit")
				return Response.json({ sessionKnown: true, inject: RECALL_MARKER });
			if (request.method === "POST" && path === "/api/hooks/session-end") return Response.json({});
			if (request.method === "POST" && path === "/api/memory/recall") {
				await request.text();
				return Response.json({ results: [{ content: MEMORY_MARKER }] });
			}
			if (request.method === "POST" && path === "/api/hooks/remember")
				return Response.json({ saved: true });
			return new Response("unexpected", { status: 500 });
		},
	});
	cleanups.push(() => server.stop());
	return { url: `http://127.0.0.1:${server.port}`, seen };
}

// Capturing echo provider: records the exact LLM context so the test can
// prove hidden auto-recall reached the model.
function capturingProvider(captured: unknown[]): ExtensionFactory {
	return (pi) => {
		pi.registerProvider("fixture", {
			baseUrl: "http://localhost/unused",
			apiKey: "fixture-only-key",
			api: "fixture-api",
			models: [
				{
					id: "echo",
					name: "Echo",
					reasoning: false,
					input: ["text", "image"],
					cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
					contextWindow: 64000,
					maxTokens: 1024,
				},
			],
			streamSimple: (_model, context) => {
				captured.push(context);
				const stream = createAssistantMessageEventStream();
				const message: AssistantMessage = {
					role: "assistant",
					api: "fixture-api",
					provider: "fixture",
					model: "echo",
					content: [{ type: "text", text: "Hello from Pi" }],
					stopReason: "stop",
					timestamp: Date.now(),
					usage: {
						input: 5,
						output: 4,
						cacheRead: 0,
						cacheWrite: 0,
						totalTokens: 9,
						cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
					},
				};
				queueMicrotask(() => {
					stream.push({ type: "done", reason: "stop", message });
					stream.end(message);
				});
				return stream;
			},
		});
	};
}

test("auto-recall reaches the model hidden and stays out of the transcript", async () => {
	const { dir, configHome, cwd } = await freshAgentDir();
	const daemon = startFakeDaemon();
	setEnv("SIGNET_DAEMON_URL", daemon.url);
	await installManaged(dir, configHome);
	const captured: unknown[] = [];
	const sessions = new Sessions(dir, [capturingProvider(captured)], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const echo = entry.modelRuntime.getModel("fixture", "echo");
	if (!echo) throw new Error("Echo model unavailable");
	await entry.session.setModel(echo);
	expect(daemon.seen).toContain("POST /api/hooks/session-start");
	await sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content: [{ type: "text", text: "What did we decide?" }],
	});
	expect(daemon.seen).toContain("POST /api/hooks/user-prompt-submit");
	// The injected context reached the model inside hidden custom messages.
	const contextText = JSON.stringify(captured);
	expect(contextText).toContain("signet-memory");
	expect(contextText).toContain(SESSION_MARKER);
	expect(contextText).toContain(RECALL_MARKER);
	// ...but the projected transcript shows none of it.
	const snapshot = sessions.snapshot(entry, true);
	const transcript = JSON.stringify(snapshot.messages);
	expect(transcript).not.toContain("signet-memory");
	expect(transcript).not.toContain(SESSION_MARKER);
	expect(transcript).not.toContain(RECALL_MARKER);
});

test("online recall and remember round-trip through the daemon", async () => {
	const { dir, configHome, cwd } = await freshAgentDir();
	const daemon = startFakeDaemon();
	setEnv("SIGNET_DAEMON_URL", daemon.url);
	await installManaged(dir, configHome);
	const sessions = new Sessions(dir, [echoProvider()], () => {});
	cleanups.push(() => sessions.close());
	const entry = await sessions.create(cwd);
	const signal = new AbortController().signal;
	const recall = findTool(entry, "signet_recall");
	const recalled = await recall.execute(
		"parity-signet-online-recall",
		{ query: "decisions" },
		signal,
	);
	expect(String((recalled.content[0] as { text: string }).text)).toContain(MEMORY_MARKER);
	expect(recalled.details).toMatchObject({ memoriesFound: 1 });
	const remember = findTool(entry, "signet_remember");
	const saved = await remember.execute(
		"parity-signet-online-remember",
		{ content: "a durable fact" },
		signal,
	);
	expect(saved.details).toMatchObject({ saved: true });
	expect(daemon.seen).toContain("POST /api/memory/recall");
	expect(daemon.seen).toContain("POST /api/hooks/remember");
});

test("installing twice is idempotent and contained to the agent directory", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-signet-idem-`);
	cleanups.push(() => rm(dir, { recursive: true, force: true }));
	const configHome = await mkdtemp(`${tmpdir()}/pixie-signet-idem-config-`);
	cleanups.push(() => rm(configHome, { recursive: true, force: true }));
	setEnv("PI_CODING_AGENT_DIR", dir);
	setEnv("XDG_CONFIG_HOME", configHome);
	const first = await new PiConnector().install("");
	const second = await new PiConnector().install("");
	expect(first.success).toBe(true);
	expect(second.message).toMatch(/up to date/);
	expect(second.filesWritten).toHaveLength(0);
	await stat(join(dir, "extensions", "signet-pi.js"));
	await stat(join(configHome, "signet", "pi.json"));
});
