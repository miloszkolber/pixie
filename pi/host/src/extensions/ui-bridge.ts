import { randomUUID } from "node:crypto";
import type {
	ExtensionUIContext,
	ExtensionUIDialogOptions,
	Theme,
} from "@earendil-works/pi-coding-agent";

// Generic Pi extension UI bridge.
//
// Pi extensions request user interaction through `ctx.ui` dialog primitives
// (`select`, `confirm`, `input`, `editor`, `notify`). In RPC mode the host
// service has no terminal, so this bridge forwards each dialog call as a
// `pixie:ui:request` session event over the Pi host WebSocket. The Pixie
// controller relays the request to the Web UI, which renders a matching
// dialog (choice modal, confirm dialog, single-line input, multiline editor,
// toast) and answers through `session.uiResponse`. The awaiting promise then
// settles with the user's response.
//
// Every interaction carries a request ID, the originating Pi session ID, the
// primitive type, and the payload. Dialog state is scoped to the originating
// Pi session: responses for another session or an unknown request are
// rejected, and each request settles exactly once. Cancellation flows through
// `AbortSignal` (per-dialog `signal` option) and timeouts; both publish a
// `pixie:ui:cancel` event so the controller can dismiss the Web UI dialog.
//
// Blocking requests (`select`, `confirm`, `input`, `editor`) stay pending
// server-side until answered, cancelled, timed out, stopped, or destroyed.
// The host re-publishes unresolved requests on `session.load` so a
// reconnecting controller replays them to late subscribers instead of leaving
// an orphaned modal or a hanging tool call. Non-blocking updates (`notify`,
// `setStatus`, `setWidget`, `setTitle`, `setWorkingMessage`) emit immediately
// and carry no pending state.
//
// This module is generic infrastructure: it knows the dialog primitives and
// the ephemeral status/widget/title/working projections, and nothing about
// any specific extension. The pinned SDK (0.85.1) provides `select`,
// `confirm`, `input`, `editor` and `notify` with wire shapes
// `select(title, options, opts?)`, `confirm(title, message, opts?)`,
// `input(title, placeholder?, opts?)`, `editor(title, prefill?)` and
// `notify(message, type?)`, so no degradation shims are needed. Terminal-only
// members (raw terminal input, working visibility/indicator chrome, hidden
// thinking labels, custom footers/headers, editor components, autocomplete,
// `custom` component factories, editor text) stay no-ops, matching the SDK's
// headless behavior; `theme` returns unstyled text instead of the SDK's
// ANSI-styled singleton so extensions that format status strings (for
// example Signet's `ui.theme.fg`) keep working without leaking terminal
// escapes into RPC transcripts. `setWidget` component factories cannot render
// without a TUI, so only string-array widgets project; factory content is
// ignored.

export type UiPrimitive = "select" | "confirm" | "input" | "editor" | "notify";

export const UI_REQUEST_EVENT = "pixie:ui:request";
export const UI_NOTIFY_EVENT = "pixie:ui:notify";
export const UI_CANCEL_EVENT = "pixie:ui:cancel";
export const UI_STATUS_EVENT = "pixie:ui:status";
export const UI_WIDGET_EVENT = "pixie:ui:widget";
export const UI_TITLE_EVENT = "pixie:ui:title";
export const UI_WORKING_EVENT = "pixie:ui:working";

// Backstop so a lost controller never leaves a tool call waiting forever.
// Matches the controller's pending-dialog timeout.
export const DEFAULT_UI_TIMEOUT_MS = 30 * 60 * 1000;

export interface UiBridgeRequest {
	requestId: string;
	sessionId: string;
	primitive: UiPrimitive;
	title: string;
	message?: string;
	options?: string[];
	placeholder?: string;
	prefill?: string;
	timeout: number;
}

export interface UiBridgeResponse {
	sessionId: string;
	requestId: string;
	value?: string | boolean;
	cancelled?: boolean;
	error?: string;
}

interface PendingDialog {
	primitive: UiPrimitive;
	request: UiBridgeRequest;
	settle: (value: string | boolean | undefined, cancelled: boolean) => void;
	timer: ReturnType<typeof setTimeout>;
	onAbort: (() => void) | undefined;
}

function dismissedValue(primitive: UiPrimitive): string | boolean | undefined {
	return primitive === "confirm" ? false : undefined;
}

