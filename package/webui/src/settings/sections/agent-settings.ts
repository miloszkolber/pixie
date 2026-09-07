import type { AgentProfile } from "@pixie/contracts";

export const OPERATION_LABELS: Record<keyof AgentProfile["operations"], string> = {
	deleteSession: "Delete chats",
	forkSession: "Fork chats",
	promptImage: "Image prompts",
	promptEmbeddedContext: "Text resource prompts",
	httpMcp: "HTTP MCP servers",
	steer: "Steer a running chat",
	renameSession: "Rename chats",
	archiveSession: "Archive chats",
	administration: "Agent administration",
};

export function agentOperationRows(profile: AgentProfile) {
	return Object.entries(profile.operations).map(([operation, available]) => ({
		operation: operation as keyof AgentProfile["operations"],
		label: OPERATION_LABELS[operation as keyof AgentProfile["operations"]],
		available,
	}));
}
