import { expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import {
	adapterOperationRoute,
	ADAPTER_RUNTIME_REGISTER_EVENT,
	ADAPTER_RUNTIME_SNAPSHOT_EVENT,
	ADAPTER_STATUS_EVENT,
	blockedPiToolsCall,
	emitRuntimeRegister,
	emitRuntimeSnapshot,
	PI_MCP_ADAPTER_TESTED_VERSION,
	PI_TOOLS_CALL_BLOCKER,
	readAdapterStatusSnapshot,
	RETAINED_MCP_OPERATIONS,
	SUPPORTED_ADAPTER_APIS,
	validateMcpName,
} from "../../../assistant/src/extensions/adapter-mcp.ts";

const RETAINED_IDS = [
	"mcp.attach",
	"pi.config.extensions.list",
	"pi.config.extensions.add",
	"pi.config.extensions.set-enabled",
	"pi.config.extensions.remove",
	"pi.session.extensions.list",
	"pi.session.extensions.add",
	"pi.session.extensions.remove",
	"pi.tools.call",
	"adapter.status",
	"adapter.registerBrowser",
	"adapter.session.forget",
];

test("every retained MCP admin operation has a supported route and never a private one", () => {
	expect(RETAINED_MCP_OPERATIONS.map((entry) => entry.id).sort()).toEqual([...RETAINED_IDS].sort());
	expect(PI_MCP_ADAPTER_TESTED_VERSION).toBe("2.32.1");
	for (const entry of RETAINED_MCP_OPERATIONS) {
		expect(entry.note.length).toBeGreaterThan(0);
		if (entry.id === "pi.tools.call") {
			expect(entry.route).toBe("blocked-no-supported-api");
			expect(entry.channel).toBeNull();
		} else if (entry.channel !== null) {
			expect(
				SUPPORTED_ADAPTER_APIS.some((api) => api.channel === entry.channel),
				`${entry.id} must use a supported adapter channel`,
			).toBe(true);
		}
	}
	expect(adapterOperationRoute("mcp.attach").channel).toBe(ADAPTER_RUNTIME_REGISTER_EVENT);
	expect(adapterOperationRoute("pi.session.extensions.list").channel).toBe(
		ADAPTER_RUNTIME_SNAPSHOT_EVENT,
	);
	expect(adapterOperationRoute("adapter.status").channel).toBe(ADAPTER_STATUS_EVENT);
	expect(() => adapterOperationRoute("mcp.unknown")).toThrow("Unknown MCP operation");
});

test("runtime register and snapshot use only the public event bus", async () => {
	const registration = {
		dispose: async () => {},
	};
	const registered = emitRuntimeRegister(
		(channel, request) => {
			expect(channel).toBe(ADAPTER_RUNTIME_REGISTER_EVENT);
			const seen = request as { version: number; name: string; result?: unknown };
			expect(seen.version).toBe(1);
			expect(seen.name).toBe("fixture");
			seen.result = { ok: true, registration };
		},
		"fixture",
		{ url: "http://127.0.0.1:9/mcp" },
	);
	expect(registered).toBe(registration);

	expect(() =>
		emitRuntimeRegister(
			(_channel, request) => {
				(request as { result?: unknown }).result = {
					ok: false,
					error: new Error("name taken"),
				};
			},
			"fixture",
			{ url: "http://127.0.0.1:9/mcp" },
		),
	).toThrow("name taken");
	expect(() => emitRuntimeRegister(() => {}, "fixture", { url: "http://127.0.0.1:9/mcp" })).toThrow(
		"not installed",
	);
	expect(() => emitRuntimeRegister(() => {}, "bad__name", {})).toThrow("Invalid MCP name");
	expect(() => validateMcpName("has spaces")).toThrow("Invalid MCP name");

	const snapshot = emitRuntimeSnapshot(
		(channel, request) => {
			expect(channel).toBe(ADAPTER_RUNTIME_SNAPSHOT_EVENT);
			const seen = request as { version: number; name: string; result?: unknown };
			expect(seen.version).toBe(1);
			seen.result = {
				ok: true,
				snapshot: { name: "fixture", definition: { url: "http://x/mcp" }, runtime: true, persisted: false },
			};
		},
		"fixture",
	);
	expect(snapshot).toMatchObject({ name: "fixture", runtime: true, persisted: false });
	expect(() => emitRuntimeSnapshot(() => {}, "fixture")).toThrow("not installed");
});

test("adapter status snapshots validate without connecting or executing", () => {
	expect(
		readAdapterStatusSnapshot({ version: 1, servers: [{ name: "fixture" }] })?.servers,
	).toHaveLength(1);
	expect(readAdapterStatusSnapshot({ version: 2, servers: [] })).toBeNull();
	expect(readAdapterStatusSnapshot({ version: 1 })).toBeNull();
	expect(readAdapterStatusSnapshot(null)).toBeNull();
});

test("tool execution reports the BRIDGE-02 blocker and never private access", async () => {
	expect(blockedPiToolsCall.length).toBe(0);
	try {
		blockedPiToolsCall();
		expect.unreachable();
	} catch (error) {
		expect(String((error as Error).message)).toContain("BRIDGE-02 blocker");
		expect(String((error as Error).message)).toContain(PI_TOOLS_CALL_BLOCKER.missingPublicSymbol);
	}
	expect(PI_TOOLS_CALL_BLOCKER.route).toBe("blocked-no-supported-api");
	expect(PI_TOOLS_CALL_BLOCKER.owner).toBe("E/B");

	// Regression guard: the public adapter module must not reach the private
	// live-tool surface. The literal below names the forbidden surface only in
	// this test; the adapter source itself must not contain it.
	const source = await readFile(
		new URL("../../../assistant/src/extensions/adapter-mcp.ts", import.meta.url),
		"utf8",
	);
	expect(source).not.toContain("state.tools");
	expect(source).not.toContain("ctx.session");
});
