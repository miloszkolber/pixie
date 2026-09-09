import { expect, test } from "bun:test";
import {
	type ArchiveEvidence,
	type BinaryEvidence,
	collectReleaseIdentityInput,
	formatReleaseIdentityReport,
	inspectReleaseIdentity,
	type ReleaseIdentityInput,
} from "../../scripts/check-release-identity.ts";

const sourceCommit = "71590cac48925b31b9d5d3c7d1746ee94b351772";
const releaseId = `sha-${sourceCommit.slice(0, 12)}`;
const archiveHash = "a".repeat(64);
const digest = `sha256:${"b".repeat(64)}`;

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
    run: test "$SOURCE_COMMIT" =~ "^[0-9a-f]{40}$"
    run: RELEASE_ID="sha-\${SOURCE_COMMIT:0:12}"
  release:
    if: github.event_name == 'push'
    run: stage pixie-assistant-${releaseId}-linux-amd64.tar.gz pixie-assistant-${releaseId}-linux-arm64.tar.gz pixie-${releaseId}-linux-amd64.tar.gz pixie-${releaseId}-linux-arm64.tar.gz checksums.txt release-manifest.json
    run: docker buildx build --label org.opencontainers.image.version=${releaseId} --label org.opencontainers.image.revision=$SOURCE_COMMIT --sbom=true --attest type=provenance
    run: reject collision when an existing tag has a different full commit
    run: retain partial publication for retry with the same payload; never move latest backward
    run: validate-only on pull requests, schedules and manual dispatch
