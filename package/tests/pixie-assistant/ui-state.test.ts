import { expect, test } from "bun:test";
import {
	applyPassiveEvent,
	applyPassiveReplay,
	createDraftProposal,
	createDraftState,
	createPassiveReplay,
	createPassiveState,
	describeNativeUiControl,
	evaluateDraftProposal,
	forwardCancellation,
	reportUnsupportedControl,
	resolveDraftConflict,
} from "../../../assistant/src/ui-state/index.ts";

test("passive state keeps latest values scoped to the exact session and generation", () => {
	let state = createPassiveState({ sessionId: "session-1", generation: 4 });
	const status = applyPassiveEvent(state, {
		type: "pixie:ui:status",
		sessionId: "session-1",
		generation: 4,
		sequence: 1,
		key: "phase",
		text: "working",
	});
	expect(status.accepted).toBe(true);
	if (!status.accepted) throw new Error("status was rejected");
	state = status.state;
	const widget = applyPassiveEvent(state, {
		type: "pixie:ui:widget",
		sessionId: "session-1",
		generation: 4,
		sequence: 2,
		key: "summary",
		lines: ["one", "two"],
	});
	expect(widget.accepted).toBe(true);
	if (!widget.accepted) throw new Error("widget was rejected");
	state = widget.state;
	const clear = applyPassiveEvent(state, {
		type: "pixie:ui:status",
		sessionId: "session-1",
		generation: 4,
		sequence: 3,
		key: "phase",
	});
	expect(clear.accepted).toBe(true);
	if (!clear.accepted) throw new Error("clear was rejected");
	state = clear.state;
	expect(state.statuses).toEqual({});
	expect(state.widgets.summary).toEqual(["one", "two"]);

	const foreign = applyPassiveEvent(state, {
		type: "pixie:ui:title",
		sessionId: "session-1",
		generation: 3,
		sequence: 4,
		title: "old child",
	});
	expect(foreign).toMatchObject({ accepted: false, reason: "stale-generation" });
	expect(foreign.state.title).toBe("");
	const otherSession = applyPassiveEvent(state, {
		type: "pixie:ui:title",
		sessionId: "session-2",
		generation: 4,
		sequence: 4,
		title: "foreign",
	});
	expect(otherSession).toMatchObject({ accepted: false, reason: "foreign-session" });
});

test("replay accepts a newer exact-generation snapshot and rejects stale replay", () => {
	let current = createPassiveState({ sessionId: "session-1", childGeneration: 2 });
	const accepted = applyPassiveEvent(current, {
		type: "pixie:ui:title",
		sessionId: "session-1",
		childGeneration: 2,
		sequence: 1,
		title: "latest",
	});
	if (!accepted.accepted) throw new Error("title was rejected");
	current = accepted.state;
	const replay = createPassiveReplay(current);
	const fresh = createPassiveState({ sessionId: "session-1", generation: 2 });
	const restored = applyPassiveReplay(fresh, replay);
	expect(restored).toMatchObject({ accepted: true, state: { title: "latest", sequence: 1 } });
	if (!restored.accepted) throw new Error("replay was rejected");
	const duplicate = applyPassiveReplay(restored.state, replay);
	expect(duplicate).toMatchObject({ accepted: false, reason: "stale-sequence" });
	const staleGeneration = applyPassiveReplay(
		createPassiveState({ sessionId: "session-1", generation: 3 }),
		replay,
	);
	expect(staleGeneration).toMatchObject({ accepted: false, reason: "stale-generation" });
});

test("native draft proposals auto-apply only to unchanged empty drafts", () => {
	const empty = createDraftState({ sessionId: "session-1", clientId: "browser-a", generation: 7 });
	const proposal = createDraftProposal({
		sessionId: "session-1",
		clientId: "browser-a",
		generation: 7,
		operation: "setEditorText",
		baseRevision: 0,
		originatingText: "",
		text: "native queue",
	});
	const applied = evaluateDraftProposal(empty, proposal);
	expect(applied).toMatchObject({
		accepted: true,
		outcome: "auto-applied",
		autoSubmitted: false,
		state: { text: "native queue", revision: 1 },
	});

	const occupied = createDraftState({
		sessionId: "session-1",
		clientId: "browser-a",
		generation: 7,
		text: "user text",
	});
	const conflict = evaluateDraftProposal(occupied, { ...proposal, baseRevision: 0 });
	expect(conflict).toMatchObject({
		accepted: false,
		outcome: "conflict",
		conflict: { reason: "non-empty-draft", autoSubmitted: false },
	});
	if (conflict.outcome !== "conflict") throw new Error("proposal was not made explicit");
	expect(conflict.conflict.choices).toEqual(["insert", "replace", "dismiss"]);
	const inserted = resolveDraftConflict(occupied, conflict.conflict, "insert");
	expect(inserted).toMatchObject({
		accepted: true,
		outcome: "inserted",
		autoSubmitted: false,
		state: { text: "user textnative queue", revision: 1 },
	});
	const replaced = resolveDraftConflict(occupied, conflict.conflict, "replace");
	expect(replaced).toMatchObject({
		accepted: true,
		outcome: "replaced",
		state: { text: "native queue", revision: 1 },
	});
});

test("draft conflicts and replays cannot cross client or generation boundaries", () => {
	const state = createDraftState({
		sessionId: "session-1",
		clientId: "browser-a",
		generation: 2,
		text: "local",
	});
	const proposal = createDraftProposal({
		sessionId: "session-1",
		clientId: "browser-a",
		generation: 2,
		operation: "pasteToEditor",
		baseRevision: 0,
		originatingText: "",
		text: "native",
	});
	const conflict = evaluateDraftProposal(state, proposal);
	if (conflict.outcome !== "conflict") throw new Error("expected explicit conflict");
	const newer = createDraftState({
		sessionId: "session-1",
		clientId: "browser-a",
		generation: 2,
		revision: 1,
		text: "changed",
	});
	expect(resolveDraftConflict(newer, conflict.conflict, "replace")).toMatchObject({
		accepted: false,
		reason: "stale-conflict",
	});
	expect(
		evaluateDraftProposal({ ...state, generation: 3, childGeneration: 3 }, proposal),
	).toMatchObject({ accepted: false, reason: "stale-generation" });
	expect(evaluateDraftProposal({ ...state, clientId: "browser-b" }, proposal)).toMatchObject({
		accepted: false,
		reason: "foreign-client",
	});
});

test("unsupported controls are diagnostics and cancellation never fabricates native success", () => {
	const unsupported = reportUnsupportedControl("custom");
	expect(unsupported).toMatchObject({ supported: false, executed: false, draftChanged: false });
	expect(unsupported.message).toContain("No composer draft was changed");
	expect(describeNativeUiControl("setWorkingMessage")).toMatchObject({
		support: "supported-with-limits",
		executes: false,
		nativeOutcome: "unknown",
	});
	const cancellation = forwardCancellation({
		sessionId: "session-1",
		generation: 9,
		requestId: "request-1",
		reason: "aborted",
	});
	expect(cancellation).toMatchObject({
		forwarded: true,
		nativeCancelled: "unknown",
		requestId: "request-1",
	});
	expect(cancellation).not.toHaveProperty("cancelled", true);
});
