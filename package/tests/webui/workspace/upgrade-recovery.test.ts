import { expect, test } from "bun:test";
import {
	buildUpgradeRecoveryState,
	classifyLazyAssetFailure,
	compareUpgradeCompatibility,
	preserveDraftsForRecovery,
	pruneRetainedAssets,
	reconcilePendingMutations,
	restoreDraftsAfterUpgrade,
	UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
	UPGRADE_RECOVERY_MAX_RETAINED_ASSETS,
	type LazyAssetFailure,
	type MutationLedgerEntry,
	type PendingMutation,
} from "@/workspace/views/upgrade-recovery";

const webuiSrc = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiSrc)).text();
}

function pending(
	mutationId: string,
	disposition: PendingMutation["disposition"] = "uncertain",
): PendingMutation {
	return { mutationId, method: "schedule.create", label: `label ${mutationId}`, disposition };
}

function committed(mutationId: string): MutationLedgerEntry {
	return { mutationId, outcome: "committed" };
}

test("upgrade recovery never re-executes mutations and reuses original identities", async () => {
	const original = pending("schedule-aaa", "uncertain");
	const reconciled = reconcilePendingMutations([original], []);
	expect(reconciled.retryWithSameId).toHaveLength(1);
	expect(reconciled.retryWithSameId[0]?.mutationId).toBe("schedule-aaa");
	expect(reconciled.retryWithSameId[0]?.method).toBe("schedule.create");
	expect(reconciled.held).toHaveLength(0);
	expect(reconciled.settled).toHaveLength(0);
	// The helper returns the same identity verbatim; it never mints fresh work.
	expect(reconciled.retryWithSameId[0]).toEqual(original);

	const acknowledged = pending("schedule-bbb", "acknowledged");
	const unknown = pending("schedule-ccc", "unknown");
	const held = reconcilePendingMutations([acknowledged, unknown], []);
	expect(held.retryWithSameId).toHaveLength(0);
	expect(held.held.map((item) => item.mutationId).sort()).toEqual([
		"schedule-bbb",
		"schedule-ccc",
	]);

	const settled = reconcilePendingMutations([original, acknowledged], [committed("schedule-aaa")]);
	expect(settled.settled).toEqual(["schedule-aaa"]);
	expect(settled.retryWithSameId.map((item) => item.mutationId)).not.toContain("schedule-aaa");
	expect(settled.held.map((item) => item.mutationId)).not.toContain("schedule-aaa");

	// Implementation never generates a fresh identity internally.
	const helper = await source("workspace/views/upgrade-recovery.ts");
	expect(helper).not.toContain("randomId");
	expect(helper).not.toContain("crypto.randomUUID");
	expect(helper).not.toContain("Math.random");
});

test("upgrade recovery never loses drafts when storage is unavailable", () => {
	const drafts = { "session-1": "upgrade draft", "session-2": "  keep me  " };
	const memoryOnly = preserveDraftsForRecovery(drafts, null);
	expect(memoryOnly.preserved).toEqual(drafts);
	expect(memoryOnly.preserved).not.toBe(drafts);
	expect(memoryOnly.storageAvailable).toBeFalse();
	expect(memoryOnly.unsavedWarning).toContain("Copy unsaved work");

	const throwing = preserveDraftsForRecovery(drafts, {
		save: () => {
			throw new Error("quota exceeded");
		},
	});
	expect(throwing.preserved).toEqual(drafts);
	expect(throwing.storageAvailable).toBeFalse();
	expect(throwing.unsavedWarning).toContain("kept in memory");

	let saved: Record<string, string> | null = null;
	const stored = preserveDraftsForRecovery(drafts, {
		save: (next) => {
			saved = next;
		},
	});
	expect(stored.preserved).toEqual(drafts);
	expect(stored.storageAvailable).toBeTrue();
	expect(stored.unsavedWarning).toBeNull();
	expect(saved).toEqual(drafts);
});

test("upgrade recovery restores drafts without empty overwriting non-empty", () => {
	expect(restoreDraftsAfterUpgrade({ "s-1": "preserved draft" }, { "s-1": "" })).toEqual({
		"s-1": "preserved draft",
	});
	expect(restoreDraftsAfterUpgrade({ "s-1": "" }, { "s-1": "current draft" })).toEqual({
		"s-1": "current draft",
	});
	expect(restoreDraftsAfterUpgrade({ "s-1": "old" }, { "s-1": "newer" })).toEqual({
		"s-1": "newer",
	});
	expect(restoreDraftsAfterUpgrade({ "s-1": "  " }, {})).toEqual({});
	expect(
		restoreDraftsAfterUpgrade({ "s-1": "a", "s-2": "b" }, { "s-2": "", "s-3": "c" }),
	).toEqual({ "s-1": "a", "s-2": "b", "s-3": "c" });
});

