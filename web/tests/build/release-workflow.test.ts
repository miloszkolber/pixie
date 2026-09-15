import { expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const workflowPath = resolve(import.meta.dir, "../../../.github/workflows/release.yml");
const ciWorkflowPath = resolve(import.meta.dir, "../../../.github/workflows/ci.yml");
const npmWorkflowPath = resolve(import.meta.dir, "../../../.github/workflows/npm-publish.yml");
const assistantManifestPath = resolve(import.meta.dir, "../../../assistant/package.json");
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
	expect(workflow).toContain("docker buildx imagetools create");
	expect(workflow).toContain("partial publication");
	expect(workflow).toContain("short-ID collision");
	expect(workflow).toContain("workflow_dispatch revision must be a full 40-character commit SHA");
	expect(workflow).toContain("git merge-base --is-ancestor");
	expect(workflow).toContain("REQUESTED_REVISION: $" + "{{ inputs.revision }}");
	expect(workflow).not.toContain("GITHUB_RUN_NUMBER");
	expect(workflow).not.toContain("git describe");
	expect(workflow).not.toContain("pixie-assistant-v");
});

test("assistant workspace has no npm publication surface", async () => {
	const npmWorkflowExists = await readFile(npmWorkflowPath, "utf8").then(
		() => true,
		() => false,
	);
	const manifest = JSON.parse(await readFile(assistantManifestPath, "utf8")) as Record<
		string,
		unknown
	>;

	expect(npmWorkflowExists).toBe(false);
	expect(manifest.private).toBe(true);
	expect(manifest).not.toHaveProperty("publishConfig");
	expect(manifest).not.toHaveProperty("bin");
	expect(manifest).not.toHaveProperty("files");
});

test("validation image builds are read-only and carry the source identity as build arguments", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const validation = workflow.slice(
		workflow.indexOf("\n  image:"),
		workflow.indexOf("\n  evidence:"),
	);

	expect(validation).toContain("permissions:\n      contents: read");
	expect(validation).not.toContain("packages: write");
	expect(validation).not.toContain("attestations: write");
	expect(validation).not.toContain("id-token: write");
	expect(validation).not.toContain("--push");
	expect(validation).toContain('--build-arg "VERSION=$RELEASE_ID"');
	expect(validation).toContain('--build-arg "REVISION=$SOURCE_COMMIT"');
	expect(workflow).toContain("  publish-image:");
	expect(workflow).not.toContain("id-token: write");
	expect(workflow).toContain("containerimage.digest");
	expect(workflow).toContain("in-toto.io/predicate-type");
	expect(workflow).toContain("SBOM_PRESENT=false");
	expect(workflow).not.toContain("sbom: true, provenance: true");
	expect(workflow).not.toContain("pixie-assistant");
	expect(workflow).not.toContain("pixie_assistant");
});

test("release retries compare immutable asset hashes instead of clobbering", async () => {
	const workflow = await readFile(workflowPath, "utf8");

	expect(workflow).toContain("--json databaseId");
	expect(workflow).toContain('gh api "repos/$GITHUB_REPOSITORY/releases/$RELEASE_NUM"');
	expect(workflow).toContain("DOWNLOAD_URL=$(jq -r --arg name");
	expect(workflow).toContain("LOCAL_HASH=$(sha256sum");
	expect(workflow).toContain("REMOTE_HASH=$(sha256sum");
	expect(workflow).toContain("different SHA-256; refusing to overwrite");
	expect(workflow).toContain("already matches ($LOCAL_HASH); skipping upload");
	expect(workflow).toContain("complete six-archive payload; refusing to publish");
	expect(workflow).toContain("contains assets outside the complete six-archive payload");
	expect(workflow).not.toContain("--clobber");
	expect(workflow).toContain("isDraft == true");
});

test("validate-only container image carries the source identity as build arguments", async () => {
	const workflow = await readFile(containerWorkflowPath, "utf8");
	const repositoryOwner = "$" + "{GITHUB_REPOSITORY_OWNER}";
	const releaseId = "$" + "{RELEASE_ID}";

	expect(workflow).toContain('RELEASE_ID="sha-${SOURCE_COMMIT:0:12}"');
	expect(workflow).toContain("--target pixie_web");
	expect(workflow).toContain(`--tag "ghcr.io/${repositoryOwner}/pixie_web:${releaseId}"`);
	expect(workflow).toContain('--build-arg "VERSION=${RELEASE_ID}"');
	expect(workflow).toContain('--build-arg "REVISION=${SOURCE_COMMIT}"');
	expect(workflow).not.toContain("--push");
	expect(workflow).not.toContain("packages: write");
});

