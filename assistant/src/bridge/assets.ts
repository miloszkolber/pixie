/**
 * Pure policy for loading the optional bridge asset.
 *
 * Materialization, hashing and chmod are deliberately outside this module.
 * Callers must provide evidence that the asset is already content-addressed,
 * read-only, private and free of symlink replacement before this planner can
 * produce the documented `--extension <path>` argv pair.
 */

import { isAbsolute, relative, resolve } from "node:path";
import {
	BRIDGE_SDK_BASELINE,
	type BridgeBlocker,
	type BridgeDistribution,
	createBridgeBlocker,
} from "./feasibility.ts";

export const BRIDGE_EXTENSION_FLAG = "--extension";
export const BRIDGE_ASSET_MAX_BYTES = 1024 * 1024;

export interface BridgeAssetInput {
	readonly path: string;
	readonly realPath: string;
	readonly bytes: number;
	readonly sha256: string;
	readonly contentAddressed: boolean;
	readonly digestVerified: boolean;
	readonly readOnly: boolean;
	/** The caller checked the private root and asset identity without following a replacement. */
	readonly pathVerified: boolean;
}

export interface BridgeAssetOptInInput {
	readonly enabled: boolean;
	readonly distribution: BridgeDistribution;
	readonly sdkVersion?: string | null;
	readonly privateRoot: string;
	/** The caller verified ownership and restrictive permissions for privateRoot. */
	readonly privateRootVerified?: boolean;
	readonly asset?: BridgeAssetInput;
	readonly nativeArgv?: readonly string[];
	/** Only the documented Pi extension loader is accepted. */
	readonly extensionLoader?: "documented" | "unsupported" | "unverified";
}

export interface BridgeAssetPlan {
	readonly version: 1;
	readonly enabled: boolean;
	readonly status: "disabled" | "ready" | "blocked";
	readonly argv: readonly string[];
	readonly asset: BridgeAssetInput | null;
	readonly blockers: readonly BridgeBlocker[];
}

function privatePath(root: string, path: string): boolean {
	if (!isAbsolute(root) || !isAbsolute(path) || root.includes("\0") || path.includes("\0"))
		return false;
	const relativePath = relative(resolve(root), resolve(path));
	return relativePath === "" || (!relativePath.startsWith("..") && !isAbsolute(relativePath));
}

function validArgv(argv: readonly string[]): boolean {
	return argv.every(
		(value) => typeof value === "string" && value.length > 0 && !value.includes("\0"),
	);
}

/**
 * Plan explicit bridge asset loading.  Disabled opt-in is a no-op and does
 * not inspect or infer an asset.  Enabled opt-in fails closed on every missing
 * provenance, storage or loader proof.
 */
export function planBridgeAssetOptIn(input: BridgeAssetOptInInput): BridgeAssetPlan {
	if (!input.enabled)
		return {
			version: 1,
			enabled: false,
			status: "disabled",
			argv: [],
			asset: null,
			blockers: [],
		};

	const version = input.sdkVersion ?? null;
	const blockers: BridgeBlocker[] = [];
	const add = (
		missingPublicSymbol: string,
		reproduction: string,
		userVisibleLimitation?: string,
	) => {
		blockers.push(
			createBridgeBlocker({
				distribution: input.distribution,
				version,
				surface: "asset",
				missingPublicSymbol,
				reproduction,
				userVisibleLimitation,
				releaseConsequence:
					"Bridge asset loading stays unavailable until explicit opt-in checks pass.",
			}),
		);
	};

	if (input.extensionLoader !== "documented")
		add(
			"Pi documented --extension loader",
			`The selected ${input.distribution} Pi did not provide a verified documented extension loader.`,
			"The bridge is not loaded; native extension discovery and vanilla RPC remain unchanged.",
		);
	if (version !== BRIDGE_SDK_BASELINE)
		add(
			`Pi SDK ${BRIDGE_SDK_BASELINE}`,
			`The selected ${input.distribution} Pi reports SDK version ${version ?? "unknown"}; the bridge asset baseline is ${BRIDGE_SDK_BASELINE}.`,
		);
	if (!privatePath(input.privateRoot, input.privateRoot))
		add(
			"assistant-owned private bridge storage",
			"The supplied bridge storage root is not an absolute private path.",
		);
	else if (input.privateRootVerified !== true)
		add(
			"verified assistant-owned private bridge storage",
			"Private-root ownership and restrictive permissions were not verified before opt-in.",
		);
	if (!input.asset) {
		add(
			"content-addressed read-only bridge asset",
			"Explicit opt-in was requested without an asset descriptor.",
		);
	} else {
		const asset = input.asset;
		if (!privatePath(input.privateRoot, asset.path))
			add(
				"private bridge asset path",
				"The selected asset is outside the assistant-owned private storage root.",
			);
		if (!asset.pathVerified || asset.path !== asset.realPath)
			add(
				"verified bridge asset file identity",
				"The asset path was not verified as the same regular file after staging.",
			);
		if (!asset.contentAddressed || !asset.digestVerified)
			add(
				"content-addressed bridge asset digest",
				"The asset was not verified against its declared content digest.",
			);
		if (!asset.readOnly)
			add(
				"read-only bridge asset",
				"The staged bridge asset is writable and cannot be passed to native Pi.",
			);
		if (
			!Number.isSafeInteger(asset.bytes) ||
			asset.bytes <= 0 ||
			asset.bytes > BRIDGE_ASSET_MAX_BYTES
		)
			add(
				`bridge asset size <= ${BRIDGE_ASSET_MAX_BYTES} bytes`,
				`The staged bridge asset size ${asset.bytes} exceeds the bounded asset limit or is invalid.`,
			);
		if (!/^[a-f0-9]{64}$/.test(asset.sha256))
			add("sha256 bridge asset digest", "The asset digest is not a lowercase SHA-256 value.");
	}

	const argv = [...(input.nativeArgv ?? [])];
	if (!validArgv(argv))
		add("operator native argv", "Native argv contains an empty or NUL-bearing argument.");
	if (input.asset && argv.includes(input.asset.path))
		add(
			"single explicit bridge asset argv entry",
			"Native argv already contains the bridge asset path; duplicate loading is refused.",
		);

	if (blockers.length > 0)
		return {
			version: 1,
			enabled: true,
			status: "blocked",
			argv: [],
			asset: input.asset ?? null,
			blockers,
		};

	const asset = input.asset!;
	return {
		version: 1,
		enabled: true,
		status: "ready",
		argv: [...argv, BRIDGE_EXTENSION_FLAG, asset.path],
		asset,
		blockers: [],
	};
}
