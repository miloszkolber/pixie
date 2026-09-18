#!/usr/bin/env bun

import { resolve } from "node:path";
import {
	collectPackageArtifactInput,
	defaultReductionsManifestPath,
	expandPackageArtifactReductions,
	expandReleaseGateReductions,
	inspectPackageArtifacts,
	loadReductionsManifest,
	type PackageArtifactInput,
	type PackageArtifactReduction,
	type PackageArtifactReport,
	packageArtifactInputFromEvidence,
	RELEASE_MANIFEST_ASSERTION_ID,
	type ReleaseGateReduction,
	reductionMatches,
} from "./check-package-artifacts.ts";
import {
	type ArchiveEvidence,
	type BinaryEvidence,
	collectReleaseIdentityInput,
	type DockerEvidence,
	inspectReleaseIdentity,
	type ReleaseIdentityInput,
	type ReleaseIdentityReport,
	type ReleaseManifestEvidence,
	releaseIdForCommit,
} from "./check-release-identity.ts";
import {
	CONTROLLER_IMAGE_ASSERTION_ID,
	decodeControllerImageDetail,
	decodePackageArchiveDetail,
	type EvidenceBundle,
	findAssertion,
	PACKAGE_ARCHIVE_ASSERTION_PREFIX,
	parsePackageArchiveAssertionId,
	readEvidenceBundle,
} from "./evidence-bundle.ts";

const DOC_PATH = /^(?:\.github\/[^/]+\.md|(?:docs|roadmap)\/|[^/]+\.md$)/i;
const RELEASE_SOURCE_PATH = /^(?:assistant|web|shared|patches)\//i;
const RELEASE_ROOT_FILE = /^(?:package\.json|bun\.lockb?|biome\.jsonc?|Dockerfile)$/i;
const PUBLISH_OPERATION =
	/\b(?:publish|upload|release|attest|imagetools\s+create|buildx\s+build[^\n]*--push)\b/i;
const RELEASE_TAG_PUSH = /\bpush\s*:[\s\S]{0,200}\btags\s*:/i;

export interface ReleasePolicyInput {
	workflowSources?: Readonly<Record<string, string>>;
	changedPaths?: readonly string[];
	mainBranch?: string;
	/** Explicit repository approval for the automatic main-commit policy. */
	automaticMainAuthorized?: boolean;
	/** Evidence that the selected source revision is reachable from the protected main branch. */
	sourceReachableFromMain?: boolean;
	/** Static evidence that documentation-only changes remain validation-only. */
	docsOnlyValidateOnly?: boolean;
}

export interface LatestPublicationEvidence {
	currentSourceCommit?: string;
	candidateSourceCommit?: string;
	candidateDescendsFromCurrent?: boolean;
	comparison?: "source-ancestry" | "publication-time" | "lexicographic-hash";
	completeSetVerified?: boolean;
	promoted?: boolean;
}

export interface ReleaseGateInput {
	identity: ReleaseIdentityInput;
	packages: PackageArtifactInput;
	policy?: ReleasePolicyInput;
	latest?: LatestPublicationEvidence;
	/** Explicitly approved reductions for publication-only evidence rows. */
	reductions?: readonly ReleaseGateReduction[];
	/**
	 * Fail-closed findings produced while translating an evidence bundle. They
	 * never replace an identity/package/policy assertion.
	 */
	evidenceViolations?: readonly string[];
}

export interface ReleasePolicyFacts {
	workflowFiles: readonly string[];
	publishWorkflows: readonly string[];
	validateOnlyTriggers: readonly string[];
	docsOnly: boolean;
	automaticMainAuthorized: boolean;
}

export interface ReleasePolicyReport {
	ok: boolean;
	violations: readonly string[];
	missingInputs: readonly string[];
	facts: ReleasePolicyFacts;
}

export interface ReleaseGateFacts {
	releaseId: string | null;
	sourceCommit: string | null;
	identity: ReleaseIdentityReport["facts"];
	packages: PackageArtifactReport["facts"];
	policy: ReleasePolicyFacts;
	staticChecks: readonly string[];
	missingLiveInputs: readonly string[];
	/** Missing inputs that an approved reduction removed from the blocking set. */
	reducedLiveInputs: readonly string[];
}

export interface ReleaseGateReport {
	ok: boolean;
	violations: readonly string[];
	missingLiveInputs: readonly string[];
	facts: ReleaseGateFacts;
}

