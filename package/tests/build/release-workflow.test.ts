import { expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const workflowPath = resolve(import.meta.dir, "../../../.github/workflows/release.yml");
const ciWorkflowPath = resolve(import.meta.dir, "../../../.github/workflows/ci.yml");
const containerWorkflowPath = resolve(
	import.meta.dir,
	"../../../.github/workflows/container-images.yml",
);

test("commit release workflow has one source identity derivation and validate-only non-main paths", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	expect(workflow).toContain("pull_request:");
	expect(workflow).toContain("schedule:");
	expect(workflow).toContain("workflow_dispatch:");
	expect(workflow).toContain("branches: [main]");
	expect(workflow).toContain("git rev-parse --verify 'HEAD^{commit}'");
	expect(workflow.match(/RELEASE_ID="sha-\$\{SOURCE_COMMIT:0:12\}"/g) ?? []).toHaveLength(1);
	expect(workflow).toContain("vars.PIXIE_RELEASE_ENABLED == 'true'");
	expect(workflow).toContain("--push");
	expect(workflow).toContain("partial publication");
	expect(workflow).toContain("short-ID collision");
	expect(workflow).toContain("workflow_dispatch revision must be a full 40-character commit SHA");
	expect(workflow).toContain("git merge-base --is-ancestor");
	expect(workflow).toContain("REQUESTED_REVISION: $" + "{{ inputs.revision }}");
	expect(workflow).not.toContain("GITHUB_RUN_NUMBER");
	expect(workflow).not.toContain("git describe");
	expect(workflow).not.toContain("pixie-assistant-v");
});

test("validation image builds are read-only and carry the source identity as build arguments", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const validation = workflow.slice(
		workflow.indexOf("\n  image:"),
		workflow.indexOf("\n  publish-image:"),
	);

	expect(validation).toContain("permissions:\n      contents: read");
	expect(validation).not.toContain("packages: write");
	expect(validation).not.toContain("attestations: write");
	expect(validation).not.toContain("id-token: write");
	expect(validation).not.toContain("--push");
	expect(validation).toContain('--build-arg "VERSION=$RELEASE_ID"');
	expect(validation).toContain('--build-arg "REVISION=$SOURCE_COMMIT"');
	expect(workflow).toContain("  publish-image:");
	expect(workflow).toContain("id-token: write");
	expect(workflow).toContain('has("buildx.build.provenance")');
	expect(workflow).toContain("SBOM_PRESENT=false");
	expect(workflow).not.toContain("sbom: true, provenance: true");
	expect(workflow).toContain("verify_archive()");
	expect(workflow).toContain('--version)" == "pixie-assistant $RELEASE_ID');
});

test("release retries compare immutable asset hashes instead of clobbering", async () => {
	const workflow = await readFile(workflowPath, "utf8");

	expect(workflow).toContain('gh api "repos/$GITHUB_REPOSITORY/releases/tags/$RELEASE_ID"');
	expect(workflow).toContain("DOWNLOAD_URL=$(jq -r --arg name");
	expect(workflow).toContain("LOCAL_HASH=$(sha256sum");
	expect(workflow).toContain("REMOTE_HASH=$(sha256sum");
	expect(workflow).toContain("different SHA-256; refusing to overwrite");
	expect(workflow).toContain("already matches ($LOCAL_HASH); skipping upload");
	expect(workflow).not.toContain("--clobber");
	expect(workflow).toContain("isDraft == true");
});

test("validate-only container image carries the source identity as build arguments", async () => {
	const workflow = await readFile(containerWorkflowPath, "utf8");

	expect(workflow).toContain('RELEASE_ID="sha-${SOURCE_COMMIT:0:12}"');
	expect(workflow).toContain('--build-arg "VERSION=${RELEASE_ID}"');
	expect(workflow).toContain('--build-arg "REVISION=${SOURCE_COMMIT}"');
	expect(workflow).not.toContain("--push");
	expect(workflow).not.toContain("packages: write");
});

test("CI invokes the coverage, package and release gates as blocking checks", async () => {
	const workflow = await readFile(ciWorkflowPath, "utf8");

	expect(workflow).toContain("name: Enforce coverage gate");
	expect(workflow).toContain("run: bun run check:coverage");
	expect(workflow).toContain("name: Enforce package artifact gate");
	expect(workflow).toContain("run: bun scripts/check-package-artifacts.ts");
	expect(workflow).toContain("name: Enforce release identity and policy gate");
	expect(workflow).toContain("run: bun scripts/release-gate.ts");
});
