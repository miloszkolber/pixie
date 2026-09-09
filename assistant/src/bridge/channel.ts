/**
 * Pure checks and framing for the private bridge channel.
 *
 * The caller owns the actual inherited descriptor.  This module only accepts
 * explicit attestations for a private duplex descriptor, close-on-exec and
 * descendant-inheritance behavior, then validates bounded versioned JSONL.
 */

import {
	BRIDGE_PROBE_VERSION,
	createBridgeBlocker,
	type BridgeBlocker,
	type BridgeDistribution,
} from "./feasibility.ts";

export const BRIDGE_CHANNEL_VERSION = BRIDGE_PROBE_VERSION;
export const BRIDGE_CHANNEL_MAX_FRAME_BYTES = 64 * 1024;
export const BRIDGE_CHANNEL_MAX_BUFFER_BYTES = 1024 * 1024;
export const BRIDGE_CHANNEL_MAX_PENDING = 8;
export const BRIDGE_CHANNEL_MAX_ID_LENGTH = 128;
export const BRIDGE_CHANNEL_MAX_METHOD_LENGTH = 128;
export const BRIDGE_CHANNEL_MAX_TOKEN_LENGTH = 256;

/** This list is intentionally small until later bridge tasks prove more APIs. */
export const BRIDGE_ALLOWED_METHODS = ["bridge.probe", "bridge.close"] as const;
export type BridgeMethod = (typeof BRIDGE_ALLOWED_METHODS)[number];

export interface PrivateBridgeChannelInput {
	readonly distribution: BridgeDistribution;
	readonly sdkVersion?: string | null;
	readonly transport: "inherited-duplex" | "other";
	readonly descriptor: number;
	readonly private: boolean;
	readonly authenticated: boolean;
	readonly closeOnExec: boolean;
	readonly descendantsInherit: boolean;
	readonly listener: boolean;
	readonly credentialExport: boolean;
	readonly shellEval: boolean;
	readonly maxFrameBytes: number;
	readonly maxBufferedBytes: number;
	readonly maxPending: number;
	readonly nonce: string;
	readonly epoch: string;
}

export interface PrivateBridgeChannelResult {
	readonly allowed: boolean;
	readonly maxFrameBytes: number;
	readonly maxBufferedBytes: number;
	readonly maxPending: number;
	readonly blockers: readonly BridgeBlocker[];
}

export interface BridgeHelloFrame {
	readonly version: 1;
	readonly type: "hello";
	readonly nonce: string;
	readonly epoch: string;
}

export interface BridgeRequestFrame {
	readonly version: 1;
	readonly type: "request";
	readonly id: string;
	readonly method: BridgeMethod;
	readonly payload: Record<string, unknown>;
}

export interface BridgeResponseFrame {
	readonly version: 1;
	readonly type: "response";
	readonly id: string;
	readonly ok: boolean;
	readonly result?: unknown;
	readonly error?: string;
}

export type BridgeFrame = BridgeHelloFrame | BridgeRequestFrame | BridgeResponseFrame;

