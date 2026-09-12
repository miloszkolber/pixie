import { expect, test } from "bun:test";
import {
	classifyPiDistribution,
	discoverPiInstallationFromRefs,
	formatIndependentVersionReport,
	isSupportedPiSdkVersion,
	parsePiVersionText,
	piCandidatePaths,
	reportIndependentVersions,
	splitPathEnv,
	summarizePiDiscovery,
} from "../../../assistant/src/discovery.ts";

test("PATH splitting preserves spaces and drops empty entries", () => {
	expect(splitPathEnv(undefined)).toEqual([]);
	expect(splitPathEnv("")).toEqual([]);
	expect(splitPathEnv("/usr/local/bin:/opt/pi/bin")).toEqual(["/usr/local/bin", "/opt/pi/bin"]);
	expect(splitPathEnv("/custom prefix with spaces/bin:/nonexistent::/usr/bin")).toEqual([
		"/custom prefix with spaces/bin",
		"/nonexistent",
		"/usr/bin",
	]);
	expect(piCandidatePaths(["/custom prefix with spaces/bin", "/usr/bin"])).toEqual([
		"/custom prefix with spaces/bin/pi",
		"/usr/bin/pi",
	]);
	expect(piCandidatePaths([])).toEqual([]);
});

test("distribution classification keeps npm and standalone explicit", () => {
	expect(classifyPiDistribution({ sdkVersion: null })).toBe("unknown");
	expect(
		classifyPiDistribution({
			sdkVersion: "0.85.1",
			resolved: "/repo/node_modules/.bin/pi",
			realPath: "/repo/node_modules/@earendil-works/pi-coding-agent/dist/pi.js",
		}),
	).toBe("npm");
	expect(
		classifyPiDistribution({
			sdkVersion: "0.85.1",
			resolved: "/opt/pi/bin/pi.js",
			realPath: "/opt/pi/bin/pi.js",
		}),
	).toBe("npm");
	// SDK plus native binary outside node_modules is standalone, not npm.
	expect(
		classifyPiDistribution({
			sdkVersion: "0.85.1",
			resolved: "/usr/local/bin/pi",
			realPath: "/usr/local/bin/pi",
		}),
	).toBe("standalone");
	// SDK visible without an executable is the npm profile (binary may still resolve later).
	expect(classifyPiDistribution({ sdkVersion: "0.85.1" })).toBe("npm");
	// Standalone binary without an SDK import surface stays standalone.
	expect(
		classifyPiDistribution({
			sdkVersion: null,
			resolved: "/usr/local/bin/pi",
			realPath: "/usr/local/bin/pi",
		}),
	).toBe("standalone");
	// Paths with spaces and custom prefixes classify by content, not by splitting.
	expect(
		classifyPiDistribution({
			sdkVersion: null,
			resolved: "/custom prefix with spaces/bin/pi",
			realPath: "/custom prefix with spaces/bin/pi",
		}),
	).toBe("standalone");
	// Symlinked npm launcher still counts as npm through the real path.
	expect(
		classifyPiDistribution({
			sdkVersion: null,
			resolved: "/tmp/links/pi",
			realPath: "/tmp/pkg/node_modules/@earendil-works/pi-coding-agent/pi.js",
		}),
	).toBe("npm");
});

test("version text parsing never conflates assistant and Pi identities", () => {
	expect(parsePiVersionText("0.85.1")).toBe("0.85.1");
	expect(parsePiVersionText("v0.85.1")).toBe("0.85.1");
	expect(parsePiVersionText("pi 0.85.1")).toBe("0.85.1");
	expect(parsePiVersionText(JSON.stringify({ version: "0.85.1" }))).toBe("0.85.1");
	expect(parsePiVersionText("no version here")).toBeNull();
	expect(parsePiVersionText("")).toBeNull();
	expect(parsePiVersionText(null)).toBeNull();
	expect(parsePiVersionText(undefined)).toBeNull();
	expect(isSupportedPiSdkVersion("0.85.1")).toBe(true);
	expect(isSupportedPiSdkVersion("0.84.0")).toBe(false);
	expect(isSupportedPiSdkVersion(null)).toBe(false);
	expect(isSupportedPiSdkVersion("", [])).toBe(false);
});

test("independent version reporting keeps assistant, SDK and executable separate", () => {
	const report = reportIndependentVersions({
		assistantVersion: "0.1.0",
		piSdkVersion: "0.85.1",
		piExecutableVersion: "0.85.1",
		piDistribution: "npm",
		protocolVersion: 1,
	});
	expect(report.assistantVersion).toBe("0.1.0");
	expect(report.piSdkVersion).toBe("0.85.1");
	expect(report.piExecutableVersion).toBe("0.85.1");
	expect(report).not.toHaveProperty("secret");

	const formatted = formatIndependentVersionReport(report);
	expect(formatted).toContain("pixie-assistant 0.1.0");
	expect(formatted).toContain("Pi SDK 0.85.1");
	expect(formatted).toContain("Pi executable 0.85.1");
	expect(formatted).toContain("protocol 1");

	// SDK and executable versions can differ; formatting must not merge them.
	const diverged = reportIndependentVersions({
		assistantVersion: "0.1.0",
		piSdkVersion: "0.85.1",
		piExecutableVersion: "0.84.0",
		piDistribution: "standalone",
		protocolVersion: 1,
	});
	const divergedText = formatIndependentVersionReport(diverged);
	expect(divergedText).toContain("Pi SDK 0.85.1");
	expect(divergedText).toContain("Pi executable 0.84.0");

	const unknownExecutable = reportIndependentVersions({
		assistantVersion: "0.1.0",
		piSdkVersion: null,
		piDistribution: "unknown",
		protocolVersion: 1,
	});
	expect(unknownExecutable.piSdkVersion).toBe("unknown");
	expect(unknownExecutable.piExecutableVersion).toBeNull();
	expect(formatIndependentVersionReport(unknownExecutable)).toContain("Pi SDK unknown");
});

test("discovery refs combine into explicit npm, standalone and missing summaries", () => {
	const npm = discoverPiInstallationFromRefs({
		sdkVersion: "0.85.1",
		sdkPath: "/repo/node_modules/@earendil-works/pi-coding-agent/package.json",
		executable: {
			requested: "pi",
			resolved: "/repo/node_modules/.bin/pi",
			realPath: "/repo/node_modules/@earendil-works/pi-coding-agent/dist/pi.js",
			source: "path",
		},
		executableError: null,
	});
	expect(npm.kind).toBe("npm");
	expect(summarizePiDiscovery(npm)).toMatch(/npm Pi/);

	const standalone = discoverPiInstallationFromRefs({
		sdkVersion: null,
		sdkPath: null,
		executable: {
			requested: "pi",
			resolved: "/custom prefix/bin/pi",
			realPath: "/custom prefix/bin/pi",
			source: "path",
		},
		executableError: null,
	});
	expect(standalone.kind).toBe("standalone");
	expect(summarizePiDiscovery(standalone)).toMatch(/standalone Pi/);
	expect(summarizePiDiscovery(standalone)).toMatch(/SDK not visible/);

	const missing = discoverPiInstallationFromRefs({
		sdkVersion: null,
		sdkPath: null,
		executable: null,
		executableError: "Pi executable not found on PATH (searched 1 directories)",
	});
	expect(missing.kind).toBe("unknown");
	expect(summarizePiDiscovery(missing)).toMatch(/No usable Pi installation/);
});
