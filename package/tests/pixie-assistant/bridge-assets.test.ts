import { expect, test } from "bun:test";
import {
	BRIDGE_ASSET_MAX_BYTES,
	BRIDGE_EXTENSION_FLAG,
	planBridgeAssetOptIn,
} from "../../../assistant/src/bridge/assets.ts";

const asset = {
	path: "/var/lib/pixie/assistant/bridge/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.js",
	realPath: "/var/lib/pixie/assistant/bridge/sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.js",
	bytes: 4096,
	sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	contentAddressed: true,
	digestVerified: true,
	readOnly: true,
	pathVerified: true,
} as const;

test("bridge assets remain disabled until explicit opt-in", () => {
	const result = planBridgeAssetOptIn({
		enabled: false,
		distribution: "npm",
		privateRoot: "/var/lib/pixie/assistant/bridge",
		privateRootVerified: true,
		asset,
		extensionLoader: "documented",
	});

	expect(result.status).toBe("disabled");
	expect(result.enabled).toBe(false);
	expect(result.argv).toEqual([]);
	expect(result.asset).toBeNull();
	expect(result.blockers).toEqual([]);
});

test("enabled npm and standalone assets use only the documented explicit extension argv", () => {
	for (const distribution of ["npm", "standalone"] as const) {
		const result = planBridgeAssetOptIn({
			enabled: true,
			distribution,
			sdkVersion: "0.85.1",
			privateRoot: "/var/lib/pixie/assistant/bridge",
			privateRootVerified: true,
			asset,
			nativeArgv: ["--some-operator-flag"],
			extensionLoader: "documented",
		});
		expect(result.status).toBe("ready");
		expect(result.argv).toEqual(["--some-operator-flag", BRIDGE_EXTENSION_FLAG, asset.path]);
		expect(result.asset).toEqual(asset);
	}
});

test("asset provenance, private path and loader gaps block enablement without a fallback", () => {
	const result = planBridgeAssetOptIn({
		enabled: true,
		distribution: "standalone",
		privateRoot: "/var/lib/pixie/assistant/bridge",
		privateRootVerified: true,
		asset: {
			...asset,
			path: "/tmp/not-private.js",
			realPath: "/tmp/not-private.js",
			bytes: BRIDGE_ASSET_MAX_BYTES + 1,
			readOnly: false,
			digestVerified: false,
		},
		extensionLoader: "unverified",
	});

	expect(result.status).toBe("blocked");
	expect(result.argv).toEqual([]);
	expect(result.blockers.map((blocker) => blocker.missingPublicSymbol)).toEqual(
		expect.arrayContaining([
			"Pi documented --extension loader",
			"private bridge asset path",
			"content-addressed bridge asset digest",
			"read-only bridge asset",
		]),
	);
	expect(result.blockers.every((blocker) => blocker.code === "bridge_unavailable")).toBe(true);
});
