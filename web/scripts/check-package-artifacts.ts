#!/usr/bin/env bun

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { basename, relative, resolve } from "node:path";
import {
	decodePackageArchiveDetail,
	decodeProbeDetail,
	type EvidenceBundle,
	PACKAGE_ARCHIVE_ASSERTION_PREFIX,
	PACKAGED_BINARY_ASSERTION_PREFIX,
	parsePackageArchiveAssertionId,
	readEvidenceBundle,
} from "./evidence-bundle.ts";

export type { BinaryProbeDetail } from "./evidence-bundle.ts";
// The probe-detail codec lives with the bundle schema; re-export it here for the
// gate callers that previously owned the decoder.
export { decodeProbeDetail };

export const PACKAGE_VARIANTS = ["assistant", "host"] as const;
export const PACKAGE_ARCHITECTURES = ["amd64", "arm64"] as const;
export type PackageVariant = (typeof PACKAGE_VARIANTS)[number];
export type PackageArchitecture = (typeof PACKAGE_ARCHITECTURES)[number];

export interface PackageArchiveEvidence {
	name: string;
	entries: readonly string[];
}

export interface PackageBinaryEvidence {
	variant: PackageVariant;
	architecture: PackageArchitecture;
	path: string;
	version?: string;
	doctor?: boolean;
	readiness?: boolean;
	lifecycle?: boolean;
	uninstall?: boolean;
	uiEmbedded?: boolean;
}

/**
 * One explicitly approved package-artifact reduction. A reduction removes a
 * live-execution requirement from the staged-artifact gate; it is never a pass.
 * The row id is `base` or `base/target`; a trailing `/*` wildcard matches every
 * target under that base.
 */
export interface PackageArtifactReduction {
	id: string;
	approved: boolean;
	approver?: string;
	reason?: string;
}

export interface PackageArtifactInput {
	releaseId?: string;
	archives?: readonly PackageArchiveEvidence[];
	units?: Readonly<Record<string, string>>;
	configs?: Readonly<Record<string, string>>;
	commandSources?: Readonly<Record<string, string>>;
	webuiSources?: Readonly<Record<string, string>>;
	embeddedUiFiles?: readonly string[];
	facadeSources?: Readonly<Record<string, string>>;
	binaries?: readonly PackageBinaryEvidence[];
	/** Explicitly approved reductions for rows that cannot be produced locally. */
	reductions?: readonly PackageArtifactReduction[];
	/**
	 * Fail-closed findings produced while translating an evidence bundle. They
	 * are always reported as violations and never weaken an archive/binary check.
	 */
	evidenceViolations?: readonly string[];
}

export interface PackageArtifactFacts {
	expectedArchives: readonly string[];
	observedArchives: readonly string[];
	unitFiles: readonly string[];
	configFiles: readonly string[];
	staticChecks: readonly string[];
	missingLiveEvidence: readonly string[];
	/** Missing rows that an approved reduction removed from the blocking set. */
	reducedLiveEvidence: readonly string[];
}

export interface PackageArtifactReport {
	ok: boolean;
	staticOk: boolean;
	complete: boolean;
	violations: readonly string[];
	missingLiveEvidence: readonly string[];
	facts: PackageArtifactFacts;
}

/** GATE assertion carrying the staged local release manifest, when one exists. */
export const RELEASE_MANIFEST_ASSERTION_ID = "RELEASE-MANIFEST";

const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
const ARCHIVE_PATTERN = /^pixie(?:-assistant)?-sha-[0-9a-f]{12}-linux-(?:amd64|arm64)\.tar\.gz$/;
const SECRET_PATTERN = /(?:secret|token|password|passwd|credential|api[-_]?key)/i;
const INSTALL_ENTRY_PATTERN = /^(?:INSTALL|INSTALL\.md|README(?:\.install)?(?:\.md)?)$/i;

/**
 * Shared shape of the `packageArtifacts` and `releaseGate` sections in
 * `web/reductions.json`. Each section carries its own approval
 * block so the two reductions can be reviewed independently, and each reduction
 * group states that a reduction removes a requirement rather than proving it.
 */
export interface ReductionApproval {
	approved?: boolean;
	approver?: string;
	reference?: string;
	statement?: string;
}

export interface GateReductionGroup {
	ids: readonly string[];
	reason: string;
}

export interface GateReductionSection {
	approval?: ReductionApproval;
	reductionGroups?: readonly GateReductionGroup[];
}

export interface ReductionsManifest {
	schemaVersion?: number;
	packageArtifacts?: GateReductionSection;
	releaseGate?: GateReductionSection;
}

/** Explicitly approved reduction for a release-gate publication row. */
export interface ReleaseGateReduction {
	id: string;
	approved: boolean;
	approver?: string;
	reason?: string;
}

