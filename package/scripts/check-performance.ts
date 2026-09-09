#!/usr/bin/env bun

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

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

export function formatPerformanceReport(report: PerformanceReport): string {
	const label = report.ok ? "OK" : "FAILED";
	return [
		`check-performance: ${label} (${report.facts.measuredTargets.length}/${report.facts.targets.length} process targets)`,
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
	].join("\n");
}

export async function runPerformanceCheck(
	path = resolve(import.meta.dir, "../performance-evidence.json"),
): Promise<number> {
	const report = inspectPerformance(await collectPerformanceInput(path));
	const output = formatPerformanceReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main)
	process.exit(
		await runPerformanceCheck(
			process.argv[2] ?? resolve(import.meta.dir, "../performance-evidence.json"),
		),
	);
