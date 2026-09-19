import { expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const workflowPath = resolve(import.meta.dir, "../../.github/workflows/container-images.yml");

test("container validation builds the immutable Pixie controller image without publication", async () => {
	const workflow = await readFile(workflowPath, "utf8");
	const repositoryOwner = "$" + "{GITHUB_REPOSITORY_OWNER}";
	const releaseId = "$" + "{RELEASE_ID}";

	expect(workflow).toContain("--target pixie_web");
	expect(workflow).not.toContain("--target pixie ");
	expect(workflow).toContain("--platform linux/amd64,linux/arm64");
	expect(workflow).toContain(`ghcr.io/${repositoryOwner}/pixie_web:${releaseId}`);
	expect(workflow).not.toContain(`ghcr.io/${repositoryOwner}/pixie:${releaseId}`);
	expect(workflow).toContain("--output=type=oci");
	expect(workflow).not.toContain("--push");
	expect(workflow).not.toContain("packages: write");
});