/** Rows the staged-artifact package gate may reduce; every other row is structural. */
const PACKAGE_REDUCIBLE_BASES = [
	"binary.version",
	"binary.doctor",
	"binary.readiness",
	"binary.lifecycle",
	"binary.uninstall",
	"binary.embedded-ui",
	"facade.native-engine",
] as const;

/** Publication-only release-gate rows. Static policy/identity checks stay mandatory. */
const RELEASE_REDUCIBLE_BASES = [
	"tag.identity",
	"release.identity",
	"registry.tag",
	"registry.provenance",
	"registry.sbom",
	"registry.image-index",
	"registry.platform-digest",
	"manifest.complete-set",
	"latest.promotion",
] as const;

export function defaultReductionsManifestPath(): string {
	return resolve(import.meta.dir, "../reductions.json");
}

export async function loadReductionsManifest(path: string): Promise<ReductionsManifest> {
	let text: string;
	try {
		text = await readFile(path, "utf8");
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`reductions manifest is unreadable (${path}): ${message}`);
	}
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`reductions manifest is not valid JSON (${path}): ${message}`);
	}
	if (value === null || typeof value !== "object" || Array.isArray(value)) {
		throw new Error("reductions manifest must be a JSON object");
	}
	return value as ReductionsManifest;
}

function expandSectionApproval(
	section: GateReductionSection | undefined,
	label: string,
): { approver: string; reason: (group: GateReductionGroup) => string } {
	const approval = section?.approval;
	if (
		approval?.approved !== true ||
		approval.approver?.trim() === undefined ||
		approval.approver.trim() === "" ||
		approval.reference?.trim() === undefined ||
		approval.reference.trim() === "" ||
		approval.statement?.trim() === undefined ||
		approval.statement.trim() === ""
	) {
		throw new Error(
			`reductions manifest ${label}: approved approver, reference and statement are required`,
		);
	}
	const approver = approval.approver.trim();
	return {
		approver,
		reason: (group) => (group.reason?.trim() === "" ? "" : (group.reason ?? "").trim()),
	};
}

function findReducibleBase(id: string, bases: readonly string[]): string | null {
	for (const base of bases) {
		if (id === base || id.startsWith(`${base}/`)) return base;
	}
	return null;
}

function validBinaryTarget(target: string): boolean {
	if (target === "*") return true;
	const [variant, architecture, ...rest] = target.split("/");
	return (
		rest.length === 0 &&
		(variant === "assistant" || variant === "host") &&
		(architecture === "*" || architecture === "amd64" || architecture === "arm64")
	);
}

function validateReductionRow(id: string, base: string, label: string): string | null {
	const target = id === base ? "" : id.slice(base.length + 1);
	if (base === "facade.native-engine") {
		return target === "" ? null : `${label}: ${id} does not accept a target`;
	}
	if (base.startsWith("binary.")) {
		return validBinaryTarget(target) ? null : `${label}: invalid binary target in ${id}`;
	}
	if (base === "registry.platform-digest") {
		return target === "*" || target === "amd64" || target === "arm64"
			? null
			: `${label}: invalid platform target in ${id}`;
	}
	return null;
}

function expandReductionSection(
	section: GateReductionSection | undefined,
	bases: readonly string[],
	label: string,
): { id: string; approved: true; approver: string; reason: string }[] {
	const expanded = expandSectionApproval(section, label);
	const rows: { id: string; approved: true; approver: string; reason: string }[] = [];
	const seen = new Set<string>();
	for (const group of section?.reductionGroups ?? []) {
		const reason = expanded.reason(group);
		if (reason === "") {
			throw new Error(`reductions manifest ${label}: every reduction group needs a reason`);
		}
		for (const id of group.ids ?? []) {
			const base = findReducibleBase(id, bases);
			if (base === null) {
				throw new Error(`reductions manifest ${label}: ${id} is not a reducible row`);
			}
			const issue = validateReductionRow(id, base, label);
			if (issue !== null) throw new Error(`reductions manifest ${issue}`);
			if (seen.has(id)) continue;
			seen.add(id);
			rows.push({ id, approved: true, approver: expanded.approver, reason });
		}
	}
	return rows;
}

/** Expand and validate the `packageArtifacts` reduction group against its full row space. */
export function expandPackageArtifactReductions(
	manifest: ReductionsManifest,
): PackageArtifactReduction[] {
	return expandReductionSection(
		manifest.packageArtifacts,
		PACKAGE_REDUCIBLE_BASES,
		"packageArtifacts",
	);
}

