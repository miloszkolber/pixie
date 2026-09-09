// EXT-01 native UI row mapping over public adapter APIs.
//
// Pure mapping table for every supported native UI row. This module owns the
// row inventory, exact limitations, and the single final-response mapping. It
// performs no I/O, holds no pending state, and never touches private Pi
// internals. The live `ui-bridge.ts` remains the runtime owner; this file is
// the auditable mapping contract that BRIDGE-02/BRIDGE-03 build on.

export type BlockingPrimitive = "select" | "confirm" | "input" | "editor";
export type PassivePrimitive = "notify" | "setStatus" | "setWidget" | "setTitle";
export type DraftPrimitive = "setEditorText" | "pasteToEditor";
export type NativeUiPrimitive = BlockingPrimitive | PassivePrimitive | DraftPrimitive;

export type UiRowSupport = "supported" | "supported-with-limits" | "unsupported";

export interface NativeUiRow {
	readonly id: NativeUiPrimitive;
	/** Native `ExtensionUIContext` method name. */
	readonly nativeMethod: string;
	/** Native RPC `extension_ui_request` method name, or null when RPC has no frame. */
	readonly rpcMethod: string | null;
	/** Pixie session event emitted for this row, or null when local-only. */
	readonly pixieEvent: string | null;
	readonly support: UiRowSupport;
	/** Exact user-visible limitation. Empty only for fully supported rows. */
	readonly limitation: string;
	/** Single final-response mapping description. */
	readonly finalMapping: string;
}

// Bounds mirror contracts.md and the live bridge so mapping validation agrees
// with admission. They are repeated here (not imported) to keep this contract
// readable without a runtime dependency.
export const NATIVE_UI_BOUNDS = {
	maxPendingPerSession: 16,
	defaultTimeoutMs: 30 * 60 * 1000,
	maxTitleChars: 2000,
	maxPayloadChars: 8000,
	maxResponseChars: 8000,
	maxSelectOptions: 24,
	maxSelectOptionChars: 500,
	maxStatusKeys: 16,
	maxWidgetKeys: 16,
	maxWidgetLines: 32,
	maxTextChars: 2000,
	maxKeyChars: 128,
	maxUpdatesPerSecond: 64,
} as const;

