import type { McpAppOpenResult } from "@pixie/contracts";
import { getTransport } from "@/connection";

type AppTransport = ReturnType<typeof getTransport>;

export interface McpAppViewScope {
	projectId: string;
	sessionId: string;
	toolCallId: string;
	viewId: string;
}

const APP_CLIENT_MAX_HTML_BYTES = 5 * 1024 * 1024;
const APP_CLIENT_MAX_CHUNK_BYTES = 256 * 1024;
const APP_READ_TIMEOUT_MS = 60_000;
const APP_KEEP_ALIVE_TIMEOUT_MS = 60_000;
export const APP_KEEP_ALIVE_INTERVAL_MS = 30_000;
// This exceeds the controller's disconnected-client grace period. Either the
// close is replayed after a reconnect or controller cleanup wins first.
export const APP_CLOSE_TIMEOUT_MS = 75_000;

function decodeChunk(data: string): Uint8Array {
	if (data.length % 4 !== 0 || /[^A-Za-z0-9+/=]/.test(data)) {
		throw new Error("The app content response was invalid.");
	}
	let binary: string;
	try {
		binary = atob(data);
	} catch {
		throw new Error("The app content response was invalid.");
	}
	if (binary.length > APP_CLIENT_MAX_CHUNK_BYTES || btoa(binary) !== data) {
		throw new Error("The app content response was invalid.");
	}
	const result = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) {
		result[index] = binary.charCodeAt(index);
	}
	return result;
}

export async function readMcpAppHTML(
	opened: McpAppOpenResult,
	scope: Omit<McpAppViewScope, "viewId">,
	signal: AbortSignal,
	transport: AppTransport = getTransport(),
): Promise<string> {
	const byteLength = opened.resource.byteLength;
	if (
		!Number.isSafeInteger(byteLength) ||
		byteLength < 0 ||
		byteLength > APP_CLIENT_MAX_HTML_BYTES
	) {
		throw new Error("The app content response was invalid.");
	}
	const content = new Uint8Array(byteLength);
	let offset = 0;
	while (offset < byteLength) {
		const chunk = await transport.request(
			"session.appContentRead",
			{ ...scope, viewId: opened.viewId, offset },
			{ signal, timeoutMs: APP_READ_TIMEOUT_MS },
		);
		const decoded = decodeChunk(chunk.data);
		if (
			chunk.offset !== offset ||
			chunk.nextOffset !== offset + decoded.length ||
			decoded.length === 0 ||
			chunk.nextOffset > byteLength
		) {
			throw new Error("The app content response was invalid.");
		}
		content.set(decoded, offset);
		offset = chunk.nextOffset;
	}
	try {
		return new TextDecoder("utf-8", { fatal: true }).decode(content);
	} catch {
		throw new Error("The app content response was invalid.");
	}
}

export async function renewMcpAppView(
	scope: McpAppViewScope,
	signal: AbortSignal,
	transport: AppTransport = getTransport(),
): Promise<void> {
	await transport.request("session.appKeepAlive", scope, {
		signal,
		timeoutMs: APP_KEEP_ALIVE_TIMEOUT_MS,
	});
}

export async function revokeMcpAppView(
	viewId: string,
	transport: AppTransport = getTransport(),
): Promise<void> {
	await transport.request("session.appClose", { viewId }, { timeoutMs: APP_CLOSE_TIMEOUT_MS });
}
