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

test("legacy assistant has no npm publication surface", async () => {
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
	expect(workflow).toContain("name: Test Go assistant module");
});

test("release stages exact-commit artifacts before evidence and does not gate staging on it", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const stage = jobBlock(workflow, "stage");
	const image = jobBlock(workflow, "image");
	const evidence = jobBlock(workflow, "evidence");

	expect(stage).toContain("needs: [validate, identity]");
	expect(image).toContain("needs: [validate, identity]");
	expect(evidence).toContain("needs: [validate, identity, stage, image]");
	expect(evidence).toContain("pixie-release-${{ needs.identity.outputs.source_commit }}");
	expect(evidence).toContain("pixie-image-${{ needs.identity.outputs.source_commit }}");
	expect(evidence).toContain('run_gate "check:coverage" bun run check:coverage');
	expect(evidence).toContain(
		'run_gate "check-package-artifacts" bun scripts/check-package-artifacts.ts',
	);
	expect(evidence).toContain('run_gate "check-performance" bun scripts/check-performance.ts');
	expect(evidence).toContain('run_gate "release-gate" bun scripts/release-gate.ts');
});

test("publication depends on the passing evidence job and uses the exact source commit", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const publishImage = jobBlock(workflow, "publish-image");
	const publish = jobBlock(workflow, "publish");

	expect(publishImage).toContain("needs: [validate, identity, stage, image, evidence]");
	expect(publish).toContain("needs: [validate, identity, stage, image, evidence, publish-image]");
	expect(publishImage).toContain("vars.PIXIE_RELEASE_ENABLED == 'true'");
	expect(publish).toContain("vars.PIXIE_RELEASE_ENABLED == 'true'");
	expect(workflow).not.toContain("name: pixie-release-${{ github.sha }}");
	expect(workflow).not.toContain("name: pixie-image-${{ github.sha }}");
	expect(workflow).not.toContain("name: pixie-image-evidence-${{ github.sha }}");
});
