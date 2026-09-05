import { afterEach, expect, test } from "bun:test";
import type { UiDialogRequest } from "@pixie/contracts";
import { uiDialogForSession } from "@/chat/dialogs/ui-dialog-state";
import { appStoreApi } from "@/store";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

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
	appStoreApi.getState().dismissUiDialog("request-1");
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
