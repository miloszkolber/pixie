#!/usr/bin/env bun

import { readdir, readFile } from "node:fs/promises";
import { resolve } from "node:path";

export const SOURCE_COMMIT_PATTERN = /^[0-9a-f]{40}$/;
export const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
export const SHA256_PATTERN = /^[0-9a-f]{64}$/;
export const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;

export const RELEASE_ARCHIVES = [
	"pixie-assistant-{releaseId}-linux-amd64.tar.gz",
	"pixie-assistant-{releaseId}-linux-arm64.tar.gz",
	"pixie-{releaseId}-linux-amd64.tar.gz",
	"pixie-{releaseId}-linux-arm64.tar.gz",
] as const;

const RELEASE_VARIANTS = ["assistant", "host"] as const;
const RELEASE_ARCHITECTURES = ["amd64", "arm64"] as const;
type ReleaseVariant = (typeof RELEASE_VARIANTS)[number];
type ReleaseArchitecture = (typeof RELEASE_ARCHITECTURES)[number];

export interface TagEvidence {
	name: string;
	target: string;
}

export interface ReleaseEvidence {
	title: string;
	tag: string;
	target: string;
}

export interface ArchiveEvidence {
	name: string;
	sourceCommit: string;
	releaseId: string;
	sha256: string;
	binaryName: string;
}

export interface BinaryEvidence {
	name: string;
	variant: ReleaseVariant;
	architecture: ReleaseArchitecture;
	sourceCommit: string;
	releaseId: string;
	sha256: string;
}

export interface DockerEvidence {
	tag: string;
	version: string;
	revision: string;
	indexDigest: string;
	platformDigests: Readonly<Partial<Record<ReleaseArchitecture, string>>>;
	/** Explicit OCI labels when they are available from the image inspection. */
	labels?: Readonly<Record<string, string>>;
	/** Whether an in-toto/SLSA provenance attestation was attached to the image. */
	provenance?: boolean;
}

export interface ReleaseManifestEvidence {
	path?: string;
	releaseId: string;
	sourceCommit: string;
	cleanSourceTree: boolean;
	completeSet: boolean;
	archiveHashes: Readonly<Record<string, string>>;
	imageIndexDigest: string;
	platformDigests: Readonly<Partial<Record<ReleaseArchitecture, string>>>;
	checksumsPresent: boolean;
	sbomPresent: boolean;
	provenancePresent: boolean;
}

export interface ExistingReleaseEvidence {
	releaseId: string;
	sourceCommit: string;
}

export interface PartialPublicationEvidence {
	releaseId: string;
	sourceCommit: string;
	publishedArtifacts: readonly string[];
	latestPromoted?: boolean;
}

export interface RetryEvidence {
	releaseId: string;
	sourceCommit: string;
	reusedPayload: boolean;
}

export interface ReleaseIdentityInput {
	sourceCommit?: string;
	releaseId?: string;
	tag?: TagEvidence;
	release?: ReleaseEvidence;
	archives?: readonly ArchiveEvidence[];
	binaries?: readonly BinaryEvidence[];
	docker?: DockerEvidence;
	manifest?: ReleaseManifestEvidence;
	workflowSources?: Readonly<Record<string, string>>;
	existingRelease?: ExistingReleaseEvidence;
	existingReleases?: readonly ExistingReleaseEvidence[];
	partialPublication?: PartialPublicationEvidence;
	retry?: RetryEvidence;
}

export interface ReleaseIdentityFacts {
	expectedReleaseId: string | null;
	sourceCommit: string | null;
	requiredArchives: readonly string[];
	observedArchives: readonly string[];
	observedBinaries: readonly string[];
	workflowFiles: readonly string[];
	missingWorkflowEvidence: readonly string[];
	missingArtifactEvidence: readonly string[];
	missingLiveInputs: readonly string[];
}

export interface ReleaseIdentityReport {
	ok: boolean;
	violations: readonly string[];
	facts: ReleaseIdentityFacts;
}

function expectedArchiveNames(releaseId: string): string[] {
	return RELEASE_ARCHIVES.map((name) => name.replace("{releaseId}", releaseId));
}

