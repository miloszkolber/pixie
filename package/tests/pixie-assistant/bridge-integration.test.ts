import { expect, test } from "bun:test";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { CAPABILITY_EVENT, type Capability } from "../../../assistant/src/capabilities.ts";
import {
	ADAPTER_RUNTIME_REGISTER_EVENT,
	ADAPTER_RUNTIME_SNAPSHOT_EVENT,
	ADAPTER_STATUS_EVENT,
} from "../../../assistant/src/extensions/adapter-mcp.ts";
import { mcpAdminBridgeWithConfig } from "../../../assistant/src/extensions/mcp-admin-bridge.ts";
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
