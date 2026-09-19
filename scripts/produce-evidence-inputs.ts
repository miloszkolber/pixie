#!/usr/bin/env bun

import { createHash } from "node:crypto";
import { chmod, mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { assertProductArchiveLayout, RELEASE_PRODUCTS } from "./build-release.ts";
import {
	ACCEPTANCE_IDS,
	CORE_GATE_IDS,
	type CoverageEvidence,
	type CoverageInput,
	type CoverageReduction,
	MANDATORY_FEATURE_IDS,
} from "./check-coverage.ts";
import {
	expectedArchiveName,
	type PackageArchitecture,
	type PackageProduct,
} from "./check-package-artifacts.ts";
import {
	PERFORMANCE_ARCHITECTURES,
	PERFORMANCE_VARIANTS,
	type PerformanceFieldReduction,
	type PerformanceInput,
	type PerformanceMeasurement,
	type PerformanceTargetReduction,
	REDUCIBLE_PERFORMANCE_FIELDS,
} from "./check-performance.ts";
import { parseTarGz, type TarEntry } from "./collect-evidence.ts";

/**
 * Lean live-evidence producer.
 *
 * It emits `coverage.json` and `performance.json` from the staged commit
 * archives and, when a base URL is supplied, the running host. Only checks that
 * actually ran against the staged binaries are recorded as `actual`/`live`;
 * everything else stays absent and the gate fails closed. Legacy rows that the
 * current architecture cannot honestly produce are skipped only through the
 * committed, operator-approved manifest in `web/reductions.json`.
 */

const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
const READINESS_TIMEOUT_MS = 5000;
const PROBE_OUTPUT_LIMIT = 4096;
const MIN_SAMPLES = 5;

/** The frozen performance matrix still calls its two rows assistant/full-host. */
type PerformanceVariant = "assistant" | "full-host";

export interface ProducerProbe {
	name: string;
	/** Current product name when the probe ran against an archive entrypoint. */
	product?: PackageProduct;
	/** Retained for readers of older producer output; current product names are used. */
	variant: string;
	architecture: string;
	status: "executed" | "blocked" | "failed";
	command: string;
	detail: string;
}

export interface ProducedCoverageInput extends CoverageInput {
	probes?: readonly ProducerProbe[];
	reductionManifest?: string;
	reductionNotes?: readonly { id: string; reason: string }[];
}

export interface ResolvedBinary {
	product: PackageProduct;
	/** Frozen performance row represented by this product, when applicable. */
	performanceVariant?: PerformanceVariant;
	architecture: PackageArchitecture;
	path: string;
	sha256: string;
	archive?: string;
}

export interface ProducerResult {
	coverage: ProducedCoverageInput;
	performance: PerformanceInput;
	coveragePath: string;
	performancePath: string;
	binaries: readonly ResolvedBinary[];
	probes: readonly ProducerProbe[];
}

export interface ProduceEvidenceOptions {
	outputDir: string;
	sourceCommit: string;
	/** Defaults to `sha-<first 12 of sourceCommit>`. */
	releaseId?: string;
	/** Staged archive directory; required to validate the complete release. */
	artifactsDir?: string;
	/** Explicit current-product binaries; overrides extracted probe paths (used by tests). */
	binaryPaths?: readonly string[];
	/** Readiness origin for the running host. Absent keeps readiness blocked. */
	baseUrl?: string;
	/** Defaults to `web/reductions.json`. */
	reductionManifestPath?: string;
	/** Fresh-process startup samples per variant; at least 5. */
	minSamples?: number;
}

interface ReductionGroup {
	ids: readonly string[];
	reason: string;
}

interface FieldReductionGroup {
	target: string;
	fields: readonly string[];
	reason: string;
}

interface TargetReductionGroup {
	targets: readonly string[];
	reason: string;
}

export interface ReductionsManifest {
	schemaVersion: number;
	approval: {
		approved: boolean;
		approver: string;
		reference?: string;
		statement?: string;
	};
	coverage: {
		reductionGroups: readonly ReductionGroup[];
		optionalFeatures?: readonly string[];
		evidenceNote?: string;
	};
	gates: {
		reductionGroups: readonly ReductionGroup[];
	};
	performance: {
		fieldReductions: readonly FieldReductionGroup[];
		/** Optional: the current manifest carries no target reductions. */
		targetReductions?: readonly TargetReductionGroup[];
	};
	namedReductions?: readonly { id: string; reason: string }[];
}

export interface ExpandedReductions {
	coverageReductions: CoverageReduction[];
	gateReductions: CoverageReduction[];
	fieldReductions: PerformanceFieldReduction[];
	targetReductions: PerformanceTargetReduction[];
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function sha256(buffer: Uint8Array): string {
	return createHash("sha256").update(buffer).digest("hex");
}

function nativeArchitecture(): PackageArchitecture {
	if (process.arch === "x64") return "amd64";
	if (process.arch === "arm64") return "arm64";
	throw new Error(`produce-evidence-inputs: unsupported host architecture ${process.arch}`);
}

function performanceVariant(product: PackageProduct): PerformanceVariant | undefined {
	// The matrix has one host target. The single pixie archive is the retained
	// assistant/host target; the controller-only archive has no host variant.
	if (product === "pixie") return "assistant";
	return undefined;
}

function expectedTargets(): string[] {
	return PERFORMANCE_VARIANTS.flatMap((variant) =>
		PERFORMANCE_ARCHITECTURES.map((architecture) => `${variant}/${architecture}`),
	);
}

/** Expand and validate the committed manifest. Every id must be a real,
 * required row; a typo or an optional row must fail loudly rather than silently
 * dropping a gate. */
export function expandReductions(manifest: ReductionsManifest): ExpandedReductions {
	if (manifest.schemaVersion !== 1) {
		throw new Error(
			`produce-evidence-inputs: unsupported reductions schema ${manifest.schemaVersion}`,
		);
	}
	const approval = manifest.approval;
	if (!approval.approved || !approval.approver?.trim()) {
		throw new Error("produce-evidence-inputs: reductions manifest requires operator approval");
	}
	const requiredCoverage = new Set<string>([...MANDATORY_FEATURE_IDS, ...ACCEPTANCE_IDS]);
	const coverageReductions: CoverageReduction[] = [];
	for (const group of manifest.coverage.reductionGroups) {
		for (const id of group.ids) {
			if (!requiredCoverage.has(id)) {
				throw new Error(`produce-evidence-inputs: reduction ${id} is not a required FC/X row`);
			}
			coverageReductions.push({
				id,
				approved: true,
				approver: approval.approver,
				reason: group.reason,
			});
		}
	}
	const gateReductions: CoverageReduction[] = [];
	for (const group of manifest.gates.reductionGroups) {
		for (const id of group.ids) {
			if (!(CORE_GATE_IDS as readonly string[]).includes(id)) {
				throw new Error(`produce-evidence-inputs: gate reduction ${id} is not a core gate`);
			}
			gateReductions.push({
				id,
				approved: true,
				approver: approval.approver,
				reason: group.reason,
			});
		}
	}
	const targets = expectedTargets();
	const fieldReductions: PerformanceFieldReduction[] = [];
	for (const group of manifest.performance.fieldReductions) {
		if (group.target !== "*" && !targets.includes(group.target)) {
			throw new Error(
				`produce-evidence-inputs: field reduction target ${group.target} is not a performance target`,
			);
		}
		for (const field of group.fields) {
			if (!(REDUCIBLE_PERFORMANCE_FIELDS as readonly string[]).includes(field)) {
				throw new Error(
					`produce-evidence-inputs: field ${field} is not a reducible performance field`,
				);
			}
			fieldReductions.push({
				target: group.target,
				field,
				approved: true,
				approver: approval.approver,
				reason: group.reason,
			});
		}
	}
	const targetReductions: PerformanceTargetReduction[] = [];
	for (const group of manifest.performance.targetReductions ?? []) {
		for (const target of group.targets) {
			if (!targets.includes(target)) {
				throw new Error(
					`produce-evidence-inputs: target reduction ${target} is not a performance target`,
				);
			}
			targetReductions.push({
				target,
				approved: true,
				approver: approval.approver,
				reason: group.reason,
			});
		}
	}
	return { coverageReductions, gateReductions, fieldReductions, targetReductions };
}

export function defaultReductionManifestPath(): string {
	return resolve(import.meta.dir, "../reductions.json");
}

export async function loadReductionsManifest(path: string): Promise<ReductionsManifest> {
	let text: string;
	try {
		text = await readFile(path, "utf8");
	} catch (error) {
		throw new Error(
			`produce-evidence-inputs: reductions manifest is unreadable: ${errorMessage(error)}`,
		);
	}
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (error) {
		throw new Error(
			`produce-evidence-inputs: reductions manifest is not valid JSON: ${errorMessage(error)}`,
		);
	}
	if (value === null || typeof value !== "object" || Array.isArray(value)) {
		throw new Error("produce-evidence-inputs: reductions manifest must be a JSON object");
	}
	return value as ReductionsManifest;
}

interface ProbeRun {
	exitCode: number | null;
	stdout: string;
	stderr: string;
	spawnError: string | null;
}

async function runCommand(command: readonly string[]): Promise<ProbeRun> {
	try {
		const child = Bun.spawn([...command], { stdout: "pipe", stderr: "pipe" });
		const [stdout, stderr] = await Promise.all([
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
		]);
		const exitCode = await child.exited;
		return { exitCode, stdout, stderr, spawnError: null };
	} catch (error) {
		return { exitCode: null, stdout: "", stderr: "", spawnError: errorMessage(error) };
	}
}

function probeDetail(path: string, run: ProbeRun): string {
	return JSON.stringify({
		path,
		exitCode: run.exitCode,
		stdout: run.stdout.slice(0, PROBE_OUTPUT_LIMIT),
		stderr: run.stderr.slice(0, PROBE_OUTPUT_LIMIT),
		...(run.spawnError === null ? {} : { spawnError: run.spawnError }),
	});
}

interface ProbeResult {
	probe: ProducerProbe;
	evidence?: CoverageEvidence;
}

async function probeVersion(
	binary: ResolvedBinary,
	releaseId: string,
	sourceCommit: string,
): Promise<ProbeResult> {
	const command = `${binary.path} --version`;
	const run = await runCommand([binary.path, "--version"]);
	const probe: ProducerProbe = {
		name: "version",
		product: binary.product,
		variant: binary.product,
		architecture: binary.architecture,
		status: "failed",
		command,
		detail: probeDetail(binary.path, run),
	};
	if (run.spawnError !== null) return { probe: { ...probe, status: "blocked" } };
	if (run.exitCode !== 0) return { probe };
	const stdout = run.stdout;
	if (!stdout.includes(releaseId) || !stdout.includes(sourceCommit)) return { probe };
	return {
		probe: { ...probe, status: "executed" },
		evidence: {
			id: "FC01",
			kind: "artifact",
			source: binary.path,
			test: `${binary.product}-${binary.architecture}-version`,
			profile: `linux-${binary.architecture}-staged-artifact`,
			actual: true,
			live: true,
		},
	};
}

async function probeDoctor(binary: ResolvedBinary): Promise<ProbeResult> {
	const command = `${binary.path} doctor`;
	const run = await runCommand([binary.path, "doctor"]);
	const probe: ProducerProbe = {
		name: "doctor",
		product: binary.product,
		variant: binary.product,
		architecture: binary.architecture,
		status: "failed",
		command,
		detail: probeDetail(binary.path, run),
	};
	if (run.spawnError !== null) return { probe: { ...probe, status: "blocked" } };
	if (run.exitCode === 0) {
		return {
			probe: { ...probe, status: "executed" },
			evidence: {
				id: "FC01",
				kind: "native",
				source: binary.path,
				test: `${binary.product}-${binary.architecture}-doctor`,
				profile: `linux-${binary.architecture}-staged-artifact`,
				actual: true,
				live: true,
			},
		};
	}
	if (
		!PRODUCT_DOCTOR_SUPPORTED[binary.product] ||
		/unknown command/i.test(`${run.stdout}\n${run.stderr}`)
	) {
		return { probe: { ...probe, status: "blocked" } };
	}
	return { probe };
}

function readinessEndpoint(baseUrl: string): string {
	const trimmed = baseUrl.replace(/\/+$/, "");
	return /\/readyz$/.test(trimmed) ? trimmed : `${trimmed}/readyz`;
}

async function probeReadiness(
	baseUrl: string | undefined,
	architecture: PackageArchitecture,
): Promise<ProbeResult> {
	if (baseUrl === undefined || baseUrl.trim() === "") {
		return {
			probe: {
				name: "readiness",
				variant: "running-host",
				architecture,
				status: "blocked",
				command: "test -n <base-url>",
				detail: JSON.stringify({ url: null, skipped: "--base-url was not provided" }),
			},
		};
	}
	const url = readinessEndpoint(baseUrl.trim());
	const command = `curl -fsS ${url}`;
	try {
		const response = await fetch(url, {
			redirect: "manual",
			signal: AbortSignal.timeout(READINESS_TIMEOUT_MS),
		});
		const body = (await response.text().catch(() => "")).slice(0, PROBE_OUTPUT_LIMIT);
		const probe: ProducerProbe = {
			name: "readiness",
			variant: "running-host",
			architecture,
			status: response.ok ? "executed" : "failed",
			command,
			detail: JSON.stringify({ url, httpStatus: response.status, body }),
		};
		if (!response.ok) return { probe };
		return {
			probe,
			evidence: {
				id: "FC01",
				kind: "native",
				source: url,
				test: "running-host-readiness",
				profile: `linux-${architecture}-running-host`,
				actual: true,
				live: true,
			},
		};
	} catch (error) {
		return {
			probe: {
				name: "readiness",
				variant: "running-host",
				architecture,
				status: "failed",
				command,
				detail: JSON.stringify({ url, error: errorMessage(error) }),
			},
		};
	}
}

interface ValidatedArchive {
	product: PackageProduct;
	architecture: PackageArchitecture;
	archive: string;
	entries: ReturnType<typeof parseTarGz>;
}

const PRODUCT_EXECUTABLE_PATHS: Readonly<Record<PackageProduct, readonly string[]>> = {
	pixie_web: ["pixie_web"],
	pixie: ["pixie"],
};

/**
 * Archive-local files that must extract as regular 0o644 files. The internal
 * `libexec/pixie_assistant.js` is a portable `bun build --target=bun` bundle
 * run by the staged runtime, not a compiled executable, so it stays absent
 * from the executable list above.
 */
const PRODUCT_REGULAR_PATHS: Readonly<Record<PackageProduct, readonly string[]>> = {
	pixie_web: [],
	pixie: ["libexec/pixie_assistant.js"],
};

/**
 * The archive-local launcher each product probes for release identity. The
 * single `pixie` host binary owns the release `--version` contract
 * (`pixie --pi-version` reports the bundled Pi version separately).
 */
export function productProbeEntrypoint(product: PackageProduct): string {
	if (product === "pixie") return "pixie";
	return "pixie_web";
}

/**
 * Launchers that implement a real `doctor` command. The `pixie` host
 * launcher deliberately exposes only serve/version/help, so probing its
 * unsupported `doctor` reports blocked rather than a failed execution.
 */
const PRODUCT_DOCTOR_SUPPORTED: Readonly<Record<PackageProduct, boolean>> = {
	pixie_web: true,
	pixie: false,
};

function legacyArchivePath(name: string): boolean {
	return name.startsWith("pixie-assistant-");
}

function legacyExecutablePath(entries: readonly { name: string }[]): string | undefined {
	return entries.find(({ name }) => name.split("/").includes("pixie-assistant"))?.name;
}

/** Only the pinned Bun runtime may ship; a bundled Node runtime is rejected. */
function nodeRuntimePath(entries: readonly { name: string }[]): string | undefined {
	return entries.find(({ name }) => /(?:^|\/)runtime\/node(?:\/|$)/.test(name))?.name;
}

/**
 * Reject any archive member that could escape the extraction root before a
 * single byte is written. Mirrors `verifyBundledPiRuntimeArchive`'s runtime
 * path checks (`runtimePath`) and additionally rejects duplicate names so a
 * later member cannot overwrite an earlier extraction. `parseTar` strips one
 * leading `./`, so `./../escaped` still fails the `..` segment check.
 */
function assertSafeMemberPaths(archive: string, entries: readonly TarEntry[]): void {
	const seen = new Set<string>();
	for (const { name } of entries) {
		if (
			name === "" ||
			name.startsWith("/") ||
			name.includes("\\") ||
			name.split("/").some((part) => part === "" || part === "." || part === "..")
		) {
			throw new Error(
				`produce-evidence-inputs: ${archive} has an unsafe member path ${JSON.stringify(name)}`,
			);
		}
		if (seen.has(name)) {
			throw new Error(
				`produce-evidence-inputs: ${archive} has a duplicate member ${JSON.stringify(name)}`,
			);
		}
		seen.add(name);
	}
}

/**
 * Verify the complete merged release before creating any probe input. The
 * architecture-specific runner only executes its native product entrypoints,
 * but its evidence is meaningful only when all six staged archives use the
 * same current three-product contract.
 */
async function validateStagedArchives(
	artifactsDir: string,
	releaseId: string,
): Promise<ValidatedArchive[]> {
	let names: string[];
	try {
		names = await readdir(artifactsDir);
	} catch (error) {
		throw new Error(
			`produce-evidence-inputs: staged archive directory is unreadable: ${errorMessage(error)}`,
		);
	}
	const legacy = names.find(legacyArchivePath);
	if (legacy !== undefined) {
		throw new Error(
			`produce-evidence-inputs: legacy pixie-assistant archive ${legacy} is not a current product archive`,
		);
	}

	const archives: ValidatedArchive[] = [];
	for (const architecture of ["amd64", "arm64"] as const) {
		for (const product of RELEASE_PRODUCTS) {
			const archive = expectedArchiveName(product, architecture, releaseId);
			let buffer: Buffer;
			try {
				buffer = await readFile(join(artifactsDir, archive));
			} catch (error) {
				throw new Error(
					`produce-evidence-inputs: required staged archive ${archive} is unreadable: ${errorMessage(error)}`,
				);
			}
			let entries: ReturnType<typeof parseTarGz>;
			try {
				entries = parseTarGz(buffer);
			} catch (error) {
				throw new Error(
					`produce-evidence-inputs: required staged archive ${archive} cannot be read: ${errorMessage(error)}`,
				);
			}
			assertSafeMemberPaths(archive, entries);
			const legacyEntry = legacyExecutablePath(entries);
			if (legacyEntry !== undefined) {
				throw new Error(
					`produce-evidence-inputs: ${archive} has legacy pixie-assistant executable path ${legacyEntry}`,
				);
			}
			const nodeEntry = nodeRuntimePath(entries);
			if (nodeEntry !== undefined) {
				throw new Error(
					`produce-evidence-inputs: ${archive} bundles a Node runtime at ${nodeEntry}`,
				);
			}
			try {
				assertProductArchiveLayout(
					product,
					entries.map(({ name }) => name),
				);
			} catch (error) {
				throw new Error(
					`produce-evidence-inputs: ${archive} has an invalid ${product} layout: ${errorMessage(error)}`,
				);
			}
			archives.push({ product, architecture, archive, entries });
		}
	}
	return archives;
}

/**
 * Extract each native archive into an isolated product tree. The probe
 * entrypoint stays inside that tree so its runtime/ and libexec/ siblings are
 * present; a bare copy would make the launcher fail before `--version`. */
async function extractNativeBinaries(
	archives: readonly ValidatedArchive[],
	architecture: PackageArchitecture,
	outputDir: string,
): Promise<ResolvedBinary[]> {
	const binaryRoot = join(outputDir, "binaries");
	const resolved: ResolvedBinary[] = [];
	for (const product of RELEASE_PRODUCTS) {
		const archive = archives.find(
			(candidate) => candidate.product === product && candidate.architecture === architecture,
		);
		if (archive === undefined) {
			throw new Error(
				`produce-evidence-inputs: validated release is missing ${product} linux-${architecture}`,
			);
		}
		const productRoot = join(binaryRoot, "products", product);
		for (const entry of archive.entries) {
			const path = join(productRoot, entry.name);
			await mkdir(dirname(path), { recursive: true });
			await writeFile(path, entry.content);
			const executable =
				PRODUCT_EXECUTABLE_PATHS[product].includes(entry.name) ||
				// The Pi-bearing archives ship the runtime executable at 0o755; a
				// probe that starts the root `pixie` launcher needs it executable.
				(product !== "pixie_web" && entry.name === "runtime/bin/bun");
			await chmod(path, executable ? 0o755 : 0o644);
		}
		// The internal assistant bundle must be a regular 0o644 file, never an
		// executable; a layout that dropped or replaced it is not probed.
		for (const expected of PRODUCT_REGULAR_PATHS[product]) {
			if (!archive.entries.some((entry) => entry.name === expected)) {
				throw new Error(`produce-evidence-inputs: ${archive.archive} does not contain ${expected}`);
			}
		}
		const probeEntrypoint = productProbeEntrypoint(product);
		const entrypoint = archive.entries.find((entry) => entry.name === probeEntrypoint);
		if (entrypoint === undefined) {
			throw new Error(
				`produce-evidence-inputs: ${archive.archive} does not contain ${probeEntrypoint}`,
			);
		}
		const path = join(productRoot, probeEntrypoint);
		const variant = performanceVariant(product);
		resolved.push({
			product,
			...(variant === undefined ? {} : { performanceVariant: variant }),
			architecture,
			path,
			sha256: sha256(entrypoint.content),
			archive: archive.archive,
		});
	}
	return resolved;
}

async function resolveBinaries(
	options: ProduceEvidenceOptions,
	architecture: PackageArchitecture,
	archives: readonly ValidatedArchive[],
	outputDir: string,
): Promise<ResolvedBinary[]> {
	const staged = await extractNativeBinaries(archives, architecture, outputDir);
	const explicit = (options.binaryPaths ?? [])
		.map((path) => path.trim())
		.filter((path) => path !== "");
	if (explicit.length === 0) return staged;

	const explicitByProduct = new Map<PackageProduct, string>();
	for (const path of explicit) {
		const absolute = resolve(path);
		const product = basename(absolute);
		if (product === "pixie-assistant") {
			throw new Error(
				"produce-evidence-inputs: legacy pixie-assistant executable path is not a current product path",
			);
		}
		if (!(RELEASE_PRODUCTS as readonly string[]).includes(product)) {
			throw new Error(`produce-evidence-inputs: ${absolute} is not a current product executable`);
		}
		if (explicitByProduct.has(product as PackageProduct)) {
			throw new Error(`produce-evidence-inputs: duplicate explicit binary for ${product}`);
		}
		explicitByProduct.set(product as PackageProduct, absolute);
	}
	for (const product of RELEASE_PRODUCTS) {
		if (!explicitByProduct.has(product)) {
			throw new Error(`produce-evidence-inputs: explicit binaries must include ${product}`);
		}
	}
	return Promise.all(
		staged.map(async (binary) => {
			const path = explicitByProduct.get(binary.product);
			if (path === undefined) throw new Error(`missing explicit ${binary.product} binary`);
			return { ...binary, path, sha256: sha256(await readFile(path)) };
		}),
	);
}

function percentile(samples: readonly number[], percent: number): number {
	const ordered = [...samples].sort((left, right) => left - right);
	return ordered[Math.min(ordered.length - 1, Math.floor((ordered.length * percent) / 100))] ?? 0;
}

function parseMaxRss(stderr: string): number | null {
	const match = stderr.match(/Maximum resident set size \(kbytes\):\s*(\d+)/);
	if (match === null) return null;
	const kbytes = Number(match[1]);
	return Number.isFinite(kbytes) && kbytes > 0 ? kbytes * 1024 : null;
}

async function resolveTimeBinary(): Promise<string | null> {
	const candidate = Bun.which("time");
	if (candidate === null) return null;
	const probe = await runCommand([candidate, "-v", "true"]);
	if (probe.exitCode === 0 && /Maximum resident set size/.test(probe.stderr)) return candidate;
	return null;
}

async function measureStartup(
	binary: ResolvedBinary,
	variant: PerformanceVariant,
	timeBinary: string,
	minSamples: number,
	sourceCommit: string,
): Promise<PerformanceMeasurement> {
	const samples: number[] = [];
	let peakProcessRssBytes = 0;
	for (let index = 0; index < minSamples; index += 1) {
		const started = performance.now();
		const child = Bun.spawn([timeBinary, "-v", binary.path, "--version"], {
			stdout: "pipe",
			stderr: "pipe",
		});
		const [, stderr] = await Promise.all([
			new Response(child.stdout).text(),
			new Response(child.stderr).text(),
		]);
		const exitCode = await child.exited;
		const elapsed = performance.now() - started;
		if (exitCode !== 0) {
			throw new Error(
				`produce-evidence-inputs: ${binary.product} sample ${index} exited ${exitCode}: ${stderr.trim()}`,
			);
		}
		const rss = parseMaxRss(stderr);
		if (rss === null) {
			throw new Error(
				`produce-evidence-inputs: ${binary.product} sample ${index} reported no peak RSS (${timeBinary})`,
			);
		}
		if (!Number.isFinite(elapsed) || elapsed <= 0) {
			throw new Error(
				`produce-evidence-inputs: ${binary.product} sample ${index} has invalid duration`,
			);
		}
		samples.push(elapsed);
		peakProcessRssBytes = Math.max(peakProcessRssBytes, rss);
	}
	return {
		architecture: binary.architecture,
		variant,
		profile: `linux-${binary.architecture}-fresh-process-startup`,
		artifact: `sha256:${binary.sha256}`,
		sourceCommit,
		sampleDurationsMs: samples,
		p50Ms: percentile(samples, 50),
		p95Ms: percentile(samples, 95),
		peakProcessRssBytes,
		decodedMemoryBytes: 0,
		bufferMemoryBytes: 0,
		workerMemoryBytes: 0,
		workerPids: 0,
		workerCpuQuotaMillis: 0,
		workerWallTimeMs: 0,
		workerOutputBytes: 0,
		scratchBytes: 0,
		scratchInodes: 0,
		// GNU/BusyBox `time -v` reports the waited-for process tree's peak RSS;
		// the startup probe has no long-lived descendants.
		fullProcessTree: true,
		// No content-filled UI fixture is loaded by a startup probe.
		contentFilledUI: false,
		live: true,
	};
}

export async function produceEvidenceInputs(
	options: ProduceEvidenceOptions,
): Promise<ProducerResult> {
	const sourceCommit = options.sourceCommit.trim();
	if (!SOURCE_COMMIT_PATTERN.test(sourceCommit)) {
		throw new Error("produce-evidence-inputs: --source-commit must be 40 lowercase hex characters");
	}
	const releaseId = (options.releaseId ?? `sha-${sourceCommit.slice(0, 12)}`).trim();
	if (!RELEASE_ID_PATTERN.test(releaseId) || releaseId !== `sha-${sourceCommit.slice(0, 12)}`) {
		throw new Error(
			`produce-evidence-inputs: --release-id must be sha-${sourceCommit.slice(0, 12)}`,
		);
	}
	const minSamples = options.minSamples ?? MIN_SAMPLES;
	if (!Number.isSafeInteger(minSamples) || minSamples < MIN_SAMPLES) {
		throw new Error(`produce-evidence-inputs: --min-samples must be at least ${MIN_SAMPLES}`);
	}
	const manifestPath = options.reductionManifestPath ?? defaultReductionManifestPath();
	const manifest = await loadReductionsManifest(manifestPath);
	const expanded = expandReductions(manifest);

	if (options.artifactsDir === undefined || options.artifactsDir.trim() === "") {
		throw new Error(
			"produce-evidence-inputs: --artifacts is required to validate the complete release",
		);
	}
	const artifacts = await validateStagedArchives(resolve(options.artifactsDir), releaseId);
	const architecture = nativeArchitecture();
	const outputDir = resolve(options.outputDir);
	await mkdir(join(outputDir, "binaries"), { recursive: true });
	const binaries = await resolveBinaries(options, architecture, artifacts, outputDir);

	const probes: ProducerProbe[] = [];
	const evidence: CoverageEvidence[] = [];
	for (const binary of binaries) {
		const version = await probeVersion(binary, releaseId, sourceCommit);
		probes.push(version.probe);
		if (version.evidence !== undefined) evidence.push(version.evidence);
		const doctor = await probeDoctor(binary);
		probes.push(doctor.probe);
		if (doctor.evidence !== undefined) evidence.push(doctor.evidence);
	}
	const readiness = await probeReadiness(options.baseUrl, architecture);
	probes.push(readiness.probe);
	if (readiness.evidence !== undefined) evidence.push(readiness.evidence);

	const coverage: ProducedCoverageInput = {
		mandatoryFeatureIds: [...MANDATORY_FEATURE_IDS],
		applicableAcceptanceIds: [...ACCEPTANCE_IDS],
		evidence,
		reductions: expanded.coverageReductions,
		gateReductions: expanded.gateReductions,
		removalRequested: true,
		legacyPaths: [],
		probes,
		reductionManifest: manifestPath,
		reductionNotes: manifest.namedReductions ?? [],
	};

	const timeBinary = await resolveTimeBinary();
	const measurements: PerformanceMeasurement[] = [];
	if (timeBinary !== null) {
		for (const binary of binaries) {
			if (binary.performanceVariant === undefined) continue;
			measurements.push(
				await measureStartup(
					binary,
					binary.performanceVariant,
					timeBinary,
					minSamples,
					sourceCommit,
				),
			);
		}
	}
	const performance: PerformanceInput = {
		measurements,
		fieldReductions: expanded.fieldReductions,
		targetReductions: expanded.targetReductions,
		...(timeBinary === null
			? {
					staticViolations: [
						"no `time -v` measurement binary is available; fresh-process peak RSS was not measured",
					],
				}
			: {}),
	};

	const coveragePath = join(outputDir, "coverage.json");
	const performancePath = join(outputDir, "performance.json");
	await writeFile(coveragePath, `${JSON.stringify(coverage, null, 2)}\n`);
	await writeFile(performancePath, `${JSON.stringify(performance, null, 2)}\n`);
	return {
		coverage,
		performance,
		coveragePath,
		performancePath,
		binaries,
		probes,
	};
}

export const PRODUCE_EVIDENCE_USAGE = [
	"usage: bun scripts/produce-evidence-inputs.ts --output <dir> --source-commit <40-hex>",
	"         --artifacts <dir> [--binary <current-product-path>]... [--release-id sha-<12>]",
	"         [--base-url <origin>] [--reduction-manifest <json>] [--min-samples <n>]",
	"",
	"Validates all four current product archives, their exact layouts and every member",
	"path before extracting each product tree. It probes the launcher that owns each",
	"product's release identity (pixie for the host archive, pixie_web for the",
	"controller) and runs only checks that genuinely execute (--version and doctor, plus a readiness",
	"GET when --base-url is supplied), then measures pixie for the",
	"frozen assistant startup row. Everything else is absent or reduced",
	"only by the committed, approved reductions manifest.",
].join("\n");

interface ProducerCliOptions {
	outputDir: string;
	sourceCommit: string;
	releaseId?: string;
	artifactsDir?: string;
	binaryPaths: string[];
	baseUrl?: string;
	reductionManifestPath?: string;
	minSamples?: number;
}

function readValue(args: readonly string[], index: number, flag: string): [string, number] {
	const value = args[index + 1];
	if (value === undefined || value.trim() === "") throw new Error(`${flag} requires a value`);
	return [value, index + 1];
}

function parseProducerArgs(args: readonly string[]): ProducerCliOptions {
	const values: Record<string, string> = {};
	const binaryPaths: string[] = [];
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index] ?? "";
		if (argument === "--help" || argument === "-h") {
			console.log(PRODUCE_EVIDENCE_USAGE);
			process.exit(0);
		}
		if (argument === "--binary") {
			const [value, next] = readValue(args, index, argument);
			binaryPaths.push(value);
			index = next;
			continue;
		}
		if (
			argument === "--output" ||
			argument === "--source-commit" ||
			argument === "--release-id" ||
			argument === "--artifacts" ||
			argument === "--base-url" ||
			argument === "--reduction-manifest" ||
			argument === "--min-samples"
		) {
			const [value, next] = readValue(args, index, argument);
			values[argument.slice(2)] = value;
			index = next;
			continue;
		}
		const separator = argument.indexOf("=");
		if (argument.startsWith("--") && separator > 2) {
			const key = argument.slice(2, separator);
			const value = argument.slice(separator + 1);
			if (value.trim() === "") throw new Error(`--${key} requires a value`);
			if (key === "binary") binaryPaths.push(value);
			else values[key] = value;
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	const outputDir = values.output;
	const sourceCommit = values["source-commit"];
	if (outputDir === undefined || outputDir === "") throw new Error("--output <dir> is required");
	if (sourceCommit === undefined || sourceCommit === "")
		throw new Error("--source-commit <40-hex> is required");
	const minSamplesValue = values["min-samples"];
	const minSamples =
		minSamplesValue === undefined ? undefined : Number.parseInt(minSamplesValue, 10);
	return {
		outputDir,
		sourceCommit,
		binaryPaths,
		...(values["release-id"] === undefined ? {} : { releaseId: values["release-id"] }),
		...(values.artifacts === undefined ? {} : { artifactsDir: values.artifacts }),
		...(values["base-url"] === undefined ? {} : { baseUrl: values["base-url"] }),
		...(values["reduction-manifest"] === undefined
			? {}
			: { reductionManifestPath: values["reduction-manifest"] }),
		...(minSamples === undefined ? {} : { minSamples }),
	};
}

if (import.meta.main) {
	try {
		const options = parseProducerArgs(Bun.argv.slice(2));
		const result = await produceEvidenceInputs(options);
		console.log(
			`produce-evidence-inputs: wrote ${result.coveragePath} and ${result.performancePath} ` +
				`(${result.binaries.length} native binaries, ${result.coverage.reductions?.length ?? 0} coverage reductions)`,
		);
		for (const probe of result.probes) {
			console.log(
				`  probe ${probe.name} [${probe.status}]: ${probe.product ?? probe.variant} ${probe.command}`,
			);
		}
	} catch (error) {
		console.error(`produce-evidence-inputs: ${errorMessage(error)}`);
		process.exitCode = 1;
	}
}
