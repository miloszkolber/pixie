import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import {
	HOST_AVAILABLE_OPERATIONS,
	HOST_OPERATION_STATUS,
	HOST_OPERATIONS,
} from "../../shared/src/generated/protocol-catalog.ts";
import { FakeSession, rawFrames, rawHost, registerHostCleanup } from "./harness.ts";

registerHostCleanup();

describe("Bun host operationSet truthfulness", () => {
	test("advertises every catalog operation with truthful enablement", async () => {
		const raw = rawHost(new FakeSession("truth"), { protocol: "auto" });
		await raw.send({
			id: 1,
			method: "runtime.hello",
			params: { protocolVersion: 1, supportedProtocolVersions: [2, 1] },
		});
		const hello = rawFrames(raw.socket).at(-1)?.result;
		expect(Object.keys(hello?.operationSet ?? {}).sort()).toEqual([...HOST_OPERATIONS].sort());
		// Every advertised bit is derived from the catalog status, except the
		// opt-in restart route which stays false unless explicitly enabled.
		for (const name of HOST_OPERATIONS) {
			const expected =
				name === "runtime.restart" ? false : HOST_OPERATION_STATUS[name] === "available";
			expect(hello?.operationSet?.[name]).toBe(expected);
		}
		for (const name of [
			"session.configure",
			"session.fork",
			"session.clone",
			"session.compact",
			"session.rename",
			"session.commands",
			"session.followUp",
			"session.clearQueue",
			"session.switch",
			"session.getMessages",
			"session.stats",
			"session.prompt.image",
			"pi.sources.list",
			"pi.sources.create",
			"pi.sources.update",
			"pi.sources.delete",
			"pi.agent-mentions.list",
			"pi.mcp.servers.read",
			"pi.mcp.servers.probe",
			"pi.providers.list",
			"pi.providers.config.read",
			"pi.defaults.read",
			"pi.preferences.read",
			"pi.extensions.list",
			"pi.slash-commands.list",
			"provider.loginStart",
		]) {
			expect(hello?.operationSet?.[name]).toBe(true);
		}
		for (const name of [
			"session.delete",
			"session.archive",
			"session.steer",
			"session.prompt.resource",
			"mcp.attach",
			"pi.session.info",
			"pi.session.steer",
			"pi.tools.list",
			"pi.tools.call",
			"runtime.capabilities",
			"pi.subagent.execute",
			"pi.todo.plan",
			"pi.llama",
			"pi.native-extensions",
		]) {
			expect(hello?.operationSet?.[name]).toBe(false);
		}
		// A catalogued-but-unavailable route fails closed with the catalog's
		// stated reason instead of inventing an implementation.
		await raw.send({ id: 5, method: "pi.session.steer", params: {} });
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("public run identifier");
		await raw.send({ id: 2, method: "session.delete", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 2,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
		await raw.send({ id: 3, method: "nope.unknown", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 3,
				error: expect.objectContaining({ code: -32601, reason: "method_not_found" }),
			}),
		);
		await raw.send({ id: 4, method: "pi.tools.list", params: {} });
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({
				id: 4,
				error: expect.objectContaining({ code: -32004, reason: "capability_unavailable" }),
			}),
		);
	});

	test("supports image prompts and fails resource prompts closed", async () => {
		const raw = rawHost(new FakeSession("images"));
		await raw.send({ id: 1, method: "runtime.hello", params: { protocolVersion: 1 } });
		await raw.send({ id: 2, method: "session.create", params: { cwd: process.cwd() } });
		await raw.send({
			id: 3,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [
					{ type: "text", text: "look" },
					{ type: "image", data: "aGVsbG8=", mimeType: "image/png" },
				],
			},
		});
		expect(rawFrames(raw.socket).at(-1)).toEqual(
			expect.objectContaining({ id: 3, result: expect.objectContaining({ stopReason: "stop" }) }),
		);
		await raw.send({
			id: 4,
			method: "session.prompt",
			params: {
				sessionId: "images",
				content: [{ type: "resource", resource: { text: "x" } }],
			},
		});
		expect(rawFrames(raw.socket).at(-1)?.error?.message).toContain("resource");
	});
});

describe("Bun host dispatch coverage", () => {
	// Available catalog operations intentionally exposed without a host dispatch
	// test. Keep this empty unless a catalogued route is deliberately not served.
	const dispatchCoverageAllowlist: readonly string[] = [];

	test("references every available catalog operation from at least one sibling test", () => {
		const testDir = import.meta.dir;
		const testSource = readdirSync(testDir)
			.filter((entry) => entry.endsWith(".test.ts"))
			.map((entry) => readFileSync(join(testDir, entry), "utf8"))
			.join("\n");
		const uncovered = HOST_AVAILABLE_OPERATIONS.filter(
			(operation) =>
				!dispatchCoverageAllowlist.includes(operation) && !testSource.includes(operation),
		);
		expect(uncovered).toEqual([]);
	});
});
