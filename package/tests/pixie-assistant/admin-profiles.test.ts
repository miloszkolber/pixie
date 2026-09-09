import { expect, test } from "bun:test";
import {
	ADMIN_FEATURES,
	ADMIN_OPERATIONS,
	evaluateAdminProfiles,
	operationSupport,
	PI_TOOLS_CALL_BLOCKER,
} from "../../../assistant/src/admin-profiles/index.ts";
import {
	BRIDGE_CONTEXT_REQUIREMENTS,
	BRIDGE_PUBLIC_EXPORTS,
	BRIDGE_RUNTIME_HOOKS,
	BRIDGE_SDK_BASELINE,
	BRIDGE_STORAGE_REQUIREMENTS,
	discoverBridgeCapabilities,
} from "../../../assistant/src/bridge/feasibility.ts";

function bridge() {
	return discoverBridgeCapabilities({
		distribution: "npm",
		installationId: "admin-profile-fixture",
		sdkVersion: BRIDGE_SDK_BASELINE,
		import: {
			installationId: "admin-profile-fixture",
			moduleOrigin: "/fixture/node_modules/@earendil-works/pi-coding-agent/dist/index.js",
			packageName: "@earendil-works/pi-coding-agent",
			packageVersion: BRIDGE_SDK_BASELINE,
			live: true,
			originVerified: true,
			runtimeExports: BRIDGE_PUBLIC_EXPORTS.filter((entry) => entry.kind === "runtime").map(
				(entry) => entry.symbol,
			),
			typeExports: BRIDGE_PUBLIC_EXPORTS.filter((entry) => entry.kind === "type").map(
				(entry) => entry.symbol,
			),
		},
		context: {
			live: true,
			members: [...BRIDGE_CONTEXT_REQUIREMENTS],
			hooks: [...BRIDGE_RUNTIME_HOOKS],
		},
		storage: {
			live: true,
			members: [...BRIDGE_STORAGE_REQUIREMENTS],
			lock: { available: true, identity: "native", matchesNative: true },
		},
	});
}

test("the retained catalog covers every administration and optional feature row", () => {
	expect(ADMIN_FEATURES.map((feature) => feature.id)).toEqual([
		"FC17",
		"FC18",
		"FC19",
		"FC20",
		"FC21",
		"FC22",
		"FC23",
		"FC24",
		"FC25",
		"FC26",
		"FC27",
		"FC28",
	]);
	expect(ADMIN_OPERATIONS.every((entry) => entry.tuiFallback === false)).toBe(true);
});

test("vanilla, authoring and controller routes remain usable without optional administration", () => {
	const report = evaluateAdminProfiles({
		vanilla: { available: true },
		authoring: { available: true },
		controller: { available: true },
		optional: { nativeExtension: false, subagent: false, llama: false },
	});
	expect(report.profiles.V.status).toBe("available");
	expect(report.profiles.A.status).toBe("blocked");
	expect(
		operationSupport("pi.providers.list", {
			vanilla: { available: true },
		}).status,
	).toBe("supported");
	expect(
		operationSupport("pi.sources.update", {
			vanilla: { available: true },
			authoring: { available: true },
		}).status,
	).toBe("supported");
	expect(
		operationSupport("pi.subagent.execute", {
			vanilla: { available: true },
			optional: { subagent: false },
		}).status,
	).toBe("optional-unavailable");
});

test("public bridge evidence enables FC17-FC23 while absent adapter execution stays an honest blocker", () => {
	const evidence = {
		vanilla: { available: true },
		authoring: { available: true },
		controller: { available: true },
		bridge: bridge(),
		mcp: { available: true, publicAdministration: true, version: "2.32.1" },
		optional: { nativeExtension: true, subagent: true, llama: true },
	};
	const report = evaluateAdminProfiles(evidence);
	expect(report.profiles.A.status).toBe("available");
	expect(report.profiles.M.status).toBe("available");
	expect(operationSupport("provider.loginStart", evidence).status).toBe("supported");
	expect(operationSupport("pi.tools.call", evidence)).toMatchObject({ status: "blocked" });

	const adapterBlocked = evaluateAdminProfiles({
		...evidence,
		mcp: { available: true, publicAdministration: false },
	});
	const tool = operationSupport("pi.tools.call", {
		...evidence,
		mcp: { available: true, publicAdministration: false },
	});
	expect(adapterBlocked.profiles.M.status).toBe("blocked");
	expect(tool.status).toBe("blocked");
	expect(tool.blockers).toEqual([PI_TOOLS_CALL_BLOCKER]);
	expect(tool.userVisibleLimitation).not.toContain("TUI");
});

test("a missing public administration bridge never becomes a no-op supported control", () => {
	const result = operationSupport("pi.defaults.save", {
		vanilla: { available: true },
		bridge: {
			...bridge(),
			status: "blocked",
			blockers: [
				{
					code: "bridge_unavailable",
					fcId: "BRIDGE-01",
					surface: "storage",
					distribution: "npm",
					version: BRIDGE_SDK_BASELINE,
					missingPublicSymbol: "SettingsStorage lock",
					reproduction: "fixture has no native lock",
					attemptedAlternatives: [],
					userVisibleLimitation: "Native settings administration is unavailable.",
					owner: "B/E/A",
					releaseConsequence: "BRIDGE-01 stays open for this installed Pi profile.",
				},
			],
		},
	});
	expect(result.status).toBe("blocked");
	expect(result.blockers[0]).toMatchObject({ code: "bridge_unavailable", fcId: "BRIDGE-01" });
	expect(result.tuiFallback).toBe(false);
});
