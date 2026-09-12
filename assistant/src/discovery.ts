/**
 * Pi installation discovery for the separate assistant module.
 *
 * This is the pure, dependency-injected core behind `facade.ts` discovery:
 * npm versus standalone classification, PATH candidate construction that
 * preserves spaces, and independent version reporting where the assistant
 * version, the Pi SDK version and the Pi executable version are never merged.
 *
 * Filesystem effects stay with the caller (facade/doctor). These helpers take
 * refs and return decisions so tests prove behavior without a live Pi.
 */

import { join } from "node:path";
import type { PiDistributionKind } from "./facade.ts";

export type { PiDistributionKind };

export const PI_DISTRIBUTION_KINDS: readonly PiDistributionKind[] = [
	"npm",
	"standalone",
	"unknown",
];

export const SUPPORTED_PI_SDK_VERSIONS: readonly string[] = ["0.85.1"];

export interface PiSdkRef {
	version: string | null;
	path: string | null;
}

export interface PiExecutableRef {
	requested: string;
	resolved: string;
	realPath: string;
	source: "explicit" | "path";
}

export interface PiDiscoveryRefs {
	sdkVersion: string | null;
	sdkPath: string | null;
	executable: PiExecutableRef | null;
	executableError: string | null;
}

export interface PiDiscoveryResult extends PiDiscoveryRefs {
	kind: PiDistributionKind;
}

export interface IndependentVersionReport {
	assistantVersion: string;
	piSdkVersion: string;
	piExecutableVersion: string | null;
	piDistribution: PiDistributionKind;
	protocolVersion: number;
}

/** Split a POSIX PATH value without breaking directories that contain spaces. */
export function splitPathEnv(pathEnv: string | undefined): string[] {
	if (pathEnv === undefined || pathEnv === "") return [];
	return pathEnv.split(":").filter((dir) => dir.length > 0);
}

/** Build `pi` candidate paths for PATH directories, preserving spaces. */
export function piCandidatePaths(directories: readonly string[]): string[] {
	return directories.filter((dir) => dir.length > 0).map((dir) => join(dir, "pi"));
}

/**
 * Classify an installation as npm, standalone or unknown.
 *
 * - npm: an SDK version is visible and/or the executable resolves inside
 *   `node_modules` or ends with `.js` (the npm launcher shape).
 * - standalone: an executable resolves outside `node_modules` without a `.js`
 *   suffix (a native binary distribution).
 * - unknown: neither an SDK version nor an executable is available.
 *
 * The SDK version and the executable identity are inputs; this function never
 * probes the network, loads extensions or executes Pi.
 */
export function classifyPiDistribution(input: {
	sdkVersion: string | null;
	sdkPath?: string | null;
	resolved?: string | null;
	realPath?: string | null;
}): PiDistributionKind {
	const sdkVersion = input.sdkVersion;
	const resolved = input.resolved ?? null;
	const realPath = input.realPath ?? null;
	const executablePresent = resolved !== null || realPath !== null;
	const npmLike =
		(realPath !== null && realPath.includes("node_modules")) ||
		(resolved !== null && resolved.endsWith(".js"));

	if (sdkVersion && executablePresent) {
		return npmLike ? "npm" : "standalone";
	}
	if (sdkVersion) return "npm";
	if (executablePresent) return npmLike ? "npm" : "standalone";
	return "unknown";
}

/** Combine SDK and executable refs into one discovery result. */
export function discoverPiInstallationFromRefs(refs: PiDiscoveryRefs): PiDiscoveryResult {
	const kind = classifyPiDistribution({
		sdkVersion: refs.sdkVersion,
		sdkPath: refs.sdkPath,
		resolved: refs.executable?.resolved ?? null,
		realPath: refs.executable?.realPath ?? null,
	});
	return { ...refs, kind };
}

/**
 * Parse a version string without executing Pi.
 * Accepts `0.85.1`, `v0.85.1`, `pi 0.85.1` and package.json text containing a
 * `"version"` field. Returns the first `major.minor.patch` token or null.
 * A version string alone never proves API support; use it only for reporting.
 */
export function parsePiVersionText(output: unknown): string | null {
	if (typeof output !== "string" || output.length === 0) return null;
	try {
		const parsed: unknown = JSON.parse(output);
		if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
			const version = (parsed as Record<string, unknown>)["version"];
			if (typeof version === "string") {
				const nested = parsePiVersionText(version);
				if (nested) return nested;
			}
		}
	} catch {
		// Not JSON; fall through to token scan.
	}
	const match = output.match(/(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)/);
	return match?.[1] ?? null;
}

/** True when the SDK version is on the explicitly supported baseline list. */
export function isSupportedPiSdkVersion(
	version: string | null,
	supported: readonly string[] = SUPPORTED_PI_SDK_VERSIONS,
): boolean {
	if (version === null || version.length === 0) return false;
	return supported.includes(version);
}

/**
 * Report assistant, SDK and executable versions independently.
 * The three identities stay separate fields; formatting never merges the SDK
 * version into the executable version or vice versa.
 */
export function reportIndependentVersions(input: {
	assistantVersion: string;
	piSdkVersion: string | null;
	piExecutableVersion?: string | null;
	piDistribution: PiDistributionKind;
	protocolVersion: number;
}): IndependentVersionReport {
	const assistantVersion = input.assistantVersion.length > 0 ? input.assistantVersion : "0.0.0-dev";
	const piSdkVersion =
		typeof input.piSdkVersion === "string" && input.piSdkVersion.length > 0
			? input.piSdkVersion
			: "unknown";
	return {
		assistantVersion,
		piSdkVersion,
		piExecutableVersion: input.piExecutableVersion ?? null,
		piDistribution: input.piDistribution,
		protocolVersion: input.protocolVersion,
	};
}

/** Stable one-line rendering of an independent version report. */
export function formatIndependentVersionReport(report: IndependentVersionReport): string {
	const executable =
		report.piExecutableVersion === null
			? "Pi executable version unknown"
			: `Pi executable ${report.piExecutableVersion}`;
	return (
		`pixie-assistant ${report.assistantVersion} ` +
		`(Pi SDK ${report.piSdkVersion}; ${executable}; ` +
		`${report.piDistribution}; protocol ${report.protocolVersion})`
	);
}

/** Human summary that keeps npm and standalone distributions explicit. */
export function summarizePiDiscovery(result: PiDiscoveryResult): string {
	if (result.executable) {
		const sdk = result.sdkVersion !== null ? `Pi SDK ${result.sdkVersion}` : "Pi SDK not visible";
		return (
			`${result.kind} Pi (${sdk}) at ${result.executable.realPath} ` +
			`via ${result.executable.source}.`
		);
	}
	return `No usable Pi installation: ${result.executableError ?? "unknown"}.`;
}
