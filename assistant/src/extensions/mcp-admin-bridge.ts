import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";
import type { RecordValue } from "../storage.ts";
import {
	ADAPTER_RUNTIME_REGISTER_EVENT,
	ADAPTER_RUNTIME_REGISTER_VERSION,
	ADAPTER_RUNTIME_SNAPSHOT_EVENT,
	ADAPTER_STATUS_EVENT,
	blockedPiToolsCall,
	emitRuntimeRegister,
	PI_MCP_ADAPTER_TESTED_VERSION,
	readAdapterStatusSnapshot,
} from "./adapter-mcp.ts";
import { mcpConnectionsBridge } from "./mcp-connections.ts";

// Application administration only. Pi loads the operator-installed adapter.
// The public snapshot request discovers its runtime without registering a
// server, initializing a transport, or importing another adapter factory.

export const PI_MCP_ADAPTER_VERSION = PI_MCP_ADAPTER_TESTED_VERSION;
export const PI_MCP_ADAPTER_STATUS_EVENT = ADAPTER_STATUS_EVENT;
export const PI_MCP_ADAPTER_REGISTER_EVENT = ADAPTER_RUNTIME_REGISTER_EVENT;
export const PI_MCP_ADAPTER_REGISTER_VERSION = ADAPTER_RUNTIME_REGISTER_VERSION;
export const PIXIE_BROWSER_RUNTIME_NAME = "pixie-browser";

export function pixieBrowserDefinition(endpoint: string, token?: string): Record<string, unknown> {
	const definition: Record<string, unknown> = { url: endpoint };
	if (token !== undefined && token !== "") {
		definition.headers = { Authorization: `Bearer ${token}` };
	}
	return definition;
}

// Register Pixie Browser through the adapter runtime API where possible:
// proxy-only, synchronous `request.result`, first-wins, fail-closed on
// duplicate names. Throws when the adapter is not installed for this Pi
// instance or when the name is already registered.
export function registerPixieBrowser(pi: ExtensionAPI, endpoint: string, token?: string) {
	return emitRuntimeRegister(
		(channel, data) => pi.events.emit(channel, data),
		PIXIE_BROWSER_RUNTIME_NAME,
		pixieBrowserDefinition(endpoint, token),
	);
}

function text(value: unknown): string {
	return typeof value === "string" ? value : "";
}

export default function mcpAdminBridge(pi: ExtensionAPI): void {
	mcpAdminBridgeWithConfig()(pi);
}

// agentDir only scopes Pixie's persisted connection records. Native adapter
// configuration and execution remain entirely upstream-owned.
export function mcpAdminBridgeWithConfig(options?: {
	agentDir?: string;
}): (pi: ExtensionAPI) => void {
	return (pi: ExtensionAPI) => {
		const probe: { version: 1; name: string; result?: { ok: boolean } } = {
			version: 1,
			name: PIXIE_BROWSER_RUNTIME_NAME,
		};
		pi.events.emit(ADAPTER_RUNTIME_SNAPSHOT_EVENT, probe);
		if (typeof probe.result?.ok !== "boolean") return;
		const bridge = mcpConnectionsBridge(pi, options?.agentDir ?? getAgentDir());

		let snapshot: RecordValue | null = null;
		pi.events.on(PI_MCP_ADAPTER_STATUS_EVENT, (value: unknown) => {
			const status = readAdapterStatusSnapshot(value);
			if (status) snapshot = status as unknown as RecordValue;
		});

		// Browser registration is explicit only (adapter.registerBrowser or
		// mcp.attach from the operator/UI). Implicit session-start wiring from
		// environment was removed: headless operator configuration belongs in
		// Pi's native MCP settings, not in Pixie-owned session magic.

		registerCapability(pi, {
			id: "mcp",
			version: 1,
			close: bridge.close,
			operations: {
				...bridge.operations,
				// The installed adapter exposes registration, snapshot, and status
				// APIs, but no public programmatic tool executor. Keep the retained
				// operation visible as an honest blocker rather than reaching into a
				// live AgentSession tool array.
				"pi.tools.call": (_params, _ctx) => blockedPiToolsCall(),
				"adapter.status": () => ({
					engine: "pi-mcp-adapter",
					// The public runtime protocol does not report package version.
					version: null,
					testedVersion: PI_MCP_ADAPTER_VERSION,
					// Bun compatibility is unknown upstream (engines node>=20);
					// see the parity suite for the recorded Bun startup outcome.
					bunCompat: "unknown",
					proxyTool: "mcp",
					runtimeName: PIXIE_BROWSER_RUNTIME_NAME,
					snapshot,
				}),
				"adapter.registerBrowser": (params: RecordValue) => {
					const endpoint = text(params.url ?? params.endpoint);
					if (endpoint === "") throw new Error("Browser endpoint is required");
					registerPixieBrowser(pi, endpoint, text(params.token) || undefined);
					return { ok: true };
				},
			},
		});
	};
}
