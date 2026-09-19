import { describe, expect, test } from "bun:test";
import { CANVAS_CONTRIBUTION, canvasManagementStatusUrl } from "@/canvas";

describe("Canvas UI foundation", () => {
	test("describes a session-scoped module without HTML replay", () => {
		expect(CANVAS_CONTRIBUTION.scope).toBe("session");
		expect(CANVAS_CONTRIBUTION.secondaryViewSlot).toBe(4);
		expect(CANVAS_CONTRIBUTION.secondarySidebarSlot).toBe(5);
		expect(CANVAS_CONTRIBUTION.executableHtml).toBe(false);
	});

	test("builds the authenticated status route without credentials", () => {
		expect(canvasManagementStatusUrl({ projectId: "project-a", sessionId: "session-a" })).toBe(
			"/api/canvas/status/project-a/session-a",
		);
		expect(
			canvasManagementStatusUrl({ projectId: "project/a", sessionId: "session-a" }),
		).toBeNull();
		expect(
			canvasManagementStatusUrl({ projectId: "project-a", sessionId: "session?a" }),
		).toBeNull();
	});
});
