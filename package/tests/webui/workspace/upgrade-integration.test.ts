import { expect, test } from "bun:test";
import {
	buildUpgradeRecoveryState,
	classifyLazyAssetFailure,
	compareUpgradeCompatibility,
	reconcilePendingMutations,
	UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
	type PendingMutation,
} from "@/workspace/views/upgrade-recovery";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

function pending(mutationId: string): PendingMutation {
	return { mutationId, method: "schedule.update", label: "Update schedule", disposition: "uncertain" };
}

test("settings lazy failures enter the bounded recovery surface", async () => {
	const workArea = await source("workspace/views/project-work-area.svelte");
	for (const contract of [
		"buildUpgradeRecoveryState",
		"recordSettingsRecovery",
		"upgrade-recovery",
		"upgrade-recovery-refresh",
		"upgrade-recovery-loop-paused",
		"UPGRADE_DRAFT_STORAGE_KEY",
		"draftStorage",
		"pruneRetainedAssets",
		"restoreDraftsAfterUpgrade",
	]) {
		expect(workArea).toContain(contract);
	}
	expect(workArea).not.toContain("window.location.reload");
});

test("recovery bounds retries while keeping mutation identity and drafts", () => {
	const compatibility = compareUpgradeCompatibility(
		{ browserProtocol: 87, hostVersion: 2 },
		{ browserProtocol: 88, hostVersion: 2 },
	);
	const blocked = classifyLazyAssetFailure(
		{
			asset: "settings/models.js",
			httpStatus: 404,
			reloadAttempts: UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
			topology: "controller-only",
		},
		compatibility,
	);
	expect(blocked.blockNewMutations).toBeTrue();
	expect(blocked.allowReload).toBeFalse();
	expect(blocked.message).toContain("avoid a loop");

	const original = pending("schedule-original");
	const reconciled = reconcilePendingMutations([original, { ...original }], []);
	expect(reconciled.retryWithSameId).toEqual([original]);

	const state = buildUpgradeRecoveryState({
		drafts: { "session-1": "keep this draft" },
		storage: null,
		pending: [original],
		ledger: [],
		peer: { browserProtocol: 88, hostVersion: 2 },
		current: { browserProtocol: 88, hostVersion: 2 },
		failure: {
			asset: "settings/models.js",
			httpStatus: 404,
			reloadAttempts: 0,
			topology: "direct",
		},
	});
	expect(state.drafts.preserved).toEqual({ "session-1": "keep this draft" });
	expect(state.mutations.retryWithSameId[0]?.mutationId).toBe("schedule-original");
	expect(state.canOfferRefresh).toBeTrue();
});
