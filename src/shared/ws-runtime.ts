import {
	CONTROLLER_METHOD_SCHEMAS,
	type MethodFieldSchema,
	type MethodSchema,
} from "./generated/protocol-catalog";
import { WS_CHANNELS, type WsServerMessage } from "./ws-protocol";

const channels = new Set<string>(Object.values(WS_CHANNELS));
const controllerSchemas: ReadonlyMap<string, MethodSchema> = new Map(
	CONTROLLER_METHOD_SCHEMAS.map((schema) => [schema.name, schema]),
);

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

function methodFieldMatches(type: string, value: unknown): boolean {
	switch (type) {
		case "string":
			return typeof value === "string";
		case "number":
			return typeof value === "number" && Number.isFinite(value);
		case "boolean":
			return typeof value === "boolean";
		case "object":
			return record(value);
		case "array":
			return Array.isArray(value);
		default:
			return false;
	}
}

function validateMethodFields(
	label: string,
	fields: readonly MethodFieldSchema[],
	value: unknown,
): string | undefined {
	if (fields.length === 0) return undefined;
	if (!record(value)) return `${label} must be an object`;
	for (const field of fields) {
		const present =
			Object.hasOwn(value, field.name) &&
			value[field.name] !== null &&
			value[field.name] !== undefined;
		if (!present) {
			if (field.optional) continue;
			return `${label} is missing ${field.name}`;
		}
		if (!methodFieldMatches(field.type, value[field.name])) {
			return `${label} field ${field.name} must be ${field.type}`;
		}
	}
	return undefined;
}

/**
 * Method-level browser request validator. It checks the generated required
 * fields for the method and ignores unknown additive fields, so a newer client
 * can send extra keys without failing an older controller contract.
 */
export function validateWsMethodParams(method: string, params: unknown): string | undefined {
	const schema = controllerSchemas.get(method);
	if (schema === undefined) return undefined;
	return validateMethodFields(`${method} params`, schema.params, params);
}

/** Method-level result validator for a controller reply, with additive unknown fields allowed. */
export function validateWsMethodResult(method: string, result: unknown): string | undefined {
	const schema = controllerSchemas.get(method);
	if (schema === undefined) return undefined;
	return validateMethodFields(`${method} result`, schema.result, result);
}

/** Validates the browser-visible error envelope without inventing an error code. */
export function validateWsErrorEnvelope(value: unknown): string | undefined {
	if (!record(value)) return "error envelope must be an object";
	if (typeof value.error !== "string" || value.error === "") {
		return "error envelope requires a non-empty message";
	}
	if (
		"errorCode" in value &&
		value.errorCode !== undefined &&
		typeof value.errorCode !== "string"
	) {
		return "error envelope errorCode must be a string";
	}
	return undefined;
}

/**
 * Envelope plus method-level request validation. A method without a generated
 * schema is envelope-validated only; method membership is still owned by the
 * controller dispatch.
 */
export function isWsRequest(
	value: unknown,
): value is { id: string; method: string; params?: unknown; sessionId?: unknown } {
	if (!isWsClientEnvelope(value) || !("method" in value)) return false;
	const params = "params" in value ? value.params : undefined;
	return validateWsMethodParams(value.method, params) === undefined;
}

/**
 * Validates a server response against the generated method result schema. The
 * caller supplies the method because the wire response omits it.
 */
export function isWsMethodResult(method: string, value: unknown): boolean {
	if (!record(value) || typeof value.id !== "string" || typeof value.ok !== "boolean") return false;
	if (value.ok) {
		return "result" in value && validateWsMethodResult(method, value.result) === undefined;
	}
	return validateWsErrorEnvelope(value) === undefined;
}
