import { afterEach, expect, test } from "bun:test";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { createUiBridge } from "../../../pi/host/src/extensions/ui-bridge.ts";
import { Sessions } from "../../../pi/host/src/sessions.ts";
import { cleanups, findTool, fixture } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

function publishedUiEvents(events: unknown[]): Record<string, any>[] {
	// The fixture pushes raw session events (not { id, event } envelopes).
	return (events as Record<string, any>[]).filter(
		(event) => typeof event?.type === "string" && event.type.startsWith("pixie:ui:"),
	);
}

// A probe tool that answers through whatever ctx.ui the host binds. It proves
// the bridge is the live RPC UI context without depending on any upstream
// extension.
function probeExtension(seen: { mode?: string; hasUI?: boolean }): ExtensionFactory {
	return (pi) => {
		pi.registerTool({
			name: "probe_dialog",
			label: "Probe dialog",
			description: "Answer one select through the bound UI context.",
			parameters: Type.Object({}),
			execute: async (_id, _params, _signal, _onUpdate, ctx) => {
				seen.mode = ctx.mode;
				seen.hasUI = ctx.hasUI;
				const answer = await ctx.ui.select("Pick?", ["alpha", "beta"]);
				return {
					content: [{ type: "text", text: `answer=${answer}` }],
					details: { answer: answer ?? null },
				};
			},
		});
	};
}

test("all five primitives publish scoped requests with payloads", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-a", (event) => published.push(event));
	const answers = [
		bridge.ui.select("Choose", ["a", "b"]),
		bridge.ui.confirm("Sure?", "Proceed?"),
		bridge.ui.input("Name?", "placeholder"),
		bridge.ui.editor("Notes", "prefill"),
	];
	bridge.ui.notify("hello", "warning");
	expect(bridge.pendingCount()).toBe(4);
	const requests = published.filter((event) => event.type === "pixie:ui:request");
	expect(requests).toHaveLength(4);
	expect(requests.map((event) => event.primitive)).toEqual([
		"select",
		"confirm",
		"input",
		"editor",
	]);
	for (const event of requests) {
		expect(event.sessionId).toBe("session-a");
		expect(event.requestId).toBeString();
	}
	expect(new Set(requests.map((event) => event.requestId as string)).size).toBe(4);
	expect(requests[0]).toMatchObject({ title: "Choose", options: ["a", "b"] });
	expect(requests[1]).toMatchObject({ title: "Sure?", message: "Proceed?" });
	expect(requests[2]).toMatchObject({ title: "Name?", placeholder: "placeholder" });
	expect(requests[3]).toMatchObject({ title: "Notes", prefill: "prefill" });
	const notified = published.filter((event) => event.type === "pixie:ui:notify");
	expect(notified).toEqual([
		{ type: "pixie:ui:notify", sessionId: "session-a", message: "hello", level: "warning" },
	]);
	expect(bridge.pendingCount()).toBe(4);
	for (const request of requests) bridge.cancel(request.requestId as string);
	expect(await Promise.all(answers)).toEqual([undefined, false, undefined, undefined]);
	expect(bridge.pendingCount()).toBe(0);
});

test("responses settle each primitive and stay single-use", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-a", (event) => published.push(event));
	const pending = [
		bridge.ui.select("Choose", ["a"]),
		bridge.ui.confirm("Sure?", "Proceed?"),
		bridge.ui.input("Name?"),
		bridge.ui.editor("Notes"),
	];
	const ids = published
		.filter((event) => event.type === "pixie:ui:request")
		.map((event) => (event as { requestId: string }).requestId);
	expect(ids).toHaveLength(4);
	expect(bridge.resolve({ sessionId: "session-a", requestId: ids[0], value: "a" })).toEqual({
		ok: true,
	});
	expect(bridge.resolve({ sessionId: "session-a", requestId: ids[1], value: true })).toEqual({
		ok: true,
	});
	expect(bridge.resolve({ sessionId: "session-a", requestId: ids[2], value: "typed" })).toEqual({
		ok: true,
	});
	expect(bridge.resolve({ sessionId: "session-a", requestId: ids[3], cancelled: true })).toEqual({
		ok: true,
	});
	expect(await Promise.all(pending)).toEqual(["a", true, "typed", undefined]);
	expect(bridge.pendingCount()).toBe(0);
});

