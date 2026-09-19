import { expect, test } from "bun:test";
import {
	createSessionRuntime,
	reduceSessionEvent,
} from "../../../webui/src/chat/runtime/session-runtime";
import { mergeUsageStats } from "../../../webui/src/chat/session/session-stats";

test("compaction usage does not regress totals while context follows the SDK", () => {
	let runtime = createSessionRuntime(null, "off");
	runtime = reduceSessionEvent(runtime, {
		type: "usage",
		usage: { input: 1_000, output: 500, cacheRead: 200, cacheWrite: 100, total: 1_800, cost: 1.5 },
		costCurrency: "USD",
		reported: { input: true, output: true, total: true, cost: true },
	});
	runtime = reduceSessionEvent(runtime, {
		type: "context",
		contextUsage: { tokens: 1_800, contextWindow: 200_000, percent: 0.9 },
	});
	// A compaction removes older messages and re-emits a smaller accumulated
	// snapshot. Every prior entry still contributed, so the totals hold.
	runtime = reduceSessionEvent(runtime, {
		type: "usage",
		usage: { input: 400, output: 100, cacheRead: 200, cacheWrite: 100, total: 800, cost: 0.5 },
		reported: { input: true },
	});
	expect(runtime.stats?.tokens.input).toBe(1_000);
	expect(runtime.stats?.tokens.output).toBe(500);
	expect(runtime.stats?.tokens.cacheRead).toBe(200);
	expect(runtime.stats?.tokens.cacheWrite).toBe(100);
	expect(runtime.stats?.tokens.total).toBe(1_800);
	expect(runtime.stats?.cost).toBe(1.5);
	expect(runtime.stats?.contextUsage).toEqual({
		tokens: 1_800,
		contextWindow: 200_000,
		percent: 0.9,
	});
	// Live context usage is the SDK value and may legitimately shrink.
	runtime = reduceSessionEvent(runtime, {
		type: "context",
		contextUsage: { tokens: 800, contextWindow: 200_000, percent: 0.4 },
	});
	expect(runtime.stats?.contextUsage).toEqual({
		tokens: 800,
		contextWindow: 200_000,
		percent: 0.4,
	});
	expect(runtime.stats?.tokens.input).toBe(1_000);
});

test("mergeUsageStats keeps context usage out of the merge", () => {
	const merged = mergeUsageStats(
		{
			sessionId: "s",
			totalMessages: 3,
			tokens: { input: 10, output: 5, cacheRead: 1, cacheWrite: 1, total: 17 },
			cost: 0.25,
			costCurrency: "USD",
			contextUsage: { tokens: 42, contextWindow: 100, percent: 42 },
		},
		{ usage: { input: 4, output: 2, total: 6, cost: 0.1 } },
	);
	expect(merged.tokens).toEqual({ input: 10, output: 5, cacheRead: 1, cacheWrite: 1, total: 17 });
	expect(merged.cost).toBe(0.25);
	expect(merged.costCurrency).toBe("USD");
	expect(merged.contextUsage).toEqual({ tokens: 42, contextWindow: 100, percent: 42 });
});
