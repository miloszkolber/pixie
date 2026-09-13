import { expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	ACCEPTANCE_IDS,
	coverageInputFromEvidence,
	inspectCoverage,
	MANDATORY_FEATURE_IDS,
} from "../../scripts/check-coverage.ts";
import {
	inspectPerformance,
	performanceInputFromEvidence,
} from "../../scripts/check-performance.ts";
import { type CollectEvidenceOptions, collectEvidence } from "../../scripts/collect-evidence.ts";
import {
	COVERAGE_ASSERTION_ID,
	type EvidenceBundle,
	PACKAGED_BINARY_ASSERTION_PREFIX,
	PERFORMANCE_ASSERTION_ID,
} from "../../scripts/evidence-bundle.ts";

const sourceCommit = "0123456789abcdef0123456789abcdef01234567";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const generatedAt = "2026-01-02T03:04:05.000Z";

function assertion(
	bundle: EvidenceBundle,
	id: string,
): EvidenceBundle["assertions"][number] | undefined {
	return bundle.assertions.find((candidate) => candidate.id === id);
}

async function collect(
	root: string,
	extra: Partial<CollectEvidenceOptions>,
): Promise<EvidenceBundle> {
	return collectEvidence({
		artifactsDir: join(root, "absent-artifacts"),
		imagePath: join(root, "absent-image.tar"),
		sourceCommit,
		releaseId,
		generatedAt,
		...extra,
	});
}

function passingCoverageInput(): Record<string, unknown> {
	const kinds = ["native", "bridge", "ui", "artifact"] as const;
	const ids = [...MANDATORY_FEATURE_IDS, ...ACCEPTANCE_IDS];
	return {
		evidence: ids.map((id, index) => ({
			id,
			kind: kinds[index % kinds.length] ?? "native",
			source: `fixture/${id.toLowerCase()}.json`,
			test: `${id.toLowerCase()}-live`,
			actual: true,
			live: true,
			profile: index % 2 === 0 ? "vanilla" : "combined",
		})),
	};
}

function performanceMeasurement(
	architecture: "amd64" | "arm64",
	variant: "assistant" | "full-host",
) {
	return {
		architecture,
		variant,
		profile: "fresh-process-content-filled",
		artifact: `sha256:${"b".repeat(64)}`,
		sourceCommit,
		sampleDurationsMs: [100, 120, 140, 160, 180],
		p50Ms: 140,
		p95Ms: 180,
		peakProcessRssBytes: 80 << 20,
		decodedMemoryBytes: 24 << 20,
		bufferMemoryBytes: 8 << 20,
		workerMemoryBytes: 512 << 20,
		workerPids: 64,
		workerCpuQuotaMillis: 20_000,
		workerWallTimeMs: 30_000,
		workerOutputBytes: 32 << 20,
		scratchBytes: 128 << 20,
		scratchInodes: 8_192,
		fullProcessTree: true,
		contentFilledUI: true,
		live: true,
	};
}

function passingPerformanceInput(): Record<string, unknown> {
	return {
		measurements: [
			performanceMeasurement("amd64", "assistant"),
			performanceMeasurement("arm64", "assistant"),
			performanceMeasurement("amd64", "full-host"),
			performanceMeasurement("arm64", "full-host"),
		],
	};
}

async function writeExecutable(path: string, script: string): Promise<void> {
	await writeFile(path, script, { mode: 0o755 });
}

const succeedingBinary = `#!/bin/sh
case "$1" in
  --version) echo "pixie ${releaseId} (revision ${sourceCommit})"; exit 0 ;;
  doctor) echo "pixie doctor: configuration is readable ()"; exit 0 ;;
  *) echo "unknown command \\"$1\\"; use serve" >&2; exit 2 ;;
esac
`;

const failingBinary = `#!/bin/sh
case "$1" in
  --version) echo "version probe failed" >&2; exit 1 ;;
  doctor) echo "doctor probe failed" >&2; exit 1 ;;
  *) echo "unknown command" >&2; exit 2 ;;
esac
`;

const unsupportedDoctorBinary = `#!/bin/sh
case "$1" in
  --version) echo "pixie ${releaseId} (revision ${sourceCommit})"; exit 0 ;;
  *) echo "unknown command \\"$1\\"; use serve" >&2; exit 2 ;;
esac
`;