test("release image references use the immutable pixie_web GHCR package", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const image = jobBlock(workflow, "image");
	const publishImage = jobBlock(workflow, "publish-image");
	const verify = jobBlock(workflow, "verify-publication");
	const repositoryOwner = "$" + "{GITHUB_REPOSITORY_OWNER}";
	const releaseId = "$" + "{RELEASE_ID}";
	const ref = `ghcr.io/${repositoryOwner}/pixie_web:${releaseId}`;

	expect(image).toContain("--target pixie_web");
	expect(image).toContain(`--tag "${ref}"`);
	expect(publishImage).toContain(`REF="${ref}"`);
	expect(publishImage).toContain("docker buildx imagetools create");
	expect(publishImage).toContain("oci-layout://$STAGED_OCI_LAYOUT@$STAGED_IMAGE_INDEX_DIGEST");
	expect(workflow).toContain('"/pixie_web:" + $release_id');
	expect(verify).toContain(`REF="${ref}"`);
	expect(workflow).not.toContain(`/pixie:${releaseId}`);
});

function jobBlock(workflow: string, name: string): string {
	const match = workflow.match(new RegExp(`\\n  ${name}:[\\s\\S]*?(?=\\n  [A-Za-z0-9_-]+:|$)`));
	if (match === null) throw new Error(`missing workflow job ${name}`);
	return match[0];
}

test("static source validation does not run live evidence gates", async () => {
	const workflow = await readFile(ciWorkflowPath, "utf8");

	expect(workflow).not.toContain("check:coverage");
	expect(workflow).not.toContain("check-package-artifacts.ts");
	expect(workflow).not.toContain("check-performance.ts");
	expect(workflow).not.toContain("release-gate.ts");
	expect(workflow).toContain("bun run typecheck");
	expect(workflow).toContain("bun run test");
	// The assistant is Bun-only: no Go assistant module and no bridge sidecar
	// remain to test, while the Bun assistant suite stays mandatory.
	expect(workflow).toContain("bun run --cwd assistant test");
	expect(workflow).not.toContain("Test Go assistant module");
	expect(workflow).not.toContain("assistant/bridge");
});

test("release stages each architecture natively, then merges the exact six-archive set before evidence", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const stageAmd64 = jobBlock(workflow, "stage-amd64");
	const stageArm64 = jobBlock(workflow, "stage-arm64");
	const merge = jobBlock(workflow, "merge");
	const image = jobBlock(workflow, "image");
	const evidence = jobBlock(workflow, "evidence");

	expect(stageAmd64).toContain("needs: [validate, identity]");
	expect(stageAmd64).toContain("runs-on: ubuntu-latest");
	expect(stageAmd64).toContain("build:release --architecture amd64");
	expect(stageAmd64).toContain("checksums-linux-amd64.txt");
	expect(stageArm64).toContain("needs: [validate, identity]");
	expect(stageArm64).toContain("runs-on: ubuntu-24.04-arm");
	expect(stageArm64).toContain("build:release --architecture arm64");
	expect(stageArm64).toContain("checksums-linux-arm64.txt");
	expect(workflow).not.toContain("matrix:");
	expect(merge).toContain("needs: [validate, identity, stage-amd64, stage-arm64]");
	expect(merge).toContain("build:release:merge");
	expect(merge).toContain("checksums.txt");
	expect(merge).toContain("completeSet == true");
	expect(merge).toContain("(.artifacts | length == 6)");
	expect(image).toContain("needs: [validate, identity]");
	expect(evidence).toContain("needs: [validate, identity, merge, image, evidence-arm64]");
	expect(evidence).toContain("pixie-release-${{ needs.identity.outputs.source_commit }}");
	expect(evidence).toContain("pixie-image-${{ needs.identity.outputs.source_commit }}");
	expect(evidence).toContain('run_gate "check:coverage" bun run check:coverage');
	expect(evidence).toContain(
		'run_gate "check-package-artifacts" bun scripts/check-package-artifacts.ts',
	);
	expect(evidence).toContain('run_gate "check-performance" bun scripts/check-performance.ts');
	expect(evidence).toContain('run_gate "release-gate" bun scripts/release-gate.ts');
});

