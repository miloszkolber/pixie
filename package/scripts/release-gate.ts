#!/usr/bin/env bun

import { resolve } from "node:path";
import {
	collectPackageArtifactInput,
	inspectPackageArtifacts,
	type PackageArtifactInput,
	type PackageArtifactReport,
} from "./check-package-artifacts.ts";
import {
	collectReleaseIdentityInput,
	inspectReleaseIdentity,
	type ReleaseIdentityInput,
	type ReleaseIdentityReport,
	releaseIdForCommit,
} from "./check-release-identity.ts";

const DOC_PATH = /^(?:\.github\/[^/]+\.md|(?:docs|roadmap)\/|[^/]+\.md$)/i;
const RELEASE_SOURCE_PATH = /^(?:assistant|package|patches)\//i;
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
	const violations = [...identity.violations, ...packages.violations, ...policy.violations];
	const missingLiveInputs = [
		...identity.facts.missingLiveInputs,
		...packages.missingLiveEvidence,
		...policy.missingInputs,
	];
	inspectDockerInputs(input.identity, missingLiveInputs);
	inspectLatest(input.latest, input.identity, violations, missingLiveInputs);
	const sourceCommit = input.identity.sourceCommit ?? null;
	const releaseId = sourceCommit === null ? null : releaseIdForCommit(sourceCommit);
	if (
		releaseId !== null &&
		packageInput.releaseId !== undefined &&
		packageInput.releaseId !== releaseId
	) {
		violations.push(`package artifacts must use ${releaseId}`);
	}
	if (releaseId !== null && packageInput.releaseId === undefined) {
		missingLiveInputs.push(`package artifacts for ${releaseId}`);
	}
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
		},
	};
}

export const inspectReleasePipeline = inspectReleaseGate;

export async function collectReleaseGateInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<ReleaseGateInput> {
	const identity = await collectReleaseIdentityInput(repositoryRoot);
	const releaseId =
		identity.sourceCommit === undefined
			? undefined
			: (releaseIdForCommit(identity.sourceCommit) ?? undefined);
	const packages = await collectPackageArtifactInput(repositoryRoot);
	return {
		identity,
		packages: { ...packages, ...(releaseId === undefined ? {} : { releaseId }) },
		policy:
			identity.workflowSources === undefined ? {} : { workflowSources: identity.workflowSources },
	};
}

export function formatReleaseGateReport(report: ReleaseGateReport): string {
	if (report.ok) {
		return `release-gate: OK (${report.facts.releaseId}, ${report.facts.identity.requiredArchives.length} archives, ${report.facts.packages.staticChecks.length} package checks)`;
	}
	return [
		"release-gate: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveInputs.map((input) => `  - missing live input: ${input}`),
	].join("\n");
}

export async function runReleaseGate(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const report = inspectReleaseGate(await collectReleaseGateInput(repositoryRoot));
	const output = formatReleaseGateReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runReleaseGate());

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
