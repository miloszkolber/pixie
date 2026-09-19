import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { createHostLogger } from "../../src/assistant/log.ts";
import {
	credential,
	expectSecretFree,
	FakeSession,
	type HostFrame,
	helloV2,
	operatorPath,
	operatorUrl,
	rawFrames,
	rawHost,
	registerHostCleanup,
	secret,
	tempAgentDir,
	waitForId,
} from "./harness.ts";

registerHostCleanup();

describe("Bun host session parity dispatch", () => {
	test("projects the native model and thinking state for create, configure, load, and fork", async () => {
		const models = [
			{ id: "alpha", provider: "test", name: "Alpha" },
			{ id: "beta", provider: "test", name: "Beta" },
			{ id: "gamma", provider: "other", name: "Gamma" },
		];
		const configuredSession = (id: string, modelId: string, provider: string, thinking: string) => {
			const session = new FakeSession(id);
			session.model = models.find((model) => model.id === modelId && model.provider === provider);
			session.thinkingLevel = thinking;
			session.thinkingLevels = ["off", "low", "medium", "high"];
			session.modelRuntime = {
				getModels: (wantedProvider?: string) =>
					wantedProvider ? models.filter((model) => model.provider === wantedProvider) : models,
				getModel: (wantedProvider: string, wantedModel: string) =>
					models.find((model) => model.provider === wantedProvider && model.id === wantedModel),
			};
			return session;
		};
		const created = configuredSession("created", "alpha", "test", "medium");
		created.sessionFile = "/sessions/created.jsonl";
		const loaded = configuredSession("loaded", "gamma", "other", "low");
		loaded.sessionFile = "/sessions/loaded.jsonl";
		const forked = configuredSession("forked", "beta", "test", "high");
		forked.sessionFile = "/sessions/forked.jsonl";
		const raw = rawHost(created, {
			sdk: {
				SessionManager: {
					create: () => ({ kind: "create" }),
					list: async (cwd: string) => [{ id: "loaded", path: loaded.sessionFile ?? "", cwd }],
					open: () => ({ kind: "open" }),
					forkFrom: () => ({ kind: "fork" }),
				},
			},
			sessionFactory: async ({ sessionManager }) => {
				const kind = (sessionManager as { kind?: string })?.kind;
				if (kind === "open") return { session: loaded };
				if (kind === "fork") return { session: forked };
				return { session: created };
			},
		});
		const projection = (frame: HostFrame): Record<string, unknown> => {
			if (!frame.result) throw new Error("session result is missing");
			return frame.result as Record<string, unknown>;
		};
		const expectProjection = (
			frame: HostFrame,
			provider: string,
			model: string,
			thinking: string,
		) => {
			const result = projection(frame);
			expect(result.metadata).toMatchObject({
				providerId: provider,
				modelId: model,
				thinkingLevel: thinking,
			});
			expect(result.configOptions).toEqual(
				expect.arrayContaining([
					expect.objectContaining({ id: "provider", type: "select", currentValue: provider }),
					expect.objectContaining({ id: "model", type: "select", currentValue: model }),
					expect.objectContaining({ id: "thinking", type: "select", currentValue: thinking }),
				]),
			);
		};

		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "alpha", "medium");
		await raw.send({
			id: 3,
			method: "session.configure",
			params: { sessionId: "created", configId: "model", value: "beta", provider: "test" },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "medium");
		await raw.send({
			id: 4,
			method: "session.configure",
			params: { sessionId: "created", configId: "thinking", value: "high" },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "high");
		await raw.send({
			id: 5,
			method: "session.release",
			params: { sessionId: "created", cwd: process.cwd() },
		});
		await raw.send({
			id: 6,
			method: "session.load",
			params: { sessionId: "loaded", cwd: process.cwd() },
		});
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "other", "gamma", "low");
		await raw.send({ id: 7, method: "session.fork", params: { sessionId: "loaded" } });
		expectProjection(rawFrames(raw.socket).at(-1) as HostFrame, "test", "beta", "high");
	});

	// AUX-14: the SDK AgentMessage has no stable id, so the host must project the
	// native session-entry id from the SDK's own fork selector onto user messages.
	// It must survive both the create snapshot and session.getMessages, and must
	// never be attached to a non-user message.
	test("projects the native session-entry id for user messages only", async () => {
		const session = new FakeSession("entries");
		session.messages.push(
			{ role: "user", content: "branch me", messageId: "u1", timestamp: 1 },
			{ role: "assistant", content: [{ type: "text", text: "ok" }], messageId: "a1" },
		);
		session.userMessagesForForking = [{ entryId: "entry-42", text: "branch me" }];
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const created = rawFrames(raw.socket).at(-1)?.result as {
			messages?: Array<Record<string, unknown>>;
		};
		expect(created.messages?.[0]?.entryId).toBe("entry-42");
		expect(created.messages?.[1]?.entryId).toBeUndefined();
		await raw.send({ id: 3, method: "session.getMessages", params: { sessionId: "entries" } });
		const fetched = rawFrames(raw.socket).at(-1)?.result as {
			messages?: Array<Record<string, unknown>>;
		};
		expect(fetched.messages?.[0]?.entryId).toBe("entry-42");
	});

	test("configures thinking and model, then serves stats/messages/commands/queues", async () => {
		const session = new FakeSession("parity");
		session.modelRuntime = {
			getModels: (provider: string) =>
				provider === "test" ? [{ id: "m1", provider: "test" }] : [],
			getModel: (provider: string, model: string) =>
				provider === "test" && model === "m1" ? { id: "m1", provider: "test" } : undefined,
			getAvailable: async () => [{ id: "m1", provider: "test" }],
		};
		const raw = rawHost(session);
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.configure",
			params: { sessionId: "parity", configId: "thinking", value: "high" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		expect(session.thinkingLevel).toBe("high");
		await raw.send({
			id: 4,
			method: "session.configure",
			params: { sessionId: "parity", configId: "model", value: "m1", provider: "test" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		expect(session.model).toEqual({ id: "m1", provider: "test" });
		await raw.send({
			id: 5,
			method: "session.rename",
			params: { sessionId: "parity", name: "new" },
		});
		expect(session.sessionName).toBe("new");
		await raw.send({
			id: 6,
			method: "session.compact",
			params: { sessionId: "parity", customInstructions: "short" },
		});
		expect(session.compactInstructions).toBe("short");
		await raw.send({ id: 7, method: "session.stats", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ sessionId: "parity" });
		await raw.send({ id: 8, method: "session.getMessages", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ messages: [] });
		await raw.send({ id: 9, method: "session.commands", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toEqual(
			expect.objectContaining({ commands: expect.any(Array) }),
		);
		await raw.send({
			id: 10,
			method: "session.steer",
			params: { sessionId: "parity", content: [{ type: "text", text: "steer me" }] },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("run identifier");
		expect(session.steered).toEqual([]);
		await raw.send({
			id: 11,
			method: "session.followUp",
			params: { sessionId: "parity", message: "later" },
		});
		expect(session.followed.at(-1)?.text).toBe("later");
		await raw.send({ id: 12, method: "session.clearQueue", params: { sessionId: "parity" } });
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		await raw.send({
			id: 13,
			method: "session.switch",
			params: { sessionId: "parity" },
		});
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ sessionId: "parity" });
	});

	test("branches via the public SessionManager file layer", async () => {
		const agentDir = tempAgentDir();
		const sessionDir = join(agentDir, "sessions");
		const calls: string[] = [];
		const sdk = {
			getDefaultSessionDir: () => sessionDir,
			SessionManager: {
				create: () => ({ kind: "create" }),
				list: async () => [],
				open: (path: string) => ({
					kind: "open",
					path,
					getEntry: (id: string) => (id === "e1" ? { id: "e1" } : undefined),
					createBranchedSession: (leaf: string) => {
						calls.push(`branch:${leaf}`);
						return undefined;
					},
				}),
				forkFrom: (source: string, target: string) => {
					calls.push(`fork:${source}:${target}`);
					return { kind: "forked" };
				},
			},
		};
		let next = 0;
		const raw = rawHost(new FakeSession("unused"), {
			agentDir,
			sdk,
			sessionFactory: async () => {
				next += 1;
				const session = new FakeSession(next === 1 ? "parent" : `branch-${next}`);
				session.sessionFile = join(sessionDir, `${session.sessionId}.jsonl`);
				return { session };
			},
		});
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		const parent = rawFrames(raw.socket).at(-1)?.result as unknown as { sessionId: string };
		expect(parent.sessionId).toBe("parent");
		await raw.send({ id: 3, method: "session.clone", params: { sessionId: "parent" } });
		expect(await waitForId(raw.socket, 3)).toMatchObject({
			result: expect.objectContaining({ sessionId: "branch-2" }),
		});
		expect(calls.some((call) => call.startsWith("fork:"))).toBe(true);
	});

	test("rejects create-time overrides instead of silently ignoring them", async () => {
		const raw = rawHost(new FakeSession("overrides"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "session.create",
			params: { cwd: process.cwd(), provider: "test" },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("create-time");
	});
});

describe("Bun host filesystem parity", () => {
	test("authors agent sources without touching unrelated files", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("sources"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.sources.create",
			params: { name: "helper", description: "helps", content: "body" },
		});
		const created = rawFrames(raw.socket).at(-1)?.result as unknown as {
			source: { path: string; revision: string; name: string };
		};
		expect(created.source.name).toBe("helper");
		expect(existsSync(created.source.path)).toBe(true);
		await raw.send({ id: 3, method: "pi.sources.list", params: {} });
		const listed = rawFrames(raw.socket).at(-1)?.result as unknown as {
			sources: Array<{ name: string }>;
		};
		expect(listed.sources.map((source) => source.name)).toContain("helper");
		await raw.send({ id: 4, method: "pi.agent-mentions.list", params: {} });
		const mentions = rawFrames(raw.socket).at(-1)?.result as unknown as {
			agents: Array<{ mention: string }>;
		};
		expect(mentions.agents.map((agent) => agent.mention)).toContain("@helper");
		await raw.send({
			id: 5,
			method: "pi.sources.update",
			params: {
				path: created.source.path,
				name: "helper",
				description: "helps more",
				content: "body2",
				expectedRevision: created.source.revision,
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		await raw.send({
			id: 6,
			method: "pi.sources.delete",
			params: {
				path: created.source.path,
				expectedRevision: (
					rawFrames(raw.socket).at(-1)?.result as unknown as {
						source: { revision: string };
					}
				).source.revision,
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ ok: true });
		expect(existsSync(created.source.path)).toBe(false);
	});

	test("merges MCP layers for reads and writes only the agent layer", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.mcp.servers.upsert",
			params: { name: "alpha", definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(Object.keys(stored.mcpServers)).toEqual(["alpha"]);
		await raw.send({ id: 3, method: "pi.mcp.servers.read", params: {} });
		const read = rawFrames(raw.socket).at(-1)?.result as unknown as {
			servers: Array<{ name: string; layer: string }>;
		};
		expect(read.servers.map((server) => server.name)).toContain("alpha");
		expect(read.servers.find((server) => server.name === "alpha")?.layer).toBe("agent-dir");
		await raw.send({
			id: 4,
			method: "pi.mcp.servers.probe",
			params: { definition: { url: "http://127.0.0.1:1/mcp" } },
		});
		const probe = (await waitForId(raw.socket, 4)).result as unknown as { reachable: boolean };
		expect(probe.reachable).toBe(false);
		await raw.send({
			id: 5,
			method: "pi.mcp.servers.probe",
			params: { definition: {} },
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toContain("required");
		await raw.send({ id: 6, method: "pi.mcp.servers.remove", params: { name: "alpha" } });
		expect(rawFrames(raw.socket).at(-1)?.result).toMatchObject({ ok: true });
	});

	test("rejects non-ASCII MCP server names and accepts the ASCII length boundary", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp-name"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({
			id: 2,
			method: "pi.mcp.servers.upsert",
			params: { name: "bröwser", definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("invalid MCP server name");
		expect(existsSync(join(agentDir, "mcp.json"))).toBe(false);

		const name = "a".repeat(128);
		await raw.send({
			id: 3,
			method: "pi.mcp.servers.upsert",
			params: { name, definition: { url: "http://127.0.0.1:9/mcp" } },
		});
		expect(rawFrames(raw.socket).at(-1)?.error).toBeUndefined();
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(stored.mcpServers[name]).toEqual({ url: "http://127.0.0.1:9/mcp" });
	});

	test("preserves concurrent MCP upserts without lost updates", async () => {
		const agentDir = tempAgentDir();
		const raw = rawHost(new FakeSession("mcp-race"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await Promise.all([
			raw.send({
				id: 2,
				method: "pi.mcp.servers.upsert",
				params: { name: "alpha", definition: { url: "http://127.0.0.1:9/mcp" } },
			}),
			raw.send({
				id: 3,
				method: "pi.mcp.servers.upsert",
				params: { name: "beta", definition: { url: "http://127.0.0.1:10/mcp" } },
			}),
		]);
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();
		expect((await waitForId(raw.socket, 3)).error).toBeUndefined();
		await raw.send({ id: 4, method: "pi.mcp.servers.read", params: {} });
		const read = (await waitForId(raw.socket, 4)).result as unknown as {
			servers: Array<{ name: string }>;
		};
		expect(read.servers.map((server) => server.name).sort()).toEqual(["alpha", "beta"]);
		const stored = JSON.parse(readFileSync(join(agentDir, "mcp.json"), "utf8")) as {
			mcpServers: Record<string, unknown>;
		};
		expect(Object.keys(stored.mcpServers).sort()).toEqual(["alpha", "beta"]);
	});

	test("conditionally rejects forged, colliding, and changed Browser MCP ownership", async () => {
		const agentDir = tempAgentDir();
		const projectDir = tempAgentDir();
		const expectedToken = "pixie-owned-token";
		const raceToken = "token-observed-before-race";
		const agentDocument = {
			mcpServers: {
				static: {
					url: "https://third-party.example/mcp?secret=never-returned",
					headers: { Authorization: "Bearer never-returned" },
					_pixieManagedBy: "browser-mcp/v1",
				},
				random: {
					url: "https://third-party.example/random",
					_pixieManagedBy: "browser-mcp/v2",
					_pixieOwnershipToken: "forged-random-token",
				},
				race: {
					url: "https://browser.example/original",
					_pixieManagedBy: "browser-mcp/v2",
					_pixieOwnershipToken: raceToken,
				},
			},
		};
		writeFileSync(join(agentDir, "mcp.json"), JSON.stringify(agentDocument));
		writeFileSync(
			join(projectDir, ".mcp.json"),
			JSON.stringify({
				mcpServers: {
					lower: {
						url: "https://third-party.example/lower",
						headers: { Authorization: "Bearer lower-secret" },
					},
				},
			}),
		);
		const raw = rawHost(new FakeSession("mcp-conditional"), { agentDir });
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });

		await raw.send({ id: 2, method: "pi.mcp.servers.read", params: { name: "static" } });
		const inspected = (await waitForId(raw.socket, 2)).result as unknown as {
			servers: Array<Record<string, unknown>>;
		};
		expect(inspected.servers).toEqual([
			{
				layer: "agent-dir",
				path: join(agentDir, "mcp.json"),
				disabled: false,
				markers: { _pixieManagedBy: "browser-mcp/v1" },
			},
		]);
		expect(JSON.stringify(inspected)).not.toContain("never-returned");

		const staticBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 3,
			method: "pi.mcp.servers.upsert",
			params: {
				name: "static",
				definition: { url: "http://127.0.0.1:3000/mcp" },
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 3)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(staticBefore);

		const randomBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 4,
			method: "pi.mcp.servers.remove",
			params: {
				name: "random",
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 4)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(randomBefore);

		const lowerBefore = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 5,
			method: "pi.mcp.servers.upsert",
			params: {
				name: "lower",
				projectDir,
				definition: { url: "http://127.0.0.1:3000/mcp" },
				expectedAgentOwnershipToken: expectedToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 5)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(lowerBefore);

		await raw.send({ id: 6, method: "pi.mcp.servers.read", params: { name: "race" } });
		expect((await waitForId(raw.socket, 6)).error).toBeUndefined();
		const changedDocument = structuredClone(agentDocument);
		changedDocument.mcpServers.race._pixieOwnershipToken = "changed-during-race";
		writeFileSync(join(agentDir, "mcp.json"), JSON.stringify(changedDocument));
		const changedBeforeRemove = readFileSync(join(agentDir, "mcp.json"));
		await raw.send({
			id: 7,
			method: "pi.mcp.servers.remove",
			params: {
				name: "race",
				expectedAgentOwnershipToken: raceToken,
				requireNoOtherLayerCollisions: true,
			},
		});
		expect((await waitForId(raw.socket, 7)).error?.message).toBe("MCP ownership conflict");
		expect(readFileSync(join(agentDir, "mcp.json"))).toEqual(changedBeforeRemove);
	});
});

describe("Bun host runtime release", () => {
	test("releases a resident session through runtime.release and rejects malformed identity", async () => {
		const logger = createHostLogger({ secrets: [credential] });
		const session = new FakeSession("release");
		const raw = rawHost(session, { logger, protocol: "auto" });
		const forbidden = [credential, operatorPath, operatorUrl, raw.agentDir, secret];
		const hello = await helloV2(raw);
		expect(hello.result?.operationSet?.["runtime.release"]).toBe(true);

		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		expect((await waitForId(raw.socket, 2)).error).toBeUndefined();

		await raw.send({
			id: 3,
			method: "runtime.release",
			params: { sessionId: "release", cwd: raw.agentDir },
		});
		const mismatch = await waitForId(raw.socket, 3);
		expect(mismatch.error?.message).toContain("does not match");
		expect(session.disposed).toBe(false);
		expectSecretFree(mismatch, logger, forbidden);

		await raw.send({ id: 4, method: "runtime.release", params: { sessionId: "release" } });
		const missingCwd = await waitForId(raw.socket, 4);
		expect(missingCwd.error).toMatchObject({ code: -32000, reason: "internal" });
		expect(missingCwd.error?.message).toContain("cwd");
		expectSecretFree(missingCwd, logger, forbidden);

		await raw.send({ id: 5, method: "runtime.release", params: { cwd: process.cwd() } });
		const missingId = await waitForId(raw.socket, 5);
		expect(missingId.error?.message).toContain("sessionId");
		expectSecretFree(missingId, logger, forbidden);

		await raw.send({
			id: 6,
			method: "runtime.release",
			params: { sessionId: credential, cwd: process.cwd() },
		});
		const unknown = await waitForId(raw.socket, 6);
		expect(unknown.error?.message).toContain("not loaded");
		expectSecretFree(unknown, logger, forbidden);

		await raw.send({
			id: 7,
			method: "runtime.release",
			params: { sessionId: "release", cwd: process.cwd() },
		});
		const released = await waitForId(raw.socket, 7);
		expect(released).toMatchObject({ id: 7, result: { ok: true } });
		expect(session.disposed).toBe(true);
		expectSecretFree(released, logger, forbidden);

		await raw.send({
			id: 8,
			method: "runtime.release",
			params: { sessionId: "release", cwd: process.cwd() },
		});
		const gone = await waitForId(raw.socket, 8);
		expect(gone.error?.message).toContain("not loaded");
		expectSecretFree(gone, logger, forbidden);
	});
});
