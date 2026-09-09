#!/usr/bin/env bun

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

/** Mandatory retained feature rows. Optional rows stay available for an
 * explicitly supplied profile, but do not silently become cutover gates. */
export const MANDATORY_FEATURE_IDS = [
	"FC01",
	"FC02",
	"FC03",
	"FC04",
	"FC05",
	"FC06",
	"FC07",
	"FC08",
	"FC09",
	"FC10",
	"FC11",
	"FC12",
	"FC13",
	"FC14",
	"FC15",
	"FC16",
	"FC17",
	"FC18",
	"FC19",
	"FC20",
	"FC21",
	"FC22",
	"FC23",
	"FC25",
	"FC26",
	"FC29",
	"FC30",
	"FC31",
] as const;

export const OPTIONAL_FEATURE_IDS = ["FC24", "FC27", "FC28", "FC32", "FC33"] as const;
export const ALL_FEATURE_IDS = [...MANDATORY_FEATURE_IDS, ...OPTIONAL_FEATURE_IDS] as const;
export const ACCEPTANCE_IDS = [
	"X01",
	"X02",
	"X03",
	"X04",
	"X05",
	"X06",
	"X07",
	"X08",
	"X09",
	"X10",
	"X11",
	"X12",
	"X13",
	"X14",
] as const;
export const CORE_GATE_IDS = ["Gate 1", "Gate 2", "Gate 3", "Gate 4", "Gate 5"] as const;

export type FeatureID = (typeof ALL_FEATURE_IDS)[number];
export type AcceptanceID = (typeof ACCEPTANCE_IDS)[number];
export type CoverageID = FeatureID | AcceptanceID;
export type CoverageEvidenceKind = "native" | "bridge" | "ui" | "artifact";

/** One independently produced evidence item. Static source inspection is not
 * live evidence: callers must explicitly set both actual and live. */
export interface CoverageEvidence {
	id: string;
	kind: CoverageEvidenceKind;
	source: string;
	test?: string;
	profile?: string;
	actual?: boolean;
	live?: boolean;
}

export interface CoverageReduction {
	id: string;
	approved: boolean;
	reason?: string;
	approver?: string;
}

export interface CoverageInput {
	evidence?: readonly CoverageEvidence[];
	/** Defaults to every X row. A module/profile may narrow this explicitly. */
	applicableAcceptanceIds?: readonly string[];
	/** Defaults to the mandatory retained rows above. */
	mandatoryFeatureIds?: readonly string[];
	reductions?: readonly CoverageReduction[];
	/** Collector diagnostics that must remain blocking rather than becoming defaults. */
	staticViolations?: readonly string[];
}

export interface CoverageFacts {
	featureIds: readonly string[];
	acceptanceIds: readonly string[];
	evidenceById: Readonly<Record<string, readonly CoverageEvidence[]>>;
	evidenceKindsById: Readonly<Record<string, readonly CoverageEvidenceKind[]>>;
	missingMappings: readonly string[];
	missingLiveEvidence: readonly string[];
	approvedReductions: readonly string[];
}

export interface CoverageReport {
	ok: boolean;
	staticOk: boolean;
	complete: boolean;
	violations: readonly string[];
	missingLiveEvidence: readonly string[];
	facts: CoverageFacts;
}

export interface GateEvidence {
	id: string;
	passed: boolean;
	live?: boolean;
	source?: string;
	profile?: string;
}

export interface CutoverInput {
	coverage: CoverageInput | CoverageReport;
	gates?: readonly GateEvidence[];
	/** A caller asking to remove the legacy runtime/UI must opt into this check. */
	removalRequested?: boolean;
	legacyPaths?: readonly string[];
}

export interface CutoverFacts {
	removalAllowed: boolean;
	gateStatus: Readonly<Record<string, "passed" | "failed" | "missing" | "not-live">>;
	missingLiveEvidence: readonly string[];
}

