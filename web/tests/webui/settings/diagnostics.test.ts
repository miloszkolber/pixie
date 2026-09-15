import { expect, test } from "bun:test";
import type { RuntimeDiagnosticsReport } from "@pixie/shared";
import {
	hostHealthSummary,
	supportSnapshotExportAvailability,
	toDiagnosticsViewModel,
} from "@/settings/diagnostics";
import { settingsTabs } from "@/settings/settings-dialog";
import { SETTINGS_SECTION_LOADERS } from "@/settings/settings-sections";
import { SettingsSection } from "@/settings/state";
import { renderSvelte } from "../chat/svelte-render";

test("missing diagnostics remain unknown rather than zero or no-action", async () => {
	expect(settingsTabs().some((tab) => tab.section === SettingsSection.Diagnostics)).toBeTrue();
	expect(SETTINGS_SECTION_LOADERS.diagnostics).toBeFunction();
	const model = toDiagnosticsViewModel(null);
	expect(model.runs.activeCount).toBeNull();
	expect(model.deletionReconciliation.count).toBeNull();
	expect(model.deletionReconciliation.records).toBeNull();
	expect(model.schedule.state).toBe("unknown");
	expect(model.remediation).toBeNull();
	expect(hostHealthSummary(model.host)).toBe("Assistant-host configuration is unknown.");

	const markup = await renderSvelte("src/settings/sections/diagnostics-view.svelte", {});
	expect(markup).toContain('data-testid="diagnostics-refresh"');
	expect(markup).toContain('data-testid="diagnostics-export"');
	expect(markup).toContain("It does not collect assistant or system logs.");
	expect(markup).toContain("Active run count");
	expect(markup).toContain("Unknown");
	expect(markup).not.toContain("No action needed: host is reachable");
});

test("support export is unavailable without explicit controller authentication", () => {
	expect(supportSnapshotExportAvailability(false, true)).toEqual({
		available: false,
		reason: "Connect to the controller to export a snapshot.",
	});
	expect(supportSnapshotExportAvailability(true, false)).toEqual({
		available: false,
		reason: "Export unavailable: controller authentication is disabled.",
	});
	expect(supportSnapshotExportAvailability(true, true)).toEqual({ available: true, reason: null });
});

test("typed diagnostics retain negotiated operations and deletion remediation", () => {
	const report: RuntimeDiagnosticsReport = {
		capabilities: {
			compatible: true,
			missingRequired: [],
			operations: {
				deleteSession: true,
				forkSession: true,
				promptImage: true,
				promptEmbeddedContext: true,
				httpMcp: false,
				steer: true,
				renameSession: true,
				archiveSession: true,
				administration: false,
			},
			capabilities: { sessions: 1 },
			operationSet: { "pi.providers.list": true, "pi.preferences.read": false },
		},
		host: { configured: true, reachable: true, applicationReady: true },
		runs: { activeCount: 0 },
		deletionReconciliation: {
			count: 1,
			records: [
				{
					projectId: "project-a",
					sessionId: "chat-a",
					phase: "requested",
					reason: "dispatch outcome is uncertain",
					remediation: "Verify the native session before confirming.",
					uncertain: true,
				},
			],
		},
		schedule: { state: "degraded", reason: "schedule storage is unavailable" },
		remediation: ["Verify the native session before confirming."],
	};
	const model = toDiagnosticsViewModel(report);
	expect(model.runs.activeCount).toBe(0);
	expect(model.deletionReconciliation.count).toBe(1);
	expect(model.capabilities.operationSet ?? null).toEqual(report.capabilities.operationSet ?? null);
	expect(model.capabilities.operations?.administration).toBeFalse();
	expect(model.deletionReconciliation.records?.[0]?.uncertain).toBeTrue();
	expect(model.deletionReconciliation.records?.[0]?.remediation).toContain("Verify");
	expect(model.schedule.reason).toBe("schedule storage is unavailable");
});
