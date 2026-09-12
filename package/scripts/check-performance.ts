#!/usr/bin/env bun

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import {
	type EvidenceBundle,
	findAssertion,
	PERFORMANCE_ASSERTION_ID,
	readEvidenceBundle,
} from "./evidence-bundle.ts";

export const PERFORMANCE_ARCHITECTURES = ["amd64", "arm64"] as const;
export const PERFORMANCE_VARIANTS = ["assistant", "full-host"] as const;
export type PerformanceArchitecture = (typeof PERFORMANCE_ARCHITECTURES)[number];
export type PerformanceVariant = (typeof PERFORMANCE_VARIANTS)[number];

const MIN_SAMPLES = 5;
const SOURCE_COMMIT = /^[0-9a-f]{40}$/;

/** One repeated fresh-process measurement. Values are evidence, not budgets. */
export interface PerformanceMeasurement {
	architecture: string;
	variant: string;
	profile: string;
	artifact: string;
	sourceCommit: string;
	sampleDurationsMs: readonly number[];
	p50Ms: number;
	p95Ms: number;
	peakProcessRssBytes: number;
	decodedMemoryBytes: number;
	bufferMemoryBytes: number;
	workerMemoryBytes: number;
	workerPids: number;
	workerCpuQuotaMillis: number;
	workerWallTimeMs: number;
	workerOutputBytes: number;
	scratchBytes: number;
	scratchInodes: number;
	fullProcessTree: boolean;
	contentFilledUI: boolean;
	live: boolean;
}

export interface PerformanceInput {
	measurements?: readonly PerformanceMeasurement[];
	staticViolations?: readonly string[];
}

export interface PerformanceFacts {
	targets: readonly string[];
	missingTargets: readonly string[];
	measuredTargets: readonly string[];
	missingLiveEvidence: readonly string[];
}

export interface PerformanceReport {
	ok: boolean;
	staticOk: boolean;
	complete: boolean;
	violations: readonly string[];
	missingLiveEvidence: readonly string[];
	facts: PerformanceFacts;
}

function finitePositive(value: number): boolean {
	return Number.isFinite(value) && value > 0;
}

function percentile(samples: readonly number[], percent: number): number {
	const ordered = [...samples].sort((left, right) => left - right);
	return ordered[Math.min(ordered.length - 1, Math.floor((ordered.length * percent) / 100))] ?? 0;
}

function validArchitecture(value: string): value is PerformanceArchitecture {
	return (PERFORMANCE_ARCHITECTURES as readonly string[]).includes(value);
}

function validVariant(value: string): value is PerformanceVariant {
	return (PERFORMANCE_VARIANTS as readonly string[]).includes(value);
}

function targetKey(measurement: Pick<PerformanceMeasurement, "architecture" | "variant">): string {
	return `${measurement.variant}/${measurement.architecture}`;
}

function checkMeasurement(
	measurement: PerformanceMeasurement,
	violations: string[],
	missingLiveEvidence: string[],
): void {
	const target = targetKey(measurement);
	if (!validArchitecture(measurement.architecture))
		violations.push(`${target}: unsupported architecture`);
	if (!validVariant(measurement.variant))
		violations.push(`${target}: unsupported executable variant`);
	if (typeof measurement.profile !== "string" || !measurement.profile.trim())
		violations.push(`${target}: measurement profile is required`);
	if (typeof measurement.artifact !== "string" || !measurement.artifact.trim())
		violations.push(`${target}: artifact identity is required`);
	if (typeof measurement.sourceCommit !== "string" || !SOURCE_COMMIT.test(measurement.sourceCommit))
		violations.push(`${target}: full 40-character source commit is required`);
	const samples = Array.isArray(measurement.sampleDurationsMs) ? measurement.sampleDurationsMs : [];
	if (samples.length < MIN_SAMPLES)
		violations.push(`${target}: at least ${MIN_SAMPLES} fresh-process samples are required`);
	if (samples.some((sample) => !finitePositive(sample)))
		violations.push(`${target}: sample durations must be finite and positive`);
	if (
		!finitePositive(measurement.p50Ms) ||
		!finitePositive(measurement.p95Ms) ||
		measurement.p95Ms < measurement.p50Ms
	) {
		violations.push(`${target}: p50/p95 must be finite, positive and ordered`);
	} else if (
		measurement.p50Ms !== percentile(samples, 50) ||
		measurement.p95Ms !== percentile(samples, 95)
	) {
		violations.push(`${target}: p50/p95 do not match the recorded samples`);
	}
	for (const [name, value] of [
		["peak process RSS", measurement.peakProcessRssBytes],
		["decoded memory", measurement.decodedMemoryBytes],
		["serialized buffer memory", measurement.bufferMemoryBytes],
		["worker memory", measurement.workerMemoryBytes],
		["worker PID", measurement.workerPids],
		["worker CPU quota", measurement.workerCpuQuotaMillis],
		["worker wall time", measurement.workerWallTimeMs],
		["worker output", measurement.workerOutputBytes],
		["scratch bytes", measurement.scratchBytes],
		["scratch inodes", measurement.scratchInodes],
	] as const) {
		if (!finitePositive(value)) violations.push(`${target}: ${name} measurement is required`);
	}
	if (measurement.fullProcessTree !== true)
		violations.push(`${target}: measurement must include the complete process tree`);
	if (measurement.contentFilledUI !== true)
		violations.push(`${target}: measurement must use a content-filled UI fixture`);
	if (measurement.live !== true)
		missingLiveEvidence.push(`${target}: live deployment measurement is missing`);
}

