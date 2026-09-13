export interface SignetDetails {
	error?: string;
	memoriesFound?: number;
	sourcesFound?: number;
	sessionsFound?: number;
	memoriesSaved?: number;
}

function recordOf(value: unknown): Record<string, unknown> | undefined {
	return typeof value === "object" && value !== null && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: undefined;
}

export function signetDetails(result: unknown): SignetDetails {
	const raw = recordOf(recordOf(result)?.details);
	if (!raw) return {};
	return {
		...(typeof raw.error === "string" ? { error: raw.error } : {}),
		...(typeof raw.memoriesFound === "number" ? { memoriesFound: raw.memoriesFound } : {}),
		...(typeof raw.sourcesFound === "number" ? { sourcesFound: raw.sourcesFound } : {}),
		...(typeof raw.sessionsFound === "number" ? { sessionsFound: raw.sessionsFound } : {}),
		...(typeof raw.memoriesSaved === "number" ? { memoriesSaved: raw.memoriesSaved } : {}),
	};
}

export function signetTitle(toolName: string): string {
	if (toolName === "signet_remember") return "save memory";
	if (toolName === "signet_source_search") return "source search";
	if (toolName === "signet_session_search") return "session search";
	return "memory recall";
}

export function signetRunningLabel(toolName: string): string {
	if (toolName === "signet_remember") return "Saving memory…";
	if (toolName === "signet_source_search") return "Searching sources…";
	if (toolName === "signet_session_search") return "Searching sessions…";
	return "Recalling memory…";
}

export function signetSummary(args: Record<string, unknown>): string {
	const query = typeof args.query === "string" ? args.query : "";
	const content = typeof args.content === "string" ? args.content : "";
	return query || content || "memory";
}
