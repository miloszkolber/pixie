// Pure native-UI passive projection state.
//
// Passive values are presentation only. They are retained by session and
// child generation so reconnects cannot make an old Pi child look current.
// This module does not publish, throttle, or execute anything.

export const PASSIVE_STATE_LIMITS = {
	maxStatusKeys: 16,
	maxWidgetKeys: 16,
	maxWidgetLines: 32,
	maxTextChars: 2000,
	maxKeyChars: 128,
	maxNotifications: 16,
} as const;

export interface SessionGenerationInput {
	readonly sessionId: string;
	readonly generation?: number;
	readonly childGeneration?: number;
}

export interface PassiveState {
	readonly sessionId: string;
	/** Canonical generation used by this projection. */
	readonly generation: number;
	/** Alias retained for callers that use the native child-generation name. */
	readonly childGeneration: number;
	/** Last accepted passive event sequence. */
	readonly sequence: number;
	readonly statuses: Readonly<Record<string, string>>;
	readonly widgets: Readonly<Record<string, readonly string[]>>;
	readonly title: string;
	readonly workingMessage?: string;
	readonly notifications: readonly PassiveNotification[];
}

export interface PassiveNotification {
	readonly message: string;
	readonly level?: string;
}

interface PassiveEventBase extends SessionGenerationInput {
	readonly sequence: number;
}

export type PassiveUiEvent =
	| (PassiveEventBase & {
			readonly type: "pixie:ui:status";
			readonly key: string;
			readonly text?: string;
		})
	| (PassiveEventBase & {
			readonly type: "pixie:ui:widget";
			readonly key: string;
			readonly lines?: readonly string[];
		})
	| (PassiveEventBase & {
			readonly type: "pixie:ui:title";
			readonly title: string;
		})
	| (PassiveEventBase & {
			readonly type: "pixie:ui:working";
			readonly message?: string;
		})
	| (PassiveEventBase & {
			readonly type: "pixie:ui:notify";
			readonly message: string;
			readonly level?: string;
		});

export interface PassiveReplay {
	readonly version: 1;
	readonly sessionId: string;
	readonly generation?: number;
	readonly childGeneration?: number;
	readonly sequence: number;
	readonly statuses: Readonly<Record<string, string>>;
	readonly widgets: Readonly<Record<string, readonly string[]>>;
	readonly title: string;
	readonly workingMessage?: string;
	readonly notifications: readonly PassiveNotification[];
}

export type PassiveRejectReason =
	| "invalid-identity"
	| "foreign-session"
	| "stale-generation"
	| "stale-sequence"
	| "invalid-event"
	| "state-limit";

export type PassiveApplyResult =
	| { readonly accepted: true; readonly state: PassiveState; readonly reason: "applied" }
	| {
			readonly accepted: false;
			readonly state: PassiveState;
			readonly reason: PassiveRejectReason;
			readonly message: string;
		};

function readGeneration(input: SessionGenerationInput): number | undefined {
	if (input.generation !== undefined && input.childGeneration !== undefined) {
		if (input.generation !== input.childGeneration) return undefined;
	}
	return input.generation ?? input.childGeneration;
}

function validGeneration(generation: number | undefined): generation is number {
	return generation !== undefined && Number.isSafeInteger(generation) && generation >= 0;
}

function validateIdentity(input: SessionGenerationInput): number | undefined {
	if (!input.sessionId || typeof input.sessionId !== "string") return undefined;
	const generation = readGeneration(input);
	return validGeneration(generation) ? generation : undefined;
}

function cloneWidgets(widgets: Readonly<Record<string, readonly string[]>>): Record<string, readonly string[]> {
	return Object.fromEntries(Object.entries(widgets).map(([key, lines]) => [key, [...lines]]));
}

function reject(
	state: PassiveState,
	reason: PassiveRejectReason,
	message: string,
): PassiveApplyResult {
	return { accepted: false, state, reason, message };
}

function validText(value: unknown): value is string {
	return typeof value === "string" && value.length <= PASSIVE_STATE_LIMITS.maxTextChars;
}

