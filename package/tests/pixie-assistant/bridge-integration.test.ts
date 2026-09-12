import { expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import {
	CAPABILITY_EVENT,
	type Capability,
	type CapabilityContext,
} from "../../../assistant/src/capabilities.ts";
import {
	ADAPTER_RUNTIME_REGISTER_EVENT,
	ADAPTER_RUNTIME_SNAPSHOT_EVENT,
	ADAPTER_STATUS_EVENT,
} from "../../../assistant/src/extensions/adapter-mcp.ts";
import { mcpAdminBridgeWithConfig } from "../../../assistant/src/extensions/mcp-admin-bridge.ts";
import { mcpConnectionsBridge } from "../../../assistant/src/extensions/mcp-connections.ts";
import {
	createUiBridge,
	UI_CANCEL_EVENT,
	UI_REQUEST_EVENT,
	UI_WORKING_EVENT,
} from "../../../assistant/src/extensions/ui-bridge.ts";

test("UI runtime dispatches mapped rows and settles each dialog once", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-1", (event) => events.push(event));
	const answer = bridge.ui.select("Choose", ["alpha", "beta"]);
	await Bun.sleep(0);
	const request = events.find((event) => event.type === UI_REQUEST_EVENT);
	if (!request) throw new Error("missing mapped dialog request");

	expect(
		bridge.resolve({
			sessionId: "session-1",
			requestId: String(request.requestId),
			value: "stale",
		}),
	).toEqual({ ok: false, error: "Invalid dialog value" });
	expect(bridge.pendingCount()).toBe(1);
	expect(
		bridge.resolve({ sessionId: "session-1", requestId: String(request.requestId), value: "beta" }),
	).toEqual({ ok: true });
	expect(await answer).toBe("beta");
	expect(
		bridge.resolve({
			sessionId: "session-1",
			requestId: String(request.requestId),
			value: "alpha",
		}),
	).toEqual({ ok: false, error: "Unknown or settled dialog request" });

	bridge.ui.notify("notice");
	bridge.ui.setStatus("status", "ready");
	bridge.ui.setWidget("widget", ["line"]);
	bridge.ui.setTitle("title");
	bridge.ui.setWorkingMessage("working");
	bridge.ui.setEditorText("draft");
	const working = events.find((event) => event.type === UI_WORKING_EVENT);
	expect(working).toMatchObject({ nativeAccepted: false, blocker: { fcId: "FC15" } });
	expect(events.filter((event) => event.type === "pixie:ui:notify")).toHaveLength(2);
	bridge.dispose();
});

test("UI cancellation carries the exact request ID and an unknown native outcome", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-2", (event) => events.push(event));
	const answer = bridge.ui.input("Input");
	await Bun.sleep(0);
	const request = events.find((event) => event.type === UI_REQUEST_EVENT);
	if (!request) throw new Error("missing mapped input request");
	const requestId = String(request.requestId);

	expect(bridge.cancel(requestId, "aborted")).toBe(true);
	expect(await answer).toBeUndefined();
	const cancellation = events.find((event) => event.type === UI_CANCEL_EVENT);
	expect(cancellation).toMatchObject({
		sessionId: "session-2",
		requestId,
		reason: "aborted",
		forwarded: true,
		nativeCancelled: "unknown",
		blocker: { fcId: "FC15" },
	});
	bridge.dispose();
});

