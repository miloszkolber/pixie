export interface SessionLifecycleTarget {
	projectId: string;
	sessionId: string;
	title: string;
	// AUX-14: the native transcript entry to branch from for "Edit from here".
	// Absent when the surface has no entry selection (for example the session
	// header), which keeps the plain new-file Fork the only branch action.
	entryId?: string | undefined;
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
