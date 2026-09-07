import { afterEach, expect, test } from "bun:test";
import type { UiDialogRequest } from "@pixie/contracts";
import { uiDialogForSession } from "@/chat/dialogs/ui-dialog-state";
import { reduceExtensionUi } from "@/chat/runtime/extension-ui";
import { createSessionRuntime } from "@/chat/runtime/session-runtime";
import { appStoreApi } from "@/store";
import { renderSvelte } from "./svelte-render";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

test("widgets render escaped text in a keyboard-operable disclosure with placement and removal", async () => {
	const path = "src/chat/dialogs/extension-widgets.svelte";
	const widgets = {
		first: { lines: ["<script>alert(1)</script>", "line two"], placement: "aboveEditor" },
		second: { lines: ["below"], placement: "belowEditor" },
	};
	const html = await renderSvelte(path, { widgets, placement: "aboveEditor" });
	expect(html).toContain("<details");
	expect(html).toContain("<summary");
	expect(html).toContain("&lt;script>");
	expect(html).not.toContain("<script>alert");
	expect(html).not.toContain("below");
	expect(await renderSvelte(path, { widgets: {}, placement: "aboveEditor" })).not.toContain(
		"<details",
	);
});

test("widget replacement preserves native insertion order, including numeric keys", async () => {
	let rt = createSessionRuntime(null, "off");
	for (const [key, line] of [
		["10", "first"],
		["2", "second"],
		["10", "replaced"],
	] as const) {
		rt = reduceExtensionUi(rt, { type: "ui_widget", key, lines: [line] });
	}
	const html = await renderSvelte("src/chat/dialogs/extension-widgets.svelte", {
		widgets: rt.extensionWidgets,
		placement: "aboveEditor",
	});
	expect(html.indexOf("replaced")).toBeLessThan(html.indexOf("second"));
});

test("historical questionnaires render persisted answers without an interactive submit chain", async () => {
	const html = await renderSvelte("src/chat/tools/ask-user-question-card.svelte", {
		toolCallId: "old",
		args: { questions: [{ question: "Choose?", options: [{ label: "Alpha" }] }] },
		result: {
			details: {
				answers: [{ questionIndex: 0, question: "Choose?", kind: "option", answer: "Alpha" }],
				cancelled: false,
			},
		},
		status: "done",
	});
	expect(html).toContain("Choose?");
	expect(html).toContain("Alpha");
	expect(html).not.toContain("<button");
	expect(html).not.toContain("<input");
	const running = await renderSvelte("src/chat/tools/ask-user-question-card.svelte", {
		toolCallId: "old",
		args: {},
		status: "running",
	});
	expect(running).toContain("session dialog");
	expect(running).not.toContain("<button");
});

test("same request ID in two sessions stays independent through cancellation and replay", () => {
	const state = appStoreApi.getState();
	state.reconcileUiDialogs("session-a", [select]);
	state.reconcileUiDialogs("session-b", [{ ...select, sessionId: "session-b" }]);
	expect(Object.keys(appStoreApi.getState().uiDialogs)).toHaveLength(2);
	state.handleAgentEvent({ type: "ui_cancel", requestId: select.requestId }, "session-a");
	expect(uiDialogForSession(appStoreApi.getState().uiDialogs, "session-b")).toBeDefined();
	state.reconcileUiDialogs("session-a", []);
	expect(Object.keys(appStoreApi.getState().uiDialogs)).toHaveLength(1);
});

