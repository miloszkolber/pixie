import { expect, test } from "bun:test";
import { productArchiveLayout } from "../../scripts/build-release.ts";
import type { PackageArchiveEvidence } from "../../scripts/check-package-artifacts.ts";
import type { ArchiveEvidence, BinaryEvidence } from "../../scripts/check-release-identity.ts";
import {
	formatReleaseGateReport,
	inspectReleaseGate,
	type ReleaseGateInput,
} from "../../scripts/release-gate.ts";

const sourceCommit = "71590cac48925b31b9d5d3c7d1746ee94b351772";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const repositoryOwner = "release-test-owner";
const hash = "a".repeat(64);
const amd64Digest = `sha256:${"b".repeat(64)}`;
const arm64Digest = `sha256:${"c".repeat(64)}`;

const releaseWorkflow = `
name: commit release
on:
  pull_request:
  schedule:
    - cron: "37 4 * * 0"
  workflow_dispatch:
  push:
    branches: [main]
jobs:
  validate:
    run: SOURCE_COMMIT=$(git rev-parse --verify 'HEAD^{commit}')
    run: validate SOURCE_COMMIT as 40 lowercase characters; RELEASE_ID=sha-\${SOURCE_COMMIT:0:12}
  publish:
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    run: stage pixie_web-sha-71590cac4892-linux-amd64.tar.gz pixie_web-sha-71590cac4892-linux-arm64.tar.gz pixie-sha-71590cac4892-linux-amd64.tar.gz pixie-sha-71590cac4892-linux-arm64.tar.gz checksums.txt release-manifest.json SBOM and provenance
    run: reject collision; retry partial publication with same payload; never move latest backward
    run: docker buildx build --label org.opencontainers.image.version=\${RELEASE_ID} --label org.opencontainers.image.revision=\${SOURCE_COMMIT} --sbom=true --attest type=provenance --push
`;

const products = ["pixie_web", "pixie"] as const;

function archive(
	variant: (typeof products)[number],
	architecture: "amd64" | "arm64",
): ArchiveEvidence {
	const binary = variant;
	return {
		name: `${binary}-${releaseId}-linux-${architecture}.tar.gz`,
		sourceCommit,
		releaseId,
		sha256: hash,
		binaryName: binary,
	};
}

function binary(
	variant: (typeof products)[number],
	architecture: "amd64" | "arm64",
): BinaryEvidence {
	return {
		name: variant,
		variant,
		architecture,
		sourceCommit,
		releaseId,
		sha256: hash,
	};
}

function packageArchive(
	product: (typeof products)[number],
	architecture: "amd64" | "arm64",
): PackageArchiveEvidence {
	const runtime =
		product === "pixie"
			? [
					"runtime/manifest.json",
					"runtime/bin/bun",
					"runtime/bun/LICENSE.md",
					"runtime/node_modules/@earendil-works/pi-coding-agent/package.json",
					"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/cli.js",
					"runtime/node_modules/@earendil-works/pi-coding-agent/dist/bun/chunks/tui.js",
				]
			: [];
	return {
		name: `${product}-${releaseId}-linux-${architecture}.tar.gz`,
		entries: productArchiveLayout(product, runtime),
	};
}

