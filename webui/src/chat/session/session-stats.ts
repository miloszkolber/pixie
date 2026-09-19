import type { ContextUsage, SessionStats } from "@pixie/shared";

export type UsageField = "input" | "output" | "cacheRead" | "cacheWrite" | "total" | "cost";

export interface UsageEvent {
	usage?: Partial<SessionStats["tokens"]> & { cost?: number };
	reported?: SessionStats["reported"];
	costCurrency?: string;
}

// AUX-15: the server aggregates every transcript entry, including compaction
// and branch summaries, then merges live message deltas. A compaction can still
// re-emit a smaller accumulated snapshot, so this boundary merges the event
// monotonically instead of replacing the totals. Context usage is intentionally
// not touched here: it is the live SDK value and may legitimately shrink.
export function mergeUsageStats(previous: SessionStats | null, event: UsageEvent): SessionStats {
	const usage = event.usage ?? {};
	const tokens = previous?.tokens;
	const input = Math.max(tokens?.input ?? 0, usage.input ?? 0);
	const output = Math.max(tokens?.output ?? 0, usage.output ?? 0);
	const cacheRead = Math.max(tokens?.cacheRead ?? 0, usage.cacheRead ?? 0);
	const cacheWrite = Math.max(tokens?.cacheWrite ?? 0, usage.cacheWrite ?? 0);
	const total = Math.max(tokens?.total ?? 0, usage.total ?? 0);
	const cost = Math.max(previous?.cost ?? 0, usage.cost ?? 0);
	return {
		sessionId: previous?.sessionId ?? "",
		totalMessages: previous?.totalMessages ?? 0,
		tokens: { input, output, cacheRead, cacheWrite, total },
		cost,
		...(event.costCurrency
			? { costCurrency: event.costCurrency }
			: previous?.costCurrency
				? { costCurrency: previous.costCurrency }
				: {}),
		...(event.reported
			? { reported: event.reported }
			: previous?.reported
				? { reported: previous.reported }
				: {}),
		...(previous?.contextUsage ? { contextUsage: previous.contextUsage } : {}),
	};
}

export function isUsageReported(stats: SessionStats, field: UsageField, value: number): boolean {
	return stats.reported ? stats.reported[field] === true : value !== 0;
}

export function formatTokens(count: number): string {
	if (count < 1_000) return count.toString();
	if (count < 10_000) return `${(count / 1_000).toFixed(1)}k`;
	if (count < 1_000_000) return `${Math.round(count / 1_000)}k`;
	if (count < 10_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
	return `${Math.round(count / 1_000_000)}M`;
}

export function formatCost(stats: SessionStats): string {
	return !stats.costCurrency || stats.costCurrency === "USD"
		? `$${stats.cost.toFixed(3)}`
		: `${stats.cost.toFixed(3)} ${stats.costCurrency}`;
}

export function usageParts(stats: SessionStats): string[] {
	const parts: string[] = [];
	if (isUsageReported(stats, "input", stats.tokens.input))
		parts.push(`↑${formatTokens(stats.tokens.input)}`);
	if (isUsageReported(stats, "output", stats.tokens.output))
		parts.push(`↓${formatTokens(stats.tokens.output)}`);
	if (isUsageReported(stats, "cacheRead", stats.tokens.cacheRead))
		parts.push(`R${formatTokens(stats.tokens.cacheRead)}`);
	if (isUsageReported(stats, "cacheWrite", stats.tokens.cacheWrite))
		parts.push(`W${formatTokens(stats.tokens.cacheWrite)}`);
	if (isUsageReported(stats, "cost", stats.cost)) parts.push(formatCost(stats));
	return parts;
}

export function contextPart(usage: ContextUsage): { bar: string; text: string } {
	const filled =
		usage.percent === null ? 0 : Math.round(Math.min(100, Math.max(0, usage.percent)) / 20);
	const contextWindow = formatTokens(usage.contextWindow);
	return {
		bar: `${"▰".repeat(filled)}${"▱".repeat(5 - filled)}`,
		text:
			usage.percent === null
				? `?/${contextWindow}`
				: `${usage.percent.toFixed(1)}%/${contextWindow}`,
	};
}