function stripComments(source: string): string {
	return source.replace(/(^|\s)#.*$/gm, "$1");
}

function hasTrigger(source: string, trigger: string): boolean {
	return new RegExp(`\\b${trigger}\\s*:`).test(source);
}

function hasMainPushGuard(source: string, mainBranch: string): boolean {
	const clean = stripComments(source);
	const escapedBranch = mainBranch.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	const mainTrigger = new RegExp(
		`\\bpush\\s*:[\\s\\S]{0,240}\\bbranches\\s*:\\s*(?:\\[[^\\]]*\\b${escapedBranch}\\b|[\\s\\S]{0,80}\\b${escapedBranch}\\b)`,
		"i",
	);
	const guardedJob =
		/\bif\s*:[^\n]*(?:event_name\s*==\s*["']push|refs?\/heads\/main|github\.ref\s*==[^\n]*main)/i;
	return mainTrigger.test(clean) && guardedJob.test(clean);
}

function hasValidateOnlyGuard(source: string): boolean {
	const clean = stripComments(source);
	return /validate[- ]only|dry[-_ ]?run|nothing will be published|event_name\s*==\s*["']push|github\.ref\s*==[^\n]*refs\/heads\/main/i.test(
		clean,
	);
}

function hasDocsOnlyGuard(source: string): boolean {
	const clean = stripComments(source);
	return /docs?[-_ ]only|documentation[-_ ]only|paths[-_ ]ignore[^\n]*docs|changed[^\n]*docs|no[^\n]*publish/i.test(
		clean,
	);
}

function inspectPolicy(input: ReleasePolicyInput | undefined): ReleasePolicyReport {
	const policy = input ?? {};
	const sources = policy.workflowSources ?? {};
	const workflowFiles = Object.keys(sources).sort();
	const violations: string[] = [];
	const missingInputs: string[] = [];
	const releaseEntries = Object.entries(sources).filter(
		([path, source]) => /release/i.test(path) || PUBLISH_OPERATION.test(stripComments(source)),
	);
	const publishWorkflows = releaseEntries.map(([path]) => path).sort();
	const validateOnlyTriggers = ["pull_request", "schedule", "workflow_dispatch"].filter((trigger) =>
		releaseEntries.some(
			([, source]) => hasTrigger(source, trigger) && hasValidateOnlyGuard(source),
		),
	);
	const mainBranch = policy.mainBranch ?? "main";
	const changedPaths = policy.changedPaths ?? [];
	const docsOnly = changedPaths.length > 0 && changedPaths.every((path) => DOC_PATH.test(path));

	if (releaseEntries.length === 0) {
		missingInputs.push("a release workflow source with an explicit publication job");
		violations.push("missing release workflow publication policy");
	}
	for (const trigger of ["pull_request", "schedule", "workflow_dispatch"] as const) {
		if (!releaseEntries.some(([, source]) => hasTrigger(source, trigger))) {
			missingInputs.push(`${trigger} validate-only path`);
			continue;
		}
		if (
			!releaseEntries.some(
				([, source]) => hasTrigger(source, trigger) && hasValidateOnlyGuard(source),
			)
		) {
			violations.push(`${trigger} must be validate-only and must not publish`);
		}
	}
	if (
		releaseEntries.length > 0 &&
		!releaseEntries.some(([, source]) => hasMainPushGuard(source, mainBranch))
	) {
		violations.push(`automatic publication must be guarded to an authorized ${mainBranch} push`);
	}
	if (policy.automaticMainAuthorized !== true) {
		missingInputs.push("explicit approval for automatic main publication policy");
		violations.push("automatic main publication is not explicitly authorized");
	}
	if (policy.sourceReachableFromMain !== true) {
		missingInputs.push("source reachability from the protected main branch");
		violations.push("release source must be verified reachable from main");
	}
	if (
		docsOnly &&
		policy.docsOnlyValidateOnly !== true &&
		!releaseEntries.some(([, source]) => hasDocsOnlyGuard(source))
	) {
		violations.push("documentation-only changes must remain validate-only");
	}
	if (releaseEntries.some(([, source]) => RELEASE_TAG_PUSH.test(stripComments(source)))) {
		violations.push("release-tag pushes must not trigger another publication loop");
	}
	for (const [path, source] of releaseEntries) {
		const clean = stripComments(source);
		if (
			PUBLISH_OPERATION.test(clean) &&
			/pull_request\s*:|schedule\s*:|workflow_dispatch\s*:/i.test(clean) &&
			!hasValidateOnlyGuard(clean)
		) {
			violations.push(`${path}: privileged publication lacks validation-trigger guards`);
		}
		if (
			/packages?\s*:\s*write|contents\s*:\s*write/i.test(clean) &&
			!/publish|release/i.test(path + clean)
		) {
			violations.push(`${path}: write credentials are not scoped to the release job`);
		}
	}

	return {
		ok: violations.length === 0 && missingInputs.length === 0,
		violations,
		missingInputs,
		facts: {
			workflowFiles,
			publishWorkflows,
			validateOnlyTriggers,
			docsOnly,
			automaticMainAuthorized: policy.automaticMainAuthorized === true,
		},
	};
}

function inspectLatest(
	latest: LatestPublicationEvidence | undefined,
	identity: ReleaseIdentityInput,
	violations: string[],
	missingLiveInputs: string[],
): void {
	if (latest === undefined) {
		missingLiveInputs.push("latest promotion ancestry and complete-set evidence");
		return;
	}
	const sourceCommit = identity.sourceCommit;
	if (latest.candidateSourceCommit !== undefined && latest.candidateSourceCommit !== sourceCommit) {
		violations.push("latest promotion candidate must use the selected full source commit");
	}
	if (
		latest.currentSourceCommit !== undefined &&
		latest.candidateDescendsFromCurrent === undefined
	) {
		missingLiveInputs.push("latest source-ancestry comparison");
	}
	if (latest.comparison === "lexicographic-hash") {
		violations.push("latest chronology must not be decided by hash ordering");
	}
	if (latest.promoted === true && latest.completeSetVerified !== true) {
		violations.push("latest must not be promoted before the complete artifact set is verified");
	}
	if (latest.currentSourceCommit !== undefined && latest.candidateDescendsFromCurrent === false) {
		violations.push("latest promotion would regress verified source ancestry");
	}
}

function inspectDockerInputs(identity: ReleaseIdentityInput, missingLiveInputs: string[]): void {
	const docker = identity.docker;
	if (docker === undefined) return;
	if (docker.labels === undefined) missingLiveInputs.push("OCI version and revision labels");
	if (docker.provenance !== true) missingLiveInputs.push("image provenance attestation");
}

/** Publication-only rows the release gate may reduce; static checks stay mandatory. */
const RELEASE_REDUCIBLE_BASES = [
	"tag.identity",
	"release.identity",
	"registry.tag",
	"registry.provenance",
	"registry.sbom",
	"registry.image-index",
	"registry.platform-digest",
	"manifest.complete-set",
	"latest.promotion",
] as const;

function releaseReductionBase(id: string): string | null {
	for (const base of RELEASE_REDUCIBLE_BASES) {
		if (id === base || id.startsWith(`${base}/`)) return base;
	}
	return null;
}

function validateReleaseReductionRow(id: string, base: string): string | null {
	if (base !== "registry.platform-digest") {
		return id === base ? null : `${id} does not accept a target`;
	}
	const target = id.slice(base.length + 1);
	return target === "*" || target === "amd64" || target === "arm64"
		? null
		: `invalid platform target in ${id}`;
}

/**
 * Map a release identity/policy message to its publication row. Static policy,
 * archive, binary, checksum and source-reachability messages return null and
 * therefore can never be reduced.
 */
function classifyReleaseRow(message: string): string | null {
	if (/^Git tag\b/.test(message)) return "tag.identity";
	if (/^GitHub Release\b/.test(message)) return "release.identity";
	if (/^Docker tag must be\b/.test(message)) return "registry.tag";
	if (/provenance/i.test(message)) return "registry.provenance";
	if (/\bSBOM\b/.test(message)) return "registry.sbom";
	if (/\bimage index\b/i.test(message)) return "registry.image-index";
	if (/linux\/arm64\b/.test(message)) return "registry.platform-digest/arm64";
	if (/linux\/amd64\b/.test(message)) return "registry.platform-digest/amd64";
	if (/complete (?:four-archive|three-product\/six-archive) image set/.test(message))
		return "manifest.complete-set";
	if (/^latest\b/i.test(message)) return "latest.promotion";
	return null;
}

function validateReleaseReductions(reductions: readonly ReleaseGateReduction[]): {
	approved: ReleaseGateReduction[];
	violations: string[];
} {
	const approved: ReleaseGateReduction[] = [];
	const violations: string[] = [];
	for (const reduction of reductions) {
		const base = releaseReductionBase(reduction.id);
		if (base === null) {
			violations.push(`releaseGate reduction ${reduction.id}: row is not reducible`);
			continue;
		}
		if (!reduction.approved || !reduction.reason?.trim() || !reduction.approver?.trim()) {
			violations.push(
				`releaseGate reduction ${reduction.id}: explicit approval, approver and reason are required`,
			);
			continue;
		}
		const issue = validateReleaseReductionRow(reduction.id, base);
		if (issue !== null) {
			violations.push(`releaseGate reduction ${issue}`);
			continue;
		}
		approved.push(reduction);
	}
	return { approved, violations };
}

function reduceMessages(
	messages: readonly string[],
	approved: readonly ReleaseGateReduction[],
): { kept: string[]; reduced: string[] } {
	const kept: string[] = [];
	const reduced: string[] = [];
	for (const message of messages) {
		const id = classifyReleaseRow(message);
		if (id !== null && approved.some((reduction) => reductionMatches(id, reduction.id))) {
			reduced.push(message);
		} else {
			kept.push(message);
		}
	}
	return { kept, reduced };
}

export function inspectReleasePolicy(input: ReleasePolicyInput | undefined): ReleasePolicyReport {
	return inspectPolicy(input);
}

export function inspectReleaseGate(input: ReleaseGateInput): ReleaseGateReport {
	const identity = inspectReleaseIdentity(input.identity);
	const packageInput = input.packages;
	const packages = inspectPackageArtifacts(packageInput);
	const policyInput =
		input.policy ??
		(input.identity.workflowSources === undefined
			? {}
			: { workflowSources: input.identity.workflowSources });
	const policy = inspectPolicy(policyInput);

	const reductionValidation = validateReleaseReductions(input.reductions ?? []);
	const approvedReductions = reductionValidation.approved;
	const identityViolations = reduceMessages(identity.violations, approvedReductions);
	const identityMissing = reduceMessages(identity.facts.missingLiveInputs, approvedReductions);

	const extraViolations: string[] = [];
	const extraMissing: string[] = [];
	inspectDockerInputs(input.identity, extraMissing);
	inspectLatest(input.latest, input.identity, extraViolations, extraMissing);
	const dockerViolations = reduceMessages(extraViolations, approvedReductions);
	const dockerMissing = reduceMessages(extraMissing, approvedReductions);

	const sourceCommit = input.identity.sourceCommit ?? null;
	const releaseId = sourceCommit === null ? null : releaseIdForCommit(sourceCommit);
	const crossViolations: string[] = [];
	const crossMissing: string[] = [];
	if (
		releaseId !== null &&
		packageInput.releaseId !== undefined &&
		packageInput.releaseId !== releaseId
	) {
		crossViolations.push(`package artifacts must use ${releaseId}`);
	}
	if (releaseId !== null && packageInput.releaseId === undefined) {
		crossMissing.push(`package artifacts for ${releaseId}`);
	}

	const violations = [
		...(input.evidenceViolations ?? []),
		...identityViolations.kept,
		...packages.violations,
		...policy.violations,
		...dockerViolations.kept,
		...crossViolations,
		...reductionValidation.violations,
	];
	const missingLiveInputs = [
		...identityMissing.kept,
		...packages.missingLiveEvidence,
		...policy.missingInputs,
		...dockerMissing.kept,
		...crossMissing,
	];
	const reducedLiveInputs = [
		...identityViolations.reduced,
		...identityMissing.reduced,
		...dockerViolations.reduced,
		...dockerMissing.reduced,
	];
	const staticChecks = [
		...packages.facts.staticChecks,
		...policy.facts.validateOnlyTriggers.map((trigger) => `${trigger}: validate-only guard`),
	];
	return {
		ok: violations.length === 0 && missingLiveInputs.length === 0,
		violations,
		missingLiveInputs: [...new Set(missingLiveInputs)],
		facts: {
			releaseId,
			sourceCommit,
			identity: identity.facts,
			packages: packages.facts,
			policy: policy.facts,
			staticChecks,
			missingLiveInputs: [...new Set(missingLiveInputs)],
			reducedLiveInputs: [...new Set(reducedLiveInputs)],
		},
	};
}

export const inspectReleasePipeline = inspectReleaseGate;

export interface ReleaseEvidenceMapping {
	identity: ReleaseIdentityInput;
	violations: readonly string[];
}

function isRecordValue(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringRecord(value: unknown): Record<string, string> {
	if (!isRecordValue(value)) return {};
	const record: Record<string, string> = {};
	for (const [key, entry] of Object.entries(value)) {
		if (typeof entry === "string" && entry !== "") record[key] = entry;
	}
	return record;
}

/**
 * Decode the collector's staged `release-manifest.json` payload. The values are
 * echoed verbatim from the staged file: absent publication fields stay absent
 * here and are reduced explicitly, never invented as passes.
 */
function decodeStagedManifest(detail: string): ReleaseManifestEvidence | null {
	let value: unknown;
	try {
		value = JSON.parse(detail);
	} catch {
		return null;
	}
	if (!isRecordValue(value)) return null;
	const platformDigests = stringRecord(value.platformDigests);
	return {
		releaseId: typeof value.releaseId === "string" ? value.releaseId : "",
		sourceCommit: typeof value.sourceCommit === "string" ? value.sourceCommit : "",
		cleanSourceTree: value.cleanSourceTree === true,
		completeSet: value.completeSet === true,
		archiveHashes: stringRecord(value.archiveHashes),
		imageIndexDigest: typeof value.imageIndexDigest === "string" ? value.imageIndexDigest : "",
		platformDigests,
		checksumsPresent: value.checksumsPresent === true,
		sbomPresent: value.sbomPresent === true,
		provenancePresent: value.provenancePresent === true,
	};
}

/**
 * Translate package/image GATE assertions into release-identity evidence. Only
 * `pass` assertions with a machine-readable detail are accepted; any
 * blocked/failed/malformed assertion is surfaced as a violation. Git tag,
 * GitHub Release, registry provenance and latest-promotion evidence are not
 * local and therefore stay absent and fail-closed.
 */
export function identityInputFromEvidence(
	evidence: EvidenceBundle,
	base: ReleaseIdentityInput,
): ReleaseEvidenceMapping {
	const violations: string[] = [];
	const archives: ArchiveEvidence[] = [];
	const binaries: BinaryEvidence[] = [];
	for (const assertion of evidence.assertions) {
		if (assertion.kind !== "GATE") continue;
		if (!assertion.id.startsWith(PACKAGE_ARCHIVE_ASSERTION_PREFIX)) continue;
		if (assertion.status !== "pass") {
			violations.push(
				`evidence ${assertion.id}: archive assertion is ${assertion.status} (${assertion.detail})`,
			);
			continue;
		}
		const artifact = assertion.artifact;
		const detail = decodePackageArchiveDetail(assertion.detail);
		if (artifact === undefined || detail === null) {
			violations.push(`evidence ${assertion.id}: malformed archive artifact evidence`);
			continue;
		}
		const key = parsePackageArchiveAssertionId(assertion.id);
		if (
			key === null ||
			key.product !== detail.product ||
			key.architecture !== detail.architecture
		) {
			violations.push(
				`evidence ${assertion.id}: assertion id does not match inspected ${detail.product}/${detail.architecture}`,
			);
			continue;
		}
		archives.push({
			name: artifact.name,
			sourceCommit: evidence.sourceCommit,
			releaseId: evidence.releaseId,
			sha256: artifact.sha256,
			binaryName: detail.binary,
		});
		binaries.push({
			name: detail.binary,
			variant: detail.product,
			architecture: detail.architecture,
			sourceCommit: evidence.sourceCommit,
			releaseId: evidence.releaseId,
			sha256: detail.binarySha256,
		});
	}

	const manifestAssertion = findAssertion(evidence, "GATE", RELEASE_MANIFEST_ASSERTION_ID);
	let manifest: ReleaseManifestEvidence | undefined;
	if (manifestAssertion !== undefined) {
		if (manifestAssertion.status !== "pass") {
			violations.push(
				`evidence ${RELEASE_MANIFEST_ASSERTION_ID}: assertion is ${manifestAssertion.status} (${manifestAssertion.detail})`,
			);
		} else {
			const decoded = decodeStagedManifest(manifestAssertion.detail);
			if (decoded === null) {
				violations.push(
					`evidence ${RELEASE_MANIFEST_ASSERTION_ID}: malformed staged manifest detail`,
				);
			} else {
				manifest = decoded;
			}
		}
	}

	const imageAssertion = findAssertion(evidence, "GATE", CONTROLLER_IMAGE_ASSERTION_ID);
	let docker: DockerEvidence | undefined;
	if (imageAssertion === undefined) {
		violations.push(`evidence: missing ${CONTROLLER_IMAGE_ASSERTION_ID} assertion`);
	} else if (imageAssertion.status !== "pass") {
		violations.push(
			`evidence ${CONTROLLER_IMAGE_ASSERTION_ID}: image assertion is ${imageAssertion.status} (${imageAssertion.detail})`,
		);
	} else {
		const detail = decodeControllerImageDetail(imageAssertion.detail);
		if (detail === null) {
			violations.push(
				`evidence ${CONTROLLER_IMAGE_ASSERTION_ID}: malformed image inspection detail`,
			);
		} else {
			docker = {
				tag: detail.tag ?? "",
				version: detail.labels["org.opencontainers.image.version"] ?? "",
				revision: detail.labels["org.opencontainers.image.revision"] ?? "",
				indexDigest: detail.indexDigest ?? "",
				platformDigests: detail.platformDigests,
				labels: detail.labels,
				provenance: detail.provenance,
			};
		}
	}

	return {
		identity: {
			...base,
			sourceCommit: evidence.sourceCommit,
			releaseId: evidence.releaseId,
			archives,
			binaries,
			...(manifest === undefined ? {} : { manifest }),
			...(docker === undefined ? {} : { docker }),
		},
		violations,
	};
}

export interface ReleaseGateReductionGroups {
	packageArtifacts: PackageArtifactReduction[];
	releaseGate: ReleaseGateReduction[];
}

export async function loadReleaseGateReductions(
	path = defaultReductionsManifestPath(),
): Promise<ReleaseGateReductionGroups> {
	const manifest = await loadReductionsManifest(path);
	return {
		packageArtifacts: expandPackageArtifactReductions(manifest),
		releaseGate: expandReleaseGateReductions(manifest),
	};
}

export async function collectReleaseGateInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
	evidencePath?: string,
	reductionsPath?: string,
): Promise<ReleaseGateInput> {
	const identityBase = await collectReleaseIdentityInput(repositoryRoot);
	const packagesBase = await collectPackageArtifactInput(repositoryRoot);
	const reductions = await loadReleaseGateReductions(reductionsPath);
	const policy: ReleasePolicyInput = {
		...(identityBase.workflowSources === undefined
			? {}
			: { workflowSources: identityBase.workflowSources }),
		automaticMainAuthorized: process.env.PIXIE_AUTOMATIC_MAIN_AUTHORIZED === "true",
		sourceReachableFromMain: process.env.PIXIE_SOURCE_REACHABLE_FROM_MAIN === "true",
	};
	if (evidencePath === undefined) {
		const releaseId =
			identityBase.sourceCommit === undefined
				? undefined
				: (releaseIdForCommit(identityBase.sourceCommit) ?? undefined);
		return {
			identity: identityBase,
			packages: {
				...packagesBase,
				reductions: reductions.packageArtifacts,
				...(releaseId === undefined ? {} : { releaseId }),
			},
			policy,
			reductions: reductions.releaseGate,
		};
	}
	const evidence = await readEvidenceBundle(evidencePath);
	const identityMapping = identityInputFromEvidence(evidence, identityBase);
	const packageMapping = packageArtifactInputFromEvidence(evidence, packagesBase);
	return {
		identity: identityMapping.identity,
		packages: { ...packageMapping.input, reductions: reductions.packageArtifacts },
		policy,
		reductions: reductions.releaseGate,
		evidenceViolations: identityMapping.violations,
	};
}

export function formatReleaseGateReport(report: ReleaseGateReport): string {
	if (report.ok) {
		return (
			`release-gate: OK (${report.facts.releaseId}, ` +
			`${report.facts.identity.requiredArchives.length} archives, ` +
			`${report.facts.packages.staticChecks.length} package checks, ` +
			`${report.facts.reducedLiveInputs.length} reduced live inputs)`
		);
	}
	return [
		"release-gate: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveInputs.map((input) => `  - missing live input: ${input}`),
		...report.facts.reducedLiveInputs.map((input) => `  - reduced live input: ${input}`),
	].join("\n");
}

export const RELEASE_GATE_USAGE = [
	"usage: bun scripts/release-gate.ts [--evidence <bundle.json>]",
	"         [--reductions <reductions.json>]",
	"",
	"Without --evidence this command keeps its fail-closed behavior: it derives",
	"the release identity from the checkout and reports every absent archive,",
	"binary, manifest, OCI, tag/release and authorization input.",
	"",
	"With --evidence it consumes a schema-versioned bundle produced by",
	"collect-evidence.ts for archive, binary, local controller-image and staged",
	"manifest identity. A missing, malformed or non-passing assertion fails closed.",
	"",
	"--reductions (or PIXIE_REDUCTIONS_MANIFEST, defaulting to the committed",
	"web/reductions.json) exempts exactly the operator-approved",
	"publication rows. A reduced input is reported as reduced, never as passed;",
	"static policy, source reachability, archive/binary identity and checksum",
	"consistency stay mandatory. Set PIXIE_AUTOMATIC_MAIN_AUTHORIZED and",
	"PIXIE_SOURCE_REACHABLE_FROM_MAIN to true when the release job has verified",
	"them; they default to false.",
].join("\n");

interface ReleaseGateCliOptions {
	repositoryRoot: string;
	evidencePath?: string;
	reductionsPath: string;
}

function parseReleaseGateArgs(args: readonly string[]): ReleaseGateCliOptions {
	const repositoryRoot = resolve(import.meta.dir, "../..");
	let evidencePath: string | undefined;
	let reductionsPath =
		process.env.PIXIE_REDUCTIONS_MANIFEST?.trim() || defaultReductionsManifestPath();
	for (let index = 0; index < args.length; index += 1) {
		const argument = args[index];
		if (argument === "--help" || argument === "-h") {
			console.log(RELEASE_GATE_USAGE);
			process.exit(0);
		}
		if (argument === "--evidence") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "")
				throw new Error("--evidence requires a bundle path");
			evidencePath = value;
			index += 1;
			continue;
		}
		if (argument?.startsWith("--evidence=")) {
			const value = argument.slice("--evidence=".length).trim();
			if (value === "") throw new Error("--evidence requires a bundle path");
			evidencePath = value;
			continue;
		}
		if (argument === "--reductions") {
			const value = args[index + 1];
			if (value === undefined || value.trim() === "")
				throw new Error("--reductions requires a manifest path");
			reductionsPath = value;
			index += 1;
			continue;
		}
		if (argument?.startsWith("--reductions=")) {
			const value = argument.slice("--reductions=".length).trim();
			if (value === "") throw new Error("--reductions requires a manifest path");
			reductionsPath = value;
			continue;
		}
		throw new Error(`unknown argument ${argument}`);
	}
	return {
		repositoryRoot,
		reductionsPath,
		...(evidencePath === undefined ? {} : { evidencePath }),
	};
}

export async function runReleaseGate(
	repositoryRoot = resolve(import.meta.dir, "../.."),
	evidencePath?: string,
	reductionsPath?: string,
): Promise<number> {
	let input: ReleaseGateInput;
	try {
		input = await collectReleaseGateInput(repositoryRoot, evidencePath, reductionsPath);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`release-gate: FAILED\n  - evidence: ${message}`);
		return 1;
	}
	const report = inspectReleaseGate(input);
	const output = formatReleaseGateReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) {
	try {
		const options = parseReleaseGateArgs(Bun.argv.slice(2));
		process.exit(
			await runReleaseGate(options.repositoryRoot, options.evidencePath, options.reductionsPath),
		);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		console.error(`release-gate: ${message}`);
		console.error(RELEASE_GATE_USAGE);
		process.exit(2);
	}
}

export function isDocumentationOnlyChange(paths: readonly string[]): boolean {
	return paths.length > 0 && paths.every((path) => DOC_PATH.test(path));
}

export function isReleaseRelevantChange(path: string): boolean {
	return (
		RELEASE_SOURCE_PATH.test(path) ||
		RELEASE_ROOT_FILE.test(path) ||
		/^\.github\/workflows\//i.test(path)
	);
}