export interface UiBridge {
	readonly sessionId: string;
	readonly ui: ExtensionUIContext;
	readonly pendingCount: () => number;
	/** Unresolved blocking requests, for session.load replay to late subscribers. */
	readonly pendingRequests: () => UiBridgeRequest[];
	/** Re-publish every unresolved blocking request (reconnect replay). */
	readonly republishPending: () => void;
	/** Settle a pending request. Returns false when unknown, foreign, or already settled. */
	readonly resolve: (response: UiBridgeResponse) => { ok: boolean; error?: string };
	/** Dismiss one pending request without a user answer. */
	readonly cancel: (requestId: string, reason?: string) => boolean;
	/** Dismiss every pending request, e.g. on session abort or close. */
	readonly cancelAll: (reason?: string) => void;
}

// Headless RPC theme: every styling call returns its input unchanged. The
// cast is structural; the object stays a stateless singleton because no
// session or terminal state is involved.
const headlessTheme = {
	fg: (_color: string, text: string) => text,
	bg: (_color: string, text: string) => text,
	bold: (text: string) => text,
	italic: (text: string) => text,
	underline: (text: string) => text,
	inverse: (text: string) => text,
	strikethrough: (text: string) => text,
	getFgAnsi: (_color: string) => "",
	getBgAnsi: (_color: string) => "",
	getColorMode: () => "truecolor" as const,
	getThinkingBorderColor: (_level: string) => (text: string) => text,
	getBashModeBorderColor: () => (text: string) => text,
} as unknown as Theme;