export interface CutoverReport {
	ok: boolean;
	removalAllowed: boolean;
	violations: readonly string[];
	missingLiveEvidence: readonly string[];
	facts: CutoverFacts;
}

function isFeatureID(id: string): id is FeatureID {
	return (ALL_FEATURE_IDS as readonly string[]).includes(id);
}

function isAcceptanceID(id: string): id is AcceptanceID {
	return (ACCEPTANCE_IDS as readonly string[]).includes(id);
}

function addMissingLive(missing: string[], id: string, detail: string): void {
	missing.push(`${id}: ${detail}`);
}

function normalizeIDs(ids: readonly string[] | undefined, fallback: readonly string[]): string[] {
	return [...new Set((ids ?? fallback).map((id) => id.trim()).filter((id) => id !== ""))];
}

function reportCoverage(input: CoverageInput): CoverageReport {
	const violations: string[] = [];
	const missingLiveEvidence: string[] = [];
	const mandatoryFeatures = normalizeIDs(input.mandatoryFeatureIds, MANDATORY_FEATURE_IDS);
	const acceptanceIds = normalizeIDs(input.applicableAcceptanceIds, ACCEPTANCE_IDS);
	const requiredIDs = [...mandatoryFeatures, ...acceptanceIds];
	const evidenceById = new Map<string, CoverageEvidence[]>();
	const seen = new Set<string>();
	const approvedReductionSet = new Set<string>();
	for (const issue of input.staticViolations ?? []) {
		if (issue.trim()) violations.push(`static coverage input: ${issue.trim()}`);
	}

	for (const id of mandatoryFeatures) {
		if (!isFeatureID(id)) violations.push(`unknown mandatory feature row ${id}`);
	}
	for (const id of acceptanceIds) {
		if (!isAcceptanceID(id)) violations.push(`unknown applicable acceptance row ${id}`);
	}

	for (const evidence of input.evidence ?? []) {
		if (!requiredIDs.includes(evidence.id)) {
			if (!isFeatureID(evidence.id) && !isAcceptanceID(evidence.id)) {
				violations.push(`evidence ${evidence.id}: unknown FC/X row`);
			} else {
				violations.push(
					`evidence ${evidence.id}: row is not mandatory or applicable in this profile`,
				);
			}
			continue;
		}
		if (!evidence.source?.trim()) {
			violations.push(`evidence ${evidence.id}: source is required`);
			continue;
		}
		if (evidence.test !== undefined && !evidence.test.trim()) {
			violations.push(`evidence ${evidence.id}: test name cannot be empty`);
			continue;
		}
		if (
			!(
				"native" === evidence.kind ||
				"bridge" === evidence.kind ||
				"ui" === evidence.kind ||
				"artifact" === evidence.kind
			)
		) {
			violations.push(`evidence ${evidence.id}: unknown evidence kind ${String(evidence.kind)}`);
			continue;
		}
		const key = `${evidence.id}\u0000${evidence.kind}\u0000${evidence.source}\u0000${evidence.test ?? ""}`;
		if (seen.has(key)) {
			violations.push(`evidence ${evidence.id}: duplicate ${evidence.kind} evidence`);
			continue;
		}
		seen.add(key);
		const rows = evidenceById.get(evidence.id) ?? [];
		rows.push(evidence);
		evidenceById.set(evidence.id, rows);
	}

	for (const reduction of input.reductions ?? []) {
		if (!requiredIDs.includes(reduction.id)) {
			violations.push(`reduction ${reduction.id}: row is not mandatory or applicable`);
			continue;
		}
		if (!reduction.approved || !reduction.reason?.trim() || !reduction.approver?.trim()) {
			violations.push(
				`reduction ${reduction.id}: explicit approval, approver and reason are required`,
			);
			continue;
		}
		approvedReductionSet.add(reduction.id);
	}

	const mappedIDs = new Set<string>(approvedReductionSet);
	for (const id of requiredIDs) {
		if (approvedReductionSet.has(id)) continue;
		const rows = evidenceById.get(id) ?? [];
		if (rows.length === 0) {
			violations.push(`${id}: no native/bridge/UI/artifact evidence is mapped`);
			addMissingLive(missingLiveEvidence, id, "live evidence is missing");
			continue;
		}
		mappedIDs.add(id);
		const actualRows = rows.filter((row) => row.actual === true);
		if (actualRows.length === 0) {
			violations.push(`${id}: evidence is static or unverified; actual evidence is required`);
		}
		const liveRows = actualRows.filter((row) => row.live === true);
		if (liveRows.length === 0)
			addMissingLive(missingLiveEvidence, id, "actual live evidence is missing");
	}

	const approvedReductions = [...approvedReductionSet];

	const missingMappings = requiredIDs.filter((id) => !mappedIDs.has(id));
	const staticOk = violations.length === 0;
	const complete = staticOk && missingLiveEvidence.length === 0;
	const evidenceRecord: Record<string, readonly CoverageEvidence[]> = {};
	const evidenceKindsRecord: Record<string, readonly CoverageEvidenceKind[]> = {};
	for (const id of requiredIDs) {
		evidenceRecord[id] = [...(evidenceById.get(id) ?? [])];
		evidenceKindsRecord[id] = [...new Set((evidenceById.get(id) ?? []).map((row) => row.kind))];
	}
	return {
		ok: complete,
		staticOk,
		complete,
		violations,
		missingLiveEvidence,
		facts: {
			featureIds: mandatoryFeatures,
			acceptanceIds,
			evidenceById: evidenceRecord,
			evidenceKindsById: evidenceKindsRecord,
			missingMappings,
			missingLiveEvidence,
			approvedReductions,
		},
	};
}

