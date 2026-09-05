import { expect, test } from "bun:test";
import type { SessionPlanState } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import {
	planIconStatus,
	planProgress,
	planStatusLabel,
	sessionPlanLabel,
} from "@/chat/session/session-plan";

const sessionRoot = new URL("../../../webui/src/chat/session/", import.meta.url);

test("plan helpers preserve progress, status, and empty-truncated labels", () => {
	const plan: SessionPlanState = {
		entries: [
			{ content: "Read <script>alert(1)</script>", priority: "high", status: "completed" },
			{ content: "Implement", priority: "medium", status: "in_progress" },
		],
		truncated: true,
	};
	expect(planProgress(plan)).toEqual({ completed: 1, total: 2 });
	expect(planStatusLabel("completed")).toBe("Completed");
	expect(planStatusLabel("in_progress")).toBe("In progress");
	expect(planIconStatus("completed")).toBe("done");
	expect(planIconStatus("in_progress")).toBe("active");
	expect(sessionPlanLabel(plan)).toBe(
		"Session plan, 1 of 2 complete, shortened to fit display limits",
	);
	expect(sessionPlanLabel({ entries: [], truncated: true })).toBe(
		"Session plan, shortened to fit display limits",
	);
});

test("session plan Svelte controls retain safe accessible markup", async () => {
	const paths = [
		"plan-status-icon.svelte",
		"session-plan-content.svelte",
		"session-plan-control.svelte",
	] as const;
	const sources = await Promise.all(
		paths.map((path) => Bun.file(new URL(path, sessionRoot)).text()),
	);
	for (let index = 0; index < paths.length; index += 1) {
		const path = paths[index] as string;
		const source = sources[index] as string;
		expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
		expect(
			compile(source, { filename: new URL(path, sessionRoot).pathname, generate: false }).warnings,
		).toEqual([]);
	}

	const content = sources[1] as string;
	expect(content).toContain('data-testid="session-plan-content"');
	expect(content).toContain("{progress.completed} of {progress.total} complete");
	expect(content).toContain("{planStatusLabel(entry.status)}: ");
	expect(content).toContain("Plan shortened to fit display limits.");
	expect(content).toContain("{entry.content}");
	expect(content).not.toContain("{@html");
	expect(content).toContain("{#if hasEntries}\n\t\t<ol");

	const control = sources[2] as string;
	expect(control).toContain('data-testid="session-plan-trigger"');
	expect(control).toContain("sessionPlanLabel(planState)");
	expect(control).toContain('"Limited"');
});
