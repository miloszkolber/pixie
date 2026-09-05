import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { planBlockedByLabel } from "@/chat/session/session-plan";

const contentUrl = new URL(
	"../../../webui/src/chat/session/session-plan-content.svelte",
	import.meta.url,
);

test("plan dependency labels list upstream blockers", () => {
	expect(
		planBlockedByLabel({
			content: "Ship",
			priority: "medium",
			status: "pending",
			id: 2,
			blockedBy: [1],
		}),
	).toBe("Blocked by #1");
	expect(
		planBlockedByLabel({
			content: "Ship",
			priority: "medium",
			status: "pending",
			blockedBy: [1, 3],
		}),
	).toBe("Blocked by #1, #3");
	expect(
		planBlockedByLabel({ content: "Legacy", priority: "medium", status: "pending" }),
	).toBeNull();
	expect(
		planBlockedByLabel({
			content: "Solo",
			priority: "medium",
			status: "pending",
			id: 1,
			blockedBy: [],
		}),
	).toBeNull();
});

test("session plan content renders dependency labels as text", async () => {
	const source = await Bun.file(contentUrl).text();
	expect(compile(source, { filename: contentUrl.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain("planBlockedByLabel(entry)");
	expect(source).not.toContain("{@html");
});
