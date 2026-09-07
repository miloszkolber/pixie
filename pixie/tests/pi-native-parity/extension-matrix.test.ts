import { afterEach, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { PiConnector } from "@signetai/connector-pi";
import llama from "../../../agent/pixie-assistant/src/extensions/llama.ts";
import { startHost } from "../../../agent/pixie-assistant/src/server.ts";
import { Sessions } from "../../../agent/pixie-assistant/src/sessions.ts";
import { cleanups, echoProvider, fixture, tempDir, toolNames } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

const savedEnv = new Map<string, string | undefined>();
function setEnv(name: string, value: string | undefined): void {
	if (!savedEnv.has(name)) savedEnv.set(name, process.env[name]);
	if (value === undefined) delete process.env[name];
	else process.env[name] = value;
}
function restoreEnv(): void {
	for (const [name, value] of savedEnv) {
		if (value === undefined) delete process.env[name];
		else process.env[name] = value;
	}
	savedEnv.clear();
}

const optionalPackages = [
	"pi-mcp-adapter",
	"@juicesharp/rpiv-todo",
	"@juicesharp/rpiv-web-tools",
	"@juicesharp/rpiv-ask-user-question",
	"@mjakl/pi-subagent",
];
async function configurePackages(dir: string) {
	const require = createRequire(import.meta.url);
	await writeFile(
		join(dir, "settings.json"),
		JSON.stringify({
			packages: optionalPackages.map((name) => {
				let path = dirname(require.resolve(name));
				// Native local package sources resolve their pi manifest themselves.
				while (!existsSync(join(path, "package.json"))) {
					const parent = dirname(path);
					if (parent === path) throw new Error(`Package manifest not found: ${name}`);
					path = parent;
				}
				return path;
			}),
		}),
	);
}

test("host loads native configured optional packages once without package markers", async () => {
	const dir = await tempDir("pixie-pi-parity-host-");
	const secret = "parity-matrix-secret";
	await configurePackages(dir);
	setEnv("PI_CODING_AGENT_DIR", dir);
	try {
		const host = await startHost({ agentDir: dir, secret, port: 0 });
		try {
			const base = `http://127.0.0.1:${host.server.port}`;
			const ready = (await (
				await fetch(`${base}/readyz`, { headers: { Authorization: `Bearer ${secret}` } })
			).json()) as {
				protocolVersion: number;
				runtimeId: string;
				capabilities: Record<string, number>;
			};
			expect(ready.protocolVersion).toBe(1);
			expect(ready.runtimeId).toBeString();
			expect(ready.capabilities).toEqual({ sessions: 1, providers: 1, agents: 1, mcp: 1 });
			const inventory = host.sessions.inventory(host.control);
			expect(inventory.errors).toEqual([]);
			for (const name of [
				"mcp",
				"todo",
				"web_fetch",
				"web_search",
				"ask_user_question",
				"subagent",
			]) {
				expect(
					inventory.extensions
						.flatMap((extension) => extension.tools)
						.filter((tool) => tool === name),
				).toHaveLength(1);
			}
			expect(ready.capabilities.plans).toBeUndefined();
		} finally {
			await host.close();
		}
	} finally {
		restoreEnv();
	}
});

test("llama profile loads the SDK built-in and registers the provider", async () => {
	const dir = await tempDir("pixie-pi-parity-llama-");
	const secret = "parity-llama-secret";
	const host = await startHost({ agentDir: dir, secret, port: 0, llama: true });
	try {
		const base = `http://127.0.0.1:${host.server.port}`;
		const ready = (await (
			await fetch(`${base}/readyz`, { headers: { Authorization: `Bearer ${secret}` } })
		).json()) as { capabilities: Record<string, number> };
		expect(ready.capabilities.llama).toBe(1);
	} finally {
		await host.close();
	}
	const { sessions } = await fixture([llama]);
	const entry = await sessions.create(dir);
	expect(entry.modelRuntime.getProviders().map((p) => p.id)).toContain("llama.cpp");
	expect(entry.capabilities.snapshot()).toMatchObject({ llama: 1 });
});

test("baseline has no optional markers or model tools", async () => {
	const dir = await tempDir("pixie-pi-parity-markers-");
	const secret = "parity-matrix-secret";
	const host = await startHost({
		agentDir: dir,
		secret,
		port: 0,
	});
	try {
		const base = `http://127.0.0.1:${host.server.port}`;
		const ready = (await (
			await fetch(`${base}/readyz`, { headers: { Authorization: `Bearer ${secret}` } })
		).json()) as { capabilities: Record<string, number> };
		expect(ready.capabilities).toEqual({ sessions: 1, providers: 1, agents: 1 });
		expect(host.control.session.getActiveToolNames()).toEqual(["read", "bash", "edit", "write"]);
		expect(host.control.modelRuntime.getProviders().map((provider) => provider.id)).toContain(
			"openai",
		);
	} finally {
		await host.close();
	}
});

test("native optional packages and managed Signet preserve core tools", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	await configurePackages(dir);
	// Contain the upstream install and discovery side effects (managed file,
	// Pi config, starter agent) to the fixture directory.
	const configHome = await tempDir("pixie-pi-parity-signet-config-");
	setEnv("PI_CODING_AGENT_DIR", dir);
	setEnv("XDG_CONFIG_HOME", configHome);
	const warn = console.warn;
	console.warn = () => {};
	try {
		await new PiConnector().install("");
		const entry = await sessions.create(dir);
		const names = toolNames(entry);
		expect(names).toEqual(
			expect.arrayContaining([
				"read",
				"bash",
				"edit",
				"write",
				"todo",
				"web_fetch",
				"web_search",
				"ask_user_question",
				"subagent",
				"signet_recall",
				"signet_source_search",
				"signet_session_search",
				"signet_remember",
			]),
		);
		expect(entry.capabilities.snapshot()).toEqual({ agents: 1, mcp: 1 });
		expect(new Set(names).size).toBe(names.length);
	} finally {
		console.warn = warn;
		restoreEnv();
	}
});

