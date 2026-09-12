import { expect, test } from "bun:test";
import {
	carryOwnership,
	dismissedValue,
	isOfferedOption,
	isValidFinalValue,
	mapFinalResponse,
	NATIVE_UI_BOUNDS,
	NATIVE_UI_ROWS,
	nativeUiRow,
	SingleFinalResponse,
	UNSUPPORTED_UI_MEMBERS,
} from "../../../assistant/src/extensions/native-ui-mapping.ts";

test("every supported native UI row is mapped with its exact limitation and final-response mapping", () => {
	const ids = NATIVE_UI_ROWS.map((row) => row.id);
	expect(ids).toEqual([
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
	]);
	for (const row of NATIVE_UI_ROWS) {
		expect(row.nativeMethod.length).toBeGreaterThan(0);
		expect(row.limitation.length).toBeGreaterThan(0);
		expect(row.finalMapping.length).toBeGreaterThan(0);
		expect(row.support).not.toBe("unsupported");
	}
	// Spot-check exact limitations that must stay visible.
	expect(nativeUiRow("select").limitation).toContain("matched exactly");
	expect(nativeUiRow("confirm").finalMapping).toContain("confirmed");
	expect(nativeUiRow("setWidget").limitation).toContain("string-array");
	expect(nativeUiRow("setWidget").limitation).toContain("factories are unsupported");
	expect(nativeUiRow("setTitle").limitation).toContain("Never renames");
	expect(nativeUiRow("setEditorText").limitation).toContain("unchanged empty originating draft");
	expect(nativeUiRow("pasteToEditor").limitation).toContain("set_editor_text");
	expect(NATIVE_UI_BOUNDS.maxPendingPerSession).toBe(16);
	expect(NATIVE_UI_BOUNDS.defaultTimeoutMs).toBe(30 * 60 * 1000);
});

test("terminal-only members stay explicitly unsupported and never execute", () => {
	for (const member of [
		"onTerminalInput",
		"custom",
		"getEditorText",
		"setEditorComponent",
		"setWorkingVisible",
	]) {
		expect(UNSUPPORTED_UI_MEMBERS).toContain(member);
	}
	expect(NATIVE_UI_ROWS.some((row) => UNSUPPORTED_UI_MEMBERS.includes(row.id))).toBe(false);
});

test("select matches offered values exactly while confirm uses the native boolean", () => {
	expect(isOfferedOption([" exact offered value "], " exact offered value ")).toBe(true);
	expect(isOfferedOption([" exact offered value "], "exact offered value")).toBe(false);
	expect(
		isValidFinalValue("select", " exact offered value ", {
			selectOptions: [" exact offered value "],
		}),
	).toBe(true);
	expect(
		isValidFinalValue("select", "exact offered value", {
			selectOptions: [" exact offered value "],
		}),
	).toBe(false);
	expect(isValidFinalValue("confirm", true)).toBe(true);
	expect(isValidFinalValue("confirm", "true")).toBe(false);
	expect(isValidFinalValue("input", "x".repeat(8001))).toBe(false);
	expect(dismissedValue("confirm")).toBe(false);
	expect(dismissedValue("select")).toBeUndefined();
	expect(dismissedValue("input")).toBeUndefined();
	expect(dismissedValue("editor")).toBeUndefined();
});

test("single final-response mapping settles once and rejects stale replies", () => {
	const tracker = new SingleFinalResponse({
		primitive: "select",
		selectOptions: ["alpha", "beta"],
	});
	const first = tracker.settle({ value: "beta" });
	expect(first).toMatchObject({
		accepted: true,
		settled: true,
		nativeValue: "beta",
		reason: "answer",
	});
	const stale = tracker.settle({ value: "alpha" });
	expect(stale.accepted).toBe(false);
	expect(stale.error).toContain("Already settled");
	expect(tracker.isSettled).toBe(true);

	const confirm = new SingleFinalResponse({ primitive: "confirm" });
	expect(confirm.settle({ value: "yes" as unknown as string }).accepted).toBe(false);
	expect(confirm.isSettled).toBe(false);
	expect(confirm.settle({ value: true })).toMatchObject({ accepted: true, nativeValue: true });
	expect(mapFinalResponse({ primitive: "input" }, { cancelled: true })).toMatchObject({
		settled: true,
		nativeValue: undefined,
		reason: "dismissed",
	});
	expect(mapFinalResponse({ primitive: "confirm" }, { error: "gone" })).toMatchObject({
		settled: true,
		nativeValue: false,
		reason: "dismissed",
	});
});

test("ownership preserves exact session, generation, and original deadline", () => {
	const owned = carryOwnership({
		requestId: "req-1",
		sessionId: "sess-1",
		childGeneration: 3,
		primitive: "input",
		timeoutMs: 60_000,
	});
	expect(owned).toMatchObject({ requestId: "req-1", sessionId: "sess-1", childGeneration: 3 });
	expect(() =>
		carryOwnership({
			requestId: "",
			sessionId: "sess-1",
			childGeneration: 0,
			primitive: "input",
			timeoutMs: 1000,
		}),
	).toThrow("request and session");
	expect(() =>
		carryOwnership({
			requestId: "req-1",
			sessionId: "sess-1",
			childGeneration: -1,
			primitive: "input",
			timeoutMs: 1000,
		}),
	).toThrow("child generation");
	expect(() =>
		carryOwnership({
			requestId: "req-1",
			sessionId: "sess-1",
			childGeneration: 0,
			primitive: "input",
			timeoutMs: NATIVE_UI_BOUNDS.defaultTimeoutMs + 1,
		}),
	).toThrow("original deadline");
});