test("release produces native arm64 performance and probe evidence before the gate and publication", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const arm64 = jobBlock(workflow, "evidence-arm64");
	const evidence = jobBlock(workflow, "evidence");
	const publishImage = jobBlock(workflow, "publish-image");

	expect(arm64).toContain("needs: [validate, identity, merge, image]");
	expect(arm64).toContain("runs-on: ubuntu-24.04-arm");
	expect(arm64).toContain("produce-evidence-inputs.ts");
	expect(arm64).toContain("collect-evidence.ts");
	// Probes run against the extracted product tree so each launcher keeps its
	// runtime/ and libexec/ siblings; the bare binaries/ copies are runtime-less.
	expect(arm64).toContain("products/pixie_cli/pixie_cli");
	expect(arm64).toContain("products/pixie/libexec/pixie_full");
	expect(arm64).not.toContain('"$BIN_DIR/pixie_cli"');
	expect(arm64).not.toContain('"$BIN_DIR/pixie"');
	expect(evidence).toContain("products/pixie_cli/pixie_cli");
	expect(evidence).toContain("products/pixie/libexec/pixie_full");
	expect(arm64).not.toContain("--base-url");
	expect(arm64).toContain("pixie-performance-arm64-${{ needs.identity.outputs.source_commit }}");
	expect(arm64).toContain("pixie-probe-evidence-arm64-${{ needs.identity.outputs.source_commit }}");
	// The arm64 job produces evidence only; the live gates run in `evidence`.
	expect(arm64).not.toContain("check-package-artifacts.ts");
	expect(arm64).not.toContain("release-gate.ts");
	expect(evidence).toContain("needs: [validate, identity, merge, image, evidence-arm64]");
	expect(evidence).toContain("merge-performance.ts");
	expect(evidence).toContain("pixie-performance-arm64-${{ needs.identity.outputs.source_commit }}");
	expect(evidence).toContain(
		"pixie-probe-evidence-arm64-${{ needs.identity.outputs.source_commit }}",
	);
	expect(evidence).toContain("--probe-evidence");
	expect(publishImage).toContain("needs: [validate, identity, merge, image, evidence]");

	// The arm64 producer runs before the gate job, which runs before publication.
	const arm64Index = workflow.indexOf("\n  evidence-arm64:");
	const evidenceIndex = workflow.indexOf("\n  evidence:");
	const publishIndex = workflow.indexOf("\n  publish:");
	expect(arm64Index).toBeGreaterThan(-1);
	expect(evidenceIndex).toBeGreaterThan(arm64Index);
	expect(publishIndex).toBeGreaterThan(evidenceIndex);
});

test("publication depends on the passing evidence job and uses the exact source commit", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const publishImage = jobBlock(workflow, "publish-image");
	const publish = jobBlock(workflow, "publish");

	expect(publishImage).toContain("needs: [validate, identity, merge, image, evidence]");
	expect(publish).toContain("needs: [validate, identity, merge, image, evidence, publish-image]");
	expect(publishImage).toContain("vars.PIXIE_RELEASE_ENABLED == 'true'");
	expect(publish).toContain("vars.PIXIE_RELEASE_ENABLED == 'true'");
	expect(workflow).not.toContain("name: pixie-release-${{ github.sha }}");
	expect(workflow).not.toContain("name: pixie-image-${{ github.sha }}");
	expect(workflow).not.toContain("name: pixie-image-evidence-${{ github.sha }}");
});

test("publication rejects a post-evidence Dockerfile rebuild and pushes the exact staged OCI index", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const publishImage = jobBlock(workflow, "publish-image");

	expect(publishImage).toContain("Download evidence-gated OCI image artifact");
	expect(publishImage).toContain("Download evidence gate bundle");
	expect(publishImage).toContain("pixie-image-${{ needs.identity.outputs.source_commit }}");
	expect(publishImage).toContain("pixie-evidence-${{ needs.identity.outputs.source_commit }}");
	expect(publishImage).toContain("EVIDENCE_ARCHIVE_SHA256");
	expect(publishImage).toContain("EVIDENCE_LAYOUT_DIGEST");
	expect(publishImage).toContain("STAGED_IMAGE_INDEX_DIGEST");
	expect(publishImage).toContain('[[ "$INDEX_DIGEST" == "$STAGED_IMAGE_INDEX_DIGEST" ]]');
	expect(publishImage).toContain('[[ "$PUBLISHED_INDEX_DIGEST" == "$STAGED_IMAGE_INDEX_DIGEST" ]]');
	expect(publishImage).toContain("docker buildx imagetools create");
	expect(publishImage).toContain("oci-layout://$STAGED_OCI_LAYOUT@$STAGED_IMAGE_INDEX_DIGEST");
	expect(publishImage).not.toContain("docker buildx build");
	expect(publishImage).not.toContain("web/Dockerfile");
	expect(publishImage).not.toContain("--build-arg");
});

test("publication is followed by an explicit post-publish verification job", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const verify = jobBlock(workflow, "verify-publication");

	expect(verify).toContain("needs: [identity, merge, evidence, publish-image, publish]");
	expect(verify).toContain(
		"if: github.event_name == 'push' && github.ref == 'refs/heads/main' && vars.PIXIE_RELEASE_ENABLED == 'true'",
	);
	expect(verify).toContain("runs-on: ubuntu-latest");
	expect(verify).toContain("contents: read");
	expect(verify).toContain("packages: read");
	expect(verify).toContain("refs/tags/");
	expect(verify).toContain("releases/tags/");
	expect(verify).toContain("pixie-image-evidence-");
	expect(verify).toContain("docker buildx imagetools inspect --raw");
	expect(verify).toContain("vnd.docker.reference.type");
	expect(verify).toContain("in-toto.io/predicate-type");
	// The verifier runs only after publication, so it cannot gate the release.
	expect(verify).toContain("cannot block the release");
	expect(verify).toContain("exit 1");

	const publishIndex = workflow.indexOf("\n  publish:");
	const verifyIndex = workflow.indexOf("\n  verify-publication:");
	expect(publishIndex).toBeGreaterThan(-1);
	expect(verifyIndex).toBeGreaterThan(publishIndex);
});
