import { expect, test } from "bun:test";
import type { SessionStats } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import { formatTokens, usageParts } from "@/chat/session/session-stats";

const component = new URL(
	"../../../webui/src/chat/session/session-stats-bar.svelte",
	import.meta.url,
);

function stats(overrides: Partial<SessionStats> = {}): SessionStats {
	return {
		sessionId: "session",
		totalMessages: 0,
		tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
		cost: 0,
		...overrides,
	};
}

test("usage formatting distinguishes reported zeroes from unavailable fields", () => {
	expect(usageParts(stats({ reported: { input: true } }))).toEqual(["↑0"]);
	expect(
		usageParts(
			stats({ tokens: { input: 1_500, output: 0, cacheRead: 0, cacheWrite: 0, total: 1_500 } }),
		),
	).toEqual(["↑1.5k"]);
	expect(usageParts(stats({ reported: {} }))).toEqual([]);
	expect(formatTokens(2_500_000)).toBe("2.5M");
});

test("the session stats popover compiles as Svelte and retains its accessible usage contract", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain('data-testid="usage-tracker"');
	expect(source).toContain('aria-label="Open session usage"');
	expect(source).toContain('data-testid="session-stats"');
	expect(source).toContain('role="progressbar"');
	expect(source).toContain('aria-label="Context window used"');
	expect(source).toContain("aria-valuemin={0}");
	expect(source).toContain("aria-valuemax={100}");
	expect(source).toContain("Math.min(100, Math.max(0, view.progress))");
	expect(source).toContain('popover="auto"');
	expect(source).toContain("mewa(popoverBehavior)");
});