test("passive UI is bounded, session-scoped, cleared on reconnect and cannot rename or overwrite drafts", () => {
	appStoreApi.setState({
		sessions: {
			a: { ...createSessionRuntime(null, "off"), draft: "unsent" },
			b: createSessionRuntime(null, "off"),
		},
	});
	const state = appStoreApi.getState();
	state.handleAgentEvent({ type: "ui_title", title: "<script>not HTML</script>" }, "a");
	state.handleAgentEvent({ type: "ui_working", message: "Working" }, "a");
	for (let i = 0; i < 100; i++) {
		state.handleAgentEvent({ type: "ui_status", key: `s${i}`, text: "x".repeat(5000) }, "a");
		state.handleAgentEvent(
			{
				type: "ui_widget",
				key: `w${i}`,
				lines: Array(100).fill("x".repeat(5000)),
				placement: "belowEditor",
			},
			"a",
		);
	}
	const runtime = appStoreApi.getState().sessions.a;
	if (!runtime) throw new Error("Missing fixture session");
	expect(Object.keys(runtime.extensionWidgets)).toHaveLength(16);
	expect(Object.keys(runtime.extensionStatuses)).toHaveLength(16);
	expect(runtime.extensionWidgets.w0?.lines).toHaveLength(32);
	expect(runtime.extensionWidgets.w0?.lines[0]).toHaveLength(2000);
	expect(runtime.extensionStatuses.s0).toHaveLength(2000);
	expect(runtime.draft).toBe("unsent");
	expect(appStoreApi.getState().tabsByProjectArea).toEqual({});
	expect(appStoreApi.getState().sessions.b?.extensionTitle).toBe("");
	state.handleAgentEvent({ type: "ui_widget", key: "w0" }, "a");
	state.handleAgentEvent({ type: "ui_status", key: "s0" }, "a");
	expect(appStoreApi.getState().sessions.a?.extensionWidgets.w0).toBeUndefined();
	expect(appStoreApi.getState().sessions.a?.extensionStatuses.s0).toBeUndefined();
	state.setStatus("connecting");
	expect(appStoreApi.getState().sessions.a?.extensionWidgets).toEqual({});
	expect(appStoreApi.getState().sessions.a?.extensionTitle).toBe("");
	expect(appStoreApi.getState().sessions.a?.draft).toBe("unsent");
	state.handleAgentEvent({ type: "ui_title", title: "stale deleted session" }, "missing");
	expect(appStoreApi.getState().sessions.missing).toBeUndefined();
});

test("an extension title equal to an existing chat name never renames tabs or drafts", () => {
	const tab = {
		kind: "chat" as const,
		id: "chat-tab",
		projectAreaId: "area-1",
		name: "Deep work",
		sessionId: "a",
	};
	appStoreApi.setState({
		sessions: { a: { ...createSessionRuntime(null, "off"), draft: "unsent" } },
		tabsByProjectArea: { "area-1": [tab] },
	});
	const before = appStoreApi.getState().tabsByProjectArea;
	appStoreApi.getState().handleAgentEvent({ type: "ui_title", title: "Deep work" }, "a");
	expect(appStoreApi.getState().sessions.a?.extensionTitle).toBe("Deep work");
	expect(appStoreApi.getState().tabsByProjectArea).toEqual(before);
	expect(appStoreApi.getState().tabsByProjectArea["area-1"]?.[0]).toMatchObject({
		name: "Deep work",
		sessionId: "a",
	});
	expect(appStoreApi.getState().sessions.a?.draft).toBe("unsent");
});

const select: UiDialogRequest = {
	requestId: "request-1",
	sessionId: "session-a",
	primitive: "select",
	title: "Choose",
	options: ["Red", "Blue"],
};

test("extension dialog requests open scoped to their session", () => {
	appStoreApi.getState().handleAgentEvent({ type: "ui_request", request: select }, "session-a");
	expect(uiDialogForSession(appStoreApi.getState().uiDialogs, "session-a")).toEqual(select);
	expect(uiDialogForSession(appStoreApi.getState().uiDialogs, "session-b")).toBeUndefined();
	// A foreign envelope on this session's channel never moves the dialog across sessions.
	appStoreApi
		.getState()
		.handleAgentEvent(
			{ type: "ui_request", request: { ...select, sessionId: "session-b" } },
			"session-a",
		);
	expect(uiDialogForSession(appStoreApi.getState().uiDialogs, "session-b")).toBeUndefined();
	expect(uiDialogForSession(appStoreApi.getState().uiDialogs, "session-a")).toEqual(select);
});

test("cancellation dismisses the dialog and answers stay out of the transcript", () => {
	appStoreApi.getState().handleAgentEvent({ type: "ui_request", request: select }, "session-a");
	appStoreApi
		.getState()
		.handleAgentEvent({ type: "ui_cancel", requestId: "request-1" }, "session-a");
	expect(appStoreApi.getState().uiDialogs).toEqual({});
	expect(appStoreApi.getState().sessions["session-a"]).toBeUndefined();
	appStoreApi.getState().dismissUiDialog("session-a", "request-1");
});

test("extension notifications surface as toasts", () => {
	appStoreApi
		.getState()
		.handleAgentEvent({ type: "ui_notify", message: "Saved", level: "info" }, "session-a");
	expect(appStoreApi.getState().toasts.map((toast) => toast.message)).toEqual(["Saved"]);
	appStoreApi
		.getState()
		.handleAgentEvent({ type: "ui_notify", message: "Failed", level: "error" }, "session-a");
	expect(appStoreApi.getState().toasts.at(-1)).toMatchObject({
		variant: "error",
		message: "Failed",
	});
	expect(appStoreApi.getState().sessions["session-a"]).toBeUndefined();
});
