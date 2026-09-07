export interface SessionLifecycleTarget {
	projectId: string;
	sessionId: string;
	title: string;
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

export function unsupportedLifecycleReason(
	agentName: string | undefined,
	action: "renaming" | "archiving" | "deleting",
): string {
	return `${agentName || "The connected agent"} does not support ${action} chats`;
}