test("MCP administration keeps tool execution blocked and uses adapter public events", () => {
	const listeners = new Map<string, (value: unknown) => void>();
	let capability: Capability | undefined;
	const registration = { dispose: async () => {} };
	const events = {
		emit(channel: string, value: unknown) {
			if (channel === CAPABILITY_EVENT) capability = value as Capability;
			if (channel === ADAPTER_RUNTIME_SNAPSHOT_EVENT)
				(value as { result?: unknown }).result = { ok: true };
			if (channel === ADAPTER_RUNTIME_REGISTER_EVENT)
				(value as { result?: unknown }).result = { ok: true, registration };
			listeners.get(channel)?.(value);
		},
		on(channel: string, listener: (value: unknown) => void) {
			listeners.set(channel, listener);
			return () => listeners.delete(channel);
		},
	};
	const pi = {
		events,
		on() {
			return () => {};
		},
	} as unknown as ExtensionAPI;

	mcpAdminBridgeWithConfig({ agentDir: "/tmp/pixie-bridge-integration" })(pi);
	if (!capability) throw new Error("missing mcp capability");
	const call = capability.operations["pi.tools.call"];
	if (!call) throw new Error("missing retained pi.tools.call operation");
	expect(() => call({}, {} as never)).toThrow("BRIDGE-02 blocker");
	const registerBrowser = capability.operations["adapter.registerBrowser"];
	if (!registerBrowser) throw new Error("missing adapter.registerBrowser operation");
	expect(registerBrowser({ url: "http://127.0.0.1:8787/mcp" }, {} as never)).toEqual({ ok: true });

	const status = capability.operations["adapter.status"];
	if (!status) throw new Error("missing adapter.status operation");
	events.emit(ADAPTER_STATUS_EVENT, { version: 1, servers: [{ name: "fixture" }] });
	expect(status({}, {} as never)).toMatchObject({
		engine: "pi-mcp-adapter",
		snapshot: { version: 1, servers: [{ name: "fixture" }] },
	});
});

