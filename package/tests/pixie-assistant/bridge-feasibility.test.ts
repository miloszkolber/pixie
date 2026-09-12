import { expect, test } from "bun:test";
import {
	BRIDGE_CONTEXT_REQUIREMENTS,
	BRIDGE_PUBLIC_EXPORTS,
	BRIDGE_RUNTIME_HOOKS,
	BRIDGE_SDK_BASELINE,
	BRIDGE_STORAGE_REQUIREMENTS,
	discoverBridgeCapabilities,
} from "../../../assistant/src/bridge/feasibility.ts";

const imports = {
	installationId: "npm-fixture",
	moduleOrigin: "/fixture/node_modules/@earendil-works/pi-coding-agent/dist/index.js",
	packageName: "@earendil-works/pi-coding-agent",
	packageVersion: BRIDGE_SDK_BASELINE,
	live: true,
	originVerified: true,
	runtimeExports: ["ModelRuntime", "SettingsManager", "DefaultPackageManager"],
	typeExports: ["ExtensionAPI", "ExtensionContext", "SettingsStorage"],
} as const;

const context = {
	live: true,
	members: [...BRIDGE_CONTEXT_REQUIREMENTS],
	hooks: [...BRIDGE_RUNTIME_HOOKS],
} as const;

const storage = {
	live: true,
	members: [...BRIDGE_STORAGE_REQUIREMENTS],
	lock: { available: true, identity: "native", matchesNative: true },
} as const;

test("npm and standalone profiles require a live import from the selected installation", () => {
	const npm = discoverBridgeCapabilities({
		distribution: "npm",
		installationId: "npm-fixture",
		sdkVersion: BRIDGE_SDK_BASELINE,
		import: imports,
		context,
		storage,
	});
	const standalone = discoverBridgeCapabilities({
		distribution: "standalone",
		installationId: "standalone-fixture",
		sdkVersion: BRIDGE_SDK_BASELINE,
		import: {
			...imports,
			installationId: "standalone-fixture",
			moduleOrigin: "/opt/pi/dist/index.js",
		},
		context,
		storage,
	});

	expect(npm.status).toBe("available");
	expect(standalone.status).toBe("available");
	expect(npm.vanillaFunctional).toBe(true);
	expect(npm.blockers).toEqual([]);
	expect(BRIDGE_PUBLIC_EXPORTS.map((entry) => entry.symbol)).toContain("ModelRuntime");
});

test("missing public exports and private lock identity are blockers, never mocked capabilities", () => {
	const result = discoverBridgeCapabilities({
		distribution: "standalone",
		installationId: "standalone-fixture",
		sdkVersion: BRIDGE_SDK_BASELINE,
		import: {
			...imports,
			installationId: "standalone-fixture",
			runtimeExports: ["ModelRuntime", "SettingsManager"],
		},
		context: { ...context, members: context.members.filter((member) => member !== "context.ui") },
		storage: { ...storage, lock: { available: true, identity: "different", matchesNative: false } },
	});

	expect(result.status).toBe("blocked");
	expect(result.vanillaFunctional).toBe(true);
	expect(result.importedPublicApi.available).toBe(false);
	expect(result.blockers.map((blocker) => blocker.missingPublicSymbol)).toEqual(
		expect.arrayContaining(["DefaultPackageManager"]),
	);
	expect(result.blockers.some((blocker) => blocker.missingPublicSymbol === "context.ui")).toBe(
		true,
	);
	expect(
		result.blockers.some(
			(blocker) => blocker.missingPublicSymbol === "native SettingsStorage lock identity",
		),
	).toBe(true);
	for (const blocker of result.blockers) {
		expect(blocker.code).toBe("bridge_unavailable");
		expect(blocker.fcId).toBe("BRIDGE-01");
		expect(blocker.reproduction.length).toBeGreaterThan(0);
		expect(blocker.releaseConsequence).toContain("stays open");
	}
});

test("static declarations without a live selected-installation import stay blocked", () => {
	const result = discoverBridgeCapabilities({
		distribution: "npm",
		installationId: "npm-fixture",
		sdkVersion: BRIDGE_SDK_BASELINE,
		import: { ...imports, live: false, originVerified: false },
		context,
		storage,
	});

	expect(result.status).toBe("blocked");
	expect(result.importedPublicApi.available).toBe(false);
	expect(result.blockers.map((blocker) => blocker.missingPublicSymbol)).toEqual(
		expect.arrayContaining(["verified public import origin", "live public module import"]),
	);
});
