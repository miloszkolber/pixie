import { expect, test } from "bun:test";
import type { ResidencyState, RuntimeIdentity } from "../../../assistant/src/residency/index.ts";
import {
	classifyRuntimeWork,
	completeIdleRuntimeRelease,
	completeTuiHandoff,
	createResidencyState,
	lastTerminalState,
	releaseIdleRuntime,
	releaseToTui,
	residencyUsage,
	transitionResidency,
} from "../../../assistant/src/residency/index.ts";

const identity = (sessionKey: string, generation = 0): RuntimeIdentity => ({
	sessionKey,
	generation,
});

function apply(
	state: ResidencyState,
	event: Parameters<typeof transitionResidency>[1],
): ResidencyState {
	const result = transitionResidency(state, event);
	if (!result.ok) throw new Error(result.error.message);
	return result.value;
}

function resident(state: ResidencyState, value = identity("session-1")): ResidencyState {
	const next = apply(state, { type: "launch.begin", identity: value });
	return apply(next, { type: "launch.complete", identity: value });
}

test("capacity admission is bounded before launch and active work", () => {
	let state = createResidencyState({
		maxResidents: 2,
		maxLaunching: 1,
		maxActiveWork: 1,
		maxPendingUiPerResident: 16,
		maxLivenessPinsPerResident: 16,
		maxTerminalRecords: 2,
	});
	const first = identity("one");
	const second = identity("two");
	state = apply(state, { type: "launch.begin", identity: first });
	expect(transitionResidency(state, { type: "launch.begin", identity: second })).toMatchObject({
		ok: false,
		error: { code: "launch-capacity" },
	});
	state = apply(state, { type: "launch.complete", identity: first });
	state = apply(state, { type: "work.admit", identity: first, workId: "run-1" });
	expect(residencyUsage(state)).toEqual({ residents: 1, launching: 0, activeWork: 1 });
	expect(
		transitionResidency(state, { type: "work.admit", identity: first, workId: "run-2" }),
	).toMatchObject({
		ok: false,
		error: { code: "active-capacity" },
	});
	state = apply(state, { type: "work.settle", identity: first, workId: "run-1" });
	state = apply(state, { type: "launch.begin", identity: second });
	expect(residencyUsage(state)).toMatchObject({ residents: 2, launching: 1 });
	expect(
		transitionResidency(state, { type: "launch.begin", identity: identity("three") }),
	).toMatchObject({
		ok: false,
		error: { code: "resident-capacity" },
	});
});

test("unknown, uncertain, active and pinned work are never reported idle", () => {
	expect(
		classifyRuntimeWork({
			activeWorkIds: [],
			pendingUiIds: [],
			livenessPinKeys: [],
			detached: "unknown",
		}),
	).toMatchObject({
		kind: "unknown",
		releaseable: false,
	});
	expect(
		classifyRuntimeWork({
			activeWorkIds: [],
			pendingUiIds: [],
			livenessPinKeys: [],
			detached: "uncertain",
		}),
	).toMatchObject({
		kind: "uncertain",
		releaseable: false,
	});
	expect(
		classifyRuntimeWork({
			activeWorkIds: ["run"],
			pendingUiIds: [],
			livenessPinKeys: [],
			detached: "none",
		}),
	).toMatchObject({
		kind: "active",
		releaseable: false,
	});
	expect(
		classifyRuntimeWork({
			activeWorkIds: [],
			pendingUiIds: ["dialog"],
			livenessPinKeys: [],
			detached: "none",
		}),
	).toMatchObject({
		kind: "pinned",
		releaseable: false,
	});
});

test("idle release uses explicit begin/complete states and frees a resident slot", () => {
	let state = resident(createResidencyState());
	const id = identity("session-1");
	state = apply(state, { type: "runtime.release.begin", identity: id });
	expect(state.residents[0]).toMatchObject({ phase: "releasing" });
	state = apply(state, { type: "runtime.release.complete", identity: id });
	expect(state.residents).toHaveLength(0);
	expect(lastTerminalState(state)).toMatchObject({ sessionKey: id.sessionKey, phase: "released" });
});

test("release never forces active or unknown work, and TUI handoff is separate", () => {
	let state = resident(createResidencyState());
	const id = identity("session-1");
	state = apply(state, { type: "work.admit", identity: id, workId: "run-1" });
	const active = releaseIdleRuntime(state, id);
	expect(active).toMatchObject({
		ok: false,
		error: { code: "not-idle", disposition: { kind: "active" } },
	});
	state = apply(state, { type: "work.settle", identity: id, workId: "run-1" });
	state = apply(state, { type: "work.detached", identity: id, disposition: "unknown" });
	expect(releaseIdleRuntime(state, id)).toMatchObject({
		ok: false,
		error: { code: "not-idle", disposition: { kind: "unknown" } },
	});
	state = apply(state, { type: "work.detached", identity: id, disposition: "none" });
	const handoff = releaseToTui(state, id, "Resume session-1 in the native TUI");
	if (!handoff.ok) throw new Error(handoff.error.message);
	state = handoff.value;
	expect(state.residents[0]).toMatchObject({ phase: "handoff-pending" });
	const completed = completeTuiHandoff(state, id);
	if (!completed.ok) throw new Error(completed.error.message);
	state = completed.value;
	expect(state.residents).toHaveLength(0);
	expect(lastTerminalState(state)).toMatchObject({
		phase: "released-to-tui",
		instruction: "Resume session-1 in the native TUI",
	});
});

test("late generation callbacks cannot release a newer runtime", () => {
	const state = resident(createResidencyState(), identity("session-1", 2));
	expect(completeIdleRuntimeRelease(state, identity("session-1", 1))).toMatchObject({
		ok: false,
		error: { code: "stale-generation" },
	});
});
