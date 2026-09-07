import { expect, test } from "bun:test";
import { safeBrowserURL } from "@pixie/contracts";
import {
	browserPanelScreenState,
	snapshotReferences,
} from "@/workspace/browser/browser-panel-state";
import {
	browserPanelAvailable,
	browserRestartTargetOpen,
	claimBrowserRestart,
} from "@/workspace/views/project-work-area-state";

test("browser UI stays gated until runtime reports a ready browser", () => {
	expect(browserPanelAvailable(null)).toBeFalse();
	expect(
		browserPanelAvailable({
			application: { state: "ready" },
			agent: { state: "ready" },
			browser: { state: "unavailable" },
		}),
	).toBeFalse();
	expect(
		browserPanelAvailable({
			application: { state: "ready" },
			agent: { state: "ready" },
			browser: { state: "ready" },
		}),
	).toBeTrue();
	expect(
		browserPanelAvailable({
			application: { state: "ready" },
			agent: { state: "ready" },
			browser: { state: "unavailable" },
		}),
	).toBeFalse();
});

test("browser panel validates URL entry and exposes explicit content states", () => {
	expect(safeBrowserURL("https://example.com/path")).toBe("https://example.com/path");
	expect(safeBrowserURL("javascript:alert(1)")).toBeNull();
	expect(safeBrowserURL("https://user@example.com")).toBeNull();
	expect(browserPanelScreenState(false, null, null)).toBe("empty");
	expect(browserPanelScreenState(true, null, null)).toBe("loading");
	expect(browserPanelScreenState(false, "failed", null)).toBe("error");
	expect(browserPanelScreenState(false, null, "/v1/artifacts/panel.png")).toBe("ready");
});

test("snapshot references are bounded and deduplicated for interaction controls", () => {
	expect(
		snapshotReferences(
			'button "Save" @save\nlink @next\nbutton @save\n- link "Learn more" [ref=e1]',
		),
	).toEqual(["@save", "@next", "@e1"]);
});

test("browser restart is single-flight and cannot replace a tab that was closed", () => {
	const target = {
		kind: "browser" as const,
		id: "browser-old",
		projectAreaId: "project-1",
		name: "Browser",
		panelId: "panel-old",
	};
	const inFlight = new Set<string>();
	expect(claimBrowserRestart(inFlight, target.id)).toBeTrue();
	expect(claimBrowserRestart(inFlight, target.id)).toBeFalse();
	expect(browserRestartTargetOpen([target], target)).toBeTrue();
	expect(browserRestartTargetOpen([], target)).toBeFalse();
	expect(
		browserRestartTargetOpen([{ ...target, panelId: "replacement-panel" }], target),
	).toBeFalse();
});

test("browser panel clears unsafe references and offers reconnect recovery", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/workspace/browser/browser-panel.svelte", import.meta.url),
	).text();
	expect(source).toContain('{ snapshot: result.output, reference: "" }');
	expect(source).toContain('setPanel({ snapshot: "", reference: "" })');
	expect(source).toContain("connectionGeneration");
	expect(source).toContain('command({ type: "screenshot" })');
	expect(source).toContain("Restart browser");
	expect(source).toContain("disabled={restartInFlight}");
	expect(source).toContain("disabled={controlsDisabled}");
});
