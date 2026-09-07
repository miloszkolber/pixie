import type { AgentMentionInfo } from "@pixie/contracts";

export interface LoadedAgentMentions {
	identity: string | null;
	mentions: AgentMentionInfo[];
}

export function agentMentionIdentity(
	projectId: string | undefined,
	sessionId: string,
): string | null {
	return projectId ? `${projectId}\0${sessionId}` : null;
}

export function visibleAgentMentions(
	loaded: LoadedAgentMentions,
	identity: string | null,
): AgentMentionInfo[] {
	return loaded.identity === identity ? loaded.mentions : [];
}

export interface LoadedFileMentionCandidates<T> {
	identity: string | null;
	candidates: T[];
}

export function fileMentionCandidateIdentity(
	projectAreaId: string,
	root: string | undefined,
	sessionId: string,
	query: string | null,
): string | null {
	return query === null ? null : `${projectAreaId}\0${root ?? ""}\0${sessionId}\0${query}`;
}

export function visibleFileMentionCandidates<T>(
	loaded: LoadedFileMentionCandidates<T>,
	identity: string | null,
): T[] {
	return loaded.identity === identity ? loaded.candidates : [];
}