/** Expand and validate the `releaseGate` reduction group against its full row space. */
export function expandReleaseGateReductions(manifest: ReductionsManifest): ReleaseGateReduction[] {
	return expandReductionSection(manifest.releaseGate, RELEASE_REDUCIBLE_BASES, "releaseGate");
}

/**
 * Apply an approved reduction set to a list of candidate live rows. Approved
 * rows are returned as reduced; every other row stays blocking. An unknown or
 * unapproved reduction is reported as a violation instead of silently applying.
 */
export function splitReducibleRows<T extends { id: string; message: string }>(
	rows: readonly T[],
	reductions: readonly { id: string; approved: boolean; approver?: string; reason?: string }[],
	bases: readonly string[],
	label: string,
	validate: (id: string, base: string) => string | null,
): { missing: string[]; reduced: string[]; violations: string[] } {
	const violations: string[] = [];
	const approved: string[] = [];
	for (const reduction of reductions) {
		const base = findReducibleBase(reduction.id, bases);
		if (base === null) {
			violations.push(`${label} reduction ${reduction.id}: row is not reducible`);
			continue;
		}
		if (!reduction.approved || !reduction.reason?.trim() || !reduction.approver?.trim()) {
			violations.push(
				`${label} reduction ${reduction.id}: explicit approval, approver and reason are required`,
			);
			continue;
		}
		const issue = validate(reduction.id, base);
		if (issue !== null) {
			violations.push(`${label} reduction ${issue}`);
			continue;
		}
		approved.push(reduction.id);
	}
	const missing: string[] = [];
	const reduced: string[] = [];
	for (const row of rows) {
		const matches = approved.some((id) => reductionMatches(row.id, id));
		if (matches) reduced.push(row.message);
		else missing.push(row.message);
	}
	return { missing, reduced, violations };
}

/** A trailing `/*` in a reduction id matches every remaining target segment. */
export function reductionMatches(rowId: string, reductionId: string): boolean {
	const row = rowId.split("/");
	const reduction = reductionId.split("/");
	for (const [index, segment] of reduction.entries()) {
		if (segment === "*") return index === reduction.length - 1;
		if (segment !== row[index]) return false;
	}
	return reduction.length === row.length;
}

function stripComments(source: string): string {
	return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|\s)\/\/.*$/gm, "$1");
}

export function expectedArchiveName(
	variant: PackageVariant,
	architecture: PackageArchitecture,
	releaseId: string,
): string {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	return `${binary}-${releaseId}-linux-${architecture}.tar.gz`;
}

function expectedBinaryName(variant: PackageVariant): string {
	return variant === "assistant" ? "pixie-assistant" : "pixie";
}

