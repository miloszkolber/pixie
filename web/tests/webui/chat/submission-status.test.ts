import { expect, test } from "bun:test";
import { retainedSubmission, submissionStatusState } from "@/chat/composer/submission-status";
import { RequestError } from "@/connection";
import { renderSvelte } from "./svelte-render";

// AUX-19: a session.prompt refused by the drain gate is a deliberate quiesce,
// so the chat request path retains the message and reports paused status
// instead of a generic failure.
test("a refused session.prompt during drain renders quiescing status", async () => {
	const refused = retainedSubmission(
		{ text: "keep me", attachments: [], behavior: "send", busy: true },
		new RequestError("controller_quiescing", "The controller is quiescing for an update."),
	);
	expect(submissionStatusState(refused)).toBe("quiescing");
	expect(refused.error).toBeUndefined();

	const markup = await renderSvelte("src/chat/composer/submission-status.svelte", {
		pending: refused,
		onText: () => {},
		onRetry: () => {},
		onDiscard: () => {},
	});
	expect(markup).toContain('data-state="quiescing"');
	expect(markup).toContain("quiescing");
	expect(markup).toContain("Retry message");
	expect(markup).not.toContain("u-text-feedback-error");
});

// A request that fails for any other reason keeps the failed state and error
// copy so quiescing copy is not shown for a real failure.
test("a non-quiescing refusal renders the failure state", async () => {
	const failed = retainedSubmission(
		{ text: "keep me", attachments: [], behavior: "send", busy: true },
		new Error("boom"),
	);
	expect(submissionStatusState(failed)).toBe("failed");

	const markup = await renderSvelte("src/chat/composer/submission-status.svelte", {
		pending: failed,
		onText: () => {},
		onRetry: () => {},
		onDiscard: () => {},
	});
	expect(markup).toContain('data-state="failed"');
	expect(markup).toContain("u-text-feedback-error");
	expect(markup).not.toContain('data-state="quiescing"');
});
