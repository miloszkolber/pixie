// BRIDGE-02 MCP administration over supported public adapter APIs.
//
// Every retained MCP admin operation routes through a documented public
// surface: the pi-mcp-adapter runtime register/snapshot/status events, the
// public ExtensionAPI event bus, or Pixie-local persisted membership records.
// Programmatic tool execution has no supported adapter API from an ordinary
// extension context, so `pi.tools.call` is an explicit documented blocker and
// never falls back to private live-tool access.

export const PI_MCP_ADAPTER_TESTED_VERSION = "2.32.1";
export const ADAPTER_RUNTIME_REGISTER_EVENT = "pi-mcp-adapter:runtime-register:v1";
export const ADAPTER_RUNTIME_SNAPSHOT_EVENT = "pi-mcp-adapter:runtime-snapshot:v1";
export const ADAPTER_STATUS_EVENT = "pi-mcp-adapter/status/v1";
export const ADAPTER_RUNTIME_REGISTER_VERSION = 1;
export const ADAPTER_RUNTIME_SNAPSHOT_VERSION = 1;
export const ADAPTER_STATUS_SNAPSHOT_VERSION = 1;
export const PIXIE_BROWSER_RUNTIME_NAME = "pixie-browser";

export interface SupportedAdapterApi {
	readonly channel: string;
	readonly version: number;
	readonly purpose: string;
}

export const SUPPORTED_ADAPTER_APIS: readonly SupportedAdapterApi[] = [
	{
		channel: ADAPTER_RUNTIME_REGISTER_EVENT,
		version: ADAPTER_RUNTIME_REGISTER_VERSION,
		purpose: "Register or dispose a runtime MCP server without touching native config files.",
	},
	{
		channel: ADAPTER_RUNTIME_SNAPSHOT_EVENT,
		version: ADAPTER_RUNTIME_SNAPSHOT_VERSION,
		purpose: "Read a runtime server snapshot without registering, connecting, or executing tools.",
	},
	{
		channel: ADAPTER_STATUS_EVENT,
		version: ADAPTER_STATUS_SNAPSHOT_VERSION,
		purpose: "Observe the adapter-owned connection/tool snapshot for this Pi instance.",
	},
] as const;

export type AdapterRoute =
	| "adapter-runtime-register"
	| "adapter-runtime-snapshot"
	| "adapter-status"
	| "pixie-local-store"
	| "pixie-local-membership"
	| "blocked-no-supported-api";

export interface RetainedMcpOperation {
	readonly id: string;
	readonly route: AdapterRoute;
	readonly channel: string | null;
	readonly note: string;
}

/**
 * Every retained MCP admin operation. Config inventory stays Pixie-local
 * (never native config ownership); session attachment uses the public runtime
 * register/snapshot events; status uses the public status event; tool
 * execution is blocked because no supported adapter execution API exists.
 */
export const RETAINED_MCP_OPERATIONS: readonly RetainedMcpOperation[] = [
	{
		id: "mcp.attach",
		route: "adapter-runtime-register",
		channel: ADAPTER_RUNTIME_REGISTER_EVENT,
		note: "Registers each attached server through the public runtime-register event; membership deltas stay Pixie-local.",
	},
	{
		id: "pi.config.extensions.list",
		route: "pixie-local-store",
		channel: null,
		note: "Reads Pixie-persisted connection records only; native adapter configuration stays upstream-owned.",
	},
	{
		id: "pi.config.extensions.add",
		route: "pixie-local-store",
		channel: null,
		note: "Validates and persists a Pixie connection record; registers nothing and executes nothing.",
	},
	{
		id: "pi.config.extensions.set-enabled",
		route: "pixie-local-store",
		channel: null,
		note: "Flips the Pixie-local enabled flag; loading remains adapter-owned.",
	},
	{
		id: "pi.config.extensions.remove",
		route: "pixie-local-store",
		channel: null,
		note: "Removes the Pixie-local record; revocable runtime registrations dispose separately.",
	},
	{
		id: "pi.session.extensions.list",
		route: "adapter-runtime-snapshot",
		channel: ADAPTER_RUNTIME_SNAPSHOT_EVENT,
		note: "Lists live runtime registrations via the public snapshot event, never native config.",
	},
	{
		id: "pi.session.extensions.add",
		route: "adapter-runtime-register",
		channel: ADAPTER_RUNTIME_REGISTER_EVENT,
		note: "Registers through the public runtime-register event and records Pixie-local session membership.",
	},
	{
		id: "pi.session.extensions.remove",
		route: "adapter-runtime-register",
		channel: ADAPTER_RUNTIME_REGISTER_EVENT,
		note: "Disposes the runtime registration and records the Pixie-local removal; late completions stay invalid.",
	},
	{
		id: "pi.tools.call",
		route: "blocked-no-supported-api",
		channel: null,
		note: "No supported adapter execution API exists from an ordinary extension; must report the blocker instead of private access.",
	},
	{
		id: "adapter.status",
		route: "adapter-status",
		channel: ADAPTER_STATUS_EVENT,
		note: "Reads the public adapter status snapshot; package version is not reported by the protocol.",
	},
	{
		id: "adapter.registerBrowser",
		route: "adapter-runtime-register",
		channel: ADAPTER_RUNTIME_REGISTER_EVENT,
		note: "Registers the explicit Pixie Browser runtime through the public runtime-register event.",
	},
	{
		id: "adapter.session.forget",
		route: "pixie-local-membership",
		channel: null,
		note: "Clears Pixie-local session membership; runtime disposal follows lifecycle transitions.",
	},
] as const;

