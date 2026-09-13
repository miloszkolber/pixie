#!/usr/bin/env bun

import { readFile, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import type { PerformanceInput, PerformanceMeasurement } from "./check-performance.ts";

/**
 * Merge one native runner's performance input into another.
 *
 * The release workflow runs `produce-evidence-inputs.ts` once per native
 * architecture, so neither output alone carries all four required
 * `variant/architecture` targets. This helper concatenates the measurements and
 * keeps the `--into` input's reductions so the downstream gate sees an honest
 * four-target record without inventing a measurement for a non-native binary.
 */

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function targetKey(measurement: PerformanceMeasurement): string {
	const { variant, architecture } = measurement;
	if (typeof variant !== "string" || variant.trim() === "") {
		throw new Error("performance measurement is missing a variant");
	}
	if (typeof architecture !== "string" || architecture.trim() === "") {
		throw new Error("performance measurement is missing an architecture");
	}
	return `${variant}/${architecture}`;
}

/** Recursively sort object keys so structurally equal measurements compare
 * equal regardless of parsed key order. */
function stableValue(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(stableValue);
	if (value !== null && typeof value === "object") {
		const sorted: Record<string, unknown> = {};
		for (const key of Object.keys(value as Record<string, unknown>).sort()) {
			sorted[key] = stableValue((value as Record<string, unknown>)[key]);
		}
		return sorted;
	}
	return value;
}

function sameMeasurement(left: PerformanceMeasurement, right: PerformanceMeasurement): boolean {
	return JSON.stringify(stableValue(left)) === JSON.stringify(stableValue(right));
}

function uniqueStrings(values: readonly string[]): string[] {
	return [...new Set(values.filter((value) => value.trim() !== ""))];
}

export function mergePerformance(into: PerformanceInput, from: PerformanceInput): PerformanceInput {
	const measurements: PerformanceMeasurement[] = [];
	const byTarget = new Map<string, PerformanceMeasurement>();
	for (const measurement of [...(into.measurements ?? []), ...(from.measurements ?? [])]) {
		const target = targetKey(measurement);
		const existing = byTarget.get(target);
		if (existing !== undefined) {
			if (!sameMeasurement(existing, measurement)) {
				throw new Error(`conflicting measurements for target ${target}`);
			}
			continue;
		}
		byTarget.set(target, measurement);
		measurements.push(measurement);
	}

	// The reductions describe the producing manifest, not the runner, so the
	// primary input is authoritative; the secondary is only a fallback.
	const fieldReductions = into.fieldReductions ?? from.fieldReductions ?? [];
	const staticViolations = uniqueStrings([
		...(into.staticViolations ?? []),
		...(from.staticViolations ?? []),
	]);
	const merged: PerformanceInput = {
		...into,
		measurements,
		fieldReductions,
	};
	if (staticViolations.length > 0) merged.staticViolations = staticViolations;
	return merged;
}

async function readPerformanceInput(path: string): Promise<PerformanceInput> {
	let text: string;
	try {
		text = await readFile(resolve(path), "utf8");
	} catch (error) {
		throw new Error(`cannot read ${path}: ${errorMessage(error)}`);
	}
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (error) {
		throw new Error(`${path} is not valid JSON: ${errorMessage(error)}`);
	}
	if (value === null || typeof value !== "object" || Array.isArray(value)) {
		throw new Error(`${path} must be a JSON object`);
	}
	return value as PerformanceInput;
}

export const MERGE_PERFORMANCE_USAGE = [
	"usage: bun scripts/merge-performance.ts --into <performance.json> --from <performance.json>",
	"",
	"Concatenates `measurements` from both inputs, deduplicating identical",
	"variant/architecture targets and failing on conflicting duplicates. The",
	"`--into` file keeps its fieldReductions and is rewritten with the merged set.",
].join("\n");

interface MergeCliOptions {
	intoPath: string;
	fromPath: string;
}

function parseMergeArgs(args: readonly string[]): MergeCliOptions {
	const values: Record<string, string> = {};
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index] ?? "";
		if (argument === "--help" || argument === "-h") {
			console.log(MERGE_PERFORMANCE_USAGE);
			process.exit(0);
		}
		if (argument === "--into" || argument === "--from") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "") {
				throw new Error(`${argument} requires a JSON path`);
			}
			values[argument.slice(2)] = value;
			index += 1;
			continue;
		}
		const separator = argument.indexOf("=");
		if (argument.startsWith("--") && separator > 2) {
			const value = argument.slice(separator + 1).trim();
			if (value === "") throw new Error(`${argument.slice(0, separator)} requires a JSON path`);
			values[argument.slice(2, separator)] = value;
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	if (values.into === undefined) throw new Error("--into <performance.json> is required");
	if (values.from === undefined) throw new Error("--from <performance.json> is required");
	return { intoPath: values.into, fromPath: values.from };
}

if (import.meta.main) {
	try {
		const options = parseMergeArgs(Bun.argv.slice(2));
		const into = await readPerformanceInput(options.intoPath);
		const from = await readPerformanceInput(options.fromPath);
		const merged = mergePerformance(into, from);
		await writeFile(resolve(options.intoPath), `${JSON.stringify(merged, null, 2)}\n`);
		console.log(
			`merge-performance: merged ${merged.measurements?.length ?? 0} measurements into ${options.intoPath}`,
		);
	} catch (error) {
		console.error(`merge-performance: ${errorMessage(error)}`);
		process.exitCode = 1;
	}
}
