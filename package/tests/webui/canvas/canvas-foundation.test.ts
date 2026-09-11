import { describe, expect, test } from "bun:test";
import {
	applyCanvasScreenshot,
	applyCanvasVersionEvent,
	CANVAS_CONTRIBUTION,
	CANVAS_TEMPLATES,
	canvasArtifactUrl,
	canvasControlAvailability,
	canvasCreateArgs,
	canvasManagementRemoveUrl,
	canvasManagementStatusUrl,
	canvasPreviewFromScreenshot,
	canvasRemovalConfirmation,
	canvasRenderingDisplay,
	canvasSidebarViewModel,
	canvasStateFromStatus,
	canvasTemplateById,
	canvasTemplateSummary,
	canvasToolCardViewModel,
	canvasUpdateDisplay,
	canvasVersionDisplay,
	canvasViewerViewModel,
	canvasTemplateOptions,
	isOwnedCanvasTemplate,
	emptyCanvasState,
	canvasVersionRecovery,
	recoverCanvasVersion,
} from "@/canvas";

const canvasId = "abcdefghijklmnopqrstuvwxyz234567";
const renderKey = "0123456789abcdef0123456789abcdef";

describe("Canvas UI foundation", () => {
	test("describes a session-scoped raster contribution without HTML replay", () => {
		expect(CANVAS_CONTRIBUTION.scope).toBe("session");
		expect(CANVAS_CONTRIBUTION.secondaryViewSlot).toBe(4);
		expect(CANVAS_CONTRIBUTION.secondarySidebarSlot).toBe(5);
		expect(CANVAS_CONTRIBUTION.executableHtml).toBe(false);
	});

	test("builds same-origin artifact URLs without accepting credentials", () => {
		const reference = `pixie://canvas/artifact/${canvasId}/${renderKey}.png`;
		expect(canvasArtifactUrl(reference)).toBe(`/mcp/canvas/artifact/${canvasId}/${renderKey}.png`);
		expect(canvasArtifactUrl(reference, "https://pixie.test/")).toBe(
			`https://pixie.test/mcp/canvas/artifact/${canvasId}/${renderKey}.png`,
		);
		expect(canvasArtifactUrl(reference, "/pixie")).toBe(
			`/pixie/mcp/canvas/artifact/${canvasId}/${renderKey}.png`,
		);
		expect(canvasArtifactUrl(`${reference}?token=secret`)).toBeNull();
		expect(canvasArtifactUrl("https://evil.test/image.png")).toBeNull();
		expect(canvasArtifactUrl(reference, "https://pixie.test/?token=secret")).toBeNull();
		expect(canvasArtifactUrl(reference, "//evil.example")).toBeNull();
		expect(canvasArtifactUrl("../canvas", renderKey)).toBeNull();
		expect(canvasArtifactUrl("short", renderKey)).toBeNull();
		const scope = { projectId: "project-a", sessionId: "session-a" };
		expect(canvasArtifactUrl(reference, scope)).toBe(
			`/api/canvas/artifact/project-a/session-a/${canvasId}/${renderKey}.png`,
		);
		expect(canvasArtifactUrl(reference, scope, "https://pixie.test/")).toBe(
			`https://pixie.test/api/canvas/artifact/project-a/session-a/${canvasId}/${renderKey}.png`,
		);
		expect(canvasArtifactUrl(reference, { ...scope, sessionId: "session/a" })).toBeNull();
		expect(canvasManagementStatusUrl(scope)).toBe("/api/canvas/status/project-a/session-a");
		expect(canvasManagementRemoveUrl(scope)).toBe("/api/canvas/remove/project-a/session-a");
	});

	test("keeps version and generation identity while making a new event stale", () => {
		let state = canvasStateFromStatus(emptyCanvasState("session-a"), {
			canvasId,
			generation: 3,
			version: 1,
			availability: "ready",
		});
		state = applyCanvasScreenshot(
			state,
			{
				canvasId,
				generation: 3,
				version: 1,
				mime: "image/png",
				width: 640,
				height: 400,
				bytes: 128,
				artifact: `pixie://canvas/artifact/${canvasId}/${renderKey}.png`,
			},
			canvasArtifactUrl(`pixie://canvas/artifact/${canvasId}/${renderKey}.png`),
		);
		expect(state.preview.status).toBe("ready");
		const next = applyCanvasVersionEvent(state, {
			sessionId: "session-a",
			canvasId,
			generation: 3,
			version: 2,
		});
		expect(next.currentVersion).toBe(2);
		expect(next.preview.status).toBe("stale");
		const refreshed = canvasStateFromStatus(next, {
			canvasId,
			generation: 3,
			version: 3,
			availability: "ready",
		});
		expect(refreshed.preview.status).toBe("stale");
		expect(
			applyCanvasVersionEvent(next, { sessionId: "other", canvasId, generation: 3, version: 3 })
				.currentVersion,
		).toBe(2);
	});

	test("recovers stale writes without presenting them as current", () => {
		const state = canvasStateFromStatus(emptyCanvasState("session-a"), {
			canvasId,
			generation: 3,
			version: 2,
			availability: "ready",
		});
		const recovery = canvasVersionRecovery(
			{ code: "conflict" },
			{ expectedVersion: 1, currentVersion: 2 },
		);
		expect(recovery.action).toBe("refresh-status");
		expect(recoverCanvasVersion(state, { code: "conflict" }).status).toBe("stale");
		expect(recoverCanvasVersion(state, { code: "conflict" }).error?.code).toBe("conflict");
	});

	test("cards expose compact version and raster metadata only", () => {
		const scope = { projectId: "project-a", sessionId: "session-a" };
		const view = canvasToolCardViewModel({
			toolName: "canvas_screenshot",
			result: {
				outcome: "screenshot",
				canvasId,
				generation: 3,
				version: 2,
				mime: "image/png",
				artifact: `pixie://canvas/artifact/${canvasId}/${renderKey}.png`,
			},
			artifactScope: scope,
		});
		expect(view.summary).toBe("screenshot · version 2");
		expect(view.preview?.kind).toBe("raster");
		expect(view.preview?.url).toBe(
			`/api/canvas/artifact/project-a/session-a/${canvasId}/${renderKey}.png`,
		);
		expect(canvasPreviewFromScreenshot({ canvasId, generation: 3, version: 2 }, null).status).toBe(
			"unavailable",
		);
	});

	test("exposes version, rendering-state, and update-time controls", () => {
		const empty = emptyCanvasState("session-a");
		expect(canvasVersionDisplay(empty).label).toBe("No Canvas version yet");
		expect(canvasUpdateDisplay(null).label).toBe("Not updated yet");
		expect(canvasRenderingDisplay(empty).label).toContain("Empty");

		let state = canvasStateFromStatus(empty, {
			canvasId,
			generation: 3,
			version: 2,
			availability: "ready",
			updatedAt: "2026-09-11T10:00:00.000Z",
		});
		expect(canvasVersionDisplay(state).label).toBe("Version 2");
		expect(canvasUpdateDisplay(state.document).iso).toBe("2026-09-11T10:00:00.000Z");
		expect(canvasUpdateDisplay(state.document).label).toContain("2026-09-11");
		expect(canvasRenderingDisplay(state).label).toBe("Ready");

		// Capture the current raster, then observe a newer version event. The
		// prior preview must read stale while the newer render is pending.
		state = applyCanvasScreenshot(
			state,
			{
				canvasId,
				generation: 3,
				version: 2,
				mime: "image/png",
				artifact: `pixie://canvas/artifact/${canvasId}/${renderKey}.png`,
			},
			canvasArtifactUrl(`pixie://canvas/artifact/${canvasId}/${renderKey}.png`),
		);
		expect(state.preview.status).toBe("ready");
		expect(state.viewedVersion).toBe(2);
		state = applyCanvasVersionEvent(state, {
			sessionId: "session-a",
			canvasId,
			generation: 3,
			version: 3,
		});
		const version = canvasVersionDisplay(state);
		expect(version.isStale).toBe(true);
		expect(version.label).toContain("Viewing version 2");
		expect(version.label).toContain("current 3");
		expect(canvasRenderingDisplay(state).isStale).toBe(true);

		const viewer = canvasViewerViewModel(state, { ready: true, pending: false });
		expect(viewer.heading).toContain("version 2");
		expect(viewer.isStalePreview).toBe(true);
		expect(viewer.updated.iso).toBe("2026-09-11T10:00:00.000Z");
		const sidebar = canvasSidebarViewModel(state, { ready: true, pending: false });
		expect(sidebar.scopeLabel).toContain("Session-scoped");
		expect(sidebar.scopeLabel).toContain("session-a");
		expect(sidebar.version.isStale).toBe(true);
	});

	test("gates screenshot, refresh, and removal with explicit preconditions", () => {
		const state = canvasStateFromStatus(emptyCanvasState("session-a"), {
			canvasId,
			generation: 3,
			version: 2,
			availability: "ready",
		});
		const ready = canvasControlAvailability(state, { ready: true, pending: false });
		expect(ready.canScreenshot).toBe(true);
		expect(ready.canRefresh).toBe(true);
		expect(ready.canRemove).toBe(true);
		expect(ready.screenshotReason).toBeNull();

		const pending = canvasControlAvailability(state, { ready: true, pending: true });
		expect(pending.canRefresh).toBe(false);
		expect(pending.refreshReason).toContain("already running");
		expect(pending.canScreenshot).toBe(false);

		const offline = canvasControlAvailability(state, { ready: false, pending: false });
		expect(offline.canScreenshot).toBe(false);
		expect(offline.canRefresh).toBe(false);
		// Removal stays available while the module reports disabled/unavailable.
		const disabled = canvasStateFromStatus(emptyCanvasState("session-a"), {
			canvasId,
			generation: 3,
			version: 2,
			availability: "disabled",
		});
		expect(canvasControlAvailability(disabled, { ready: false, pending: false }).canRemove).toBe(
			true,
		);
		expect(canvasRemovalConfirmation(disabled)?.request.expectedVersion).toBe(2);
		expect(canvasRemovalConfirmation(disabled)?.requiresConfirmation).toBe(true);
		expect(canvasRemovalConfirmation(disabled)?.message).toContain("generation 3");
		expect(canvasRemovalConfirmation(emptyCanvasState("session-a"))).toBeNull();

		const removed = canvasStateFromStatus(emptyCanvasState("session-a"), {
			canvasId,
			generation: 3,
			version: 2,
			availability: "removed",
		});
		expect(canvasControlAvailability(removed, { ready: true, pending: false }).canRemove).toBe(
			false,
		);
	});

	test("demonstrates two owned Mewa test templates without executable HTML", () => {
		expect(CANVAS_TEMPLATES).toHaveLength(2);
		expect([...CANVAS_TEMPLATES.map((template) => template.id)].sort()).toEqual([
			"mewa-basic",
			"mewa-card",
		]);
		for (const template of CANVAS_TEMPLATES) {
			expect(isOwnedCanvasTemplate(template.id)).toBe(true);
			expect(canvasTemplateById(template.id)?.label).toBe(template.label);
			expect(template.mewaComponents.length).toBeGreaterThan(0);
			expect(canvasTemplateSummary(template)).toContain("session-scoped");
			expect(canvasTemplateSummary(template)).not.toContain("<html");
			expect(canvasTemplateSummary(template)).not.toContain("{@html}");
		}
		expect(isOwnedCanvasTemplate("basic")).toBe(false);
		expect(isOwnedCanvasTemplate("blank")).toBe(false);
		expect(isOwnedCanvasTemplate(null)).toBe(false);
		expect(canvasTemplateById("unknown")).toBeNull();
		expect(canvasCreateArgs("mewa-basic")).toEqual({ templateId: "mewa-basic" });
		expect(canvasCreateArgs("mewa-card")).toEqual({ templateId: "mewa-card" });
		expect(canvasCreateArgs("basic")).toEqual({});
		expect(canvasCreateArgs(null)).toEqual({});
		expect(canvasTemplateOptions()).toHaveLength(2);
	});
});
