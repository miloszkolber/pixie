// Honest native UI capability and cancellation projections.
//
// Unsupported controls are diagnostics only: they never execute a factory or
// mutate a composer draft. A forwarded cancellation carries the exact identity
// but keeps native outcome unknown because plain Pi RPC does not acknowledge it.

export type NativeUiSupport = "supported" | "supported-with-limits" | "unsupported";

export const UNSUPPORTED_NATIVE_CONTROLS = [
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

const LIMITED_NATIVE_CONTROLS = new Set([
	"select",
	"confirm",
	"input",
	"editor",
	"notify",
	"setStatus",
	"setWidget",
	"setTitle",
	"setEditorText",
	"pasteToEditor",
	"setWorkingMessage",
]);

export interface NativeUiCapability {
	readonly control: string;
	readonly support: NativeUiSupport;
	readonly executes: boolean;
	readonly changesDraft: boolean;
	/** Native acceptance is deliberately absent for local projections. */
	readonly nativeOutcome: "not-applicable" | "unknown";
	readonly limitation: string;
}

export interface UnsupportedControlResult {
	readonly kind: "unsupported-control";
	readonly control: string;
	readonly supported: false;
	readonly executed: false;
	readonly draftChanged: false;
	readonly message: string;
}

export interface CancellationForward {
	readonly kind: "cancellation-forward";
	readonly sessionId: string;
	readonly generation: number;
	readonly childGeneration: number;
	readonly requestId: string;
	readonly reason: string;
	readonly forwarded: true;
	/** Forwarding dismisses Pixie's dialog; native side-effect outcome is unknown. */
	readonly nativeCancelled: "unknown";
}

export function isUnsupportedNativeControl(control: string): boolean {
	return (UNSUPPORTED_NATIVE_CONTROLS as readonly string[]).includes(control) || !LIMITED_NATIVE_CONTROLS.has(control);
}

export function describeNativeUiControl(control: string): NativeUiCapability {
	if (isUnsupportedNativeControl(control)) {
		return {
			control,
			support: "unsupported",
			executes: false,
			changesDraft: false,
			nativeOutcome: "not-applicable",
			limitation: "This control is unsupported in the Web UI and is not executed.",
		};
	}
	if (control === "setWorkingMessage") {
		return {
			control,
			support: "supported-with-limits",
			executes: false,
			changesDraft: false,
			nativeOutcome: "unknown",
			limitation: "Pixie-local working projection only; plain RPC does not expose native acceptance.",
		};
	}
	return {
		control,
		support: "supported-with-limits",
		executes: true,
		changesDraft: control === "setEditorText" || control === "pasteToEditor",
		nativeOutcome: "unknown",
		limitation:
			control === "setEditorText" || control === "pasteToEditor"
				? "Revision-guarded draft proposal; it never auto-submits or silently overwrites a client draft."
				: "Bounded native UI projection; final native side effects remain owned by Pi.",
	};
}

export function reportUnsupportedControl(control: string): UnsupportedControlResult {
	const capability = describeNativeUiControl(control);
	if (capability.support !== "unsupported") throw new Error(`Control is not unsupported: ${control}`);
	return {
		kind: "unsupported-control",
		control,
		supported: false,
		executed: false,
		draftChanged: false,
		message: `${control} is unsupported in the Web UI. No composer draft was changed.`,
	};
}

export function forwardCancellation(input: {
	readonly sessionId: string;
	readonly generation?: number;
	readonly childGeneration?: number;
	readonly requestId: string;
	readonly reason?: string;
}): CancellationForward {
	const generation = input.generation ?? input.childGeneration;
	if (!input.sessionId) throw new Error("Cancellation requires a session identity");
	if (!input.requestId) throw new Error("Cancellation requires the exact request ID");
	if (input.generation !== undefined && input.childGeneration !== undefined && input.generation !== input.childGeneration)
		throw new Error("Cancellation generation identity must match");
	if (generation === undefined || !Number.isSafeInteger(generation) || generation < 0)
		throw new Error("Cancellation requires a valid generation");
	return {
		kind: "cancellation-forward",
		sessionId: input.sessionId,
		generation,
		childGeneration: generation,
		requestId: input.requestId,
		reason: input.reason || "cancelled",
		forwarded: true,
		nativeCancelled: "unknown",
	};
}
