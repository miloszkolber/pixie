import { expect, test } from "bun:test";
import type { PackageArchiveEvidence } from "../../scripts/check-package-artifacts.ts";
import type { ArchiveEvidence, BinaryEvidence } from "../../scripts/check-release-identity.ts";
import {
	formatReleaseGateReport,
	inspectReleaseGate,
	type ReleaseGateInput,
} from "../../scripts/release-gate.ts";

const sourceCommit = "71590cac48925b31b9d5d3c7d1746ee94b351772";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
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
    run: stage pixie-assistant-sha-71590cac4892-linux-amd64.tar.gz pixie-assistant-sha-71590cac4892-linux-arm64.tar.gz pixie-sha-71590cac4892-linux-amd64.tar.gz pixie-sha-71590cac4892-linux-arm64.tar.gz checksums.txt release-manifest.json SBOM and provenance
    run: reject collision; retry partial publication with same payload; never move latest backward
    run: docker buildx build --label org.opencontainers.image.version=\${RELEASE_ID} --label org.opencontainers.image.revision=\${SOURCE_COMMIT} --sbom=true --attest type=provenance --push
`;

const assistantUnit = `[Unit]
StartLimitIntervalSec=60
StartLimitBurst=5
[Service]
Type=exec
ExecStart=%h/.local/bin/pixie-assistant serve --config %h/.config/pixie/assistant.json
Restart=on-failure
RestartSec=2
RestartForceExitStatus=75
TimeoutStopSec=30
KillMode=mixed
UMask=0077
StandardOutput=journal
StandardError=journal
[Install]
WantedBy=default.target
`;

const hostUnit = assistantUnit
	.replaceAll("pixie-assistant", "pixie")
	.replace("assistant.json", "pixie.json");

function archive(variant: "assistant" | "host", architecture: "amd64" | "arm64"): ArchiveEvidence {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	return {
		name: `${binary}-${releaseId}-linux-${architecture}.tar.gz`,
		sourceCommit,
		releaseId,
		sha256: hash,
		binaryName: binary,
	};
}

function binary(variant: "assistant" | "host", architecture: "amd64" | "arm64"): BinaryEvidence {
	return {
		name: variant === "assistant" ? "pixie-assistant" : "pixie",
		variant,
		architecture,
		sourceCommit,
		releaseId,
		sha256: hash,
	};
}

function packageArchive(
	variant: "assistant" | "host",
	architecture: "amd64" | "arm64",
): PackageArchiveEvidence {
	const binaryName = variant === "assistant" ? "pixie-assistant" : "pixie";
	return {
		name: `${binaryName}-${releaseId}-linux-${architecture}.tar.gz`,
		entries: [
			binaryName,
			`${binaryName}.service`,
			variant === "assistant" ? "assistant.json" : "pixie.json",
			"INSTALL.md",
			"LICENSE",
			"NOTICE.md",
		],
	};
}

function validCandidate(): ReleaseGateInput {
	const archives = [
		archive("assistant", "amd64"),
		archive("assistant", "arm64"),
		archive("host", "amd64"),
		archive("host", "arm64"),
	];
	const packageCommand =
		"--version doctor /readyz signal.NotifyContext serveController Shutdown uninstall";
	return {
		identity: {
			sourceCommit,
			releaseId,
			tag: { name: releaseId, target: sourceCommit },
			release: { title: releaseId, tag: releaseId, target: sourceCommit },
			archives,
			binaries: [
				binary("assistant", "amd64"),
				binary("assistant", "arm64"),
				binary("host", "amd64"),
				binary("host", "arm64"),
			],
			docker: {
				tag: `ghcr.io/miloszkolber/pixie:${releaseId}`,
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
			archives: [
				packageArchive("assistant", "amd64"),
				packageArchive("assistant", "arm64"),
				packageArchive("host", "amd64"),
				packageArchive("host", "arm64"),
			],
			units: {
				"pixie-assistant.service": assistantUnit,
				"pixie.service": hostUnit,
			},
			configs: {
				"assistant.json": '{"host":"127.0.0.1"}',
				"pixie.json": '{"host":"127.0.0.1","mode":"full-host"}',
			},
			commandSources: { "main.go": packageCommand },
			webuiSources: { "webui.go": "//go:embed all:dist" },
			embeddedUiFiles: ["dist/index.html"],
			facadeSources: { "host.go": "func Start() {}" },
			binaries: [
				...(["assistant", "host"] as const).flatMap((variant) =>
					(["amd64", "arm64"] as const).map((architecture) => ({
						variant,
						architecture,
						path: `/release/${variant === "assistant" ? "pixie-assistant" : "pixie"}`,
						version: releaseId,
						doctor: true,
						readiness: true,
						lifecycle: true,
						uninstall: true,
						...(variant === "host" ? { uiEmbedded: true } : {}),
					})),
				),
			],
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
