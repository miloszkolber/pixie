import { expect, test } from "bun:test";
import {
	buildUpgradeRecoveryState,
	classifyLazyAssetFailure,
	compareUpgradeCompatibility,
	type PendingMutation,
	reconcilePendingMutations,
	restoreDraftsAfterUpgrade,
	UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
} from "@/workspace/views/upgrade-recovery";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

function mutation(mutationId: string, disposition: PendingMutation["disposition"] = "uncertain") {
	return { mutationId, method: "schedule.update", label: "Update schedule", disposition };
}

test("UI-07 negotiates browser capabilities independently of host version and build hash", () => {
	const compatible = compareUpgradeCompatibility(
		{
			browserProtocol: 88,
			hostVersion: 1,
			buildHash: "old",
			browserCapabilities: { recovery: 2, drafts: 1 },
		},
		{
			browserProtocol: 88,
			hostVersion: 2,
			buildHash: "new",
			browserCapabilities: { recovery: 1, drafts: 1 },
		},
	);
	expect(compatible.browserCompatible).toBeTrue();
	expect(compatible.capabilitiesCompatible).toBeTrue();
	expect(compatible.hostCompatible).toBeFalse();
	expect(compatible.blockNewMutations).toBeFalse();

	const blocked = compareUpgradeCompatibility(
		{ browserProtocol: 88, hostVersion: 2, capabilities: { recovery: 0 } },
		{ browserProtocol: 88, hostVersion: 2, capabilities: { recovery: 1 } },
	);
	expect(blocked.browserCompatible).toBeFalse();
	expect(blocked.capabilitiesCompatible).toBeFalse();
	expect(blocked.blockNewMutations).toBeTrue();
});

test("UI-07 classifies a lazy 404 once, then waits for an explicit refresh", () => {
	const compatibility = compareUpgradeCompatibility(
		{ browserProtocol: 88, hostVersion: 2 },
		{ browserProtocol: 88, hostVersion: 2 },
	);
	for (const topology of ["direct", "controller-only"] as const) {
		const first = classifyLazyAssetFailure(
			{ asset: "assets/settings-models.js", httpStatus: 404, reloadAttempts: 0, topology },
			compatibility,
		);
		expect(first.kind).toBe("refresh-once");
		expect(first.allowReload).toBeTrue();
		expect(first.topology).toBe(topology);

		const repeated = classifyLazyAssetFailure(
			{
				asset: "assets/settings-models.js",
				httpStatus: 404,
				reloadAttempts: UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
				topology,
			},
			compatibility,
		);
		expect(repeated.kind).toBe("await-explicit-refresh");
		expect(repeated.allowReload).toBeFalse();
		expect(repeated.message).toContain("avoid a loop");
	}
	expect(
		classifyLazyAssetFailure(
			{
				asset: "assets/settings-models.js",
				httpStatus: 404,
				reloadAttempts: -20,
				topology: "direct",
			},
			compatibility,
		).allowReload,
	).toBeTrue();
	expect(
		classifyLazyAssetFailure(
			{
				asset: "assets/settings-models.js",
				httpStatus: 404,
				reloadAttempts: Number.POSITIVE_INFINITY,
				topology: "direct",
			},
			compatibility,
		).allowReload,
	).toBeFalse();
});

test("UI-07 preserves drafts and original mutation identities without replay", async () => {
	const original = mutation("schedule-original");
	const result = reconcilePendingMutations(
		[original, { ...original }, mutation("schedule-held", "unknown")],
		[],
	);
	expect(result.retryWithSameId).toEqual([original]);
	expect(result.retryWithSameId[0]?.mutationId).toBe("schedule-original");
	expect(result.held.map(({ mutationId }) => mutationId)).toEqual(["schedule-held"]);

	const state = buildUpgradeRecoveryState({
		drafts: { "session-1": "unsent text" },
		storage: null,
		pending: [original],
		ledger: [],
		peer: { browserProtocol: 88, hostVersion: 2 },
		current: { browserProtocol: 88, hostVersion: 2 },
		failure: {
			asset: "assets/lazy-view.js",
			httpStatus: 404,
			reloadAttempts: 0,
			topology: "direct",
		},
	});
	expect(state.drafts.preserved).toEqual({ "session-1": "unsent text" });
	expect(state.mutations.retryWithSameId[0]?.mutationId).toBe("schedule-original");
	expect(restoreDraftsAfterUpgrade(state.drafts.preserved, { "session-1": "newer text" })).toEqual({
		"session-1": "newer text",
	});

	const workArea = await source("workspace/views/project-work-area.svelte");
	const helper = await source("workspace/views/upgrade-recovery.ts");
	expect(workArea).not.toContain("window.location.reload");
	expect(helper).not.toContain("crypto.randomUUID");
	expect(helper).not.toContain("Math.random");
});
