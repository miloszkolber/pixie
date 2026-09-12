#!/usr/bin/env bun

import { readFile } from "node:fs/promises";

/**
 * Versioned evidence-bundle schema accepted for local release evidence (D7).
 *
 * A bundle records what was inspected, on which platform/profile it was
 * inspected, and the result of each named assertion. It is intentionally
 * data-only: no registry, network or publication state is implied.
 *
 * Package-artifact and controller-image GATE assertions carry a compact JSON
 * `detail` payload so the release gates can reconstruct the exact archive
 * entries and image identity that were observed. Assertions whose status is
 * not `pass` carry a plain-text reason instead.
 */

export const EVIDENCE_BUNDLE_SCHEMA_VERSION = 1 as const;

export const EVIDENCE_ASSERTION_KINDS = ["FC", "X", "GATE"] as const;
export const EVIDENCE_ASSERTION_STATUSES = ["pass", "fail", "blocked"] as const;
export const EVIDENCE_BUNDLE_PROFILES = ["assistant", "full-host", "controller-image"] as const;
export const EVIDENCE_PLATFORM_ARCHITECTURES = ["amd64", "arm64"] as const;
export const EVIDENCE_PLATFORM_OS = "linux" as const;

export const EVIDENCE_SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
export const EVIDENCE_RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
export const EVIDENCE_SHA256_PATTERN = /^[0-9a-f]{64}$/;

export const PACKAGE_ARCHIVE_ASSERTION_PREFIX = "PKG-ARCHIVE-";
export const CONTROLLER_IMAGE_ASSERTION_ID = "IMG-CONTROLLER";
/** Local coverage and performance producers own these single-assertion rows. */
export const COVERAGE_ASSERTION_ID = "COVERAGE-01";
export const PERFORMANCE_ASSERTION_ID = "PERF-01";
/** One `BIN-PROBE-<index>-<probe>` assertion is recorded per packaged-binary probe. */
export const PACKAGED_BINARY_ASSERTION_PREFIX = "BIN-PROBE";

const ISO8601_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

export type EvidenceAssertionKind = (typeof EVIDENCE_ASSERTION_KINDS)[number];
export type EvidenceAssertionStatus = (typeof EVIDENCE_ASSERTION_STATUSES)[number];
export type EvidenceBundleProfile = (typeof EVIDENCE_BUNDLE_PROFILES)[number];
export type EvidencePlatformArchitecture = (typeof EVIDENCE_PLATFORM_ARCHITECTURES)[number];

export interface EvidenceArtifact {
	name: string;
	sha256: string;
}

export interface EvidenceAssertion {
	kind: EvidenceAssertionKind;
	id: string;
	status: EvidenceAssertionStatus;
	command: string;
	artifact?: EvidenceArtifact;
	detail: string;
}

export interface EvidencePlatform {
	os: typeof EVIDENCE_PLATFORM_OS;
	arch: EvidencePlatformArchitecture;
}

export interface EvidenceBundle {
	schemaVersion: typeof EVIDENCE_BUNDLE_SCHEMA_VERSION;
	sourceCommit: string;
	releaseId: string;
	generatedAt: string;
	platform: EvidencePlatform;
	profile: EvidenceBundleProfile;
	assertions: readonly EvidenceAssertion[];
}

export const EVIDENCE_BUNDLE_TOP_LEVEL_KEYS = [
	"schemaVersion",
	"sourceCommit",
	"releaseId",
	"generatedAt",
	"platform",
	"profile",
	"assertions",
] as const;
export const EVIDENCE_ASSERTION_KEYS = [
	"kind",
	"id",
	"status",
	"command",
	"artifact",
	"detail",
] as const;
export const EVIDENCE_ARTIFACT_KEYS = ["name", "sha256"] as const;
export const EVIDENCE_PLATFORM_KEYS = ["os", "arch"] as const;

export class EvidenceBundleError extends Error {
	readonly issues: readonly string[];