export function inspectPerformance(input: PerformanceInput): PerformanceReport {
	const violations = [
		...(input.staticViolations ?? [])
			.filter((issue) => issue.trim())
			.map((issue) => `static performance input: ${issue.trim()}`),
	];
	const missingLiveEvidence: string[] = [];
	const expectedTargets = PERFORMANCE_VARIANTS.flatMap((variant) =>
		PERFORMANCE_ARCHITECTURES.map((architecture) => `${variant}/${architecture}`),
	);
	const measurements = Array.isArray(input.measurements) ? input.measurements : [];
	const byTarget = new Map<string, PerformanceMeasurement>();
	for (const candidate of measurements) {
		if (candidate === null || typeof candidate !== "object") {
			violations.push("performance evidence entry must be an object");
			continue;
		}
		const measurement = candidate as PerformanceMeasurement;
		const target = targetKey(measurement);
		if (byTarget.has(target)) {
			violations.push(`${target}: duplicate performance evidence`);
			continue;
		}
		byTarget.set(target, measurement);
		checkMeasurement(measurement, violations, missingLiveEvidence);
	}
	const missingTargets = expectedTargets.filter((target) => !byTarget.has(target));
	for (const target of missingTargets)
		missingLiveEvidence.push(`${target}: repeated full-process evidence is missing`);
	const measuredTargets = [...byTarget.keys()].sort();
	const staticOk = violations.length === 0;
	const complete = staticOk && missingLiveEvidence.length === 0;
	return {
		ok: complete,
		staticOk,
		complete,
		violations,
		missingLiveEvidence: [...new Set(missingLiveEvidence)],
		facts: {
			targets: expectedTargets,
			missingTargets,
			measuredTargets,
			missingLiveEvidence: [...new Set(missingLiveEvidence)],
		},
	};
}

export async function collectPerformanceInput(
	path = resolve(import.meta.dir, "../performance-evidence.json"),
): Promise<PerformanceInput> {
	try {
		const parsed = JSON.parse(await readFile(path, "utf8")) as PerformanceInput;
		if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed))
			throw new Error("evidence root must be an object");
		return parsed;
	} catch {
		return {
			measurements: [],
			staticViolations: [`performance evidence file is absent or invalid: ${path}`],
		};
	}
}

export interface PerformanceEvidenceMapping {
	input: PerformanceInput;
	violations: readonly string[];
}

function performanceInputFailure(message: string): PerformanceEvidenceMapping {
	return { input: { measurements: [], staticViolations: [message] }, violations: [message] };
}

/**
 * Reconstruct performance input from a validated evidence bundle. Only a `pass`
 * PERF-01 assertion carrying a JSON performance input is accepted; a missing,
 * blocked, failed or malformed assertion becomes a static violation so the gate
 * stays fail-closed rather than trusting a status.
 */
