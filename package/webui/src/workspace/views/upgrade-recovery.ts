/**
 * UI-07/X14 lazy-asset upgrade recovery (pure helpers).
 *
 * Old-browser upgrade recovery without draft loss or mutation re-execution.
 * All helpers are pure and side-effect free: reconciling never sends work,
 * preserving never discards input, and classifying never reloads. Callers
 * perform explicit user-gesture retries and reloads separately.
 *
 * Contracts covered (roadmap/contracts.md UI navigation and persistence):
 * - Compare browser protocol and capabilities independently of build hash
 *   and host v2. Unsupported peers stop new mutations and present explicit
 *   refresh or recovery.
 * - Preserve drafts and selection before reload. Unavailable storage must
 *   not silently discard unsaved input.
 * - Reconcile acknowledged and uncertain operations with their original
 *   mutation IDs, never resend as fresh work.
 * - Old lazy-asset 404s resolve to an actionable recovery state, never a
 *   blank panel or an infinite reload loop. Retained old assets stay
 *   bounded. Both direct and controller-only topologies share the path.
 */

export const UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS = 1;
export const UPGRADE_RECOVERY_MAX_RETAINED_ASSETS = 3;

export type MutationDisposition = "acknowledged" | "uncertain" | "unknown";

export interface PendingMutation {
	mutationId: string;
	method: string;
	label: string;
	disposition: MutationDisposition;
}

export interface MutationLedgerEntry {
	mutationId: string;
	outcome: "committed" | "rejected";
}

export interface MutationReconciliation {
	/** Uncertain work that may be retried only with its original identity. */
	retryWithSameId: PendingMutation[];
	/** Work that must wait for ledger confirmation; never auto-resent. */
	held: PendingMutation[];
	/** Ledger-settled mutation IDs; callers drop them from pending. */
	settled: string[];
}

function isNonEmptyId(value: unknown): value is string {
	return typeof value === "string" && value.length > 0 && value.length <= 512;
}

/**
 * Reconcile pending mutations against the authoritative ledger.
 * Never creates a fresh mutation identity: retry candidates keep their
 * original mutationId verbatim. Acknowledged and unknown entries are held,
 * never auto-resent, because a client cannot infer failure from an old
 * bundle or a lost socket alone.
 */
export function reconcilePendingMutations(
	pending: readonly PendingMutation[],
	ledger: readonly MutationLedgerEntry[],
): MutationReconciliation {
	const settledById = new Map<string, MutationLedgerEntry>();
	for (const entry of ledger) {
		if (!isNonEmptyId(entry.mutationId)) continue;
		if (!settledById.has(entry.mutationId)) settledById.set(entry.mutationId, entry);
	}
	const retryWithSameId: PendingMutation[] = [];
	const held: PendingMutation[] = [];
	const settled: string[] = [];
	for (const mutation of pending) {
		if (!isNonEmptyId(mutation.mutationId)) {
			continue;
		}
		if (settledById.has(mutation.mutationId)) {
			settled.push(mutation.mutationId);
			continue;
		}
		if (mutation.disposition === "uncertain") {
			retryWithSameId.push(mutation);
		} else {
			held.push(mutation);
		}
	}
	return { retryWithSameId, held, settled };
}

export type DraftMap = Record<string, string>;

export interface DraftPreservation {
	/** In-memory copy of the caller's drafts; never filtered. */
	preserved: DraftMap;
	unsavedWarning: string | null;
	storageAvailable: boolean;
}

export interface DraftStorage {
	save: (drafts: DraftMap) => void;
}

function copyDrafts(drafts: DraftMap): DraftMap {
	return { ...drafts };
}

/**
 * Preserve per-client drafts before offering refresh or reload.
 * Unavailable or throwing storage never discards input: drafts stay
 * preserved in memory with an explicit warning to copy unsaved work.
 */
export function preserveDraftsForRecovery(
	drafts: DraftMap,
	storage: DraftStorage | null,
): DraftPreservation {
	const preserved = copyDrafts(drafts);
	if (storage === null) {
		return {
			preserved,
			storageAvailable: false,
			unsavedWarning:
				"Local storage is unavailable. Drafts are kept in memory only. Copy unsaved work before reloading.",
		};
	}
	try {
		storage.save(copyDrafts(drafts));
	} catch {
		return {
			preserved,
			storageAvailable: false,
			unsavedWarning:
				"Drafts could not be written to local storage. Drafts are kept in memory only. Copy unsaved work before reloading.",
		};
	}
	return { preserved, unsavedWarning: null, storageAvailable: true };
}

function hasUnsavedDraft(value: unknown): value is string {
	return typeof value === "string" && value.trim() !== "";
}

/**
 * Restore drafts after an upgrade without losing either side.
 * A non-empty current draft always wins over an empty preserved one, and a
 * non-empty preserved draft restores where current is empty. Empty never
 * overwrites non-empty.
 */
export function restoreDraftsAfterUpgrade(preserved: DraftMap, current: DraftMap): DraftMap {
	const restored: DraftMap = { ...current };
	for (const [sessionKey, preservedDraft] of Object.entries(preserved)) {
		const currentDraft = restored[sessionKey];
		if (hasUnsavedDraft(currentDraft)) continue;
		if (hasUnsavedDraft(preservedDraft)) {
			restored[sessionKey] = preservedDraft;
		}
	}
	return restored;
}

export interface PeerVersions {
	browserProtocol: number | null;
	hostVersion: number | null;
	buildHash?: string;
}

export interface UpgradeCompatibility {
	browserCompatible: boolean;
	hostCompatible: boolean;
	blockNewMutations: boolean;
}