	constructor(issues: readonly string[]) {
		super(`evidence bundle is invalid: ${issues.join("; ")}`);
		this.name = "EvidenceBundleError";
		this.issues = [...issues];
	}
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function unknownKeys(record: Record<string, unknown>, allowed: readonly string[]): string[] {
	const allow = new Set(allowed);
	return Object.keys(record).filter((key) => !allow.has(key));
}

function isISO8601(value: unknown): value is string {
	return (
		typeof value === "string" && ISO8601_PATTERN.test(value) && !Number.isNaN(Date.parse(value))
	);
}

function validateArtifact(value: unknown, label: string, issues: string[]): void {
	if (!isRecord(value)) {
		issues.push(`${label}: artifact must be an object`);
		return;
	}
	for (const key of unknownKeys(value, EVIDENCE_ARTIFACT_KEYS)) {
		issues.push(`${label}: unknown artifact field ${key}`);
	}
	if (typeof value.name !== "string" || value.name.trim() === "") {
		issues.push(`${label}: artifact name is required`);
	}
	if (typeof value.sha256 !== "string" || value.sha256.trim() === "") {
		issues.push(`${label}: artifact SHA-256 digest is required`);
	} else if (!EVIDENCE_SHA256_PATTERN.test(value.sha256)) {
		issues.push(`${label}: artifact SHA-256 digest must be 64 lowercase hex characters`);
	}
}

function validateAssertion(value: unknown, index: number, issues: string[]): void {
	const label = `assertions[${index}]`;
	if (!isRecord(value)) {
		issues.push(`${label}: assertion must be an object`);
		return;
	}
	for (const key of unknownKeys(value, EVIDENCE_ASSERTION_KEYS)) {
		issues.push(`${label}: unknown assertion field ${key}`);
	}
	if (
		typeof value.kind !== "string" ||
		!(EVIDENCE_ASSERTION_KINDS as readonly string[]).includes(value.kind)
	) {
		issues.push(`${label}: unknown assertion kind ${JSON.stringify(value.kind)}`);
	}
	if (typeof value.id !== "string" || value.id.trim() === "") {
		issues.push(`${label}: assertion id is required`);
	}
	if (
		typeof value.status !== "string" ||
		!(EVIDENCE_ASSERTION_STATUSES as readonly string[]).includes(value.status)
	) {
		issues.push(`${label}: unknown assertion status ${JSON.stringify(value.status)}`);
	}
	if (typeof value.command !== "string" || value.command.trim() === "") {
		issues.push(`${label}: exact assertion command is required`);
	}
	if (typeof value.detail !== "string" || value.detail.trim() === "") {
		issues.push(`${label}: assertion detail is required`);
	}
	if (value.artifact !== undefined) {
		validateArtifact(value.artifact, `${label} artifact`, issues);
	}
}

export function evidenceBundleIssues(value: unknown): string[] {
	const issues: string[] = [];
	if (!isRecord(value)) {
		return ["evidence bundle must be a JSON object"];
	}
	for (const key of unknownKeys(value, EVIDENCE_BUNDLE_TOP_LEVEL_KEYS)) {
		issues.push(`unknown evidence bundle field ${key}`);
	}
	if (value.schemaVersion !== EVIDENCE_BUNDLE_SCHEMA_VERSION) {
		issues.push(`unknown evidence bundle schemaVersion ${JSON.stringify(value.schemaVersion)}`);
	}
	if (typeof value.sourceCommit !== "string" || value.sourceCommit.trim() === "") {
		issues.push("sourceCommit is required");
	} else if (!EVIDENCE_SOURCE_COMMIT_PATTERN.test(value.sourceCommit)) {
		issues.push("sourceCommit must be exactly 40 lowercase hexadecimal characters");
	}
	if (typeof value.releaseId !== "string" || value.releaseId.trim() === "") {
		issues.push("releaseId is required");
	} else if (!EVIDENCE_RELEASE_ID_PATTERN.test(value.releaseId)) {
		issues.push("releaseId must be sha- plus 12 lowercase hexadecimal characters");
	} else if (
		typeof value.sourceCommit === "string" &&
		EVIDENCE_SOURCE_COMMIT_PATTERN.test(value.sourceCommit) &&
		value.releaseId !== `sha-${value.sourceCommit.slice(0, 12)}`
	) {
		issues.push("releaseId must match the first 12 characters of sourceCommit");
	}
	if (!isISO8601(value.generatedAt)) {
		issues.push("generatedAt must be an ISO8601 timestamp");
	}
	if (!isRecord(value.platform)) {
		issues.push("platform is required");
	} else {
		for (const key of unknownKeys(value.platform, EVIDENCE_PLATFORM_KEYS)) {
			issues.push(`unknown platform field ${key}`);
		}
		if (value.platform.os !== EVIDENCE_PLATFORM_OS) {
			issues.push(`platform.os must be ${EVIDENCE_PLATFORM_OS}`);
		}
		if (
			typeof value.platform.arch !== "string" ||
			!(EVIDENCE_PLATFORM_ARCHITECTURES as readonly string[]).includes(value.platform.arch)
		) {
			issues.push(`platform.arch must be one of ${EVIDENCE_PLATFORM_ARCHITECTURES.join(", ")}`);
		}
	}
	if (
		typeof value.profile !== "string" ||
		!(EVIDENCE_BUNDLE_PROFILES as readonly string[]).includes(value.profile)
	) {
		issues.push(`profile must be one of ${EVIDENCE_BUNDLE_PROFILES.join(", ")}`);
	}
	if (!Array.isArray(value.assertions)) {
		issues.push("assertions must be an array");
		return issues;
	}
	const seen = new Set<string>();
	for (const [index, assertion] of value.assertions.entries()) {
		validateAssertion(assertion, index, issues);
		if (!isRecord(assertion)) continue;
		if (typeof assertion.kind !== "string" || typeof assertion.id !== "string") continue;
		const key = `${assertion.kind}\u0000${assertion.id}`;
		if (seen.has(key)) {
			issues.push(`duplicate assertion ${assertion.kind}/${assertion.id}`);
			continue;
		}
		seen.add(key);
	}
	return issues;
}

/** Parse a validated bundle. Every rejection is returned as a single precise error. */
export function parseEvidenceBundle(value: unknown): EvidenceBundle {
	const issues = evidenceBundleIssues(value);
	if (issues.length > 0) throw new EvidenceBundleError(issues);
	return value as EvidenceBundle;
}

export function parseEvidenceBundleText(text: string): EvidenceBundle {
	let parsed: unknown;
	try {
		parsed = JSON.parse(text);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new EvidenceBundleError([`evidence bundle is not valid JSON: ${message}`]);
	}
	return parseEvidenceBundle(parsed);
}

export async function readEvidenceBundle(path: string): Promise<EvidenceBundle> {
	let text: string;
	try {
		text = await readFile(path, "utf8");
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new EvidenceBundleError([`cannot read evidence bundle ${path}: ${message}`]);
	}
	return parseEvidenceBundleText(text);
}

export interface EvidenceBundleInit {
	sourceCommit: string;
	releaseId: string;
	generatedAt: string;
	platform: EvidencePlatform;
	profile: EvidenceBundleProfile;
	assertions: readonly EvidenceAssertion[];
}

/** Build a bundle at the current schema version and validate it before returning. */
export function buildEvidenceBundle(init: EvidenceBundleInit): EvidenceBundle {
	return parseEvidenceBundle({
		schemaVersion: EVIDENCE_BUNDLE_SCHEMA_VERSION,
		sourceCommit: init.sourceCommit,
		releaseId: init.releaseId,
		generatedAt: init.generatedAt,
		platform: { os: init.platform.os, arch: init.platform.arch },
		profile: init.profile,
		assertions: init.assertions.map((assertion) => ({ ...assertion })),
	});
}

export function assertionsOfKind(
	bundle: EvidenceBundle,
	kind: EvidenceAssertionKind,
): readonly EvidenceAssertion[] {
	return bundle.assertions.filter((assertion) => assertion.kind === kind);
}

export function findAssertion(
	bundle: EvidenceBundle,
	kind: EvidenceAssertionKind,
	id: string,
): EvidenceAssertion | undefined {
	return bundle.assertions.find((assertion) => assertion.kind === kind && assertion.id === id);
}

export interface PackageArchiveAssertionKey {
	variant: "assistant" | "host";
	architecture: EvidencePlatformArchitecture;
}

export function packageArchiveAssertionId(
	variant: "assistant" | "host",
	architecture: EvidencePlatformArchitecture,
): string {
	return `${PACKAGE_ARCHIVE_ASSERTION_PREFIX}${variant}-${architecture}`;
}

export function parsePackageArchiveAssertionId(id: string): PackageArchiveAssertionKey | null {
	if (!id.startsWith(PACKAGE_ARCHIVE_ASSERTION_PREFIX)) return null;
	const remainder = id.slice(PACKAGE_ARCHIVE_ASSERTION_PREFIX.length);
	const separator = remainder.lastIndexOf("-");
	if (separator <= 0) return null;
	const variant = remainder.slice(0, separator);
	const architecture = remainder.slice(separator + 1);
	if (variant !== "assistant" && variant !== "host") return null;
	if (
		typeof architecture !== "string" ||
		!(EVIDENCE_PLATFORM_ARCHITECTURES as readonly string[]).includes(architecture)
	)
		return null;
	return { variant, architecture: architecture as EvidencePlatformArchitecture };
}

/**
 * Machine-readable `detail` for a package-archive GATE assertion. The archive
 * member list and contained binary digest let the gates re-run the exact
 * archive-content and binary-identity assertions rather than trusting a status.
 */
export interface PackageArchiveDetail {
	variant: "assistant" | "host";
	architecture: EvidencePlatformArchitecture;
	entries: readonly string[];
	binary: string;
	binarySha256: string;
}

const PACKAGE_ARCHIVE_DETAIL_KEYS = [
	"variant",
	"architecture",
	"entries",
	"binary",
	"binarySha256",
] as const;

export function encodePackageArchiveDetail(detail: PackageArchiveDetail): string {
	return JSON.stringify({
		variant: detail.variant,
		architecture: detail.architecture,
		entries: [...detail.entries].sort(),
		binary: detail.binary,
		binarySha256: detail.binarySha256,
	});
}

export function decodePackageArchiveDetail(detail: string): PackageArchiveDetail | null {
	let value: unknown;
	try {
		value = JSON.parse(detail);
	} catch {
		return null;
	}
	if (!isRecord(value)) return null;
	if (unknownKeys(value, PACKAGE_ARCHIVE_DETAIL_KEYS).length > 0) return null;
	if (value.variant !== "assistant" && value.variant !== "host") return null;
	if (
		typeof value.architecture !== "string" ||
		!(EVIDENCE_PLATFORM_ARCHITECTURES as readonly string[]).includes(value.architecture)
	) {
		return null;
	}
	if (
		!Array.isArray(value.entries) ||
		value.entries.some((entry) => typeof entry !== "string" || entry === "")
	) {
		return null;
	}
	if (typeof value.binary !== "string" || value.binary.trim() === "") return null;
	if (typeof value.binarySha256 !== "string" || !EVIDENCE_SHA256_PATTERN.test(value.binarySha256)) {
		return null;
	}
	return {
		variant: value.variant,
		architecture: value.architecture as EvidencePlatformArchitecture,
		entries: [...value.entries],
		binary: value.binary,
		binarySha256: value.binarySha256,
	};
}

/** Machine-readable `detail` for the controller-image GATE assertion. */
export interface ControllerImageDetail {
	tag: string | null;
	indexDigest: string | null;
	platformDigests: Partial<Record<EvidencePlatformArchitecture, string>>;
	architecture: EvidencePlatformArchitecture;
	os: typeof EVIDENCE_PLATFORM_OS;
	labels: Readonly<Record<string, string>>;
	provenance: boolean;
	sbom: boolean;
}

const CONTROLLER_IMAGE_DETAIL_KEYS = [
	"tag",
	"indexDigest",
	"platformDigests",
	"architecture",
	"os",
	"labels",
	"provenance",
	"sbom",
] as const;

export function encodeControllerImageDetail(detail: ControllerImageDetail): string {
	const platformDigests: Record<string, string> = {};
	for (const architecture of EVIDENCE_PLATFORM_ARCHITECTURES) {
		const digest = detail.platformDigests[architecture];
		if (digest !== undefined) platformDigests[architecture] = digest;
	}
	const labels: Record<string, string> = {};
	for (const key of Object.keys(detail.labels).sort()) {
		const value = detail.labels[key];
		if (value !== undefined) labels[key] = value;
	}
	return JSON.stringify({
		tag: detail.tag,
		indexDigest: detail.indexDigest,
		platformDigests,
		architecture: detail.architecture,
		os: detail.os,
		labels,
		provenance: detail.provenance,
		sbom: detail.sbom,
	});
}

export function decodeControllerImageDetail(detail: string): ControllerImageDetail | null {
	let value: unknown;
	try {
		value = JSON.parse(detail);
	} catch {
		return null;
	}
	if (!isRecord(value)) return null;
	if (unknownKeys(value, CONTROLLER_IMAGE_DETAIL_KEYS).length > 0) return null;
	if (value.tag !== null && typeof value.tag !== "string") return null;
	if (value.indexDigest !== null && typeof value.indexDigest !== "string") return null;
	if (
		value.os !== EVIDENCE_PLATFORM_OS ||
		typeof value.architecture !== "string" ||
		!(EVIDENCE_PLATFORM_ARCHITECTURES as readonly string[]).includes(value.architecture)
	) {
		return null;
	}
	if (!isRecord(value.platformDigests)) return null;
	const platformDigests: Partial<Record<EvidencePlatformArchitecture, string>> = {};
	for (const architecture of EVIDENCE_PLATFORM_ARCHITECTURES) {
		const digest = value.platformDigests[architecture];
		if (digest === undefined) continue;
		if (typeof digest !== "string") return null;
		platformDigests[architecture] = digest;
	}
	if (!isRecord(value.labels)) return null;
	const labels: Record<string, string> = {};
	for (const [key, label] of Object.entries(value.labels)) {
		if (typeof label !== "string") return null;
		labels[key] = label;
	}
	if (typeof value.provenance !== "boolean" || typeof value.sbom !== "boolean") return null;
	return {
		tag: value.tag,
		indexDigest: value.indexDigest,
		platformDigests,
		architecture: value.architecture as EvidencePlatformArchitecture,
		os: EVIDENCE_PLATFORM_OS,
		labels,
		provenance: value.provenance,
		sbom: value.sbom,
	};
}
