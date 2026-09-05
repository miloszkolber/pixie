import { createRequire } from "node:module";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { registerCapability } from "../capabilities.ts";
import type { RecordValue } from "../storage.ts";

// Optional Pi-native MCP profile (`--extensions ...,pi-mcp-adapter`).
//
// The upstream `pi-mcp-adapter@2.32.1` factory is used unchanged: a single
// proxy tool `mcp` (~200 tokens) with lazy/eager/keep-alive lifecycle,
// metadata cache `~/.pi/agent/mcp-cache.json`, stdio (`command`/`args`/`env`/
// `cwd`), Streamable HTTP with SSE fallback, rmcp-mux sockets, header secrets
// (`${VAR}`, `$env:VAR`, `!command`), OAuth/bearer auth with tokens in
// `~/.pi/agent/mcp-tokens`, resources via `exposeResources` (default true),
// `directTools` promotion, `disabledTools`/`toolPrefix`, reconnect, keep-alive
// health, runtime registration over the Pi event bus, and a read-only status
// channel. Config discovery is project `.mcp.json` > `.pi/mcp.json` > user
// `~/.config/mcp/mcp.json`. This bridge only advertises an additive
// capability marker plus two projection operations (`adapter.status`,
// `adapter.registerBrowser`) so the controller can surface adapter state in
// the Tools UI. It adds no tools, prompts, or interception.
//
// Bun compatibility is explicitly unknown: upstream declares
// `engines: {node: ">=20"}` and only `@types/bun` in devDependencies. The
// parity suite (`tests/pi-native-parity/mcp-parity.test.ts`) starts the host
// with this profile under Bun and records the outcome there.
//
// The factory is loaded through `createRequire` instead of a static import,
// following the `pi-subagent` profile: the package ships TypeScript sources
// importing untyped helpers, and a static import would pull those sources
// into Pixie's strict typecheck. The runtime module is identical either way;
// only the type visibility changes, and the call below passes the live `pi`
// object through untouched.
//
// The custom `mcp` extension (`@pixie/pi-mcp`) stays the writer until the
// parity deletion gate in docs/roadmap.md passes; enabling `pi-mcp-adapter`
// before then surfaces both the per-tool `<conn>__<tool>` surface and the
// single `mcp` proxy tool, so use it only for parity evaluation.

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

function statusChannel(pi: ExtensionAPI): string {
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

// Programmatic-config variant for parity evaluation. The options object is
// passed through to the upstream factory untouched; only the capability
// marker and projection operations are Pixie's.
export function piMcpAdapterWithConfig(options?: {
	config?: Record<string, unknown>;
}): (pi: ExtensionAPI) => void {
	return (pi: ExtensionAPI) => {
		// Upstream unchanged: standard config discovery by default (or the
		// supplied programmatic config), single `mcp` proxy tool, lazy
		// lifecycle, cached schemas, reconnect, and status channel.
		loadUpstream().createMcpAdapter({
			...(options?.config ? { config: options.config } : {}),
		} as Record<string, never>)(pi);

		let snapshot: RecordValue | null = null;
		pi.events.on(statusChannel(pi), (value: unknown) => {
			if (value && typeof value === "object") snapshot = value as RecordValue;
		});

		// Best-effort Browser registration for parity evaluation. The pi-host
		// otherwise does not know the controller's publisher address, so this
		// only runs when the operator sets it explicitly. Duplicates fail closed
		// upstream (first registration wins); a stale registration is left alone.
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
			operations: {
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
	};
}
