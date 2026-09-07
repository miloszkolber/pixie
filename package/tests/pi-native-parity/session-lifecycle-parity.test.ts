import { afterEach, expect, test } from "bun:test";
import type { ExtensionFactory } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import {
	createUiBridge,
	UI_CANCEL_EVENT,
	UI_REQUEST_EVENT,
	UI_STATUS_EVENT,
	UI_TITLE_EVENT,
	UI_WIDGET_EVENT,
	UI_WORKING_EVENT,
} from "../../../assistant/src/extensions/ui-bridge.ts";
import { Sessions } from "../../../assistant/src/sessions.ts";
import { cleanups, echoProvider, fixture } from "./helpers.ts";

afterEach(async () => {
	for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});

function uiEvents(events: unknown[], type: string): Record<string, any>[] {
	return (events as Record<string, any>[]).filter((event) => event?.type === type);
}

function dialogExtension(primitive: "select" | "confirm" | "input" | "editor"): ExtensionFactory {
	return (pi) => {
		pi.registerTool({
			name: `probe_${primitive}`,
			label: `Probe ${primitive}`,
			description: `Answer one ${primitive} through the bound UI context.`,
			parameters: Type.Object({}),
			execute: async (_id, _params, _signal, _onUpdate, ctx) => {
				let answer: unknown;
				if (primitive === "select") answer = await ctx.ui.select("Pick?", ["alpha", "beta"]);
				else if (primitive === "confirm") answer = await ctx.ui.confirm("Sure?", "Proceed?");
				else if (primitive === "input") answer = await ctx.ui.input("Name?", "placeholder");
				else answer = await ctx.ui.editor("Notes", "prefill");
				return {
					content: [{ type: "text", text: `answer=${answer}` }],
					details: { answer: answer ?? null },
				};
			},
		});
	};
}

async function waitFor(events: unknown[], type: string): Promise<Record<string, any>> {
	for (let attempt = 0; attempt < 200; attempt++) {
		const found = uiEvents(events, type).at(-1);
		if (found) return found;
		await Bun.sleep(10);
	}
	throw new Error(`Event ${type} was never published`);
}

test("stop unwinds every blocking primitive with settled defaults", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-stop", (event) => published.push(event));
	const pending = Promise.all([
		bridge.ui.select("Choose", ["a"]),
		bridge.ui.confirm("Sure?", "Proceed?"),
		bridge.ui.input("Name?"),
		bridge.ui.editor("Notes"),
	]);
	expect(bridge.pendingCount()).toBe(4);
	bridge.cancelAll("cancelled");
	expect(await pending).toEqual([undefined, false, undefined, undefined]);
	expect(bridge.pendingCount()).toBe(0);
	expect(uiEvents(published, UI_CANCEL_EVENT)).toHaveLength(4);
});

test("pending dialogs survive session.load replay with stable request ids", async () => {
	const { dir, sessions, events } = await fixture([dialogExtension("select")]);
	const entry = await sessions.create(dir);
	const tool = (entry.session.agent.state.tools as any[]).find((t) => t.name === "probe_select");
	const pending = tool.execute("probe-replay", {}, new AbortController().signal);
	const first = await waitFor(events, UI_REQUEST_EVENT);
	const before = uiEvents(events, UI_REQUEST_EVENT).length;
	// A reconnect re-loads the session: unresolved requests re-publish with
	// the same IDs instead of orphaning the host call or the modal.
	const snapshot = (await sessions.call("session.load", {
		sessionId: entry.session.sessionId,
	})) as Record<string, any>;
	expect(snapshot.pendingDialogs).toHaveLength(1);
	expect(snapshot.pendingDialogs[0]).toMatchObject({
		requestId: first.requestId,
		primitive: "select",
		title: "Pick?",
	});
	expect(uiEvents(events, UI_REQUEST_EVENT).length).toBeGreaterThan(before);
	expect(uiEvents(events, UI_REQUEST_EVENT).at(-1)).toMatchObject({
		requestId: first.requestId,
	});
	await sessions.call("session.uiResponse", {
		sessionId: entry.session.sessionId,
		requestId: first.requestId,
		value: "alpha",
	});
	expect(((await pending) as any).details).toMatchObject({ answer: "alpha" });
	expect((sessions.snapshot(entry) as Record<string, any>).pendingDialogs).toHaveLength(0);
});

test("session.cancel dismisses input dialogs and archive clears pending state", async () => {
	const { dir, sessions, events } = await fixture([dialogExtension("input")]);
	const entry = await sessions.create(dir);
	const tool = (entry.session.agent.state.tools as any[]).find((t) => t.name === "probe_input");
	const pending = tool.execute("probe-input-cancel", {}, new AbortController().signal);
	await waitFor(events, UI_REQUEST_EVENT);
	await sessions.call("session.cancel", { sessionId: entry.session.sessionId });
	expect(((await pending) as any).details).toMatchObject({ answer: null });
	expect(uiEvents(events, UI_CANCEL_EVENT).length).toBeGreaterThan(0);

	const second = await sessions.create(dir);
	const bridge = (sessions as any).bridges.get(second.session.sessionId);
	bridge.ui.select("Choose", ["a"]);
	expect(bridge.pendingCount()).toBe(1);
	await sessions.call("pi.session.archive", { sessionId: second.session.sessionId });
	expect(bridge.pendingCount()).toBe(0);
});