function validKey(value: unknown): value is string {
	return typeof value === "string" && value.length > 0 && value.length <= PASSIVE_STATE_LIMITS.maxKeyChars;
}

function validSequence(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

export function createPassiveState(input: SessionGenerationInput): PassiveState {
	const generation = validateIdentity(input);
	if (generation === undefined) throw new Error("Passive state requires a session and generation");
	return {
		sessionId: input.sessionId,
		generation,
		childGeneration: generation,
		sequence: 0,
		statuses: {},
		widgets: {},
		title: "",
		notifications: [],
	};
}

function identityResult(state: PassiveState, event: SessionGenerationInput): PassiveApplyResult | undefined {
	if (!event.sessionId || event.sessionId !== state.sessionId)
		return reject(state, "foreign-session", "Passive UI state belongs to another session");
	const generation = readGeneration(event);
	if (!validGeneration(generation)) return reject(state, "invalid-identity", "Passive UI event has no valid generation");
	if (generation !== state.generation)
		return reject(state, "stale-generation", "Passive UI event belongs to an old or different generation");
	return undefined;
}

/** Apply one passive projection without mutating the previous state. */
export function applyPassiveEvent(state: PassiveState, event: PassiveUiEvent): PassiveApplyResult {
	if (!event || typeof event !== "object") return reject(state, "invalid-event", "Passive UI event is not an object");
	const identityError = identityResult(state, event);
	if (identityError) return identityError;
	if (!validSequence(event.sequence)) return reject(state, "invalid-event", "Passive UI event has an invalid sequence");
	if (event.sequence <= state.sequence)
		return reject(state, "stale-sequence", "Passive UI event is older than the latest accepted projection");

	if (event.type === "pixie:ui:status") {
		if (!validKey(event.key) || (event.text !== undefined && !validText(event.text)))
			return reject(state, "invalid-event", "Status key or text exceeds supported bounds");
		if (event.text !== undefined && !(event.key in state.statuses) && Object.keys(state.statuses).length >= PASSIVE_STATE_LIMITS.maxStatusKeys)
			return reject(state, "state-limit", "Passive status key limit reached");
		const statuses = { ...state.statuses };
		if (event.text === undefined) delete statuses[event.key];
		else statuses[event.key] = event.text;
		return { accepted: true, reason: "applied", state: { ...state, sequence: event.sequence, statuses } };
	}

	if (event.type === "pixie:ui:widget") {
		if (!validKey(event.key)) return reject(state, "invalid-event", "Widget key exceeds supported bounds");
		if (
			event.lines !== undefined &&
			(!Array.isArray(event.lines) ||
				event.lines.length > PASSIVE_STATE_LIMITS.maxWidgetLines ||
				event.lines.some((line) => !validText(line)))
		)
			return reject(state, "invalid-event", "Widget lines exceed supported bounds");
		if (event.lines !== undefined && !(event.key in state.widgets) && Object.keys(state.widgets).length >= PASSIVE_STATE_LIMITS.maxWidgetKeys)
			return reject(state, "state-limit", "Passive widget key limit reached");
		const widgets = cloneWidgets(state.widgets);
		if (event.lines === undefined) delete widgets[event.key];
		else widgets[event.key] = [...event.lines];
		return { accepted: true, reason: "applied", state: { ...state, sequence: event.sequence, widgets } };
	}

	if (event.type === "pixie:ui:title") {
		if (!validText(event.title)) return reject(state, "invalid-event", "Title exceeds supported bounds");
		return { accepted: true, reason: "applied", state: { ...state, sequence: event.sequence, title: event.title } };
	}

	if (event.type === "pixie:ui:working") {
		if (event.message !== undefined && !validText(event.message))
			return reject(state, "invalid-event", "Working message exceeds supported bounds");
		return {
			accepted: true,
			reason: "applied",
			state: { ...state, sequence: event.sequence, workingMessage: event.message },
		};
	}

	if (!validText(event.message) || (event.level !== undefined && typeof event.level !== "string"))
		return reject(state, "invalid-event", "Notification exceeds supported bounds");
	const notifications = [
		...state.notifications,
		{ message: event.message, ...(event.level !== undefined ? { level: event.level } : {}) },
	].slice(-PASSIVE_STATE_LIMITS.maxNotifications);
	return { accepted: true, reason: "applied", state: { ...state, sequence: event.sequence, notifications } };
}

/** Produce a detached, JSON-safe replay snapshot of the latest passive state. */
export function createPassiveReplay(state: PassiveState): PassiveReplay {
	return {
		version: 1,
		sessionId: state.sessionId,
		generation: state.generation,
		childGeneration: state.childGeneration,
		sequence: state.sequence,
		statuses: { ...state.statuses },
		widgets: cloneWidgets(state.widgets),
		title: state.title,
		...(state.workingMessage !== undefined ? { workingMessage: state.workingMessage } : {}),
		notifications: state.notifications.map((notification) => ({ ...notification })),
	};
}

/**
 * Apply a reconnect snapshot only to its exact session and generation. A
 * replay at or behind the current sequence is stale, including duplicates.
 */
export function applyPassiveReplay(state: PassiveState, replay: PassiveReplay): PassiveApplyResult {
	if (!replay || typeof replay !== "object") return reject(state, "invalid-event", "Passive replay is not an object");
	if (replay.version !== 1 || replay.sessionId !== state.sessionId)
		return reject(state, "foreign-session", "Passive replay belongs to another session");
	const generation = readGeneration(replay);
	if (!validGeneration(generation)) return reject(state, "invalid-identity", "Passive replay has no valid generation");
	if (generation !== state.generation)
		return reject(state, "stale-generation", "Passive replay belongs to an old or different generation");
	if (!validSequence(replay.sequence) || replay.sequence <= state.sequence)
		return reject(state, "stale-sequence", "Passive replay is not newer than current state");
	if (
		!replay.statuses ||
		typeof replay.statuses !== "object" ||
		Array.isArray(replay.statuses) ||
		!replay.widgets ||
		typeof replay.widgets !== "object" ||
		Array.isArray(replay.widgets) ||
		!validText(replay.title) ||
		!Array.isArray(replay.notifications)
	)
		return reject(state, "invalid-event", "Passive replay has an invalid payload");
	if (Object.keys(replay.statuses).length > PASSIVE_STATE_LIMITS.maxStatusKeys || Object.keys(replay.widgets).length > PASSIVE_STATE_LIMITS.maxWidgetKeys)
		return reject(state, "state-limit", "Passive replay exceeds key limits");
	for (const [key, text] of Object.entries(replay.statuses)) {
		if (!validKey(key) || !validText(text)) return reject(state, "invalid-event", "Passive replay has an invalid status");
	}
	for (const [key, lines] of Object.entries(replay.widgets)) {
		if (!validKey(key) || !Array.isArray(lines) || lines.length > PASSIVE_STATE_LIMITS.maxWidgetLines || lines.some((line) => !validText(line)))
			return reject(state, "invalid-event", "Passive replay has an invalid widget");
	}
	if (replay.workingMessage !== undefined && !validText(replay.workingMessage))
		return reject(state, "invalid-event", "Passive replay has an invalid working message");
	if (
		replay.notifications.length > PASSIVE_STATE_LIMITS.maxNotifications ||
		replay.notifications.some(
			(notification) =>
				!notification ||
				typeof notification !== "object" ||
				!validText(notification.message) ||
				(notification.level !== undefined && typeof notification.level !== "string"),
		)
	)
		return reject(state, "invalid-event", "Passive replay has invalid notifications");
	return {
		accepted: true,
		reason: "applied",
		state: {
			sessionId: state.sessionId,
			generation: state.generation,
			childGeneration: state.childGeneration,
			sequence: replay.sequence,
			statuses: { ...replay.statuses },
			widgets: cloneWidgets(replay.widgets),
			title: replay.title,
			...(replay.workingMessage !== undefined ? { workingMessage: replay.workingMessage } : {}),
			notifications: replay.notifications.map((notification) => ({ ...notification })),
		},
	};
}

/** Readable alias for callers that describe replay as restoring a snapshot. */
export const restorePassiveReplay = applyPassiveReplay;