function expectedBinaryKey(variant: ReleaseVariant, architecture: ReleaseArchitecture): string {
	return `${variant}/${architecture}`;
}

function addMissing(
	violations: string[],
	missing: string[],
	kind: "workflow" | "artifact",
	item: string,
): void {
	const message = `missing ${kind} evidence: ${item}`;
	missing.push(item);
	violations.push(message);
}

function checkFullSha(
	value: string | undefined,
	label: string,
	expected: string | undefined,
	violations: string[],
): void {
	if (value === undefined || value === "") {
		violations.push(`${label}: full source commit is required`);
		return;
	}
	if (!SOURCE_COMMIT_PATTERN.test(value)) {
		violations.push(`${label}: source commit must be exactly 40 lowercase hex characters`);
		return;
	}
	if (expected !== undefined && value !== expected) {
		violations.push(`${label}: source commit does not match the selected source commit`);
	}
}

function checkArtifactHash(value: string | undefined, label: string, violations: string[]): void {
	if (value === undefined || value === "") {
		violations.push(`${label}: authoritative SHA-256 hash is required`);
	} else if (!SHA256_PATTERN.test(value)) {
		violations.push(`${label}: authoritative SHA-256 hash must be 64 lowercase hex characters`);
	}
}

function checkDigest(value: string | undefined, label: string, violations: string[]): void {
	if (value === undefined || value === "") {
		violations.push(`${label}: authoritative OCI digest is required`);
	} else if (!DIGEST_PATTERN.test(value)) {
		violations.push(`${label}: OCI digest must be sha256 plus 64 lowercase hex characters`);
	}
}

