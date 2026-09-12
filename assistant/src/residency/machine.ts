import type {
	CapacityUsage,
	DetachedWorkStatus,
	ResidencyError,
	ResidencyEvent,
	ResidencyLimits,
	ResidencyResult,
	ResidencyState,
	ResidentRuntime,
	RuntimeIdentity,
	RuntimeWork,
	TerminalRuntimeState,
	WorkDisposition,
} from "./types.ts";
import { DEFAULT_RESIDENCY_LIMITS, RESIDENCY_VERSION } from "./types.ts";

const EMPTY_WORK: RuntimeWork = {
	activeWorkIds: [],
	pendingUiIds: [],
	livenessPinKeys: [],
	detached: "none",
};

function success<T>(value: T): ResidencyResult<T> {
	return { ok: true, value };
}

function failure<T>(
	code: ResidencyError["code"],
	message: string,
	disposition?: WorkDisposition,
): ResidencyResult<T> {
	return {
		ok: false,
		error: {
			code,
			message,
			...(disposition ? { disposition } : {}),
		},
	};
}

function validId(value: unknown, max = 512): value is string {
	return (
		typeof value === "string" && value.length > 0 && value.length <= max && !value.includes("\0")
	);
}

function validGeneration(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function validIdentity(identity: unknown): identity is RuntimeIdentity {
	if (!identity || typeof identity !== "object") return false;
	const candidate = identity as RuntimeIdentity;
	return validId(candidate.sessionKey) && validGeneration(candidate.generation);
}

function validLimit(value: unknown): value is number {
	return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

function validateLimits(limits: ResidencyLimits): void {
	if (
		!validLimit(limits.maxResidents) ||
		!validLimit(limits.maxLaunching) ||
		!validLimit(limits.maxActiveWork) ||
		!validLimit(limits.maxPendingUiPerResident) ||
		!validLimit(limits.maxLivenessPinsPerResident) ||
		!validLimit(limits.maxTerminalRecords) ||
		limits.maxLaunching > limits.maxResidents
	)
		throw new TypeError("Residency limits are invalid");
}

function sameIdentity(left: RuntimeIdentity, right: RuntimeIdentity): boolean {
	return left.sessionKey === right.sessionKey && left.generation === right.generation;
}

function findResident(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidentRuntime | undefined {
	return state.residents.find((resident) => resident.sessionKey === identity.sessionKey);
}

function identityError(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyError | undefined {
	if (!validIdentity(identity))
		return { code: "invalid-request", message: "Runtime identity is invalid" };
	const resident = findResident(state, identity);
	if (!resident) return { code: "unknown-resident", message: "Runtime is not resident" };
	if (resident.generation !== identity.generation)
		return { code: "stale-generation", message: "Runtime belongs to an old child generation" };
}

function residentForIdentity(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidentRuntime> {
	const error = identityError(state, identity);
	if (error) return { ok: false, error };
	const resident = findResident(state, identity);
	return resident ? success(resident) : failure("unknown-resident", "Runtime is not resident");
}

function withResident(state: ResidencyState, replacement: ResidentRuntime): ResidencyState {
	return {
		...state,
		residents: state.residents.map((resident) =>
			resident.sessionKey === replacement.sessionKey ? replacement : resident,
		),
	};
}

function withWork(resident: ResidentRuntime, work: RuntimeWork): ResidentRuntime {
	return { ...resident, work: { ...work } };
}

function emptyState(limits: ResidencyLimits): ResidencyState {
	return { version: RESIDENCY_VERSION, limits: { ...limits }, residents: [], terminal: [] };
}

function terminalState(
	state: ResidencyState,
	identity: RuntimeIdentity,
	phase: TerminalRuntimeState["phase"],
	instruction?: string,
): ResidencyState {
	const completed: TerminalRuntimeState = {
		...identity,
		phase,
		...(instruction !== undefined ? { instruction } : {}),
	};
	return {
		...state,
		residents: state.residents.filter((resident) => !sameIdentity(resident, identity)),
		terminal: [...state.terminal, completed].slice(-state.limits.maxTerminalRecords),
	};
}

function validWorkId(value: unknown): value is string {
	return validId(value, 512);
}

function idleDisposition(): WorkDisposition {
	return { kind: "idle", releaseable: true, reason: "settled" };
}

/**
 * Classify detached/native work conservatively.  Unknown and uncertain work
 * are intentionally distinct from active work and both block release.
 */
export function classifyDetachedWork(status: DetachedWorkStatus): WorkDisposition {
	if (status === "unknown")
		return { kind: "unknown", releaseable: false, reason: "detached-unknown" };
	if (status === "uncertain")
		return { kind: "uncertain", releaseable: false, reason: "detached-uncertain" };
	if (status === "active") return { kind: "active", releaseable: false, reason: "detached-active" };
	return idleDisposition();
}

/** Decide whether a resident has a safe, fully settled release disposition. */
export function classifyRuntimeWork(work: RuntimeWork): WorkDisposition {
	const detached = classifyDetachedWork(work.detached);
	if (detached.kind !== "idle") return detached;
	if (work.activeWorkIds.length > 0)
		return { kind: "active", releaseable: false, reason: "active-work" };
	if (work.pendingUiIds.length > 0)
		return { kind: "pinned", releaseable: false, reason: "pending-ui" };
	if (work.livenessPinKeys.length > 0)
		return { kind: "pinned", releaseable: false, reason: "liveness-pin" };
	return idleDisposition();
}

export const detachedWorkDisposition = classifyDetachedWork;
export const runtimeWorkDisposition = classifyRuntimeWork;

/** Return bounded counts without mutating or inspecting native state. */
export function residencyUsage(state: ResidencyState): CapacityUsage {
	return {
		residents: state.residents.length,
		launching: state.residents.filter((resident) => resident.phase === "launching").length,
		activeWork: state.residents.reduce(
			(count, resident) => count + resident.work.activeWorkIds.length,
			0,
		),
	};
}

export const getResidencyUsage = residencyUsage;

export function createResidencyState(
	limits: ResidencyLimits = DEFAULT_RESIDENCY_LIMITS,
): ResidencyState {
	validateLimits(limits);
	return emptyState(limits);
}

function beginLaunch(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	if (!validIdentity(identity)) return failure("invalid-request", "Runtime identity is invalid");
	if (findResident(state, identity))
		return failure("duplicate-resident", "Runtime is already resident");
	const usage = residencyUsage(state);
	if (usage.residents >= state.limits.maxResidents)
		return failure("resident-capacity", "Managed runtime resident capacity is full");
	if (usage.launching >= state.limits.maxLaunching)
		return failure("launch-capacity", "Managed runtime launch capacity is full");
	const resident: ResidentRuntime = { ...identity, phase: "launching", work: { ...EMPTY_WORK } };
	return success({ ...state, residents: [...state.residents, resident] });
}

function completeLaunch(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	const residentResult = residentForIdentity(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.phase !== "launching")
		return failure("invalid-state", "Runtime launch is not pending");
	return success(withResident(state, { ...resident, phase: "resident" }));
}

function failLaunch(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	const residentResult = residentForIdentity(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.phase !== "launching")
		return failure("invalid-state", "Only a pending launch can fail");
	return success({
		...state,
		residents: state.residents.filter((candidate) => !sameIdentity(candidate, identity)),
	});
}

function residentForWork(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidentRuntime> {
	const residentResult = residentForIdentity(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.phase !== "resident")
		return failure("invalid-state", "Runtime is not accepting work in its current state");
	return success(resident);
}

function admitWork(
	state: ResidencyState,
	identity: RuntimeIdentity,
	workId: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(workId)) return failure("invalid-request", "Active work identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.work.activeWorkIds.includes(workId))
		return failure("duplicate-work", "Active work is already admitted");
	if (residencyUsage(state).activeWork >= state.limits.maxActiveWork)
		return failure("active-capacity", "Active managed-work capacity is full");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				activeWorkIds: [...resident.work.activeWorkIds, workId],
			}),
		),
	);
}

function settleWork(
	state: ResidencyState,
	identity: RuntimeIdentity,
	workId: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(workId)) return failure("invalid-request", "Active work identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (!resident.work.activeWorkIds.includes(workId))
		return failure("unknown-work", "Active work is not admitted");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				activeWorkIds: resident.work.activeWorkIds.filter((candidate) => candidate !== workId),
			}),
		),
	);
}