function archiveEntryNames(entries: readonly string[]): Set<string> {
	const names = new Set<string>();
	for (const entry of entries) {
		const normalized = entry.replaceAll("\\", "/").replace(/^\.\//, "").toLowerCase();
		names.add(normalized);
		names.add(basename(normalized));
	}
	return names;
}

function checkUnit(
	name: string,
	source: string,
	violations: string[],
	staticChecks: string[],
): void {
	const required: readonly [RegExp, string][] = [
		[/^\s*\[Unit\]/m, "[Unit] section"],
		[/^\s*\[Service\]/m, "[Service] section"],
		[/^\s*StartLimitIntervalSec=60\s*$/m, "60-second start limit window"],
		[/^\s*StartLimitBurst=5\s*$/m, "start limit burst"],
		[/^\s*Type=exec\s*$/m, "Type=exec"],
		[/^\s*Restart=on-failure\s*$/m, "Restart=on-failure"],
		[/^\s*RestartSec=\d+/m, "RestartSec"],
		[/^\s*TimeoutStopSec=30\s*$/m, "30-second stop ceiling"],
		[/^\s*KillMode=mixed\s*$/m, "KillMode=mixed"],
		[/^\s*UMask=0077\s*$/m, "private UMask"],
		[/^\s*StandardOutput=journal\s*$/m, "journal stdout"],
		[/^\s*StandardError=journal\s*$/m, "journal stderr"],
		[/^\s*\[Install\]/m, "[Install] section"],
		[/^\s*WantedBy=default\.target\s*$/m, "default.target installation"],
	];
	for (const [pattern, label] of required) {
		if (!pattern.test(source)) violations.push(`${name}: missing ${label}`);
	}

	if (name === "pixie-assistant.service") {
		if (!/^\s*ExecStart=.*\bpixie-assistant\b.*\bserve\b.*--config\s+\S+/m.test(source)) {
			violations.push(`${name}: ExecStart must run pixie-assistant with an explicit config`);
		}
	} else if (name === "pixie.service") {
		if (!/^\s*ExecStart=.*\bpixie\b.*\bserve\b.*--config\s+\S+/m.test(source)) {
			violations.push(
				`${name}: ExecStart must run the full-host pixie binary with an explicit config`,
			);
		}
		if (/^\s*(?:Requires|BindsTo)=.*pixie-assistant\.service/m.test(source)) {
			violations.push(`${name}: full-host unit must not depend on a separate assistant service`);
		}
	}
	if (!/^\s*RestartForceExitStatus=75\s*$/m.test(source)) {
		violations.push(`${name}: requested restart must use exit status 75`);
	}
	staticChecks.push(`${name}: service lifetime directives`);
}

function checkConfig(
	name: string,
	source: string | undefined,
	violations: string[],
	staticChecks: string[],
): void {
	if (source === undefined) {
		violations.push(`web/systemd: missing ${name} configuration example`);
		return;
	}
	if (SECRET_PATTERN.test(source)) {
		violations.push(`${name}: configuration example contains a secret-shaped key`);
	}
	try {
		const config = JSON.parse(source) as Record<string, unknown>;
		if (config === null || typeof config !== "object" || Array.isArray(config)) {
			violations.push(`${name}: configuration example must be a JSON object`);
		}
	} catch {
		violations.push(`${name}: configuration example is not valid JSON`);
	}
	staticChecks.push(`${name}: non-secret JSON configuration`);
}

interface LiveEvidenceRow {
	id: string;
	message: string;
}

function checkArchives(
	input: PackageArtifactInput,
	violations: string[],
	liveRows: LiveEvidenceRow[],
): { expected: string[]; observed: string[] } {
	const expected: string[] = [];
	const archives = input.archives ?? [];
	const observedReleaseIds = [
		...new Set(
			archives
				.map((archive) => archive.name.match(/-sha-([0-9a-f]{12})-linux-/)?.[1])
				.filter((release): release is string => release !== undefined),
		),
	];
	let releaseId = input.releaseId;
	if (releaseId === undefined && observedReleaseIds.length === 1)
		releaseId = `sha-${observedReleaseIds[0]}`;
	if (observedReleaseIds.length > 1)
		violations.push("archives must use one commit-based release ID");
	if (releaseId !== undefined) {
		if (!RELEASE_ID_PATTERN.test(releaseId)) {
			violations.push(
				`release ID must be sha- plus 12 lowercase hexadecimal characters (${releaseId})`,
			);
		} else {
			for (const variant of PACKAGE_VARIANTS) {
				for (const architecture of PACKAGE_ARCHITECTURES) {
					expected.push(expectedArchiveName(variant, architecture, releaseId));
				}
			}
		}
	}

	const byName = new Map<string, PackageArchiveEvidence>();
	for (const archive of archives) {
		if (byName.has(archive.name))
			violations.push(`archive ${archive.name}: duplicate archive evidence`);
		byName.set(archive.name, archive);
		if (!ARCHIVE_PATTERN.test(archive.name)) {
			violations.push(`archive ${archive.name}: name must be commit-based for linux amd64/arm64`);
			continue;
		}
		const entries = archiveEntryNames(archive.entries);
		const isAssistant = archive.name.startsWith("pixie-assistant-");
		const variant: PackageVariant = isAssistant ? "assistant" : "host";
		const binary = expectedBinaryName(variant);
		const unit = `${binary}.service`;
		const config = variant === "assistant" ? "assistant.json" : "pixie.json";
		if (!entries.has(binary))
			violations.push(`archive ${archive.name}: missing ${binary} executable`);
		if (!entries.has(unit)) violations.push(`archive ${archive.name}: missing ${unit}`);
		if (!entries.has(config)) violations.push(`archive ${archive.name}: missing ${config}`);
		if (![...entries].some((entry) => INSTALL_ENTRY_PATTERN.test(entry))) {
			violations.push(`archive ${archive.name}: missing concise install/uninstall instructions`);
		}
		for (const legal of ["license", "notice.md"]) {
			if (!entries.has(legal))
				violations.push(`archive ${archive.name}: missing ${legal.toUpperCase()} notice`);
		}
		if (
			variant === "host" &&
			[...entries].some(
				(entry) => entry === "pixie-assistant" || entry === "pixie-assistant.service",
			)
		) {
			violations.push(
				`archive ${archive.name}: full-host archive must not ship a separate assistant runtime`,
			);
		}
		if ([...entries].some((entry) => entry === "web" || entry.startsWith("web/"))) {
			violations.push(
				`archive ${archive.name}: full-host UI must be embedded, not a required web asset directory`,
			);
		}
	}
	if (expected.length === 0) {
		liveRows.push({
			id: "archive.complete-set",
			message: "four commit-named host archives (assistant and full-host, linux amd64 and arm64)",
		});
	} else {
		for (const name of expected) {
			if (!byName.has(name))
				liveRows.push({ id: "archive.present", message: `release archive ${name}` });
		}
	}
	return { expected, observed: archives.map(({ name }) => name).sort() };
}

function checkStaticCommands(
	input: PackageArtifactInput,
	violations: string[],
	staticChecks: string[],
): void {
	const source = stripComments(Object.values(input.commandSources ?? {}).join("\n"));
	const checks: readonly [RegExp, string][] = [
		[/--version\b|\bversion\b/, "version"],
		[/\/readyz\b|\breadyz\b/, "readiness"],
		[/signal\.NotifyContext|Shutdown\s*\(/, "stop"],
		[/serveController|func\s+\(r \*Runtime\) Start/, "start"],
		[/doctor\b/, "doctor"],
		[/uninstall\b/, "uninstall"],
	];
	for (const [pattern, label] of checks) {
		if (!pattern.test(source))
			violations.push(
				`package binaries: ${label} command/check is not present in the current source`,
			);
		else staticChecks.push(`binary ${label} command/check`);
	}

	const units = Object.values(input.units ?? {}).join("\n");
	if (
		!/^\s*Restart=on-failure\s*$/m.test(units) ||
		!/^\s*RestartForceExitStatus=75\s*$/m.test(units)
	) {
		violations.push("package units: restart policy evidence is missing");
	} else {
		staticChecks.push("restart policy and requested-restart status");
	}
}

function checkBinaries(
	binaries: readonly PackageBinaryEvidence[] | undefined,
	input: PackageArtifactInput,
	liveRows: LiveEvidenceRow[],
): void {
	const inferredReleaseId =
		input.releaseId ?? input.archives?.[0]?.name.match(/-sha-([0-9a-f]{12})-linux-/)?.[1];
	const expectedReleaseId =
		inferredReleaseId === undefined
			? undefined
			: inferredReleaseId.startsWith("sha-")
				? inferredReleaseId
				: `sha-${inferredReleaseId}`;
	const observed = new Map<string, PackageBinaryEvidence>();
	for (const binary of binaries ?? []) {
		const key = `${binary.variant}/${binary.architecture}`;
		if (observed.has(key))
			liveRows.push({
				id: `binary.present/${key}`,
				message: `duplicate live binary evidence ${key}`,
			});
		observed.set(key, binary);
		if (!new RegExp(`(?:^|/)${expectedBinaryName(binary.variant)}$`).test(binary.path ?? "")) {
			liveRows.push({
				id: `binary.path/${key}`,
				message: `${key} binary path is not the expected executable`,
			});
		}
		if (!binary.version)
			liveRows.push({ id: `binary.version/${key}`, message: `${key} --version output` });
		if (expectedReleaseId !== undefined && binary.version !== expectedReleaseId) {
			liveRows.push({
				id: `binary.version/${key}`,
				message: `${key} --version output matches ${expectedReleaseId}`,
			});
		}
		if (!binary.doctor)
			liveRows.push({ id: `binary.doctor/${key}`, message: `${key} doctor check` });
		if (!binary.readiness)
			liveRows.push({ id: `binary.readiness/${key}`, message: `${key} readiness check` });
		if (!binary.lifecycle)
			liveRows.push({ id: `binary.lifecycle/${key}`, message: `${key} start/stop/restart check` });
		if (!binary.uninstall)
			liveRows.push({ id: `binary.uninstall/${key}`, message: `${key} uninstall check` });
		if (binary.variant === "host" && !binary.uiEmbedded)
			liveRows.push({
				id: `binary.embedded-ui/${key}`,
				message: `${key} real embedded UI check`,
			});
	}
	for (const variant of PACKAGE_VARIANTS) {
		for (const architecture of PACKAGE_ARCHITECTURES) {
			const key = `${variant}/${architecture}`;
			if (!observed.has(key))
				liveRows.push({ id: `binary.present/${key}`, message: `built executable ${key}` });
		}
	}
	if (binaries === undefined || binaries.length === 0) {
		liveRows.push({
			id: "binary.live-matrix",
			message:
				"live version/doctor/readiness/lifecycle checks for both binaries on both architectures",
		});
	}
	if (
		input.facadeSources !== undefined &&
		Object.values(input.facadeSources).some((source) =>
			/ErrUnavailable|native engine is unavailable|capabilities["']?\s*:\s*map\[string\]int\{\}/.test(
				source,
			),
		)
	) {
		liveRows.push({
			id: "facade.native-engine",
			message: "full-host binary starts a working native assistant engine through the facade",
		});
	}
	if (
		input.webuiSources !== undefined &&
		!Object.values(input.webuiSources).some((source) => /go:embed\s+all:dist/.test(source))
	) {
		liveRows.push({
			id: "webui.embed",
			message: "full-host binary embeds the UI through web/webui",
		});
	}
	if (
		input.embeddedUiFiles !== undefined &&
		!input.embeddedUiFiles.some((path) => /(?:^|\/)dist\/index\.html$/.test(path))
	) {
		liveRows.push({
			id: "webui.dist",
			message: "full-host binary contains a real dist/index.html bundle",
		});
	}
}

export function inspectPackageArtifacts(input: PackageArtifactInput): PackageArtifactReport {
	const violations: string[] = [...(input.evidenceViolations ?? [])];
	const staticChecks: string[] = [];
	const liveRows: LiveEvidenceRow[] = [];
	for (const [name, source] of Object.entries(input.units ?? {}))
		checkUnit(basename(name), source, violations, staticChecks);
	const units = Object.keys(input.units ?? {}).map((name) => basename(name).toLowerCase());
	for (const required of ["pixie-assistant.service", "pixie.service"]) {
		if (!units.includes(required)) violations.push(`web/systemd: missing ${required}`);
	}
	checkConfig(
		"assistant.json",
		Object.entries(input.configs ?? {}).find(([name]) => basename(name) === "assistant.json")?.[1],
		violations,
		staticChecks,
	);
	checkConfig(
		"pixie.json",
		Object.entries(input.configs ?? {}).find(([name]) => basename(name) === "pixie.json")?.[1],
		violations,
		staticChecks,
	);
	checkStaticCommands(input, violations, staticChecks);
	const archives = checkArchives(input, violations, liveRows);
	checkBinaries(input.binaries, input, liveRows);

	const split = splitReducibleRows(
		liveRows,
		input.reductions ?? [],
		PACKAGE_REDUCIBLE_BASES,
		"packageArtifacts",
		(id, base) => validateReductionRow(id, base, "packageArtifacts"),
	);
	violations.push(...split.violations);
	const missingLiveEvidence = split.missing;
	const reducedLiveEvidence = split.reduced;

	const staticOk = violations.length === 0;
	const complete = missingLiveEvidence.length === 0;
	return {
		ok: staticOk && complete,
		staticOk,
		complete,
		violations,
		missingLiveEvidence,
		facts: {
			expectedArchives: archives.expected,
			observedArchives: archives.observed,
			unitFiles: units.sort(),
			configFiles: Object.keys(input.configs ?? {})
				.map((name) => basename(name))
				.sort(),
			staticChecks,
			missingLiveEvidence,
			reducedLiveEvidence,
		},
	};
}

async function collectTextTree(
	repositoryRoot: string,
	directory: string,
	extensions: ReadonlySet<string>,
): Promise<Record<string, string>> {
	const files: Record<string, string> = {};
	const root = resolve(repositoryRoot, directory);
	async function walk(current: string): Promise<void> {
		let entries: Dirent[];
		try {
			entries = await readdir(current, { withFileTypes: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
			throw error;
		}
		for (const entry of entries) {
			const path = resolve(current, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
				continue;
			}
			const extension = path.slice(path.lastIndexOf("."));
			if (!entry.isFile() || !extensions.has(extension)) continue;
			files[relative(repositoryRoot, path).replaceAll("\\", "/")] = await readFile(path, "utf8");
		}
	}
	await walk(root);
	return files;
}

async function collectFilePaths(repositoryRoot: string, directory: string): Promise<string[]> {
	const files: string[] = [];
	const root = resolve(repositoryRoot, directory);
	async function walk(current: string): Promise<void> {
		let entries: Dirent[];
		try {
			entries = await readdir(current, { withFileTypes: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
			throw error;
		}
		for (const entry of entries) {
			const path = resolve(current, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
				continue;
			}
			if (entry.isFile()) files.push(relative(repositoryRoot, path).replaceAll("\\", "/"));
		}
	}
	await walk(root);
	return files.sort();
}

export async function collectPackageArtifactInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<PackageArtifactInput> {
	const commandSources = {
		...(await collectTextTree(repositoryRoot, "web/cmd", new Set([".go"]))),
		...(await collectTextTree(repositoryRoot, "web/internal", new Set([".go"]))),
	};
	const webuiSources = await collectTextTree(repositoryRoot, "web/webui", new Set([".go"]));
	const embeddedUiFiles = await collectFilePaths(repositoryRoot, "web/webui/dist");
	const facadeSources = await collectTextTree(repositoryRoot, "assistant/host", new Set([".go"]));
	const units = await collectTextTree(repositoryRoot, "web/systemd", new Set([".service"]));
	const configs = await collectTextTree(repositoryRoot, "web/systemd", new Set([".json"]));
	return { commandSources, webuiSources, embeddedUiFiles, facadeSources, units, configs };
}

export interface PackageEvidenceMapping {
	input: PackageArtifactInput;
	violations: readonly string[];
}

function variantFromBinaryPath(path: string): PackageVariant | null {
	const name = basename(path);
	if (name === "pixie-assistant") return "assistant";
	if (name === "pixie") return "host";
	return null;
}

interface BinaryProbeFacts {
	version?: string;
	doctor?: boolean;
	readiness?: boolean;
}

/**
 * Translate a validated evidence bundle into the archive side of the package
 * input. Only `pass` archive assertions carry a machine-readable detail; any
 * blocked/failed/malformed assertion becomes a precise violation. Binary
 * presence is taken from the inspected archive members, and the version,
 * doctor and readiness rows are filled only from `pass` probes the collector
 * actually executed on the matching architecture. Lifecycle, uninstall and
 * embedded-UI facts are never synthesized: those rows stay absent and fail
 * closed unless reduced.
 */
export function packageArtifactInputFromEvidence(
	evidence: EvidenceBundle,
	base: PackageArtifactInput,
): PackageEvidenceMapping {
	const violations: string[] = [];
	const archives: PackageArchiveEvidence[] = [];
	const binaries: PackageBinaryEvidence[] = [];
	// Probe facts are keyed by `variant/architecture` because one bundle now
	// carries the native probes executed on each architecture. A detail without
	// an architecture predates the field and belongs to the bundle's platform.
	const probes = new Map<string, BinaryProbeFacts>();
	for (const assertion of evidence.assertions) {
		if (assertion.kind !== "GATE") continue;
		if (!assertion.id.startsWith(PACKAGED_BINARY_ASSERTION_PREFIX)) continue;
		if (assertion.status !== "pass") continue;
		const probe = assertion.id.endsWith("-version")
			? "version"
			: assertion.id.endsWith("-doctor")
				? "doctor"
				: assertion.id.endsWith("-readiness")
					? "readiness"
					: null;
		if (probe === null) continue;
		const detail = decodeProbeDetail(assertion.detail);
		if (detail === null) continue;
		const variant = variantFromBinaryPath(detail.path);
		if (variant === null) continue;
		const architecture = detail.architecture ?? evidence.platform.arch;
		const key = `${variant}/${architecture}`;
		const facts = probes.get(key) ?? {};
		if (probe === "version") {
			const observed = detail.stdout.match(/sha-[0-9a-f]{12}/)?.[0];
			if (observed !== undefined) facts.version = observed;
		} else if (probe === "doctor") {
			facts.doctor = true;
		} else {
			facts.readiness = true;
		}
		probes.set(key, facts);
	}
	for (const assertion of evidence.assertions) {
		if (assertion.kind !== "GATE") continue;
		if (!assertion.id.startsWith(PACKAGE_ARCHIVE_ASSERTION_PREFIX)) continue;
		if (assertion.status !== "pass") {
			violations.push(
				`evidence ${assertion.id}: archive assertion is ${assertion.status} (${assertion.detail})`,
			);
			continue;
		}
		const key = parsePackageArchiveAssertionId(assertion.id);
		const artifact = assertion.artifact;
		if (key === null || artifact === undefined) {
			violations.push(
				`evidence ${assertion.id}: archive artifact with a SHA-256 digest is required`,
			);
			continue;
		}
		const detail = decodePackageArchiveDetail(assertion.detail);
		if (detail === null) {
			violations.push(`evidence ${assertion.id}: malformed archive inspection detail`);
			continue;
		}
		if (key.variant !== detail.variant || key.architecture !== detail.architecture) {
			violations.push(
				`evidence ${assertion.id}: assertion id does not match inspected ${detail.variant}/${detail.architecture}`,
			);
			continue;
		}
		if (
			artifact.name !== expectedArchiveName(detail.variant, detail.architecture, evidence.releaseId)
		) {
			violations.push(
				`evidence ${assertion.id}: artifact ${artifact.name} is not the expected release archive`,
			);
			continue;
		}
		archives.push({ name: artifact.name, entries: detail.entries });
		binaries.push({
			variant: detail.variant,
			architecture: detail.architecture,
			path: detail.binary,
		});
	}
	// Each row is filled only from probes whose recorded architecture matches the
	// binary's own; a missing arm64 fact stays missing and fails closed.
	for (const binary of binaries) {
		const facts = probes.get(`${binary.variant}/${binary.architecture}`);
		if (facts === undefined) continue;
		if (facts.version !== undefined) binary.version = facts.version;
		if (facts.doctor === true) binary.doctor = true;
		if (facts.readiness === true) binary.readiness = true;
	}
	return {
		input: {
			...base,
			releaseId: evidence.releaseId,
			archives,
			evidenceViolations: violations,
			...(binaries.length === 0 ? {} : { binaries }),
		},
		violations,
	};
}

export const PACKAGE_ARTIFACTS_USAGE = [
	"usage: bun scripts/check-package-artifacts.ts [--evidence <bundle.json>]",
	"         [--reductions <reductions.json>]",
	"",
	"Without --evidence this command keeps its fail-closed behavior: it inspects",
	"the checked-in sources and reports every absent live archive and binary",
	"input.",
	"",
	"With --evidence it consumes a schema-versioned bundle produced by",
	"collect-evidence.ts for archive contents, digests and executed native",
	"version/doctor/readiness probes. A missing, malformed or non-passing archive",
	"assertion fails closed.",
	"",
	"--reductions (or PIXIE_REDUCTIONS_MANIFEST, defaulting to the committed",
	"web/reductions.json) exempts exactly the operator-approved",
	"rows. A reduced row is reported as reduced, never as passing; every row not",
	"listed still fails closed.",
].join("\n");

interface PackageArtifactCliOptions {
	repositoryRoot: string;
	evidencePath?: string;
	reductionsPath: string;
}

function parsePackageArtifactArgs(args: readonly string[]): PackageArtifactCliOptions {
	const repositoryRoot = resolve(import.meta.dir, "../..");
	let evidencePath: string | undefined;
	let reductionsPath =
		process.env.PIXIE_REDUCTIONS_MANIFEST?.trim() || defaultReductionsManifestPath();
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index];
		if (argument === "--help" || argument === "-h") {
			console.log(PACKAGE_ARTIFACTS_USAGE);
			process.exit(0);
		}
		if (argument === "--evidence") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "")
				throw new Error("--evidence requires a bundle path");
			evidencePath = value;
			index += 1;
			continue;
		}
		if (argument?.startsWith("--evidence=")) {
			const value = argument.slice("--evidence=".length).trim();
			if (value === "") throw new Error("--evidence requires a bundle path");
			evidencePath = value;
			continue;
		}
		if (argument === "--reductions") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "")
				throw new Error("--reductions requires a manifest path");
			reductionsPath = value;
			index += 1;
			continue;
		}
		if (argument?.startsWith("--reductions=")) {
			const value = argument.slice("--reductions=".length).trim();
			if (value === "") throw new Error("--reductions requires a manifest path");
			reductionsPath = value;
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	return {
		repositoryRoot,
		reductionsPath,
		...(evidencePath === undefined ? {} : { evidencePath }),
	};
}

export function formatPackageArtifactReport(report: PackageArtifactReport): string {
	if (report.ok) {
		return (
			`check-package-artifacts: OK (${report.facts.observedArchives.length} archives, ` +
			`${report.facts.unitFiles.length} units, ${report.facts.staticChecks.length} static checks, ` +
			`${report.facts.reducedLiveEvidence.length} reduced live rows)`
		);
	}
	return [
		"check-package-artifacts: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live artifact evidence: ${item}`),
		...report.facts.reducedLiveEvidence.map(
			(item) => `  - reduced live artifact evidence: ${item}`,
		),
	].join("\n");
}

export async function loadPackageArtifactReductions(
	reductionsPath: string,
): Promise<PackageArtifactReduction[]> {
	return expandPackageArtifactReductions(await loadReductionsManifest(reductionsPath));
}

export async function runPackageArtifactCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
	evidencePath?: string,
	reductionsPath = defaultReductionsManifestPath(),
): Promise<number> {
	let reductions: PackageArtifactReduction[];
	try {
		reductions = await loadPackageArtifactReductions(reductionsPath);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`check-package-artifacts: FAILED\n  - reductions: ${message}`);
		return 1;
	}
	const base = await collectPackageArtifactInput(repositoryRoot);
	let input = base;
	if (evidencePath !== undefined) {
		let evidence: EvidenceBundle;
		try {
			evidence = await readEvidenceBundle(evidencePath);
		} catch (error) {
			const message = error instanceof Error ? error.message : String(error);
			console.error(`check-package-artifacts: FAILED\n  - evidence: ${message}`);
			return 1;
		}
		input = packageArtifactInputFromEvidence(evidence, base).input;
	}
	const report = inspectPackageArtifacts({ ...input, reductions });
	const output = formatPackageArtifactReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) {
	try {
		const options = parsePackageArtifactArgs(Bun.argv.slice(2));
		process.exit(
			await runPackageArtifactCheck(
				options.repositoryRoot,
				options.evidencePath,
				options.reductionsPath,
			),
		);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`check-package-artifacts: ${message}`);
		console.error(PACKAGE_ARTIFACTS_USAGE);
		process.exit(2);
	}
}