/** Every supported native UI row plus the two draft rows from FC16. */
export const NATIVE_UI_ROWS: readonly NativeUiRow[] = [
	{
		id: "select",
		nativeMethod: "select(title, options, opts?)",
		rpcMethod: "select",
		pixieEvent: "pixie:ui:request",
		support: "supported-with-limits",
		limitation:
			"At most 24 options, each 1-500 chars, matched exactly (no trimming or coercion). Title 1-2000 chars. Signal/timeout dismiss without a value.",
		finalMapping:
			"Single final answer: first valid exact option string wins; stale or non-offered values are invalid and never settle the native promise.",
	},
	{
		id: "confirm",
		nativeMethod: "confirm(title, message, opts?)",
		rpcMethod: "confirm",
		pixieEvent: "pixie:ui:request",
		support: "supported-with-limits",
		limitation:
			"Title 1-2000 chars. Uses the native confirmed boolean, never a generic value string. Dismiss/timeout resolves false.",
		finalMapping:
			"Single final answer: first boolean confirmed wins (cancelled/error resolves false); non-boolean values are invalid.",
	},
	{
		id: "input",
		nativeMethod: "input(title, placeholder?, opts?)",
		rpcMethod: "input",
		pixieEvent: "pixie:ui:request",
		support: "supported-with-limits",
		limitation:
			"Title 1-2000 chars, placeholder/message/prefill at most 8000 chars. Dismiss/timeout resolves undefined.",
		finalMapping:
			"Single final answer: first string within 8000 chars wins; stale replies after settlement are invalid.",
	},
	{
		id: "editor",
		nativeMethod: "editor(title, prefill?)",
		rpcMethod: "editor",
		pixieEvent: "pixie:ui:request",
		support: "supported-with-limits",
		limitation:
			"Title 1-2000 chars, prefill at most 8000 chars. Multiline draft has separate expiry ownership; answering never auto-sends other drafts.",
		finalMapping:
			"Single final answer: first string within 8000 chars wins; stale replies after settlement are invalid.",
	},
	{
		id: "notify",
		nativeMethod: "notify(message, type?)",
		rpcMethod: "notify",
		pixieEvent: "pixie:ui:notify",
		support: "supported-with-limits",
		limitation:
			"Fire-and-forget toast, truncated to 2000 chars. Bounded to 64 updates/s; never pending and never a dialog answer.",
		finalMapping: "No final response. Emission is the complete mapping; there is nothing to settle.",
	},
	{
		id: "setStatus",
		nativeMethod: "setStatus(key, text | undefined)",
		rpcMethod: "setStatus",
		pixieEvent: "pixie:ui:status",
		support: "supported-with-limits",
		limitation:
			"At most 16 keys, key 1-128 chars, text truncated to 2000 chars, 64 updates/s. Clearing with undefined always passes through and is never throttled away.",
		finalMapping: "No final response. Latest value per key is the projection; clears remove the key.",
	},
	{
		id: "setWidget",
		nativeMethod: "setWidget(key, string[] | undefined, options?)",
		rpcMethod: "setWidget",
		pixieEvent: "pixie:ui:widget",
		support: "supported-with-limits",
		limitation:
			"Only string-array widgets project (at most 32 lines, each truncated to 2000 chars, 16 keys, 64 updates/s). Component factories are unsupported and report a warning without executing.",
		finalMapping: "No final response. Latest string lines per key are the projection; undefined clears the key.",
	},
	{
		id: "setTitle",
		nativeMethod: "setTitle(title)",
		rpcMethod: "setTitle",
		pixieEvent: "pixie:ui:title",
		support: "supported-with-limits",
		limitation:
			"Presentation only, truncated to 2000 chars. Never renames the native session and never blocks a tool call.",
		finalMapping: "No final response. Latest title is the projection.",
	},
	{
		id: "setEditorText",
		nativeMethod: "setEditorText(text)",
		rpcMethod: "set_editor_text",
		pixieEvent: null,
		support: "supported-with-limits",
		limitation:
			"Draft proposal only: applies automatically solely to the unchanged empty originating draft, otherwise offered as explicit insert/replace. Never auto-sends and never overwrites another client's input.",
		finalMapping:
			"No dialog response. The proposal carries a per-client revision; conflicts stay explicit and never settle a dialog.",
	},
	{
		id: "pasteToEditor",
		nativeMethod: "pasteToEditor(text)",
		rpcMethod: "set_editor_text",
		pixieEvent: null,
		support: "supported-with-limits",
		limitation:
			"Uses native RPC paste behavior, which falls back to set_editor_text. Same draft-proposal guard as setEditorText; large pastes collapse per native handling.",
		finalMapping: "No dialog response. Same revision-guarded proposal mapping as setEditorText.",
	},
] as const;

/** Terminal-only members that are explicitly not mapped. Kept here so a row cannot silently gain support. */
export const UNSUPPORTED_UI_MEMBERS: readonly string[] = [
	"onTerminalInput",
	"setWorkingVisible",
	"setWorkingIndicator",
	"setHiddenThinkingLabel",
	"setFooter",
	"setHeader",
	"custom",
	"getEditorText",
	"addAutocompleteProvider",
	"setEditorComponent",
	"getEditorComponent",
	"getAllThemes",
	"getTheme",
	"setTheme",
	"getToolsExpanded",
	"setToolsExpanded",
] as const;

export function nativeUiRow(id: NativeUiPrimitive): NativeUiRow {
	const row = NATIVE_UI_ROWS.find((candidate) => candidate.id === id);
	if (!row) throw new Error(`Unknown native UI row: ${id}`);
	return row;
}

/** Dismissed/cancelled value per blocking primitive. Confirm resolves false; all others resolve undefined. */
export function dismissedValue(primitive: BlockingPrimitive): string | boolean | undefined {
	return primitive === "confirm" ? false : undefined;
}