function setDetached(
	state: ResidencyState,
	identity: RuntimeIdentity,
	detached: DetachedWorkStatus,
): ResidencyResult<ResidencyState> {
	if (!["none", "active", "unknown", "uncertain"].includes(detached))
		return failure("invalid-request", "Detached-work disposition is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	return success(withResident(state, withWork(resident, { ...resident.work, detached })));
}

function addPendingUi(
	state: ResidencyState,
	identity: RuntimeIdentity,
	requestId: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(requestId)) return failure("invalid-request", "Pending UI identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.work.pendingUiIds.includes(requestId))
		return failure("duplicate-pin", "Pending UI request is already pinned");
	if (resident.work.pendingUiIds.length >= state.limits.maxPendingUiPerResident)
		return failure("active-capacity", "Pending UI capacity is full for this runtime");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				pendingUiIds: [...resident.work.pendingUiIds, requestId],
			}),
		),
	);
}

function removePendingUi(
	state: ResidencyState,
	identity: RuntimeIdentity,
	requestId: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(requestId)) return failure("invalid-request", "Pending UI identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (!resident.work.pendingUiIds.includes(requestId))
		return failure("unknown-pin", "Pending UI request is not pinned");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				pendingUiIds: resident.work.pendingUiIds.filter((id) => id !== requestId),
			}),
		),
	);
}