test("passive UI projects while unsupported composer and TUI factories report their limits", async () => {
	const published: Record<string, unknown>[] = [];
	const bridge = createUiBridge("session-project", (event) => published.push(event));
	bridge.ui.setStatus("signet", "recall: 3 notes");
	bridge.ui.setWorkingMessage("Thinking…");
	bridge.ui.setWidget("plan", ["step one", "step two"]);
	bridge.ui.setTitle("Deep work");
	bridge.ui.setStatus("signet", undefined);
	bridge.ui.setWidget("plan", undefined);
	expect(uiEvents(published, UI_STATUS_EVENT)).toEqual([
		{ type: UI_STATUS_EVENT, sessionId: "session-project", key: "signet", text: "recall: 3 notes" },
		{ type: UI_STATUS_EVENT, sessionId: "session-project", key: "signet" },
	]);
	expect(uiEvents(published, UI_WORKING_EVENT)).toEqual([
		{ type: UI_WORKING_EVENT, sessionId: "session-project", message: "Thinking…" },
	]);
	expect(uiEvents(published, UI_WIDGET_EVENT)).toEqual([
		{
			type: UI_WIDGET_EVENT,
			sessionId: "session-project",
			key: "plan",
			lines: ["step one", "step two"],
			placement: "aboveEditor",
		},
		{ type: UI_WIDGET_EVENT, sessionId: "session-project", key: "plan" },
	]);
	expect(uiEvents(published, UI_TITLE_EVENT)).toEqual([
		{ type: UI_TITLE_EVENT, sessionId: "session-project", title: "Deep work" },
	]);
	const before = published.length;
	// Terminal chrome remains silent. Composer APIs and component factories
	// report unsupported through the existing notification primitive.
	bridge.ui.setWorkingVisible(false);
	bridge.ui.setWorkingIndicator({ frames: [] });
	bridge.ui.setHiddenThinkingLabel("hidden");
	bridge.ui.setFooter(undefined);
	bridge.ui.setHeader(undefined);
	bridge.ui.setEditorComponent(undefined);
	bridge.ui.addAutocompleteProvider((current) => current);
	bridge.ui.setWidget("factory", (() => {}) as never);
	bridge.ui.pasteToEditor("clobber?");
	bridge.ui.setEditorText("clobber?");
	expect(bridge.ui.getEditorText()).toBe("");
	expect(published.slice(before)).toHaveLength(4);
	expect(
		published
			.slice(before)
			.every(
				(event) =>
					event.type === "pixie:ui:notify" && String(event.message).includes("unsupported"),
			),
	).toBe(true);
	// `custom` has no Web UI renderer and never goes pending.
	expect(await bridge.ui.custom(async () => undefined as never)).toBeUndefined();
	expect(bridge.pendingCount()).toBe(0);
});

test("fork creates an independent session and both sides resume", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const parent = await sessions.create(dir);
	await parent.session.setModel(parent.modelRuntime.getModel("fixture", "echo")!);
	await sessions.call("session.prompt", {
		sessionId: parent.session.sessionId,
		content: [{ type: "text", text: "parent turn" }],
	});
	const child = await sessions.create(dir, parent.session.sessionId);
	expect(child.session.sessionId).not.toBe(parent.session.sessionId);
	// The fork point is history, not a mutation: the parent transcript keeps
	// its single assistant turn and the child starts from the same point.
	const parentHistory = (sessions.snapshot(parent, true).messages as any[]).filter(
		(m) => m.role === "assistant",
	);
	const childHistory = (sessions.snapshot(child, true).messages as any[]).filter(
		(m) => m.role === "assistant",
	);
	expect(parentHistory).toHaveLength(1);
	expect(childHistory).toHaveLength(1);
	// Both sides resume independently afterwards.
	await child.session.setModel(child.modelRuntime.getModel("fixture", "echo")!);
	await sessions.call("session.prompt", {
		sessionId: child.session.sessionId,
		content: [{ type: "text", text: "child turn" }],
	});
	await sessions.call("session.prompt", {
		sessionId: parent.session.sessionId,
		content: [{ type: "text", text: "parent follow-up" }],
	});
	expect(
		(sessions.snapshot(parent, true).messages as any[]).filter((m) => m.role === "assistant"),
	).toHaveLength(2);
	expect(
		(sessions.snapshot(child, true).messages as any[]).filter((m) => m.role === "assistant"),
	).toHaveLength(2);
	// Forking again from the parent branches the original point, not the child.
	const secondFork = await sessions.create(dir, parent.session.sessionId);
	expect(secondFork.session.sessionId).not.toBe(child.session.sessionId);
	expect(secondFork.session.sessionId).not.toBe(parent.session.sessionId);
});

test("settled runs do not resurrect streaming state on reload", async () => {
	const { dir, sessions } = await fixture([echoProvider()]);
	const entry = await sessions.create(dir);
	await entry.session.setModel(entry.modelRuntime.getModel("fixture", "echo")!);
	await sessions.call("session.prompt", {
		sessionId: entry.session.sessionId,
		content: [{ type: "text", text: "hello" }],
	});
	const snapshot = sessions.snapshot(entry, true) as Record<string, any>;
	expect(snapshot.runId).toBe("");
	expect(snapshot.streaming).toBe(false);
	expect(snapshot.compacting).toBe(false);
	// Reloading the settled session restores history without reopening a run.
	const reloaded = new Sessions(dir, [echoProvider()], () => {});
	cleanups.push(() => reloaded.close());
	const loaded = await reloaded.get(entry.session.sessionId);
	const restored = reloaded.snapshot(loaded, true) as Record<string, any>;
	expect((restored.messages as any[]).filter((m) => m.role === "assistant")).toHaveLength(1);
	expect(restored.runId).toBe("");
});