/** Exact option membership for select. No trimming, no case folding, no coercion. */
export function isOfferedOption(options: readonly string[], value: string): boolean {
	return options.includes(value);
}

/** Validate a candidate final value before it may settle the native promise. */
export function isValidFinalValue(
	primitive: BlockingPrimitive,
	value: unknown,
	options?: { selectOptions?: readonly string[] },
): boolean {
	if (primitive === "confirm") return typeof value === "boolean";
	if (typeof value !== "string") return false;
	if (value.length > NATIVE_UI_BOUNDS.maxResponseChars) return false;
	if (primitive === "select") {
		const offered = options?.selectOptions;
		if (!offered || offered.length === 0) return false;
		return isOfferedOption(offered, value);
	}
	return true;
}

export interface FinalRequest {
	readonly primitive: BlockingPrimitive;
	readonly selectOptions?: readonly string[];
}

export interface FinalResponse {
	readonly value?: string | boolean;
	readonly cancelled?: boolean;
	readonly error?: string;
}

export interface SettledNativeResult {
	readonly settled: boolean;
	/** Exact value the native promise resolves with when settled. */
	readonly nativeValue: string | boolean | undefined;
	readonly reason: "answer" | "dismissed" | "invalid";
	readonly error?: string;
}

/**
 * Single final-response mapping. The first response settles the native
 * promise; every later response is invalid and changes nothing. Validation
 * happens before settlement so an invalid value never consumes the single
 * answer. Confirmation maps the native confirmed boolean, never a generic
 * value string.
 */
export function mapFinalResponse(request: FinalRequest, response: FinalResponse): SettledNativeResult {
	if (response.error || response.cancelled) {
		return { settled: true, nativeValue: dismissedValue(request.primitive), reason: "dismissed" };
	}
	if (!isValidFinalValue(request.primitive, response.value, { selectOptions: request.selectOptions })) {
		return { settled: false, nativeValue: undefined, reason: "invalid", error: "Invalid dialog value" };
	}
	if (request.primitive === "confirm") {
		return { settled: true, nativeValue: response.value === true, reason: "answer" };
	}
	return { settled: true, nativeValue: response.value as string, reason: "answer" };
}

/** Tracks the single-settlement rule across competing replies for one request. */
export class SingleFinalResponse {
	private done = false;
	constructor(readonly request: FinalRequest) {}
	settle(response: FinalResponse): SettledNativeResult & { accepted: boolean } {
		if (this.done) {
			return { settled: false, nativeValue: undefined, reason: "invalid", error: "Already settled", accepted: false };
		}
		const mapped = mapFinalResponse(this.request, response);
		if (!mapped.settled) return { ...mapped, accepted: false };
		this.done = true;
		return { ...mapped, accepted: true };
	}
	get isSettled(): boolean {
		return this.done;
	}
}

/** Ownership carried with every blocking request so answers route to the exact dialog. */
export interface NativeUiOwnership {
	readonly requestId: string;
	/** Pixie session key / native session id. Answers for another session are rejected. */
	readonly sessionId: string;
	/** Managed child generation that issued the request. A replaced child invalidates it. */
	readonly childGeneration: number;
	readonly primitive: BlockingPrimitive;
	/** Original native deadline in ms; reconnects replay without resetting it. */
	readonly timeoutMs: number;
}

export function carryOwnership(ownership: NativeUiOwnership): NativeUiOwnership {
	if (!ownership.requestId || !ownership.sessionId) throw new Error("Ownership requires request and session identity");
	if (!Number.isSafeInteger(ownership.childGeneration) || ownership.childGeneration < 0) {
		throw new Error("Ownership requires a child generation");
	}
	if (
		!Number.isSafeInteger(ownership.timeoutMs) ||
		ownership.timeoutMs <= 0 ||
		ownership.timeoutMs > NATIVE_UI_BOUNDS.defaultTimeoutMs
	) {
		throw new Error("Ownership carries the original deadline without extension");
	}
	return { ...ownership };
}
