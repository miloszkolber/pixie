#!/usr/bin/env bun

import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
	buildArtifacts,
	defaultPackageDir,
	loadProtocolCatalog,
	SCHEMA_RELATIVE_PATH,
	writeArtifacts,
} from "./generate-contracts.ts";

export interface ArtifactDrift {
	path: string;
	reason: "missing" | "content";
	expectedLines: number;
	actualLines: number;
	diff: string;
}

export interface ContractCheckReport {
	ok: boolean;
	schemaPath: string;
	artifacts: string[];
	drift: ArtifactDrift[];
}

function summarizeDiff(expected: string, actual: string, maxDifferences = 20): string {
	const expectedLines = expected === "" ? [] : expected.split("\n");
	const actualLines = actual === "" ? [] : actual.split("\n");
	const total = Math.max(expectedLines.length, actualLines.length);
	const lines: string[] = [];
	let shown = 0;
	for (let index = 0; index < total && shown < maxDifferences; index += 1) {
		const left = expectedLines[index];
		const right = actualLines[index];
		if (left === right) continue;
		lines.push(`    line ${index + 1}`);
		if (left !== undefined) lines.push(`      - ${left}`);
		if (right !== undefined) lines.push(`      + ${right}`);
		shown += 1;
	}
	if (shown >= maxDifferences) lines.push("    ... diff truncated");
	return lines.join("\n");
}

/** Compares freshly generated artifacts against the committed copies under committedDir. */
export function compareArtifacts(
	artifacts: ReturnType<typeof buildArtifacts>,
	committedDir: string,
): ArtifactDrift[] {
	const drift: ArtifactDrift[] = [];
	for (const artifact of artifacts) {
		let actual: string;
		try {
			actual = readFileSync(resolve(committedDir, artifact.path), "utf8");
		} catch {
			drift.push({
				path: artifact.path,
				reason: "missing",
				expectedLines: artifact.content.split("\n").length,
				actualLines: 0,
				diff: summarizeDiff(artifact.content, ""),
			});
			continue;
		}
		if (actual !== artifact.content) {
			drift.push({
				path: artifact.path,
				reason: "content",
				expectedLines: artifact.content.split("\n").length,
				actualLines: actual.split("\n").length,
				diff: summarizeDiff(artifact.content, actual),
			});
		}
	}
	return drift;
}

export function formatContractCheckReport(report: ContractCheckReport): string {
	if (report.ok) {
		return `check-contracts: OK (${report.artifacts.length} generated artifacts match ${report.schemaPath})`;
	}
	const lines = [`check-contracts: DRIFT DETECTED (${report.drift.length} artifact(s))`];
	for (const item of report.drift) {
		lines.push(
			`  ${item.path}: ${item.reason} (expected ${item.expectedLines} lines, actual ${item.actualLines})`,
		);
		if (item.diff !== "") lines.push(item.diff);
	}
	lines.push("Run `bun run generate:contracts` and commit the regenerated artifacts.");
	return lines.join("\n");
}

export function checkContracts(
	options: { packageDir?: string; committedDir?: string } = {},
): ContractCheckReport {
	const packageDir = options.packageDir ?? defaultPackageDir();
	const committedDir = options.committedDir ?? packageDir;
	const schemaPath = resolve(packageDir, SCHEMA_RELATIVE_PATH);
	const artifacts = buildArtifacts(loadProtocolCatalog(schemaPath));
	const drift = compareArtifacts(artifacts, committedDir);
	return {
		ok: drift.length === 0,
		schemaPath,
		artifacts: artifacts.map((artifact) => artifact.path),
		drift,
	};
}

/**
 * Regenerates every artifact into a task-owned temporary directory and compares
 * it with the committed copy. Exit status is non-zero when any file drifts.
 */
export function runContractCheck(
	options: { packageDir?: string; committedDir?: string } = {},
): number {
	const packageDir = options.packageDir ?? defaultPackageDir();
	const committedDir = options.committedDir ?? packageDir;
	const schemaPath = resolve(packageDir, SCHEMA_RELATIVE_PATH);
	const artifacts = buildArtifacts(loadProtocolCatalog(schemaPath));
	const staging = mkdtempSync(join(tmpdir(), "pixie-contracts-"));
	try {
		writeArtifacts(staging, artifacts);
		const drift = compareArtifacts(artifacts, committedDir);
		const report: ContractCheckReport = {
			ok: drift.length === 0,
			schemaPath,
			artifacts: artifacts.map((artifact) => artifact.path),
			drift,
		};
		const output = formatContractCheckReport(report);
		if (report.ok) console.log(output);
		else console.error(output);
		return report.ok ? 0 : 1;
	} finally {
		rmSync(staging, { recursive: true, force: true });
	}
}

if (import.meta.main) process.exit(runContractCheck());