function validCandidate(): ReleaseGateInput {
	const archives = products.flatMap((product) => [
		archive(product, "amd64"),
		archive(product, "arm64"),
	]);
	const packageCommand =
		"--version doctor /readyz signal.NotifyContext serveController Shutdown uninstall";
	return {
		identity: {
			sourceCommit,
			releaseId,
			repositoryOwner,
			tag: { name: releaseId, target: sourceCommit },
			release: { title: releaseId, tag: releaseId, target: sourceCommit },
			archives,
			binaries: products.flatMap((product) => [binary(product, "amd64"), binary(product, "arm64")]),
			docker: {
				tag: `ghcr.io/${repositoryOwner}/pixie_web:${releaseId}`,
				version: releaseId,
				revision: sourceCommit,
				indexDigest: amd64Digest,
				platformDigests: { amd64: amd64Digest, arm64: arm64Digest },
				labels: {
					"org.opencontainers.image.version": releaseId,
					"org.opencontainers.image.revision": sourceCommit,
				},
				provenance: true,
			},
			manifest: {
				releaseId,
				sourceCommit,
				cleanSourceTree: true,
				completeSet: true,
				archiveHashes: Object.fromEntries(archives.map(({ name }) => [name, hash])),
				imageIndexDigest: amd64Digest,
				platformDigests: { amd64: amd64Digest, arm64: arm64Digest },
				checksumsPresent: true,
				sbomPresent: true,
				provenancePresent: true,
			},
			workflowSources: { ".github/workflows/release.yml": releaseWorkflow },
		},
		packages: {
			releaseId,
			archives: products.flatMap((product) => [
				packageArchive(product, "amd64"),
				packageArchive(product, "arm64"),
			]),
			commandSources: { "main.go": packageCommand },
			webuiSources: { "webui.go": "//go:embed all:dist" },
			embeddedUiFiles: ["dist/index.html"],
			facadeSources: {
				"src/assistant/serve.ts": "startBunHostFromVerifiedPi(await verifyPiPackage(piPackage))",
			},
			binaries: products.flatMap((product) =>
				(["amd64", "arm64"] as const).map((architecture) => ({
					product,
					architecture,
					path: product,
				})),
			),
		},
		policy: {
			workflowSources: { ".github/workflows/release.yml": releaseWorkflow },
			automaticMainAuthorized: true,
			sourceReachableFromMain: true,
			docsOnlyValidateOnly: true,
		},
		latest: {
			candidateSourceCommit: sourceCommit,
			comparison: "source-ancestry",
			completeSetVerified: true,
			promoted: false,
		},
	};
}

test("valid candidate proves identity, package matrix, labels, provenance and safe policy", () => {
	const report = inspectReleaseGate(validCandidate());

	expect(report.ok).toBe(true);
	expect(report.facts.releaseId).toBe(releaseId);
	expect(report.facts.identity.requiredArchives).toHaveLength(4);
	expect(report.facts.policy.validateOnlyTriggers).toEqual([
		"pull_request",
		"schedule",
		"workflow_dispatch",
	]);
});

test("legacy pixie controller image identity fails the release gate", () => {
	const candidate = validCandidate();
	if (candidate.identity.docker === undefined)
		throw new Error("fixture Docker evidence is missing");
	const report = inspectReleaseGate({
		...candidate,
		identity: {
			...candidate.identity,
			docker: {
				...candidate.identity.docker,
				tag: `ghcr.io/${repositoryOwner}/pixie:${releaseId}`,
			},
		},
	});

	expect(report.ok).toBe(false);
	expect(report.violations).toContain(
		`Docker tag must be ghcr.io/${repositoryOwner}/pixie_web:${releaseId}`,
	);
});

test("invalid candidate exposes missing live inputs and unsafe publication decisions", () => {
	const candidate = validCandidate();
	if (candidate.identity.docker === undefined)
		throw new Error("fixture Docker evidence is missing");
	const archives = candidate.identity.archives ?? [];
	const binaries = candidate.packages.binaries ?? [];
	const report = inspectReleaseGate({
		...candidate,
		identity: {
			...candidate.identity,
			archives: archives.slice(0, 3),
			docker: {
				...candidate.identity.docker,
				labels: { "org.opencontainers.image.version": "v1.2.3" },
				provenance: false,
			},
		},
		packages: { ...candidate.packages, binaries: binaries.slice(0, 1) },
		policy: {
			workflowSources: {
				".github/workflows/release.yml": releaseWorkflow.replace(
					"if: github.event_name == 'push' && github.ref == 'refs/heads/main'",
					"run: publish from any trigger",
				),
			},
			changedPaths: ["docs/builds/release-checklist.md"],
			automaticMainAuthorized: false,
			sourceReachableFromMain: false,
		},
		latest: {
			currentSourceCommit: "1".repeat(40),
			candidateSourceCommit: sourceCommit,
			candidateDescendsFromCurrent: false,
			comparison: "lexicographic-hash",
			completeSetVerified: false,
			promoted: true,
		},
	});

	expect(report.ok).toBe(false);
	const output = formatReleaseGateReport(report);
	expect(output).toMatch(/missing live input/);
	expect(output).toMatch(/archive|binary|provenance|OCI/);
	expect(output).toMatch(/automatic main|validate-only|latest|hash ordering/);
});