test("mcp.attach replaces a revoked Canvas authority through dispose then register", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-canvas-`);
	const order: string[] = [];
	const active = new Map<string, { definition: Record<string, unknown>; disposed: boolean }>();
	const registrations: { name: string; definition: Record<string, unknown>; disposed: boolean }[] =
		[];
	const starts: ((event: unknown, context: unknown) => unknown)[] = [];
	const shutdowns: ((...args: unknown[]) => unknown)[] = [];
	const events = {
		emit(channel: string, value: unknown) {
			if (channel !== ADAPTER_RUNTIME_REGISTER_EVENT) return;
			const request = value as {
				name: string;
				definition: Record<string, unknown>;
				result?: unknown;
			};
			if (active.has(request.name)) {
				request.result = { ok: false, error: new Error("active runtime registration") };
				return;
			}
			const registration = { name: request.name, definition: request.definition, disposed: false };
			registrations.push(registration);
			active.set(request.name, registration);
			request.result = {
				ok: true,
				registration: {
					dispose: async () => {
						if (registration.disposed) return;
						order.push(`dispose:${registration.name}`);
						registration.disposed = true;
						if (active.get(registration.name) === registration) active.delete(registration.name);
					},
				},
			};
			order.push(`register:${request.name}`);
		},
	};
	const pi = {
		events,
		on(channel: string, listener: (...args: unknown[]) => unknown) {
			if (channel === "session_start") starts.push(listener);
			if (channel === "session_shutdown") shutdowns.push(listener);
			return () => {};
		},
	} as unknown as ExtensionAPI;
	const bridge = mcpConnectionsBridge(pi, dir);
	const session = { sessionId: "native-session" } as CapabilityContext["session"];
	const context: CapabilityContext = {
		cwd: dir,
		agentDir: dir,
		session,
		signal: new AbortController().signal,
		notify: () => {},
	};
	const startContext = {
		sessionManager: { getSessionId: () => "native-session" },
		ui: { notify: () => {} },
	} as never;
	try {
		if (starts.length !== 1) throw new Error("missing session_start handler");
		await starts[0]({}, startContext);
		const attach = bridge.operations["mcp.attach"];
		if (!attach) throw new Error("missing mcp.attach operation");
		const original = {
			name: "pixie-canvas",
			type: "http",
			url: "https://pixie.example/mcp/canvas",
			headers: { Authorization: "Bearer revoked" },
		};
		const replacement = { ...original, headers: { Authorization: "Bearer fresh" } };
		expect(await attach({ servers: [original] }, context)).toEqual({ ok: true, unavailable: [] });
		expect(await attach({ servers: [replacement] }, context)).toEqual({
			ok: true,
			unavailable: [],
		});
		expect(order).toEqual([
			"register:pixie-canvas",
			"dispose:pixie-canvas",
			"register:pixie-canvas",
		]);
		expect(registrations).toHaveLength(2);
		expect(registrations[0]?.disposed).toBe(true);
		expect(registrations[1]?.disposed).toBe(false);
		expect(active.size).toBe(1);
		expect(active.get("pixie-canvas")?.definition).toMatchObject({
			headers: { Authorization: "Bearer fresh" },
		});
		expect(await attach({ servers: [replacement] }, context)).toEqual({
			ok: true,
			unavailable: [],
		});
		expect(registrations).toHaveLength(2);
		await expect(
			attach({ servers: [replacement] }, {
				...context,
				session: { sessionId: "native-session" },
			} as never),
		).rejects.toThrow("another session");
		if (shutdowns.length !== 1) throw new Error("missing session_shutdown handler");
		await shutdowns[0]();
		expect(registrations[1]?.disposed).toBe(true);
		expect(active.size).toBe(0);
		await bridge.close();
		expect(registrations.filter((registration) => registration.disposed)).toHaveLength(2);
	} finally {
		await bridge.close();
		await rm(dir, { recursive: true, force: true });
	}
});

test("mcp.attach rejects conflicting definitions for ordinary operator MCP connections", async () => {
	const dir = await mkdtemp(`${tmpdir()}/pixie-mcp-conflict-`);
	const registrations: { name: string; definition: Record<string, unknown>; disposed: boolean }[] =
		[];
	const starts: ((event: unknown, context: unknown) => unknown)[] = [];
	const events = {
		emit(channel: string, value: unknown) {
			if (channel !== ADAPTER_RUNTIME_REGISTER_EVENT) return;
			const request = value as {
				name: string;
				definition: Record<string, unknown>;
				result?: unknown;
			};
			const registration = { name: request.name, definition: request.definition, disposed: false };
			registrations.push(registration);
			request.result = {
				ok: true,
				registration: {
					dispose: async () => {
						registration.disposed = true;
					},
				},
			};
		},
	};
	const pi = {
		events,
		on(channel: string, listener: (...args: unknown[]) => unknown) {
			if (channel === "session_start") starts.push(listener);
			return () => {};
		},
	} as unknown as ExtensionAPI;
	const bridge = mcpConnectionsBridge(pi, dir);
	const session = { sessionId: "operator-session" } as CapabilityContext["session"];
	const context: CapabilityContext = {
		cwd: dir,
		agentDir: dir,
		session,
		signal: new AbortController().signal,
		notify: () => {},
	};
	try {
		if (starts.length !== 1) throw new Error("missing session_start handler");
		await starts[0](
			{},
			{
				sessionManager: { getSessionId: () => "operator-session" },
				ui: { notify: () => {} },
			},
		);
		const add = bridge.operations["pi.session.extensions.add"];
		const attach = bridge.operations["mcp.attach"];
		if (!add || !attach) throw new Error("missing MCP operations");
		const original = { name: "operator-server", type: "http", url: "https://operator.example/mcp" };
		const conflicting = { ...original, url: "https://other.example/mcp" };
		expect(await add({ extension: original }, context)).toEqual({ ok: true });
		await expect(add({ extension: conflicting }, context)).rejects.toThrow("already registered");
		expect(await attach({ servers: [conflicting] }, context)).toEqual({
			ok: false,
			unavailable: ["MCP connection unavailable: operator-server"],
		});
		expect(registrations).toHaveLength(1);
		expect(registrations[0]?.disposed).toBe(false);
	} finally {
		await bridge.close();
		await rm(dir, { recursive: true, force: true });
	}
});