test("agent definitions survive CRUD through pi.sources operations", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const entry = await sessions.create(dir);
	const ctx = sessions.context(entry);
	await entry.capabilities.call(
		"pi.sources.create",
		{ name: "Reviewer", description: "Review", content: "Review carefully", properties: {} },
		ctx,
	);
	expect(await entry.capabilities.call("pi.sources.list", {}, ctx)).toMatchObject({
		sources: [{ name: "Reviewer" }],
	});
	expect(
		((await entry.capabilities.call("pi.agent-mentions.list", {}, ctx)) as any).agents,
	).toMatchObject([{ mention: "@Reviewer" }]);
	await entry.capabilities.call(
		"pi.sources.update",
		{
			name: "Reviewer",
			description: "Review twice",
			content: "Review carefully",
			properties: {},
			path: ((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources[0].path,
		},
		ctx,
	);
	await entry.capabilities.call(
		"pi.sources.delete",
		{ path: ((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources[0].path },
		ctx,
	);
	expect(((await entry.capabilities.call("pi.sources.list", {}, ctx)) as any).sources).toHaveLength(
		0,
	);
});

test("prompt, cancel, usage, and transcript survive session switching", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const first = await sessions.create(dir);
	const second = await sessions.create(dir);
	for (const entry of [first, second]) {
		await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	}
	await sessions.call("session.prompt", {
		sessionId: first.session.sessionId,
		content: [{ type: "text", text: "Hello first" }],
	});
	await sessions.call("session.prompt", {
		sessionId: second.session.sessionId,
		content: [{ type: "text", text: "Hello second" }],
	});
	const reloaded = new Sessions(dir, [echoProvider()], () => {});
	cleanups.push(() => reloaded.close());
	for (const entry of [first, second]) {
		const loaded = await reloaded.get(entry.session.sessionId);
		const snapshot = reloaded.snapshot(loaded, true);
		expect((snapshot.messages as any[]).filter((m) => m.role === "assistant")).toHaveLength(1);
	}
	expect(first.session.sessionId).not.toBe(second.session.sessionId);
	await sessions.call("session.cancel", { sessionId: first.session.sessionId });
});