test("foreign sessions and replayed responses never satisfy a dialog", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-a", (event) => published.push(event));
	const pending = bridge.ui.select("Choose", ["a"]);
	const requestId = (published[0] as { requestId: string }).requestId;
	expect(bridge.resolve({ sessionId: "session-b", requestId, value: "a" })).toMatchObject({
		ok: false,
	});
	expect(
		bridge.resolve({ sessionId: "session-a", requestId: "missing", value: "a" }),
	).toMatchObject({
		ok: false,
	});
	expect(bridge.pendingCount()).toBe(1);
	expect(bridge.resolve({ sessionId: "session-a", requestId, value: "a" })).toEqual({ ok: true });
	expect(await pending).toBe("a");
	// Replaying the same answer settles nothing twice.
	expect(bridge.resolve({ sessionId: "session-a", requestId, value: "a" })).toMatchObject({
		ok: false,
	});
});

test("abort dismisses the dialog, publishes cancellation, and blocks replays", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-a", (event) => published.push(event));
	const controller = new AbortController();
	const pending = bridge.ui.input("Name?", undefined, { signal: controller.signal });
	const requestId = (
		published.find((event) => event.type === "pixie:ui:request") as { requestId: string }
	).requestId;
	controller.abort();
	expect(await pending).toBeUndefined();
	expect(published).toContainEqual({
		type: "pixie:ui:cancel",
		sessionId: "session-a",
		requestId,
		reason: "aborted",
	});
	expect(bridge.resolve({ sessionId: "session-a", requestId, value: "late" })).toMatchObject({
		ok: false,
	});
	bridge.cancelAll();
});

test("timeouts dismiss the dialog and publish cancellation", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-a", (event) => published.push(event));
	const pending = bridge.ui.select("Choose", ["a"], { timeout: 20 });
	const requestId = (
		published.find((event) => event.type === "pixie:ui:request") as { requestId: string }
	).requestId;
	expect(await pending).toBeUndefined();
	expect(published).toContainEqual({
		type: "pixie:ui:cancel",
		sessionId: "session-a",
		requestId,
		reason: "timeout",
	});
	expect(bridge.pendingCount()).toBe(0);
});

test("host sessions bind the bridge as the rpc ui context", async () => {
	const seen: { mode?: string; hasUI?: boolean } = {};
	const { dir, sessions, events } = await fixture([probeExtension(seen)]);
	const entry = await sessions.create(dir);
	const tool = findTool(entry, "probe_dialog");
	const pending = tool.execute("probe-1", {}, new AbortController().signal);
	const request = await waitForUiRequest(events);
	expect(seen).toMatchObject({ mode: "rpc", hasUI: true });
	const settled = await sessions.call("session.uiResponse", {
		sessionId: entry.session.sessionId,
		requestId: request.requestId,
		value: "beta",
	});
	expect(settled).toMatchObject({ ok: true });
	const result = await pending;
	expect(result.details).toMatchObject({ answer: "beta" });
});

test("host resolves stay session-bound across tabs", async () => {
	const { dir, sessions, events } = await fixture([probeExtension({})]);
	const first = await sessions.create(dir);
	const second = await sessions.create(dir);
	const tool = findTool(first, "probe_dialog");
	const pending = tool.execute("probe-cross", {}, new AbortController().signal);
	const request = await waitForUiRequest(events);
	await expect(
		sessions.call("session.uiResponse", {
			sessionId: second.session.sessionId,
			requestId: request.requestId,
			value: "alpha",
		}),
	).rejects.toThrow(/another session|no longer awaiting|unknown or settled/i);
	const settled = await sessions.call("session.uiResponse", {
		sessionId: first.session.sessionId,
		requestId: request.requestId,
		value: "alpha",
	});
	expect(settled).toMatchObject({ ok: true });
	expect((await pending).details).toMatchObject({ answer: "alpha" });
});

test("session cancel dismisses an awaiting dialog", async () => {
	const { dir, sessions, events } = await fixture([probeExtension({})]);
	const entry = await sessions.create(dir);
	const tool = findTool(entry, "probe_dialog");
	const pending = tool.execute("probe-cancel", {}, new AbortController().signal);
	await waitForUiRequest(events);
	await sessions.call("session.cancel", { sessionId: entry.session.sessionId });
	expect((await pending).details).toMatchObject({ answer: null });
	const uiEvents = publishedUiEvents(events);
	expect(uiEvents.some((event) => event.type === "pixie:ui:cancel")).toBe(true);
});

async function waitForUiRequest(
	events: unknown[],
): Promise<{ requestId: string; options: string[] }> {
	for (let attempt = 0; attempt < 200; attempt++) {
		const found = publishedUiEvents(events).find((event) => event.type === "pixie:ui:request");
		if (found) return found as { requestId: string; options: string[] };
		await Bun.sleep(10);
	}
	throw new Error("UI request was never published");
}