function addLivenessPin(
	state: ResidencyState,
	identity: RuntimeIdentity,
	key: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(key)) return failure("invalid-request", "Liveness pin identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.work.livenessPinKeys.includes(key))
		return failure("duplicate-pin", "Liveness pin is already held");
	if (resident.work.livenessPinKeys.length >= state.limits.maxLivenessPinsPerResident)
		return failure("active-capacity", "Liveness-pin capacity is full for this runtime");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				livenessPinKeys: [...resident.work.livenessPinKeys, key],
			}),
		),
	);
}

function removeLivenessPin(
	state: ResidencyState,
	identity: RuntimeIdentity,
	key: string,
): ResidencyResult<ResidencyState> {
	if (!validWorkId(key)) return failure("invalid-request", "Liveness pin identity is invalid");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (!resident.work.livenessPinKeys.includes(key))
		return failure("unknown-pin", "Liveness pin is not held");
	return success(
		withResident(
			state,
			withWork(resident, {
				...resident.work,
				livenessPinKeys: resident.work.livenessPinKeys.filter((candidate) => candidate !== key),
			}),
		),
	);
}

function beginRelease(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	const disposition = classifyRuntimeWork(resident.work);
	if (!disposition.releaseable)
		return failure(
			"not-idle",
			"Runtime has work or a liveness pin and cannot be released",
			disposition,
		);
	return success(withResident(state, { ...resident, phase: "releasing" }));
}

function completeRelease(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	const residentResult = residentForIdentity(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.phase !== "releasing")
		return failure("invalid-state", "Runtime release is not pending");
	const disposition = classifyRuntimeWork(resident.work);
	if (!disposition.releaseable)
		return failure("not-idle", "Runtime acquired work before release completed", disposition);
	return success(terminalState(state, identity, "released"));
}

function validInstruction(value: unknown): value is string {
	return (
		typeof value === "string" &&
		value.trim().length > 0 &&
		value.length <= 4096 &&
		!value.includes("\0")
	);
}

