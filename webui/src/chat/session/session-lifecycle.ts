import type { UserMessage } from "@pixie/shared";

export interface SessionLifecycleTarget {
	projectId: string;
	sessionId: string;
	title: string;
	// AUX-14: the native transcript entry to branch from for "Edit from here".
	// Absent when the surface has no entry selection (for example the session
	// header), which keeps the plain new-file Fork the only branch action.
	entryId?: string | undefined;
}

export interface SessionForkParams {
	projectId: string;
	sessionId: string;
	entryId?: string;
}

// AUX-14: the native session-entry id a projected transcript message was read
// from. The host must project this per message (the Pi SDK exposes the ids via
// getUserMessagesForForking / SessionEntry.id); a message without it cannot
// offer an in-file branch, and callers must never substitute a display row id.
export function messageEntryId(message: UserMessage): string | undefined {
	// SAFETY: the host may project an optional native entry id alongside the
	// shared message. Until that projection lands the field is absent, and a
	// non-string value is ignored instead of being treated as a branch entry.
	const value = (message as UserMessage & { entryId?: unknown }).entryId;
	return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

// AUX-14: one owner for the session.fork params. A plain new-file fork of the
// current session omits entryId; "Edit from here" supplies the selected native
// entry so the controller routes the request to an in-file sibling branch.
export function sessionForkParams(
	target: SessionLifecycleTarget,
	entryId?: string,
): SessionForkParams {
	const entry = entryId?.trim() ?? "";
	return entry === ""
		? { projectId: target.projectId, sessionId: target.sessionId }
		: { projectId: target.projectId, sessionId: target.sessionId, entryId: entry };
}

export function forkActionState(
	streaming: boolean,
	busy: boolean,
	supported = true,
	agentName = "The connected agent",
): { disabled: boolean; label: string; title?: string } {
	return {
		disabled: !supported || streaming || busy,
		label: busy ? "Forking…" : "Fork",
		...(!supported
			? { title: `${agentName} does not support forking chats` }
			: streaming
				? { title: "Stop the running chat before forking it" }
				: {}),
	};
}

// AUX-14: "Edit from here" is an in-file sibling branch at the selected native
// entry. It shares the host session.fork route with a plain fork, so it follows
// the same streaming/support guards and is only offered when an entry exists.
export function editFromHereActionState(
	streaming: boolean,
	busy: boolean,
	supported = true,
	agentName = "The connected agent",
): { disabled: boolean; label: string; title?: string } {
	return {
		disabled: !supported || streaming || busy,
		label: busy ? "Branching…" : "Edit from here",
		...(!supported
			? { title: `${agentName} does not support branching chats` }
			: streaming
				? { title: "Stop the running chat before branching it" }
				: {}),
	};
}

export function unsupportedLifecycleReason(
	agentName: string | undefined,
	action: "renaming" | "archiving" | "deleting",
): string {
	return `${agentName || "The connected agent"} does not support ${action} chats`;
}
