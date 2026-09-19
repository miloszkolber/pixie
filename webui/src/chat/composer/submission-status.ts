import { errorText, wsErrorCode } from "../../connection";
import type { ChatSubmission } from "../runtime/types";

export type SubmissionStatusState = "busy" | "quiescing" | "failed";

/**
 * Classifies a retained submission. A deliberate AUX-19 quiesce is a paused
 * status, not a failure, so the chat request path does not report a generic
 * error for a refused prompt while the controller drains.
 */
export function submissionStatusState(pending: ChatSubmission): SubmissionStatusState {
	if (pending.busy) return "busy";
	return pending.quiescing ? "quiescing" : "failed";
}

/**
 * Builds the retained submission for a refused request. A controller quiescing
 * refusal keeps the message and marks it paused; every other cause stays a
 * failure with the request error text.
 */
export function retainedSubmission(pending: ChatSubmission, cause: unknown): ChatSubmission {
	if (wsErrorCode(cause) === "controller_quiescing") {
		const retained: ChatSubmission = { ...pending, busy: false, quiescing: true };
		delete retained.error;
		return retained;
	}
	return { ...pending, busy: false, quiescing: false, error: errorText(cause) };
}