export function adapterOperationRoute(operationId: string): RetainedMcpOperation {
	const found = RETAINED_MCP_OPERATIONS.find((entry) => entry.id === operationId);
	if (!found) throw new Error(`Unknown MCP operation: ${operationId}`);
	return found;
}

export type PublicEmit = (channel: string, data: unknown) => void;

export interface AdapterRegistration {
	dispose(): Promise<void>;
}

interface RuntimeRegisterResult {
	ok: boolean;
	registration?: AdapterRegistration;
	error?: Error;
}

interface RuntimeRegisterRequest {
	version: number;
	name: string;
	definition: Record<string, unknown>;
	result?: RuntimeRegisterResult;
}

interface RuntimeSnapshotResult {
	ok: boolean;
	snapshot?: { name: string; definition: Record<string, unknown>; runtime: true; persisted: false };
	error?: Error;
}

interface RuntimeSnapshotRequest {
	version: number;
	name: string;
	result?: RuntimeSnapshotResult;
}

/** Validate a Pixie MCP connection name. Same scope as the live bridge, without executing anything. */
export function validateMcpName(value: unknown): string {
	if (typeof value !== "string" || !value || value.length > 128 || value.includes("\0")) {
		throw new Error("Invalid MCP name");
	}
	if (!/^[a-zA-Z0-9_-]+$/.test(value) || value.includes("__")) throw new Error("Invalid MCP name");
	return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

/**
 * Register a runtime server through the public adapter event only. The emit
 * function is the public `pi.events.emit` surface; no session, tool array, or
 * private import is consulted. Throws when the adapter is absent or rejects.
 */
export function emitRuntimeRegister(
	emit: PublicEmit,
	name: string,
	definition: Record<string, unknown>,
): AdapterRegistration {
	const validName = validateMcpName(name);
	if (!isRecord(definition)) throw new Error("MCP definition must be an object");
	const request: RuntimeRegisterRequest = {
		version: ADAPTER_RUNTIME_REGISTER_VERSION,
		name: validName,
		definition,
	};
	emit(ADAPTER_RUNTIME_REGISTER_EVENT, request);
	if (!request.result) throw new Error("pi-mcp-adapter is not installed for this Pi instance");
	if (!request.result.ok || !request.result.registration) {
		throw request.result.error ?? new Error("MCP runtime registration unavailable");
	}
	return request.result.registration;
}

/** Read a runtime snapshot through the public adapter event only. */
export function emitRuntimeSnapshot(
	emit: PublicEmit,
	name: string,
): { name: string; definition: Record<string, unknown>; runtime: true; persisted: false } {
	const validName = validateMcpName(name);
	const request: RuntimeSnapshotRequest = {
		version: ADAPTER_RUNTIME_SNAPSHOT_VERSION,
		name: validName,
	};
	emit(ADAPTER_RUNTIME_SNAPSHOT_EVENT, request);
	if (!request.result) throw new Error("pi-mcp-adapter is not installed for this Pi instance");
	if (!request.result.ok || !request.result.snapshot) {
		throw request.result.error ?? new Error("MCP runtime snapshot unavailable");
	}
	return request.result.snapshot;
}

export interface AdapterStatusSnapshot {
	readonly version: number;
	readonly servers: readonly unknown[];
}

/** Read the public status snapshot without connecting or executing tools. Returns null when absent/invalid. */
export function readAdapterStatusSnapshot(value: unknown): AdapterStatusSnapshot | null {
	if (!isRecord(value)) return null;
	if (value.version !== ADAPTER_STATUS_SNAPSHOT_VERSION) return null;
	if (!Array.isArray(value.servers)) return null;
	return { version: 1, servers: value.servers };
}

export interface PiToolsCallBlocker {
	readonly operation: "pi.tools.call";
	readonly route: "blocked-no-supported-api";
	readonly distribution: string;
	readonly missingPublicSymbol: string;
	readonly userVisibleLimitation: string;
	readonly owner: string;
}

export const PI_TOOLS_CALL_BLOCKER: PiToolsCallBlocker = {
	operation: "pi.tools.call",
	route: "blocked-no-supported-api",
	distribution: "npm and standalone Pi 0.85.1 with pi-mcp-adapter 2.32.1",
	missingPublicSymbol:
		"Supported adapter tool-execution API callable from an ordinary extension context",
	userVisibleLimitation:
		"Direct MCP tool execution from Pixie administration is unavailable. Use the model turn through the adapter-owned proxy/direct tools; Pixie never executes through a private live-tool route.",
	owner: "E/B",
};

/**
 * Retained `pi.tools.call` entry point over public APIs only. There is no
 * supported adapter execution API from an ordinary extension, so this always
 * throws the documented BRIDGE-02 blocker. It accepts no session object, so it
 * cannot reach any private live-tool surface even by accident.
 */
export function blockedPiToolsCall(): never {
	throw new Error(
		`BRIDGE-02 blocker (${PI_TOOLS_CALL_BLOCKER.operation}): ${PI_TOOLS_CALL_BLOCKER.missingPublicSymbol}. ${PI_TOOLS_CALL_BLOCKER.userVisibleLimitation}`,
	);
}