/**
 * Compare browser protocol and host versions independently of build hash.
 * A build-hash change alone never marks a peer incompatible; a protocol
 * mismatch always blocks new mutations until explicit recovery.
 */
export function compareUpgradeCompatibility(
	peer: PeerVersions,
	current: PeerVersions,
): UpgradeCompatibility {
	const browserCompatible =
		peer.browserProtocol !== null &&
		current.browserProtocol !== null &&
		peer.browserProtocol === current.browserProtocol;
	const hostCompatible =
		peer.hostVersion !== null &&
		current.hostVersion !== null &&
		peer.hostVersion === current.hostVersion;
	return { browserCompatible, hostCompatible, blockNewMutations: !browserCompatible };
}

export type UpgradeTopology = "direct" | "controller-only";

export interface LazyAssetFailure {
	asset: string;
	httpStatus: number;
	reloadAttempts: number;
	topology: UpgradeTopology;
}

export type LazyAssetRecoveryKind =
	| "refresh-once"
	| "incompatible-peer"
	| "await-explicit-refresh";

export interface LazyAssetRecovery {
	kind: LazyAssetRecoveryKind;
	asset: string;
	message: string;
	allowReload: boolean;
	blockNewMutations: boolean;
	topology: UpgradeTopology;
}

function isOldLazyAssetMissing(failure: LazyAssetFailure): boolean {
	return failure.httpStatus === 404 && failure.asset.trim() !== "";
}

/**
 * Classify an old lazy-asset failure into an actionable recovery state.
 * Never returns a blank panel and never permits an infinite reload loop:
 * at most UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS automatic refreshes are
 * offered, then recovery waits for an explicit user refresh with drafts
 * already preserved.
 */
export function classifyLazyAssetFailure(
	failure: LazyAssetFailure,
	compatibility: UpgradeCompatibility,
): LazyAssetRecovery {
	if (!compatibility.browserCompatible) {
		return {
			kind: "incompatible-peer",
			asset: failure.asset,
			topology: failure.topology,
			blockNewMutations: true,
			allowReload: true,
			message:
				"This browser bundle uses an unsupported protocol. New actions are paused. Drafts are preserved. Refresh explicitly to load the compatible bundle.",
		};
	}
	if (isOldLazyAssetMissing(failure)) {
		if (failure.reloadAttempts < UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS) {
			return {
				kind: "refresh-once",
				asset: failure.asset,
				topology: failure.topology,
				blockNewMutations: false,
				allowReload: true,
				message:
					`The requested asset is from a previous deployment (${failure.asset}). Drafts are preserved. Refresh once to load the current bundle.`,
			};
		}
		return {
			kind: "await-explicit-refresh",
			asset: failure.asset,
			topology: failure.topology,
			blockNewMutations: false,
			allowReload: false,
			message:
				`Refresh was already attempted for ${failure.asset}. Automatic reload is paused to avoid a loop. Copy unsaved work, then refresh explicitly.`,
		};
	}
	if (failure.reloadAttempts < UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS) {
		return {
			kind: "refresh-once",
			asset: failure.asset,
			topology: failure.topology,
			blockNewMutations: compatibility.blockNewMutations,
			allowReload: true,
			message:
				"The requested view failed to load after a deployment. Drafts are preserved. Refresh once to retry the current bundle.",
		};
	}
	return {
		kind: "await-explicit-refresh",
		asset: failure.asset,
		topology: failure.topology,
		blockNewMutations: compatibility.blockNewMutations,
		allowReload: false,
		message:
			"Automatic reload is paused to avoid a loop. Drafts are preserved. Copy unsaved work, then refresh explicitly.",
	};
}

/**
 * Keep retained old assets bounded. Newest entries win; the cap never
 * grows with the number of deployments.
 */
export function pruneRetainedAssets(
	assets: readonly string[],
	max: number = UPGRADE_RECOVERY_MAX_RETAINED_ASSETS,
): string[] {
	const cap = Number.isFinite(max) ? Math.max(0, Math.floor(max)) : 0;
	if (cap === 0) return [];
	const seen = new Set<string>();
	const retained: string[] = [];
	for (let index = assets.length - 1; index >= 0; index -= 1) {
		const asset = assets[index];
		if (typeof asset !== "string" || asset.trim() === "") continue;
		if (seen.has(asset)) continue;
		seen.add(asset);
		retained.unshift(asset);
		if (retained.length >= cap) break;
	}
	return retained;
}

export interface UpgradeRecoveryState {
	drafts: DraftPreservation;
	mutations: MutationReconciliation;
	compatibility: UpgradeCompatibility;
	recovery: LazyAssetRecovery | null;
	/** Explicit refresh is offered only with drafts preserved and no loop. */
	canOfferRefresh: boolean;
}

/**
 * Build the explicit upgrade recovery state shown instead of a blank
 * panel. Drafts are always preserved first; refresh is offered only when
 * the lazy-asset classifier allows it.
 */
export function buildUpgradeRecoveryState(input: {
	drafts: DraftMap;
	storage: DraftStorage | null;
	pending: readonly PendingMutation[];
	ledger: readonly MutationLedgerEntry[];
	peer: PeerVersions;
	current: PeerVersions;
	failure: LazyAssetFailure | null;
}): UpgradeRecoveryState {
	const drafts = preserveDraftsForRecovery(input.drafts, input.storage);
	const mutations = reconcilePendingMutations(input.pending, input.ledger);
	const compatibility = compareUpgradeCompatibility(input.peer, input.current);
	const recovery =
		input.failure === null ? null : classifyLazyAssetFailure(input.failure, compatibility);
	return {
		drafts,
		mutations,
		compatibility,
		recovery,
		canOfferRefresh: recovery !== null && recovery.allowReload,
	};
}