test("coverage producer emits blocked on absent input, pass on satisfied input and fail on incomplete input", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-coverage-producer-"));
	try {
		const absent = await collect(root, {});
		expect(assertion(absent, COVERAGE_ASSERTION_ID)?.status).toBe("blocked");

		const passingPath = join(root, "coverage.json");
		await writeFile(passingPath, JSON.stringify(passingCoverageInput()));
		const passing = await collect(root, { coveragePath: passingPath });
		expect(assertion(passing, COVERAGE_ASSERTION_ID)?.status).toBe("pass");
		const mapped = coverageInputFromEvidence(passing);
		expect(mapped.violations).toEqual([]);
		expect(inspectCoverage(mapped.input).ok).toBe(true);

		const incompletePath = join(root, "incomplete-coverage.json");
		await writeFile(incompletePath, JSON.stringify({ evidence: [] }));
		const incomplete = await collect(root, { coveragePath: incompletePath });
		expect(assertion(incomplete, COVERAGE_ASSERTION_ID)?.status).toBe("fail");

		const malformedPath = join(root, "malformed-coverage.json");
		await writeFile(malformedPath, "{not json");
		const malformed = await collect(root, { coveragePath: malformedPath });
		expect(assertion(malformed, COVERAGE_ASSERTION_ID)?.status).toBe("fail");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("performance producer emits blocked on absent input, pass on four targets and fail on an incomplete set", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-performance-producer-"));
	try {
		const absent = await collect(root, {});
		expect(assertion(absent, PERFORMANCE_ASSERTION_ID)?.status).toBe("blocked");

		const passingPath = join(root, "performance.json");
		await writeFile(passingPath, JSON.stringify(passingPerformanceInput()));
		const passing = await collect(root, { performancePath: passingPath });
		expect(assertion(passing, PERFORMANCE_ASSERTION_ID)?.status).toBe("pass");
		const mapped = performanceInputFromEvidence(passing);
		expect(mapped.violations).toEqual([]);
		expect(inspectPerformance(mapped.input).ok).toBe(true);

		const incompletePath = join(root, "incomplete-performance.json");
		await writeFile(
			incompletePath,
			JSON.stringify({ measurements: [performanceMeasurement("amd64", "assistant")] }),
		);
		const incomplete = await collect(root, { performancePath: incompletePath });
		expect(assertion(incomplete, PERFORMANCE_ASSERTION_ID)?.status).toBe("fail");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("binary probes execute real commands and only pass when they actually succeed", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-binary-probe-"));
	try {
		const binary = join(root, "pixie");
		await writeExecutable(binary, succeedingBinary);
		const bundle = await collect(root, { binaryPaths: [binary] });

		const version = assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-version`);
		expect(version?.status).toBe("pass");
		expect(JSON.parse(version?.detail ?? "{}")).toMatchObject({ exitCode: 0 });
		expect(JSON.parse(version?.detail ?? "{}").stdout).toContain(releaseId);

		const doctor = assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-doctor`);
		expect(doctor?.status).toBe("pass");

		// Absent --base-url keeps readiness explicitly blocked, never pass.
		const readiness = assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-readiness`);
		expect(readiness?.status).toBe("blocked");
		expect(JSON.parse(readiness?.detail ?? "{}").url).toBeNull();
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("binary probes mark a failing probe fail and an unsupported doctor blocked", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-binary-fail-"));
	try {
		const failing = join(root, "pixie-failing");
		await writeExecutable(failing, failingBinary);
		const failingBundle = await collect(root, { binaryPaths: [failing] });
		expect(assertion(failingBundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-version`)?.status).toBe(
			"fail",
		);
		expect(assertion(failingBundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-doctor`)?.status).toBe(
			"fail",
		);

		const unsupported = join(root, "pixie-unsupported");
		await writeExecutable(unsupported, unsupportedDoctorBinary);
		const unsupportedBundle = await collect(root, { binaryPaths: [unsupported] });
		expect(
			assertion(unsupportedBundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-version`)?.status,
		).toBe("pass");
		expect(
			assertion(unsupportedBundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-doctor`)?.status,
		).toBe("blocked");
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("readiness probe reflects a real HTTP response and a real non-2xx failure", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-readiness-probe-"));
	try {
		const binary = join(root, "pixie");
		await writeExecutable(binary, succeedingBinary);

		const ready = Bun.serve({ port: 0, fetch: () => new Response(null, { status: 204 }) });
		try {
			const bundle = await collect(root, {
				binaryPaths: [binary],
				baseUrl: `http://127.0.0.1:${ready.port}`,
			});
			const readiness = assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-readiness`);
			expect(readiness?.status).toBe("pass");
			expect(JSON.parse(readiness?.detail ?? "{}")).toMatchObject({ httpStatus: 204 });
		} finally {
			ready.stop(true);
		}

		const degraded = Bun.serve({
			port: 0,
			fetch: () => new Response("degraded", { status: 503 }),
		});
		try {
			const bundle = await collect(root, {
				binaryPaths: [binary],
				baseUrl: `http://127.0.0.1:${degraded.port}/readyz`,
			});
			const readiness = assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-0-readiness`);
			expect(readiness?.status).toBe("fail");
			expect(JSON.parse(readiness?.detail ?? "{}")).toMatchObject({ httpStatus: 503 });
		} finally {
			degraded.stop(true);
		}
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("collect-evidence records a blocked marker when no packaged binary is provided", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-binary-absent-"));
	try {
		const bundle = await collect(root, {});
		expect(assertion(bundle, `${PACKAGED_BINARY_ASSERTION_PREFIX}-UNAVAILABLE`)?.status).toBe(
			"blocked",
		);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});