`;

function archive(variant: "assistant" | "host", architecture: "amd64" | "arm64"): ArchiveEvidence {
	const binaryName = variant === "assistant" ? "pixie-assistant" : "pixie";
	return {
		name: `${binaryName}-${releaseId}-linux-${architecture}.tar.gz`,
		sourceCommit,
		releaseId,
		sha256: archiveHash,
		binaryName,
	};
}

function binary(variant: "assistant" | "host", architecture: "amd64" | "arm64"): BinaryEvidence {
	return {
		name: variant === "assistant" ? "pixie-assistant" : "pixie",
		variant,
		architecture,
		sourceCommit,
		releaseId,
		sha256: archiveHash,
	};
}

function passingInput(): ReleaseIdentityInput {
	const archiveHashes = Object.fromEntries(
		[
			archive("assistant", "amd64"),
			archive("assistant", "arm64"),
			archive("host", "amd64"),
			archive("host", "arm64"),
		].map((item) => [item.name, item.sha256]),
	);
	return {
		sourceCommit,
		releaseId,
		tag: { name: releaseId, target: sourceCommit },
		release: { title: releaseId, tag: releaseId, target: sourceCommit },
		archives: [
			archive("assistant", "amd64"),
			archive("assistant", "arm64"),
			archive("host", "amd64"),
			archive("host", "arm64"),
		],
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
			indexDigest: digest,
			platformDigests: { amd64: digest, arm64: `sha256:${"c".repeat(64)}` },
			labels: {
				"org.opencontainers.image.version": releaseId,
				"org.opencontainers.image.revision": sourceCommit,
			},
			provenance: true,
		},
		manifest: {
			path: "release-manifest.json",
			releaseId,
			sourceCommit,
			cleanSourceTree: true,
			completeSet: true,
			archiveHashes,
			imageIndexDigest: digest,
			platformDigests: { amd64: digest, arm64: `sha256:${"c".repeat(64)}` },
			checksumsPresent: true,
			sbomPresent: true,
			provenancePresent: true,
		},
		workflowSources: { ".github/workflows/release.yml": releaseWorkflow },
	};
}

test("one full source commit derives the same identity on every release surface", () => {
	const report = inspectReleaseIdentity(passingInput());

	expect(report.ok).toBe(true);
	expect(report.facts.expectedReleaseId).toBe(releaseId);
	expect(report.facts.observedArchives).toHaveLength(4);
	expect(report.facts.observedBinaries).toHaveLength(4);
});

test("release identity rejects short source references, semver names, and incomplete digests", () => {
	const input = passingInput();
	if (input.docker === undefined) throw new Error("passing fixture Docker evidence is missing");
	const report = inspectReleaseIdentity({
		...input,
		tag: { name: "v1.2.3", target: sourceCommit.slice(0, 12) },
		release: { title: "1.2.3", tag: "v1.2.3", target: sourceCommit.slice(0, 12) },
		archives: (input.archives ?? []).slice(0, 3),
		docker: {
			...input.docker,
			version: "0.0.0-dev",
			indexDigest: "sha256:short",
			platformDigests: { amd64: digest },
		},
		manifest: undefined,
	});

	expect(report.ok).toBe(false);
	const violations = report.violations.join("\n");
	expect(violations).toMatch(/full source commit|semver|release ID|missing artifact evidence/);
	expect(violations).toMatch(/OCI digest|release-manifest/);
});

test("partial publication retries retain identity and never promote latest", () => {
	const input = passingInput();
	const validPartial = inspectReleaseIdentity({
		...input,
		partialPublication: {
			releaseId,
			sourceCommit,
			publishedArtifacts: ["checksums.txt"],
			latestPromoted: false,
		},
		retry: { releaseId, sourceCommit, reusedPayload: true },
	});
	expect(validPartial.ok).toBe(true);

	const invalidPartial = inspectReleaseIdentity({
		...input,
		partialPublication: {
			releaseId: "sha-111111111111",
			sourceCommit: "1".repeat(40),
			publishedArtifacts: ["checksums.txt"],
			latestPromoted: true,
		},
		retry: { releaseId: "sha-111111111111", sourceCommit: "1".repeat(40), reusedPayload: false },
	});

	expect(invalidPartial.ok).toBe(false);
	const violations = invalidPartial.violations.join("\n");
	expect(violations).toMatch(/partial publication|retry/);
	expect(violations).toMatch(/latest|same release ID|same payload/);
});

test("publishing from a validation trigger requires an explicit validate-only guard", () => {
	const workflow = releaseWorkflow
		.replace("    if: github.event_name == 'push'\n", "")
		.replace("    run: stage", "    run: docker buildx build --push")
		.replace("    run: validate-only on pull requests, schedules and manual dispatch\n", "");
	const report = inspectReleaseIdentity({
		...passingInput(),
		workflowSources: { ".github/workflows/release.yml": workflow },
	});

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toContain("must be validate-only");
});

test("the manifest remains authoritative only when artifact and image digests agree", () => {
	const input = passingInput();
	if (input.manifest === undefined) throw new Error("passing fixture manifest is missing");
	const firstArchive = input.archives?.[0];
	if (firstArchive === undefined) throw new Error("passing fixture archive is missing");
	const report = inspectReleaseIdentity({
		...input,
		manifest: {
			...input.manifest,
			archiveHashes: {
				...input.manifest.archiveHashes,
				[firstArchive.name]: "d".repeat(64),
			},
		},
	});

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toContain("archive hash disagrees");
});

test("workflow counters and git-describe output cannot replace the source identity", () => {
	const report = inspectReleaseIdentity({
		...passingInput(),
		workflowSources: {
			".github/workflows/release.yml": `${releaseWorkflow}\nrun: echo $GITHUB_RUN_NUMBER\nrun: git describe --tags`,
		},
	});

	expect(report.ok).toBe(false);
	const violations = report.violations.join("\n");
	expect(violations).toContain("workflow counters");
	expect(violations).toContain("git-describe");
});

test("a colliding short ID is a hard failure even when the old record is otherwise complete", () => {
	const report = inspectReleaseIdentity({
		...passingInput(),
		existingRelease: { releaseId, sourceCommit: "1".repeat(40) },
	});

	expect(report.ok).toBe(false);
	expect(report.violations.join("\n")).toContain("release identity collision");
});

test("checked-in workflows and artifacts report missing release evidence without claiming completion", async () => {
	const report = inspectReleaseIdentity(await collectReleaseIdentityInput());
	const output = formatReleaseIdentityReport(report);

	expect(report.ok).toBe(false);
	expect(output).toContain("check-release-identity: FAILED");
	expect(output).toMatch(/missing workflow evidence/);
	expect(output).toMatch(/missing artifact evidence/);
	expect(output).toMatch(/semantic-version fallback|0\.0\.0-dev/);
	expect(output).toContain("stale legacy npm semantic-version guard remains");
	expect(report.facts.missingWorkflowEvidence).toContain("a commit-named release workflow");
	expect(report.facts.missingArtifactEvidence).toContain(
		"release-manifest.json with full source and digest evidence",
	);
});