export function createUiBridge(
	sessionId: string,
	publish: (event: Record<string, unknown>) => void,
): UiBridge {
	const pending = new Map<string, PendingDialog>();

	const remove = (requestId: string): PendingDialog | undefined => {
		const found = pending.get(requestId);
		if (!found) return undefined;
		pending.delete(requestId);
		clearTimeout(found.timer);
		found.onAbort?.();
		return found;
	};

	const cancelOne = (requestId: string, reason: string): boolean => {
		const found = remove(requestId);
		if (!found) return false;
		publish({ type: UI_CANCEL_EVENT, sessionId, requestId, reason });
		found.settle(dismissedValue(found.primitive), true);
		return true;
	};

	const request = <T>(
		primitive: UiPrimitive,
		payload: {
			title: string;
			message?: string;
			options?: string[];
			placeholder?: string;
			prefill?: string;
			signal?: AbortSignal;
			timeout?: number;
		},
		toResult: (value: string | boolean | undefined, cancelled: boolean) => T,
	): Promise<T> => {
		const requestId = randomUUID();
		const timeout =
			typeof payload.timeout === "number" &&
			Number.isSafeInteger(payload.timeout) &&
			payload.timeout > 0
				? payload.timeout
				: DEFAULT_UI_TIMEOUT_MS;
		if (payload.signal?.aborted) return Promise.resolve(toResult(dismissedValue(primitive), true));
		const done = new Promise<T>((resolvePromise) => {
			const timer = setTimeout(() => cancelOne(requestId, "timeout"), timeout);
			// Unref keeps a lingering dialog from holding the host process open.
			(timer as { unref?: () => void }).unref?.();
			const settle = (value: string | boolean | undefined, cancelled: boolean) => {
				clearTimeout(timer);
				resolvePromise(toResult(value, cancelled));
			};
			const onAbort = payload.signal ? () => cancelOne(requestId, "aborted") : undefined;
			const event: UiBridgeRequest = {
				requestId,
				sessionId,
				primitive,
				title: payload.title,
				timeout,
				...(payload.message !== undefined ? { message: payload.message } : {}),
				...(payload.options !== undefined ? { options: payload.options } : {}),
				...(payload.placeholder !== undefined ? { placeholder: payload.placeholder } : {}),
				...(payload.prefill !== undefined ? { prefill: payload.prefill } : {}),
			};
			pending.set(requestId, { primitive, request: event, settle, timer, onAbort });
			if (payload.signal && onAbort)
				payload.signal.addEventListener("abort", onAbort, { once: true });
		});
		// Remove the abort listener once settled so signals never fire into a new request.
		const onAbort = pending.get(requestId)?.onAbort;
		const cleanup = () => {
			if (payload.signal && onAbort) payload.signal.removeEventListener("abort", onAbort);
		};
		void done.then(cleanup, cleanup);
		const event = pending.get(requestId)?.request;
		if (event) publish({ type: UI_REQUEST_EVENT, ...event });
		return done;
	};

	const dialogOpts = (
		opts?: ExtensionUIDialogOptions,
	): { signal?: AbortSignal; timeout?: number } => ({
		...(opts?.signal ? { signal: opts.signal } : {}),
		...(opts?.timeout !== undefined ? { timeout: opts.timeout } : {}),
	});

	// `custom` has no Web UI renderer: resolving undefined routes upstream
	// questionnaires to the select/input dialog walker (their RPC backstop).
	const custom: ExtensionUIContext["custom"] = async () => undefined as never;

	const ui: ExtensionUIContext = {
		select: (title, options, opts) =>
			request(
				"select",
				{ title, options: [...options], ...dialogOpts(opts) },
				(value, cancelled) => (cancelled || typeof value !== "string" ? undefined : value),
			),
		confirm: (title, message, opts) =>
			request("confirm", { title, message, ...dialogOpts(opts) }, (value, cancelled) =>
				cancelled || typeof value !== "boolean" ? false : value,
			),
		input: (title, placeholder, opts) =>
			request("input", { title, placeholder, ...dialogOpts(opts) }, (value, cancelled) =>
				cancelled || typeof value !== "string" ? undefined : value,
			),
		editor: (title, prefill) =>
			request("editor", { title, prefill }, (value, cancelled) =>
				cancelled || typeof value !== "string" ? undefined : value,
			),
		notify: (message, type) => {
			publish({ type: UI_NOTIFY_EVENT, sessionId, message, level: type ?? "info" });
		},
		// Ephemeral projections: emitted immediately, never pending. The
		// controller fans them out to browsers; stale values die with the
		// session and never block a tool call.
		setStatus: (key, text) => {
			if (typeof key !== "string" || key === "") return;
			publish({
				type: UI_STATUS_EVENT,
				sessionId,
				key,
				...(text === undefined ? {} : { text }),
			});
		},
		setWorkingMessage: (message) => {
			publish({
				type: UI_WORKING_EVENT,
				sessionId,
				...(message === undefined ? {} : { message }),
			});
		},
		setWidget: (key, content, options) => {
			if (typeof key !== "string" || key === "") return;
			// Component factories need a TUI to render; without one there is
			// no clean Web UI projection, so only string-array widgets travel.
			if (typeof content === "function") return;
			if (content !== undefined && !Array.isArray(content)) return;
			publish({
				type: UI_WIDGET_EVENT,
				sessionId,
				key,
				...(content === undefined
					? {}
					: { lines: [...content], placement: options?.placement ?? "aboveEditor" }),
			});
		},
		setTitle: (title) => {
			if (typeof title !== "string" || title === "") return;
			publish({ type: UI_TITLE_EVENT, sessionId, title });
		},
		// Terminal-only members stay no-ops: the RPC host renders dialogs in
		// the Web UI and has no terminal widgets, overlays, or editor chrome.
		// Editor text has no clean projection either: pushing text into the
		// browser composer would clobber what the user is typing.
		onTerminalInput: () => () => {},
		setWorkingVisible: () => {},
		setWorkingIndicator: () => {},
		setHiddenThinkingLabel: () => {},
		setFooter: () => {},
		setHeader: () => {},
		custom,
		pasteToEditor: () => {},
		setEditorText: () => {},
		getEditorText: () => "",
		addAutocompleteProvider: () => {},
		setEditorComponent: () => {},
		getEditorComponent: () => undefined,
		get theme(): Theme {
		// No terminal styling exists in RPC mode, but extensions format
		// status strings through the theme (Signet's `ui.theme.fg`), so
		// return unstyled text rather than undefined. Status output travels
		// through the `setStatus` projection above, keeping terminal escapes
		// out of transcripts.
			return headlessTheme;
		},
		getAllThemes: () => [],
		getTheme: () => undefined,
		setTheme: () => ({ success: false, error: "UI not available" }),
		getToolsExpanded: () => false,
		setToolsExpanded: () => {},
	};

	return {
		sessionId,
		ui,
		pendingCount: () => pending.size,
		pendingRequests: () => [...pending.values()].map((entry) => ({ ...entry.request })),
		republishPending: () => {
			for (const entry of pending.values()) publish({ type: UI_REQUEST_EVENT, ...entry.request });
		},
		resolve: (response) => {
			if (response.sessionId !== sessionId)
				return { ok: false, error: "Dialog belongs to another session" };
			const found = remove(response.requestId);
			if (!found) return { ok: false, error: "Unknown or settled dialog request" };
			if (response.error || response.cancelled) {
				found.settle(dismissedValue(found.primitive), true);
				return { ok: true };
			}
			found.settle(response.value, false);
			return { ok: true };
		},
		cancel: (requestId, reason) => cancelOne(requestId, reason ?? "cancelled"),
		cancelAll: (reason) => {
			for (const requestId of [...pending.keys()]) cancelOne(requestId, reason ?? "cancelled");
		},
	};
}
