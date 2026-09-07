import { expect, test } from "bun:test";
import {
	createUiBridge,
	DEFAULT_UI_TIMEOUT_MS,
	MAX_PENDING_UI_REQUESTS,
} from "../../../pi/pixie-assistant/src/extensions/ui-bridge";

test("unknown callers can sequence every primitive without a tool identity or answer adapter", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("a", (event) => events.push(event));
	const answers: unknown[] = [];
	const run = (async () => {
		answers.push(await bridge.ui.select("Choose", [" exact offered value "]));
		answers.push(await bridge.ui.confirm("Proceed", "Really?"));
		answers.push(await bridge.ui.input("Text"));
		answers.push(await bridge.ui.editor("Edit", "initial"));
		bridge.ui.notify("Saved");
	})();
	for (const [primitive, value] of [
		["select", " exact offered value "],
		["confirm", false],
		["input", " typed "],
		["editor", "line one\nline two"],
	] as const) {
		await Bun.sleep(0);
		const [request] = bridge.pendingRequests();
		expect(request?.primitive).toBe(primitive);
		expect(request).not.toHaveProperty("toolCallId");
		if (!request) throw new Error("Missing pending request");
		const requestId = request.requestId;
		bridge.republishPending();
		expect(bridge.pendingCount()).toBe(1);
		expect(bridge.resolve({ sessionId: "b", requestId, value }).ok).toBe(false);
		if (primitive === "select")
			expect(bridge.resolve({ sessionId: "a", requestId, value: "exact offered value" }).ok).toBe(
				false,
			);
		expect(bridge.resolve({ sessionId: "a", requestId, value }).ok).toBe(true);
		expect(bridge.resolve({ sessionId: "a", requestId, value }).ok).toBe(false);
	}
	await run;
	expect(answers).toEqual([" exact offered value ", false, " typed ", "line one\nline two"]);
	expect(events.at(-1)).toMatchObject({ type: "pixie:ui:notify", message: "Saved" });
	bridge.dispose();
});

test("pending requests and timeouts are bounded and invalid input does not strand promises", async () => {
	const bridge = createUiBridge("a", () => {});
	const pending = Array.from({ length: MAX_PENDING_UI_REQUESTS }, () =>
		bridge.ui.input("Text", "", { timeout: Number.MAX_SAFE_INTEGER }),
	);
	expect(bridge.pendingRequests()[0]?.timeout).toBe(DEFAULT_UI_TIMEOUT_MS);
	await expect(bridge.ui.editor("overflow")).rejects.toThrow("Too many pending");
	bridge.cancelAll();
	expect(await Promise.all(pending)).toEqual(Array(MAX_PENDING_UI_REQUESTS).fill(undefined));
	await expect(bridge.ui.select("Empty", [])).rejects.toThrow("limits");
	expect(bridge.pendingCount()).toBe(0);
	const confirm = bridge.ui.confirm("Timeout", "", { timeout: 5 });
	expect(await confirm).toBe(false);
	bridge.dispose();
});

test("Stop dismisses a sequential questionnaire rather than opening its next request", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("a", (event) => events.push(event));
	const run = (async () => [
		await bridge.ui.input("first"),
		await bridge.ui.confirm("second", ""),
		await bridge.ui.editor("third"),
	])();
	bridge.interrupt();
	expect(await run).toEqual([undefined, false, undefined]);
	expect(events.filter((event) => event.type === "pixie:ui:request")).toHaveLength(1);
	bridge.beginRun();
	const next = bridge.ui.input("new run");
	expect(bridge.pendingCount()).toBe(1);
	bridge.dispose();
	expect(await next).toBeUndefined();
});

test("widgets and status are bounded, clears survive flooding, and disposed callbacks cannot revive state", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("a", (event) => events.push(event));
	for (let i = 0; i < 1000; i++) {
		bridge.ui.setWidget(`w${i}`, Array(100).fill(`<script>${"x".repeat(4000)}`), {
			placement: "belowEditor",
		});
		bridge.ui.setStatus(`s${i}`, "x".repeat(4000));
	}
	expect(events.filter((event) => event.type === "pixie:ui:widget")).toHaveLength(16);
	expect(events[0]?.lines).toHaveLength(32);
	expect((events[0]?.lines as string[])[0]).toHaveLength(2000);
	expect(events[0]?.placement).toBe("belowEditor");
	for (let i = 0; i < 1000; i++) bridge.ui.setWorkingMessage("flood");
	expect(events.length).toBeLessThanOrEqual(64);
	bridge.ui.setWidget("w0", undefined);
	expect(events.at(-1)).toMatchObject({ type: "pixie:ui:widget", key: "w0" });
	expect(events.at(-1)).not.toHaveProperty("lines");
	bridge.ui.setStatus("s0", undefined);
	expect(events.at(-1)).not.toHaveProperty("text");
	bridge.dispose();
	const count = events.length;
	bridge.ui.setWidget("stale", ["stale"]);
	bridge.ui.setTitle("stale");
	bridge.ui.notify("stale");
	expect(await bridge.ui.input("stale")).toBeUndefined();
	expect(events).toHaveLength(count);
	expect(events.at(-2)).toMatchObject({ type: "pixie:ui:title", title: "" });
	expect(events.at(-1)).toMatchObject({ type: "pixie:ui:working" });
});

test("unsupported TUI and composer methods report once and never execute factories", async () => {
	const events: Record<string, unknown>[] = [];
	const bridge = createUiBridge("a", (event) => events.push(event));
	let called = false;
	expect(
		await bridge.ui.custom(() => {
			called = true;
			throw new Error("must not run");
		}),
	).toBeUndefined();
	bridge.ui.setEditorText("replace");
	bridge.ui.setEditorText("replace again");
	bridge.ui.pasteToEditor("paste");
	expect(bridge.ui.getEditorText()).toBe("");
	expect(called).toBe(false);
	expect(events).toHaveLength(4);
	expect(
		events.every(
			(event) => event.type === "pixie:ui:notify" && String(event.message).includes("unsupported"),
		),
	).toBe(true);
	bridge.dispose();
});