export function performanceInputFromEvidence(evidence: EvidenceBundle): PerformanceEvidenceMapping {
	const assertion = findAssertion(evidence, "GATE", PERFORMANCE_ASSERTION_ID);
	if (assertion === undefined) {
		return performanceInputFailure(`evidence ${PERFORMANCE_ASSERTION_ID}: assertion is missing`);
	}
	if (assertion.status !== "pass") {
		return performanceInputFailure(
			`evidence ${PERFORMANCE_ASSERTION_ID}: assertion is ${assertion.status} (${assertion.detail})`,
		);
	}
	let value: unknown;
	try {
		value = JSON.parse(assertion.detail);
	} catch {
		return performanceInputFailure(
			`evidence ${PERFORMANCE_ASSERTION_ID}: detail is not valid JSON`,
		);
	}
	if (value === null || typeof value !== "object" || Array.isArray(value)) {
		return performanceInputFailure(
			`evidence ${PERFORMANCE_ASSERTION_ID}: detail must be a performance input object`,
		);
	}
	return { input: value as PerformanceInput, violations: [] };
}

export function formatPerformanceReport(report: PerformanceReport): string {
	const label = report.ok ? "OK" : "FAILED";
	return [
		`check-performance: ${label} (${report.facts.measuredTargets.length}/${report.facts.targets.length} process targets)`,
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
	].join("\n");
}

export async function runPerformanceCheck(
	inputPath = resolve(import.meta.dir, "../performance-evidence.json"),
	evidencePath?: string,
): Promise<number> {
	let input: PerformanceInput;
	if (evidencePath !== undefined) {
		let evidence: EvidenceBundle;
		try {
			evidence = await readEvidenceBundle(evidencePath);
		} catch (error) {
			const message = error instanceof Error ? error.message : String(error);
			console.error(`check-performance: FAILED\n  - evidence: ${message}`);
			return 1;
		}
		input = performanceInputFromEvidence(evidence).input;
	} else {
		input = await collectPerformanceInput(inputPath);
	}
	const report = inspectPerformance(input);
	const output = formatPerformanceReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

export const PERFORMANCE_USAGE = [
	"usage: bun scripts/check-performance.ts [<performance-input.json>] [--input <json>] [--evidence <bundle.json>]",
	"",
	"Without a flag this command keeps its current fail-closed behavior: it reads",
	"package/performance-evidence.json and reports every absent target.",
	"",
	"--input (or the legacy positional path) consumes the raw four-target",
	"performance input; --evidence consumes a schema-versioned bundle produced by",
	"collect-evidence.ts. A missing, malformed or non-passing assertion fails closed.",
].join("\n");

interface PerformanceCliOptions {
	inputPath?: string;
	evidencePath?: string;
}

function parsePerformanceArgs(args: readonly string[]): PerformanceCliOptions {
	let inputPath: string | undefined;
	let evidencePath: string | undefined;
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index];
		if (argument === "--help" || argument === "-h") {
			console.log(PERFORMANCE_USAGE);
			process.exit(0);
		}
		const separator = argument?.indexOf("=") ?? -1;
		if (argument === "--input" || argument === "--evidence") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "")
				throw new Error(`${argument} requires a JSON path`);
			if (argument === "--evidence") evidencePath = value;
			else inputPath = value;
			index += 1;
			continue;
		}
		if (
			separator > 2 &&
			(argument?.startsWith("--input=") || argument?.startsWith("--evidence="))
		) {
			const value = argument.slice(separator + 1).trim();
			if (value === "") throw new Error(`${argument.slice(0, separator)} requires a JSON path`);
			if (argument.startsWith("--evidence=")) evidencePath = value;
			else inputPath = value;
			continue;
		}
		if (argument?.startsWith("--")) throw new Error(`unknown argument ${argument}`);
		if (argument !== undefined && argument.trim() !== "") inputPath = argument;
	}
	return {
		...(inputPath === undefined ? {} : { inputPath }),
		...(evidencePath === undefined ? {} : { evidencePath }),
	};
}

if (import.meta.main) {
	try {
		const options = parsePerformanceArgs(Bun.argv.slice(2));
		const defaultPath = resolve(import.meta.dir, "../performance-evidence.json");
		process.exit(await runPerformanceCheck(options.inputPath ?? defaultPath, options.evidencePath));
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`check-performance: ${message}`);
		console.error(PERFORMANCE_USAGE);
		process.exit(2);
	}
}
