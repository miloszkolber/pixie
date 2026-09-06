import { createRequire } from "node:module";
import { type ExtensionAPI, getAgentDir } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";
import type { RecordValue } from "../storage.ts";
import { mcpRuntimeBridge } from "./mcp-runtime-bridge.ts";

// Both MCP profile names select this upstream factory. Model tools, their
// visibility, transports, discovery, auth and reconnect remain upstream-owned.
// createRequire avoids pulling upstream's untyped TS helpers into Pixie's
// strict typecheck. It loads the same module used at runtime and preserves
// the exact ExtensionAPI identity required by the host APIs.

export const PI_MCP_ADAPTER_VERSION = "2.32.1";
export const PI_MCP_ADAPTER_STATUS_EVENT = "pi-mcp-adapter/status/v1";
export const PI_MCP_ADAPTER_REGISTER_EVENT = "pi-mcp-adapter:runtime-register:v1";
export const PI_MCP_ADAPTER_REGISTER_VERSION = 1;
export const PIXIE_BROWSER_RUNTIME_NAME = "pixie-browser";

interface UpstreamAdapter {
	createMcpAdapter: (options?: Record<string, never>) => (pi: ExtensionAPI) => void;
	MCP_STATUS_EVENT?: string;
}

interface RuntimeRegistration {
	dispose(): Promise<void>;
}

interface RuntimeRegisterRequest extends Record<string, unknown> {
	version: typeof PI_MCP_ADAPTER_REGISTER_VERSION;
	name: string;
	definition: Record<string, unknown>;
	result?: { ok: true; registration: RuntimeRegistration } | { ok: false; error: Error };
}

function loadUpstream(): UpstreamAdapter {
	return createRequire(import.meta.url)("pi-mcp-adapter") as UpstreamAdapter;
}

function statusChannel(): string {
	try {
		const channel = loadUpstream().MCP_STATUS_EVENT;
		if (typeof channel === "string" && channel !== "") return channel;
	} catch {
		// Fall through to the documented channel when the upstream module
		// cannot be introspected; the status cache simply stays empty.
	}
	return PI_MCP_ADAPTER_STATUS_EVENT;
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

// Programmatic configuration is passed through unchanged. agentDir only scopes
// legacy Pixie records. Upstream cache/config roots use PI_CODING_AGENT_DIR.
export function piMcpAdapterWithConfig(options?: {
	config?: Record<string, unknown>;
	agentDir?: string;
}): (pi: ExtensionAPI) => void {
	return (pi: ExtensionAPI) => {
		// Standard upstream config discovery by default (or the
		// supplied programmatic config), single `mcp` proxy tool, lazy
		// lifecycle, cached schemas, reconnect, and status channel.
		loadUpstream().createMcpAdapter({
			...(options?.config ? { config: options.config } : {}),
		} as Record<string, never>)(pi);
		const bridge = mcpRuntimeBridge(pi, options?.agentDir ?? getAgentDir());

		let snapshot: RecordValue | null = null;
		pi.events.on(statusChannel(), (value: unknown) => {
			if (value && typeof value === "object") snapshot = value as RecordValue;
		});

		// Best-effort Browser registration. The pi-host otherwise does not
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
			id: "pi-mcp-adapter",
			version: 1,
			close: bridge.close,
			operations: {
				...bridge.operations,
				"adapter.status": () => ({
					engine: "pi-mcp-adapter",
					version: PI_MCP_ADAPTER_VERSION,
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
		registerCapability(pi, {
			id: "mcp",
			version: 1,
			operations: bridge.operations,
			close: bridge.close,
		});
	};
}