export function inspectCoverage(input: CoverageInput): CoverageReport {
	return reportCoverage(input);
}

function asCoverageReport(input: CoverageInput | CoverageReport): CoverageReport {
	return "facts" in input && "complete" in input ? input : reportCoverage(input);
}

export function inspectCutover(input: CutoverInput): CutoverReport {
	const coverage = asCoverageReport(input.coverage);
	const violations: string[] = [];
	const missingLiveEvidence: string[] = [...coverage.missingLiveEvidence];
	const removalRequested = input.removalRequested ?? true;
	const gates = new Map<string, GateEvidence>();
	for (const gate of input.gates ?? []) {
		if (!(CORE_GATE_IDS as readonly string[]).includes(gate.id)) {
			violations.push(`${gate.id}: unknown core gate`);
			continue;
		}
		if (gates.has(gate.id)) {
			violations.push(`${gate.id}: duplicate gate evidence`);
			continue;
		}
		gates.set(gate.id, gate);
	}
	const gateStatus: Record<string, "passed" | "failed" | "missing" | "not-live"> = {};
	for (const gateID of CORE_GATE_IDS) {
		const gate = gates.get(gateID);
		if (gate === undefined) {
			gateStatus[gateID] = "missing";
			addMissingLive(missingLiveEvidence, gateID, "live gate result is missing");
			continue;
		}
		if (!gate.passed) {
			gateStatus[gateID] = "failed";
			violations.push(`${gateID}: gate has not passed`);
			continue;
		}
		if (gate.live !== true) {
			gateStatus[gateID] = "not-live";
			addMissingLive(missingLiveEvidence, gateID, "a live gate result is required");
			continue;
		}
		if (!gate.source?.trim()) {
			gateStatus[gateID] = "failed";
			violations.push(`${gateID}: evidence source is required`);
			continue;
		}
		gateStatus[gateID] = "passed";
	}
	if (coverage.violations.length > 0) violations.push(...coverage.violations);
	if (!coverage.staticOk) violations.push("coverage: static FC/X mapping is incomplete");
	if (!coverage.complete)
		violations.push("coverage: live native/bridge/UI/artifact evidence is incomplete");
	for (const path of input.legacyPaths ?? []) {
		if (!path.trim()) violations.push("cutover: legacy path entries cannot be empty");
	}
	const allGatesPassed = CORE_GATE_IDS.every((gateID) => gateStatus[gateID] === "passed");
	const removalAllowed = coverage.complete && violations.length === 0 && allGatesPassed;
	if (removalRequested && !removalAllowed) {
		violations.push(
			"cutover: legacy runtime/UI removal is refused until coverage and core Gates 1-5 pass with live evidence",
		);
	}
	return {
		ok: !removalRequested || removalAllowed,
		removalAllowed,
		violations,
		missingLiveEvidence: [...new Set(missingLiveEvidence)],
		facts: { removalAllowed, gateStatus, missingLiveEvidence: [...new Set(missingLiveEvidence)] },
	};
}