function inspectForbiddenWorkflowFallbacks(
	path: string,
	source: string,
	violations: string[],
): void {
	const cleanSource = source.replace(/(^|\s)#.*$/gm, "$1");
	const checks: readonly [RegExp, string][] = [
		[/\bgit\s+describe\b/i, "git-describe output cannot be a release identity fallback"],
		[
			/\b(?:GITHUB_)?RUN_NUMBER\b|\brun_number\b|\bbuild[-_ ]?number\b/i,
			"workflow counters cannot be a release identity fallback",
		],
		[
			/\bsemver\b|\b0\.0\.0-dev\b|\b(?:version|tag)\s*=\s*[^\n]*\bv\*/i,
			"semantic-version fallback is not allowed",
		],
		[
			/GITHUB_REF_NAME#v|github\.ref_name[^\n]*(?:#v|v\*)/i,
			"ref-name version fallback is not allowed",
		],
		[/pixie-assistant-v\*|tags:\s*\[\s*["']v\*/i, "semantic-version tag naming is not allowed"],
		[
			/\b(?:sort|sorted)\b[^\n]*(?:hash|sha|release)/i,
			"hash ordering cannot determine release chronology",
		],
	];
	for (const [pattern, message] of checks) {
		if (pattern.test(cleanSource)) violations.push(`${path}: ${message}`);
	}
}

function workflowPublishes(source: string): boolean {
	const cleanSource = source.replace(/(^|\s)#.*$/gm, "$1");
	return /\bnpm\s+publish\b|docker\s+(?:buildx\s+)?(?:push|imagetools\s+create)\b|docker\s+buildx\s+build[^\n]*--push|\bpush:\s*\$\{\{[^}]*event_name\s*!=\s*["']pull_request/i.test(
		cleanSource,
	);
}

function hasValidateOnlyGuard(source: string): boolean {
	const cleanSource = source.replace(/(^|\s)#.*$/gm, "$1");
	return /validate[- ]only|validate[- ]and[- ]pack|dry[-_ ]?run|nothing will be published|event_name\s*==\s*["']push["']/i.test(
		cleanSource,
	);
}

function inspectWorkflowEvidence(
	sources: Readonly<Record<string, string>> | undefined,
	violations: string[],
	missing: string[],
): void {
	const entries = Object.entries(sources ?? {});
	if (entries.length === 0) {
		addMissing(violations, missing, "workflow", "a release workflow source");
		return;
	}

	const releaseEntries = entries.filter(
		([path, source]) =>
			/release/i.test(path) ||
			(/RELEASE_ID\s*=\s*["']?sha-/i.test(source) &&
				/archive|tar\.gz|release-manifest/i.test(source)),
	);
	if (releaseEntries.length === 0) {
		addMissing(violations, missing, "workflow", "a commit-named release workflow");
	}
	const releaseText = releaseEntries
		.map(([, source]) => source.replace(/(^|\s)#.*$/gm, "$1"))
		.join("\n");
	if (!/git\s+rev-parse\s+--verify[^\n]*HEAD\^\{commit\}/i.test(releaseText)) {
		addMissing(violations, missing, "workflow", "full source SHA lookup");
	}
	if (!/SOURCE_COMMIT[^\n]*(?:40|lowercase)/i.test(releaseText)) {
		addMissing(violations, missing, "workflow", "full source SHA validation");
	}
	if (!/RELEASE_ID\s*=\s*["']?sha-[^\n]*(?:SOURCE_COMMIT|cut\s+-c1-12)/i.test(releaseText)) {
		addMissing(violations, missing, "workflow", "full source SHA to RELEASE_ID derivation");
	}
	if (
		!/pixie-assistant[^\n]*linux-amd64/i.test(releaseText) ||
		!/pixie-assistant[^\n]*linux-arm64/i.test(releaseText) ||
		!/\bpixie-(?!assistant-)[^\n]*linux-amd64/i.test(releaseText) ||
		!/\bpixie-(?!assistant-)[^\n]*linux-arm64/i.test(releaseText)
	) {
		addMissing(violations, missing, "workflow", "complete four-archive staging");
	}
	if (!/collision|same.*full|existing.*tag|mismatch/i.test(releaseText)) {
		addMissing(violations, missing, "workflow", "short-ID collision handling");
	}
	if (!/partial|retry|resume|same.*payload|latest.*backward/i.test(releaseText)) {
		addMissing(
			violations,
			missing,
			"workflow",
			"partial publication retry and non-regressing latest checks",
		);
	}
	if (
		!/org\.opencontainers\.image\.version|oci[^\n]*version[^\n]*label|--label[^\n]*version/i.test(
			releaseText,
		)
	) {
		addMissing(violations, missing, "workflow", "OCI version label evidence");
	}
	if (
		!/org\.opencontainers\.image\.revision|oci[^\n]*revision[^\n]*label|--label[^\n]*revision/i.test(
			releaseText,
		)
	) {
		addMissing(violations, missing, "workflow", "OCI full source revision label evidence");
	}
	if (!/provenance|attest[^\n]*type\s*=\s*slsai?|sbom/i.test(releaseText)) {
		addMissing(violations, missing, "workflow", "SBOM and provenance attestation evidence");
	}

	const allTriggers = entries.map(([, source]) => source).join("\n");
	for (const trigger of ["pull_request", "schedule", "workflow_dispatch"] as const) {
		if (!new RegExp(`\\b${trigger}\\s*:`).test(allTriggers)) {
			addMissing(violations, missing, "workflow", `${trigger} validate-only path`);
		}
	}

	for (const [path, source] of entries) {
		inspectForbiddenWorkflowFallbacks(path, source, violations);
		if (
			/npm/i.test(path) &&
			/semver|0\.0\.0-dev|pixie-assistant-v\*/i.test(source.replace(/(^|\s)#.*$/gm, "$1"))
		) {
			violations.push(`${path}: stale legacy npm semantic-version guard remains`);
		}
		if (!workflowPublishes(source)) continue;
		const hasNonPublishingTrigger =
			/\bpull_request\s*:|\bschedule\s*:|\bworkflow_dispatch\s*:/i.test(source);
		if (hasNonPublishingTrigger && !hasValidateOnlyGuard(source)) {
			violations.push(`${path}: pull request, schedule and manual paths must be validate-only`);
		}
	}
}

function inspectArchives(
	input: ReleaseIdentityInput,
	expectedReleaseId: string,
	expectedSource: string,
	violations: string[],
	missing: string[],
): string[] {
	const required = expectedArchiveNames(expectedReleaseId);
	const archives = input.archives ?? [];
	const byName = new Map<string, ArchiveEvidence>();
	for (const archive of archives) {
		const name = typeof archive.name === "string" ? archive.name : "<missing name>";
		if (byName.has(name)) violations.push(`archive ${name}: duplicate artifact evidence`);
		byName.set(name, archive);
		if (!name.includes(expectedReleaseId)) {
			violations.push(`archive ${name}: archive name must use ${expectedReleaseId}`);
		}
	}
	for (const name of required) {
		const archive = byName.get(name);
		if (archive === undefined) {
			addMissing(violations, missing, "artifact", name);
			continue;
		}
		checkFullSha(archive.sourceCommit, `archive ${name}`, expectedSource, violations);
		if (archive.releaseId !== expectedReleaseId) {
			violations.push(`archive ${name}: release ID must be ${expectedReleaseId}`);
		}
		checkArtifactHash(archive.sha256, `archive ${name}`, violations);
		const expectedBinary = name.startsWith("pixie-assistant-") ? "pixie-assistant" : "pixie";
		if (archive.binaryName !== expectedBinary) {
			violations.push(`archive ${name}: contained binary must be ${expectedBinary}`);
		}
	}
	return archives.map(({ name }) => (typeof name === "string" ? name : "<missing name>")).sort();
}

function inspectBinaries(
	input: ReleaseIdentityInput,
	expectedReleaseId: string,
	expectedSource: string,
	violations: string[],
	missing: string[],
): string[] {
	const binaries = input.binaries ?? [];
	const byKey = new Map<string, BinaryEvidence>();
	for (const binary of binaries) {
		const key = expectedBinaryKey(binary.variant, binary.architecture);
		if (byKey.has(key)) violations.push(`binary ${key}: duplicate artifact evidence`);
		byKey.set(key, binary);
		const expectedName = binary.variant === "assistant" ? "pixie-assistant" : "pixie";
		if (binary.name !== expectedName)
			violations.push(`binary ${key}: binary name must be ${expectedName}`);
	}
	for (const variant of RELEASE_VARIANTS) {
		for (const architecture of RELEASE_ARCHITECTURES) {
			const key = expectedBinaryKey(variant, architecture);
			const binary = byKey.get(key);
			if (binary === undefined) {
				addMissing(violations, missing, "artifact", `binary ${key}`);
				continue;
			}
			checkFullSha(binary.sourceCommit, `binary ${key}`, expectedSource, violations);
			if (binary.releaseId !== expectedReleaseId) {
				violations.push(`binary ${key}: release ID must be ${expectedReleaseId}`);
			}
			checkArtifactHash(binary.sha256, `binary ${key}`, violations);
		}
	}
	return binaries
		.map(({ variant, architecture }) => expectedBinaryKey(variant, architecture))
		.sort();
}

function inspectDocker(
	docker: DockerEvidence | undefined,
	expectedReleaseId: string,
	expectedSource: string,
	violations: string[],
	missing: string[],
): void {
	if (docker === undefined) {
		addMissing(
			violations,
			missing,
			"artifact",
			"multi-architecture Docker tag, index digest and platform digests",
		);
		return;
	}
	const expectedTag = `ghcr.io/miloszkolber/pixie:${expectedReleaseId}`;
	if (docker.tag !== expectedTag) violations.push(`Docker tag must be ${expectedTag}`);
	if (docker.version !== expectedReleaseId)
		violations.push("Docker OCI version label must be the release ID");
	checkFullSha(docker.revision, "Docker OCI revision label", expectedSource, violations);
	const labels = docker.labels;
	if (labels === undefined) {
		violations.push("Docker OCI version and revision labels are required");
	} else {
		if (labels["org.opencontainers.image.version"] !== expectedReleaseId) {
			violations.push("Docker OCI label org.opencontainers.image.version must be the release ID");
		}
		if (labels["org.opencontainers.image.revision"] !== expectedSource) {
			violations.push(
				"Docker OCI label org.opencontainers.image.revision must be the full source commit",
			);
		}
	}
	if (docker.provenance !== true)
		violations.push("Docker image verified provenance evidence is required");
	checkDigest(docker.indexDigest, "Docker image index", violations);
	const platformDigests = docker.platformDigests ?? {};
	for (const architecture of RELEASE_ARCHITECTURES) {
		checkDigest(platformDigests[architecture], `Docker linux/${architecture} image`, violations);
	}
}

function inspectManifest(
	manifest: ReleaseManifestEvidence | undefined,
	expectedReleaseId: string,
	expectedSource: string,
	requiredArchives: readonly string[],
	violations: string[],
	missing: string[],
): void {
	if (manifest === undefined) {
		addMissing(
			violations,
			missing,
			"artifact",
			"release-manifest.json with full source and digest evidence",
		);
		addMissing(violations, missing, "artifact", "checksums.txt, SBOM and provenance evidence");
		return;
	}
	if (manifest.releaseId !== expectedReleaseId)
		violations.push("release-manifest.json: release ID does not match the source commit");
	checkFullSha(manifest.sourceCommit, "release-manifest.json", expectedSource, violations);
	if (!manifest.cleanSourceTree)
		violations.push("release-manifest.json: clean source tree must be recorded");
	if (!manifest.completeSet)
		violations.push("release-manifest.json: complete four-archive/image set is not recorded");
	const archiveHashes = manifest.archiveHashes ?? {};
	for (const archive of requiredArchives)
		checkArtifactHash(
			archiveHashes[archive],
			`release-manifest.json archive ${archive}`,
			violations,
		);
	checkDigest(manifest.imageIndexDigest, "release-manifest.json image index", violations);
	const platformDigests = manifest.platformDigests ?? {};
	for (const architecture of RELEASE_ARCHITECTURES) {
		checkDigest(
			platformDigests[architecture],
			`release-manifest.json linux/${architecture} image`,
			violations,
		);
	}
	if (!manifest.checksumsPresent)
		violations.push("release-manifest.json: checksums.txt evidence is required");
	if (!manifest.sbomPresent) violations.push("release-manifest.json: SBOM evidence is required");
	if (!manifest.provenancePresent)
		violations.push("release-manifest.json: verified provenance evidence is required");
}

function inspectDigestConsistency(input: ReleaseIdentityInput, violations: string[]): void {
	const manifest = input.manifest;
	if (manifest === undefined) return;
	const archiveHashes = manifest.archiveHashes ?? {};
	for (const archive of input.archives ?? []) {
		const recordedHash = archiveHashes[archive.name];
		if (recordedHash !== undefined && recordedHash !== archive.sha256) {
			violations.push(`release-manifest.json: archive hash disagrees with ${archive.name}`);
		}
	}
	const docker = input.docker;
	if (docker === undefined) return;
	if (manifest.imageIndexDigest !== docker.indexDigest) {
		violations.push("release-manifest.json: image index digest disagrees with Docker evidence");
	}
	const manifestPlatforms = manifest.platformDigests ?? {};
	const dockerPlatforms = docker.platformDigests ?? {};
	for (const architecture of RELEASE_ARCHITECTURES) {
		if (manifestPlatforms[architecture] !== dockerPlatforms[architecture]) {
			violations.push(
				`release-manifest.json: linux/${architecture} digest disagrees with Docker evidence`,
			);
		}
	}
}

function inspectCollisionAndRetry(
	input: ReleaseIdentityInput,
	expectedReleaseId: string,
	expectedSource: string,
	violations: string[],
): void {
	const existing = [
		...(input.existingReleases ?? []),
		...(input.existingRelease === undefined ? [] : [input.existingRelease]),
	];
	for (const record of existing) {
		if (record.releaseId !== expectedReleaseId) continue;
		if (
			!SOURCE_COMMIT_PATTERN.test(record.sourceCommit) ||
			record.sourceCommit !== expectedSource
		) {
			violations.push(
				`release identity collision: ${expectedReleaseId} already names a different full source commit`,
			);
		}
	}

	const partial = input.partialPublication;
	if (partial !== undefined) {
		if (partial.releaseId !== expectedReleaseId || partial.sourceCommit !== expectedSource) {
			violations.push(
				"partial publication: retry must retain the same release ID and full source commit",
			);
		}
		const publishedArtifacts = Array.isArray(partial.publishedArtifacts)
			? partial.publishedArtifacts
			: [];
		if (publishedArtifacts.length === 0)
			violations.push("partial publication: at least one uploaded artifact must be recorded");
		const allowedPartialArtifacts = new Set([
			...expectedArchiveNames(expectedReleaseId),
			"checksums.txt",
			"release-manifest.json",
			"sbom",
			"provenance",
		]);
		for (const artifact of publishedArtifacts) {
			if (!allowedPartialArtifacts.has(artifact) && !DIGEST_PATTERN.test(artifact)) {
				violations.push(`partial publication: unknown staged artifact ${artifact}`);
			}
		}
		if (partial.latestPromoted === undefined) {
			violations.push("partial publication: latest promotion state must be recorded as false");
		} else if (partial.latestPromoted === true) {
			violations.push(
				"partial publication: latest must not be promoted before the complete set is verified",
			);
		}
		if (input.retry === undefined) {
			violations.push("partial publication: an immutable same-identity retry record is required");
		}
	}
	if (input.retry !== undefined) {
		if (
			input.retry.releaseId !== expectedReleaseId ||
			input.retry.sourceCommit !== expectedSource
		) {
			violations.push("retry: retry must retain the same release ID and full source commit");
		}
		if (!input.retry.reusedPayload) {
			violations.push("retry: retry must reuse the staged payload rather than rebuild it");
		}
		if (input.partialPublication === undefined) {
			violations.push("retry: a retry record must identify the partial publication it resumes");
		}
	}
}

export function releaseIdForCommit(sourceCommit: string): string | null {
	return SOURCE_COMMIT_PATTERN.test(sourceCommit) ? `sha-${sourceCommit.slice(0, 12)}` : null;
}

export function inspectReleaseIdentity(input: ReleaseIdentityInput): ReleaseIdentityReport {
	const violations: string[] = [];
	const missingWorkflowEvidence: string[] = [];
	const missingArtifactEvidence: string[] = [];
	const sourceCommit = input.sourceCommit;
	const expectedReleaseId = sourceCommit === undefined ? null : releaseIdForCommit(sourceCommit);

	if (sourceCommit === undefined || sourceCommit === "") {
		violations.push("source commit: full source commit is required");
	} else if (!SOURCE_COMMIT_PATTERN.test(sourceCommit)) {
		violations.push("source commit: must be exactly 40 lowercase hex characters");
	}
	if (expectedReleaseId === null) {
		if (input.releaseId !== undefined)
			violations.push("release ID cannot be derived from an invalid source commit");
		addMissing(
			violations,
			missingArtifactEvidence,
			"artifact",
			"four commit-named archives, four binary records, Docker index/platform digests and release manifest",
		);
		inspectWorkflowEvidence(input.workflowSources, violations, missingWorkflowEvidence);
		return {
			ok: false,
			violations,
			facts: {
				expectedReleaseId: null,
				sourceCommit: SOURCE_COMMIT_PATTERN.test(sourceCommit ?? "")
					? (sourceCommit ?? null)
					: null,
				requiredArchives: [],
				observedArchives: [],
				observedBinaries: [],
				workflowFiles: Object.keys(input.workflowSources ?? {}).sort(),
				missingWorkflowEvidence,
				missingArtifactEvidence,
				missingLiveInputs: missingArtifactEvidence,
			},
		};
	}
	const verifiedSourceCommit = sourceCommit ?? "";

	if (input.releaseId !== expectedReleaseId)
		violations.push(`release ID must be ${expectedReleaseId}`);
	if (!RELEASE_ID_PATTERN.test(expectedReleaseId))
		violations.push("release ID must use sha- plus 12 lowercase source-commit characters");

	if (input.tag === undefined) {
		violations.push("Git tag: release tag identity evidence is required");
	} else {
		if (input.tag.name !== expectedReleaseId)
			violations.push(`Git tag name must be ${expectedReleaseId}`);
		checkFullSha(input.tag.target, "Git tag", verifiedSourceCommit, violations);
	}
	if (input.release === undefined) {
		violations.push("GitHub Release: title, tag and target evidence are required");
	} else {
		if (input.release.title !== expectedReleaseId)
			violations.push(`GitHub Release title must be ${expectedReleaseId}`);
		if (input.release.tag !== expectedReleaseId)
			violations.push(`GitHub Release tag must be ${expectedReleaseId}`);
		checkFullSha(input.release.target, "GitHub Release", verifiedSourceCommit, violations);
	}

	const requiredArchives = expectedArchiveNames(expectedReleaseId);
	const observedArchives = inspectArchives(
		input,
		expectedReleaseId,
		verifiedSourceCommit,
		violations,
		missingArtifactEvidence,
	);
	const observedBinaries = inspectBinaries(
		input,
		expectedReleaseId,
		verifiedSourceCommit,
		violations,
		missingArtifactEvidence,
	);
	inspectDocker(
		input.docker,
		expectedReleaseId,
		verifiedSourceCommit,
		violations,
		missingArtifactEvidence,
	);
	inspectManifest(
		input.manifest,
		expectedReleaseId,
		verifiedSourceCommit,
		requiredArchives,
		violations,
		missingArtifactEvidence,
	);
	inspectDigestConsistency(input, violations);
	inspectCollisionAndRetry(input, expectedReleaseId, verifiedSourceCommit, violations);
	inspectWorkflowEvidence(input.workflowSources, violations, missingWorkflowEvidence);

	return {
		ok: violations.length === 0,
		violations,
		facts: {
			expectedReleaseId,
			sourceCommit: verifiedSourceCommit,
			requiredArchives,
			observedArchives,
			observedBinaries,
			workflowFiles: Object.keys(input.workflowSources ?? {}).sort(),
			missingWorkflowEvidence,
			missingArtifactEvidence,
			missingLiveInputs: missingArtifactEvidence,
		},
	};
}

async function readWorkflowSources(repositoryRoot: string): Promise<Record<string, string>> {
	const directory = resolve(repositoryRoot, ".github/workflows");
	const sources: Record<string, string> = {};
	for (const entry of await readdir(directory, { withFileTypes: true }).catch(() => [])) {
		if (!entry.isFile() || !/\.ya?ml$/i.test(entry.name)) continue;
		sources[`.github/workflows/${entry.name}`] = await readFile(
			resolve(directory, entry.name),
			"utf8",
		);
	}
	return sources;
}

async function gitRevision(repositoryRoot: string): Promise<string | undefined> {
	const child = Bun.spawn(["git", "-C", repositoryRoot, "rev-parse", "--verify", "HEAD^{commit}"], {
		stdout: "pipe",
		stderr: "ignore",
	});
	const [output, exitCode] = await Promise.all([new Response(child.stdout).text(), child.exited]);
	return exitCode === 0 ? output.trim() : undefined;
}

async function readManifest(repositoryRoot: string): Promise<ReleaseManifestEvidence | undefined> {
	const candidates = [
		"release-manifest.json",
		"release/release-manifest.json",
		"artifacts/release-manifest.json",
		"artifacts/release/release-manifest.json",
		"dist/release-manifest.json",
	];
	for (const candidate of candidates) {
		try {
			return JSON.parse(
				await readFile(resolve(repositoryRoot, candidate), "utf8"),
			) as ReleaseManifestEvidence;
		} catch {
			// The release manifest is generated evidence and is intentionally absent in this checkout.
		}
	}
	return undefined;
}

export async function collectReleaseIdentityInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<ReleaseIdentityInput> {
	const sourceCommit = await gitRevision(repositoryRoot);
	const manifest = await readManifest(repositoryRoot);
	return {
		workflowSources: await readWorkflowSources(repositoryRoot),
		...(sourceCommit === undefined ? {} : { sourceCommit }),
		...(manifest === undefined ? {} : { manifest }),
	};
}

export function formatReleaseIdentityReport(report: ReleaseIdentityReport): string {
	if (report.ok) {
		return (
			`check-release-identity: OK (${report.facts.expectedReleaseId}, ` +
			`${report.facts.observedArchives.length} archives, ${report.facts.observedBinaries.length} binaries)`
		);
	}
	return [
		"check-release-identity: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.facts.missingLiveInputs.map((input) => `  - missing live input: ${input}`),
	].join("\n");
}

export async function runReleaseIdentityCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const report = inspectReleaseIdentity(await collectReleaseIdentityInput(repositoryRoot));
	const output = formatReleaseIdentityReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runReleaseIdentityCheck());
