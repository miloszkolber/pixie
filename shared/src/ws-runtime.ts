import { WS_CHANNELS, type WsServerMessage } from "./ws-protocol";

const channels = new Set<string>(Object.values(WS_CHANNELS));

function record(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringArray(value: unknown): value is string[] {
	return Array.isArray(value) && value.every((entry) => typeof entry === "string");
}

export type WsClientEnvelope =
	| { ack: string[] }
	| { resume: string[] }
	| { id: string; method: string; params?: unknown; sessionId?: unknown };

/** Validates only the small transport envelope. Method-specific data stays at its owning handler. */
export function isWsClientEnvelope(value: unknown): value is WsClientEnvelope {
	if (!record(value)) return false;
	if ("ack" in value) return Object.keys(value).length === 1 && stringArray(value.ack);
	if ("resume" in value) return Object.keys(value).length === 1 && stringArray(value.resume);
	return typeof value.id === "string" && typeof value.method === "string";
}

/** Rejects malformed or ambiguous server frames before they can mutate browser state. */
export function isWsServerMessage(value: unknown): value is WsServerMessage {
	if (!record(value)) return false;
	if ("channel" in value) {
		return typeof value.channel === "string" && channels.has(value.channel) && "data" in value;
	}
	if (typeof value.id !== "string" || typeof value.ok !== "boolean") return false;
	if (value.ok) return "result" in value && !("error" in value) && !("errorCode" in value);
	return typeof value.error === "string" && !("result" in value);
}