async function readOptional(path: string): Promise<string> {
	return readFile(path, "utf8").catch(() => "");
}

/** Collects the declared row universe without promoting source mentions or
 * review prose to evidence. This intentionally returns no live claims. */
export async function collectCoverageInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<CoverageInput> {
	// Reading the ledgers makes a missing/renamed roadmap visible to this helper,
	// while keeping the authoritative FC/X lists in this script stable.
	const featureLedger = await readOptional(resolve(repositoryRoot, "roadmap/feature-coverage.md"));
	const acceptanceLedger = await readOptional(resolve(repositoryRoot, "roadmap/acceptance.md"));
	const declaredFeatures = [...featureLedger.matchAll(/\bFC\d{2}\b/g)].map((match) => match[0]);
	const declaredAcceptance = [...acceptanceLedger.matchAll(/\bX\d{2}\b/g)].map((match) => match[0]);
	const staticViolations: string[] = [];
	if (featureLedger.trim() === "") staticViolations.push("roadmap/feature-coverage.md is missing");
	if (acceptanceLedger.trim() === "") staticViolations.push("roadmap/acceptance.md is missing");
	for (const id of new Set(declaredFeatures)) {
		if (!(ALL_FEATURE_IDS as readonly string[]).includes(id))
			staticViolations.push(`unknown feature row ${id}`);
	}
	for (const id of new Set(declaredAcceptance)) {
		if (!(ACCEPTANCE_IDS as readonly string[]).includes(id))
			staticViolations.push(`unknown acceptance row ${id}`);
	}
	const mandatoryFeatureIds =
		declaredFeatures.length > 0
			? [...new Set(declaredFeatures)].filter((id) =>
					(MANDATORY_FEATURE_IDS as readonly string[]).includes(id),
				)
			: [...MANDATORY_FEATURE_IDS];
	const applicableAcceptanceIds =
		declaredAcceptance.length > 0
			? [...new Set(declaredAcceptance)].filter((id) =>
					(ACCEPTANCE_IDS as readonly string[]).includes(id),
				)
			: [...ACCEPTANCE_IDS];
	return { mandatoryFeatureIds, applicableAcceptanceIds, evidence: [], staticViolations };
}

export function formatCoverageReport(report: CoverageReport): string {
	const label = report.ok ? "OK" : "FAILED";
	return [
		`check-coverage: ${label} (${report.facts.featureIds.length} mandatory FC rows, ${report.facts.acceptanceIds.length} applicable X rows)`,
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
	].join("\n");
}

export function formatCutoverReport(report: CutoverReport): string {
	const label = report.ok ? "OK" : "FAILED";
	return [
		`check-cutover: ${label} (legacy removal ${report.removalAllowed ? "allowed" : "refused"})`,
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
	].join("\n");
}

export async function runCoverageCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const coverage = inspectCoverage(await collectCoverageInput(repositoryRoot));
	const cutover = inspectCutover({ coverage });
	const output = `${formatCoverageReport(coverage)}\n${formatCutoverReport(cutover)}`;
	if (coverage.ok && cutover.ok) console.log(output);
	else console.error(output);
	return coverage.ok && cutover.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runCoverageCheck());
