import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";
import type { RecordValue } from "../storage.ts";
import { mcpRuntimeBridge } from "./mcp-runtime-bridge.ts";

// Application administration only. Pi loads the operator-installed adapter.
// The public snapshot request discovers its runtime without registering a
// server, initializing a transport, or importing another adapter factory.

export const PI_MCP_ADAPTER_VERSION = "2.32.1";
export const PI_MCP_ADAPTER_STATUS_EVENT = "pi-mcp-adapter/status/v1";
export const PI_MCP_ADAPTER_REGISTER_EVENT = "pi-mcp-adapter:runtime-register:v1";
export const PI_MCP_ADAPTER_REGISTER_VERSION = 1;
export const PIXIE_BROWSER_RUNTIME_NAME = "pixie-browser";

interface RuntimeRegistration {
	dispose(): Promise<void>;
}

interface RuntimeRegisterRequest extends Record<string, unknown> {
	version: typeof PI_MCP_ADAPTER_REGISTER_VERSION;
	name: string;
	definition: Record<string, unknown>;
	result?: { ok: true; registration: RuntimeRegistration } | { ok: false; error: Error };
}

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
export function registerPixieBrowser(
	pi: ExtensionAPI,
	endpoint: string,
	token?: string,
): RuntimeRegistration {
	const request: RuntimeRegisterRequest = {
		version: PI_MCP_ADAPTER_REGISTER_VERSION,
		name: PIXIE_BROWSER_RUNTIME_NAME,
		definition: pixieBrowserDefinition(endpoint, token),
	};
	pi.events.emit(PI_MCP_ADAPTER_REGISTER_EVENT, request);
	if (!request.result) throw new Error("pi-mcp-adapter is not installed for this Pi instance");
	if (!request.result.ok) throw request.result.error;
	return request.result.registration;
}

function text(value: unknown): string {
	return typeof value === "string" ? value : "";
}

export default function piMcpAdapterExtension(pi: ExtensionAPI): void {
	piMcpAdapterWithConfig()(pi);
}

// agentDir only scopes Pixie's persisted connection records. Native adapter
// configuration and execution remain entirely upstream-owned.
export function piMcpAdapterWithConfig(options?: {
	agentDir?: string;
}): (pi: ExtensionAPI) => void {
	return (pi: ExtensionAPI) => {
		const probe: { version: 1; name: string; result?: { ok: boolean } } = {
			version: 1,
			name: PIXIE_BROWSER_RUNTIME_NAME,
		};
		pi.events.emit("pi-mcp-adapter:runtime-snapshot:v1", probe);
		if (typeof probe.result?.ok !== "boolean") return;
		const bridge = mcpRuntimeBridge(pi, options?.agentDir ?? getAgentDir());

		let snapshot: RecordValue | null = null;
		pi.events.on(PI_MCP_ADAPTER_STATUS_EVENT, (value: unknown) => {
			if (value && typeof value === "object") snapshot = value as RecordValue;
		});

		// Best-effort Browser registration. The assistant otherwise does not
		// know the controller's publisher address, so this only runs when the
		// operator sets it explicitly. Duplicates fail closed upstream (first
		// registration wins); a stale registration is left alone.
		pi.on("session_start", () => {
			const endpoint = process.env.PIXIE_MCP_ADAPTER_BROWSER_URL;
			if (!endpoint) return;
			try {
				registerPixieBrowser(pi, endpoint, process.env.PIXIE_MCP_ADAPTER_BROWSER_TOKEN);
			} catch {
				// Fail-closed upstream; parity runs assert the duplicate error
				// explicitly through `adapter.registerBrowser` instead.
			}
		});

		registerCapability(pi, {
			id: "mcp",
			version: 1,
			close: bridge.close,
			operations: {
				...bridge.operations,
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