function beginHandoff(
	state: ResidencyState,
	identity: RuntimeIdentity,
	instruction: string,
): ResidencyResult<ResidencyState> {
	if (!validInstruction(instruction))
		return failure("invalid-handoff", "TUI handoff requires a bounded resume instruction");
	const residentResult = residentForWork(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	const disposition = classifyRuntimeWork(resident.work);
	if (!disposition.releaseable)
		return failure(
			"not-idle",
			"Runtime has work or a liveness pin and cannot be handed to the TUI",
			disposition,
		);
	return success(
		withResident(state, { ...resident, phase: "handoff-pending", handoffInstruction: instruction }),
	);
}

function completeHandoff(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	const residentResult = residentForIdentity(state, identity);
	if (!residentResult.ok) return residentResult;
	const resident = residentResult.value;
	if (resident.phase !== "handoff-pending")
		return failure("invalid-state", "TUI handoff is not pending");
	const disposition = classifyRuntimeWork(resident.work);
	if (!disposition.releaseable)
		return failure("not-idle", "Runtime acquired work before TUI handoff completed", disposition);
	// The instruction is kept on the pending resident so a pure transition can
	// carry it to the terminal record without invoking a process or a TUI.
	return success(terminalState(state, identity, "released-to-tui", resident.handoffInstruction));
}

/**
 * Apply one immutable residency event.  Admission and release are separate
 * transitions: callers must record process launch/termination explicitly.
 */
export function transitionResidency(
	state: ResidencyState,
	event: ResidencyEvent,
): ResidencyResult<ResidencyState> {
	if (!state || state.version !== RESIDENCY_VERSION || !event || typeof event !== "object")
		return failure("invalid-request", "Residency state or event is invalid");
	switch (event.type) {
		case "launch.begin":
			return beginLaunch(state, event.identity);
		case "launch.complete":
			return completeLaunch(state, event.identity);
		case "launch.fail":
			return failLaunch(state, event.identity);
		case "work.admit":
			return admitWork(state, event.identity, event.workId);
		case "work.settle":
			return settleWork(state, event.identity, event.workId);
		case "work.detached":
			return setDetached(state, event.identity, event.disposition);
		case "ui.pin":
			return addPendingUi(state, event.identity, event.requestId);
		case "ui.unpin":
			return removePendingUi(state, event.identity, event.requestId);
		case "liveness.pin":
			return addLivenessPin(state, event.identity, event.key);
		case "liveness.unpin":
			return removeLivenessPin(state, event.identity, event.key);
		case "runtime.release.begin":
			return beginRelease(state, event.identity);
		case "runtime.release.complete":
			return completeRelease(state, event.identity);
		case "tui.handoff.begin":
			return beginHandoff(state, event.identity, event.instruction);
		case "tui.handoff.complete":
			return completeHandoff(state, event.identity);
	}
}

export const applyResidency = transitionResidency;

export function lastTerminalState(state: ResidencyState): TerminalRuntimeState | undefined {
	return state.terminal[state.terminal.length - 1];
}

export const latestTerminalState = lastTerminalState;

export function isRuntimeIdle(runtime: ResidentRuntime): boolean {
	return classifyRuntimeWork(runtime.work).releaseable;
}

export function releaseIdleRuntime(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	return transitionResidency(state, { type: "runtime.release.begin", identity });
}

export function completeIdleRuntimeRelease(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	return transitionResidency(state, { type: "runtime.release.complete", identity });
}

export const beginRuntimeRelease = releaseIdleRuntime;
export const finishRuntimeRelease = completeIdleRuntimeRelease;

export function releaseToTui(
	state: ResidencyState,
	identity: RuntimeIdentity,
	instruction: string,
): ResidencyResult<ResidencyState> {
	return transitionResidency(state, { type: "tui.handoff.begin", identity, instruction });
}

export function completeTuiHandoff(
	state: ResidencyState,
	identity: RuntimeIdentity,
): ResidencyResult<ResidencyState> {
	return transitionResidency(state, { type: "tui.handoff.complete", identity });
}

export const beginTuiHandoff = releaseToTui;
export const finishTuiHandoff = completeTuiHandoff;

export const admitResident = (state: ResidencyState, identity: RuntimeIdentity) =>
	transitionResidency(state, { type: "launch.begin", identity });
export const completeResidentAdmission = (state: ResidencyState, identity: RuntimeIdentity) =>
	transitionResidency(state, { type: "launch.complete", identity });