test("upgrade recovery compares protocol independently of build hash", () => {
	const sameProtocolDifferentHash = compareUpgradeCompatibility(
		{ browserProtocol: 88, hostVersion: 2, buildHash: "old-bundle" },
		{ browserProtocol: 88, hostVersion: 2, buildHash: "new-bundle" },
	);
	expect(sameProtocolDifferentHash.browserCompatible).toBeTrue();
	expect(sameProtocolDifferentHash.hostCompatible).toBeTrue();
	expect(sameProtocolDifferentHash.blockNewMutations).toBeFalse();

	const protocolMismatchSameHash = compareUpgradeCompatibility(
		{ browserProtocol: 87, hostVersion: 2, buildHash: "same-hash" },
		{ browserProtocol: 88, hostVersion: 2, buildHash: "same-hash" },
	);
	expect(protocolMismatchSameHash.browserCompatible).toBeFalse();
	expect(protocolMismatchSameHash.blockNewMutations).toBeTrue();

	const hostOnlyMismatch = compareUpgradeCompatibility(
		{ browserProtocol: 88, hostVersion: 1 },
		{ browserProtocol: 88, hostVersion: 2 },
	);
	expect(hostOnlyMismatch.browserCompatible).toBeTrue();
	expect(hostOnlyMismatch.hostCompatible).toBeFalse();
	expect(hostOnlyMismatch.blockNewMutations).toBeFalse();
});

test("lazy-asset 404 resolves to an actionable recovery state without reload loops", () => {
	const compatible = compareUpgradeCompatibility(
		{ browserProtocol: 88, hostVersion: 2 },
		{ browserProtocol: 88, hostVersion: 2 },
	);
	for (const topology of ["direct", "controller-only"] as const) {
		const first: LazyAssetFailure = {
			asset: "assets/lazy-view.js",
			httpStatus: 404,
			reloadAttempts: 0,
			topology,
		};
		const recovery = classifyLazyAssetFailure(first, compatible);
		expect(recovery.kind).toBe("refresh-once");
		expect(recovery.allowReload).toBeTrue();
		expect(recovery.asset).toBe("assets/lazy-view.js");
		expect(recovery.topology).toBe(topology);
		expect(recovery.message.length).toBeGreaterThan(20);
		expect(recovery.message).toContain("Drafts are preserved");

		const repeated = classifyLazyAssetFailure(
			{ ...first, reloadAttempts: UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS },
			compatible,
		);
		expect(repeated.kind).toBe("await-explicit-refresh");
		expect(repeated.allowReload).toBeFalse();
		expect(repeated.message).toContain("avoid a loop");
		expect(repeated.message.length).toBeGreaterThan(20);
	}

	const incompatible = compareUpgradeCompatibility(
		{ browserProtocol: 87, hostVersion: 2 },
		{ browserProtocol: 88, hostVersion: 2 },
	);
	const blocked = classifyLazyAssetFailure(
		{ asset: "assets/lazy-view.js", httpStatus: 404, reloadAttempts: 0, topology: "direct" },
		incompatible,
	);
	expect(blocked.kind).toBe("incompatible-peer");
	expect(blocked.blockNewMutations).toBeTrue();
	expect(blocked.allowReload).toBeTrue();
	expect(blocked.message).toContain("unsupported protocol");
});

test("upgrade recovery keeps retained assets bounded and builds an explicit state", () => {
	expect(UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS).toBe(1);
	expect(UPGRADE_RECOVERY_MAX_RETAINED_ASSETS).toBe(3);
	expect(pruneRetainedAssets(["a.js", "b.js", "c.js", "d.js"])).toEqual(["b.js", "c.js", "d.js"]);
	expect(pruneRetainedAssets(["a.js", "a.js", "b.js", ""])).toEqual(["a.js", "b.js"]);
	expect(pruneRetainedAssets([], 0)).toEqual([]);

	const state = buildUpgradeRecoveryState({
		drafts: { "session-1": "do not lose me" },
		storage: null,
		pending: [pending("schedule-aaa", "uncertain"), pending("schedule-bbb", "unknown")],
		ledger: [],
		peer: { browserProtocol: 88, hostVersion: 2 },
		current: { browserProtocol: 88, hostVersion: 2 },
		failure: { asset: "assets/lazy-view.js", httpStatus: 404, reloadAttempts: 0, topology: "direct" },
	});
	expect(state.drafts.preserved).toEqual({ "session-1": "do not lose me" });
	expect(state.mutations.retryWithSameId.map((item) => item.mutationId)).toEqual([
		"schedule-aaa",
	]);
	expect(state.mutations.held.map((item) => item.mutationId)).toEqual(["schedule-bbb"]);
	expect(state.recovery?.kind).toBe("refresh-once");
	expect(state.canOfferRefresh).toBeTrue();

	const looped = buildUpgradeRecoveryState({
		drafts: { "session-1": "do not lose me" },
		storage: null,
		pending: [],
		ledger: [],
		peer: { browserProtocol: 88, hostVersion: 2 },
		current: { browserProtocol: 88, hostVersion: 2 },
		failure: {
			asset: "assets/lazy-view.js",
			httpStatus: 404,
			reloadAttempts: 5,
			topology: "controller-only",
		},
	});
	expect(looped.recovery?.kind).toBe("await-explicit-refresh");
	expect(looped.canOfferRefresh).toBeFalse();
	expect(looped.drafts.preserved).toEqual({ "session-1": "do not lose me" });

	const idle = buildUpgradeRecoveryState({
		drafts: {},
		storage: null,
		pending: [],
		ledger: [],
		peer: { browserProtocol: 88, hostVersion: 2 },
		current: { browserProtocol: 88, hostVersion: 2 },
		failure: null,
	});
	expect(idle.recovery).toBeNull();
	expect(idle.canOfferRefresh).toBeFalse();
});
