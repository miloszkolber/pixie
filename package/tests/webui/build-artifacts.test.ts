import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkArtifacts } from "../../webui/scripts/check-artifacts";

async function withArtifact(
	index: string,
	run: (outputRoot: string, manifestPath: string) => void,
	javaScript = "export {};\n",
): Promise<void> {
	const root = await mkdtemp(join(tmpdir(), "pixie-artifact-"));
	const outputRoot = join(root, "dist");
	const manifestPath = join(root, "bundle-manifest.json");
	await mkdir(outputRoot);
	await Bun.write(join(outputRoot, "index.html"), index);
	await Bun.write(join(outputRoot, "chunk-abc12345.js"), javaScript);
	await writeFile(
		manifestPath,
		JSON.stringify({ outputs: { "index.html": {}, "chunk-abc12345.js": {} } }),
	);
	try {
		run(outputRoot, manifestPath);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
}

test("entry assets remain resolvable when the application document is served from a nested route", async () => {
	await withArtifact(
		'<script type="module" src="/chunk-abc12345.js"></script>',
		(outputRoot, manifestPath) => expect(checkArtifacts(outputRoot, manifestPath)).toBe(2),
	);
	await withArtifact(
		'<script type="module" src="./chunk-abc12345.js"></script>',
		(outputRoot, manifestPath) =>
			expect(() => checkArtifacts(outputRoot, manifestPath)).toThrow(
				"document-relative asset that breaks nested application routes",
			),
	);
});

test("large scripts require verified gzip companions", async () => {
	await withArtifact(
		'<script type="module" src="/chunk-abc12345.js"></script>',
		(outputRoot, manifestPath) =>
			expect(() => checkArtifacts(outputRoot, manifestPath)).toThrow("missing its gzip companion"),
		"x".repeat(1_024),
	);
});

test("development builds satisfy the artifact contract with gzip companions", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-development-artifact-"));
	const outputRoot = join(root, "dist");
	const intermediateRoot = join(root, "intermediate");
	const manifestPath = join(intermediateRoot, "bundle-manifest.json");
	try {
		const buildModule = new URL("../../webui/scripts/build.ts", import.meta.url).href;
		const script = `const { buildWeb } = await import(${JSON.stringify(buildModule)}); await buildWeb(${JSON.stringify({ outputRoot, intermediateRoot, development: true })});`;
		const build = Bun.spawn([process.execPath, "-e", script], {
			cwd: join(import.meta.dir, "../.."),
			stdout: "pipe",
			stderr: "pipe",
		});
		const [exitCode, stdout, stderr] = await Promise.all([
			build.exited,
			new Response(build.stdout).text(),
			new Response(build.stderr).text(),
		]);
		if (exitCode !== 0) {
			throw new Error(`Development build failed:\n${stdout}${stderr}`);
		}

		const manifest = (await Bun.file(manifestPath).json()) as {
			outputs: Record<string, { precompressedFor?: string }>;
		};
		const artifactCount = Object.keys(manifest.outputs).length;
		expect(checkArtifacts(outputRoot, manifestPath)).toBe(artifactCount);
		expect(
			Object.values(manifest.outputs).some((output) => output.precompressedFor !== undefined),
		).toBeTrue();
	} finally {
		await rm(root, { recursive: true, force: true });
	}
}, 20_000);