function record(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function validToken(value: unknown, name: string): asserts value is string {
	if (
		typeof value !== "string" ||
		value.length < 16 ||
		value.length > BRIDGE_CHANNEL_MAX_TOKEN_LENGTH ||
		value.includes("\0") ||
		!/^[A-Za-z0-9_-]+$/.test(value)
	)
		throw new Error(`Invalid bridge ${name}`);
}

function validId(value: unknown): asserts value is string {
	if (typeof value !== "string" || value.length === 0 || value.length > BRIDGE_CHANNEL_MAX_ID_LENGTH || value.includes("\0"))
		throw new Error("Invalid bridge request ID");
}

function strictKeys(value: Record<string, unknown>, keys: readonly string[]): void {
	const allowed = new Set(keys);
	if (Object.keys(value).some((key) => !allowed.has(key))) throw new Error("Unexpected bridge frame field");
}

function frameBytes(value: unknown): number {
	const encoded = new TextEncoder().encode(JSON.stringify(value));
	// Include the LF delimiter owned by encodeBridgeFrame in the bound.
	if (encoded.byteLength + 1 > BRIDGE_CHANNEL_MAX_FRAME_BYTES)
		throw new Error(`Bridge frame exceeds ${BRIDGE_CHANNEL_MAX_FRAME_BYTES} bytes`);
	return encoded.byteLength + 1;
}

function checkedFrame(value: unknown): BridgeFrame {
	if (!record(value) || value.version !== BRIDGE_CHANNEL_VERSION || typeof value.type !== "string")
		throw new Error("Invalid bridge frame version or type");
	if (value.type === "hello") {
		strictKeys(value, ["version", "type", "nonce", "epoch"]);
		validToken(value.nonce, "nonce");
		validToken(value.epoch, "epoch");
		return value as unknown as BridgeHelloFrame;
	}
	if (value.type === "request") {
		strictKeys(value, ["version", "type", "id", "method", "payload"]);
		validId(value.id);
		if (
			typeof value.method !== "string" ||
			value.method.length === 0 ||
			value.method.length > BRIDGE_CHANNEL_MAX_METHOD_LENGTH ||
			!(BRIDGE_ALLOWED_METHODS as readonly string[]).includes(value.method)
		)
			throw new Error(`Unsupported bridge method: ${String(value.method)}`);
		if (!record(value.payload)) throw new Error("Bridge request payload must be an object");
		return value as unknown as BridgeRequestFrame;
	}
	if (value.type === "response") {
		strictKeys(value, ["version", "type", "id", "ok", "result", "error"]);
		validId(value.id);
		if (typeof value.ok !== "boolean") throw new Error("Invalid bridge response status");
		if (value.ok) {
			if (value.error !== undefined) throw new Error("Successful bridge response cannot contain an error");
		} else if (typeof value.error !== "string" || value.error.length === 0 || value.error.length > 2000) {
			throw new Error("Invalid bridge response error");
		}
		return value as unknown as BridgeResponseFrame;
	}
	throw new Error(`Unsupported bridge frame type: ${value.type}`);
}

/** Check descriptor and reservation attestations before bridge enablement. */
export function checkPrivateBridgeChannel(input: PrivateBridgeChannelInput): PrivateBridgeChannelResult {
	const version = input.sdkVersion ?? null;
	const blockers: BridgeBlocker[] = [];
	const add = (missingPublicSymbol: string, reproduction: string) => {
		blockers.push(
			createBridgeBlocker({
				distribution: input.distribution,
				version,
				surface: "channel",
				missingPublicSymbol,
				reproduction,
				userVisibleLimitation: "Optional native administration is unavailable; no broad listener or model-facing channel is opened.",
				releaseConsequence: "BRIDGE-01 stays open until the private bounded channel is verified.",
			}),
		);
	};

	if (input.transport !== "inherited-duplex") add("private inherited duplex descriptor", "The bridge channel is not an inherited duplex descriptor.");
	if (!Number.isSafeInteger(input.descriptor) || input.descriptor < 3)
		add("valid inherited descriptor", "The bridge descriptor is not a dedicated non-negative safe integer.");
	if (!input.private) add("private bridge descriptor", "The channel was not attested as private to the managed child.");
	if (!input.authenticated) add("nonce-authenticated bridge handshake", "The channel peer was not authenticated before administration messages.");
	if (!input.closeOnExec) add("close-on-exec descriptor state", "The bridge descriptor may leak into an unintended descendant.");
	if (input.descendantsInherit) add("descendant descriptor non-inheritance", "The descriptor inheritance probe reports authority leakage to descendants.");
	if (input.listener) add("no broad bridge listener", "The proposed channel exposes a listener instead of a private inherited descriptor.");
	if (input.credentialExport) add("no credential export on bridge channel", "The channel would export credentials instead of using native storage.");
	if (input.shellEval) add("no shell evaluation on bridge channel", "The channel would evaluate shell text instead of dispatching an allowlisted frame.");
	if (!Number.isSafeInteger(input.maxFrameBytes) || input.maxFrameBytes <= 0 || input.maxFrameBytes > BRIDGE_CHANNEL_MAX_FRAME_BYTES)
		add("bounded bridge frame", `Configured frame budget ${input.maxFrameBytes} exceeds the bridge frame limit.`);
	if (!Number.isSafeInteger(input.maxBufferedBytes) || input.maxBufferedBytes <= 0 || input.maxBufferedBytes > BRIDGE_CHANNEL_MAX_BUFFER_BYTES)
		add("bounded bridge aggregate buffer", `Configured aggregate buffer ${input.maxBufferedBytes} exceeds the bridge buffer limit.`);
	else if (Number.isSafeInteger(input.maxFrameBytes) && input.maxFrameBytes > input.maxBufferedBytes)
		add("frame within aggregate bridge buffer", "The configured frame budget cannot fit within the aggregate channel reservation.");
	if (!Number.isSafeInteger(input.maxPending) || input.maxPending <= 0 || input.maxPending > BRIDGE_CHANNEL_MAX_PENDING)
		add("bounded bridge pending operations", `Configured pending operation count ${input.maxPending} exceeds the bridge pending limit.`);
	try {
		validToken(input.nonce, "nonce");
	} catch {
		add("per-child bridge nonce", "The private channel nonce is missing, malformed or too short.");
	}
	try {
		validToken(input.epoch, "epoch");
	} catch {
		add("bridge child epoch", "The private channel epoch is missing, malformed or too short.");
	}

	return {
		allowed: blockers.length === 0,
		maxFrameBytes: input.maxFrameBytes,
		maxBufferedBytes: input.maxBufferedBytes,
		maxPending: input.maxPending,
		blockers,
	};
}

export function createBridgeHello(nonce: string, epoch: string): BridgeHelloFrame {
	const frame: BridgeHelloFrame = { version: BRIDGE_CHANNEL_VERSION, type: "hello", nonce, epoch };
	checkedFrame(frame);
	return frame;
}

export function validateBridgeHandshake(
	frame: unknown,
	expected: { readonly nonce: string; readonly epoch: string },
): { readonly ok: true } {
	const hello = checkedFrame(frame);
	if (hello.type !== "hello") throw new Error("Bridge handshake requires a hello frame");
	if (hello.nonce !== expected.nonce || hello.epoch !== expected.epoch)
		throw new Error("Bridge handshake nonce or epoch mismatch");
	return { ok: true };
}

/** Encode one complete JSONL frame. Newline is owned by this function. */
export function encodeBridgeFrame(frame: BridgeFrame): string {
	const checked = checkedFrame(frame);
	const json = JSON.stringify(checked);
	frameBytes(checked);
	return `${json}\n`;
}

/** Decode one complete JSONL frame, accepting only an optional trailing CR. */
export function decodeBridgeFrame(line: string): BridgeFrame {
	if (typeof line !== "string" || line.length === 0 || line.includes("\n"))
		throw new Error("Bridge frame must be one JSONL record");
	const withoutCr = line.endsWith("\r") ? line.slice(0, -1) : line;
	if (withoutCr.length === 0) throw new Error("Bridge frame is empty");
	let parsed: unknown;
	try {
		parsed = JSON.parse(withoutCr);
	} catch {
		throw new Error("Invalid bridge JSONL frame");
	}
	frameBytes(parsed);
	return checkedFrame(parsed);
}
