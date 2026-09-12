import { expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, stat, writeFile } from "node:fs/promises";
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

test("index.html must load the declared JavaScript entry bundle", async () => {
	const root = await mkdtemp(join(tmpdir(), "pixie-entry-"));
	const outputRoot = join(root, "dist");
	const manifestPath = join(root, "bundle-manifest.json");
	await mkdir(outputRoot);
	// A wrong-but-present chunk evaluates cleanly yet never mounts the app, so the
	// gate must reject it even though every referenced file exists.
	await Bun.write(join(outputRoot, "chunk-entry.js"), "export {};\n");
	await Bun.write(join(outputRoot, "chunk-other.js"), "export {};\n");
	await writeFile(
		manifestPath,
		JSON.stringify({
			entrypoint: "index.html",
			outputs: {
				"index.html": {},
				"chunk-entry.js": { entryPoint: "index.html" },
				"chunk-other.js": {},
			},
		}),
	);
	try {
		await Bun.write(
			join(outputRoot, "index.html"),
			'<script type="module" src="/chunk-other.js"></script>',
		);
		expect(() => checkArtifacts(outputRoot, manifestPath)).toThrow(
			"index.html does not load the Web UI JavaScript entry bundle: chunk-entry.js",
		);
		await Bun.write(
			join(outputRoot, "index.html"),
			'<script type="module" src="/chunk-entry.js"></script>',
		);
		expect(checkArtifacts(outputRoot, manifestPath)).toBe(3);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
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
			entrypoint: string;
			outputs: Record<string, { entryPoint?: string; precompressedFor?: string }>;
		};
		const artifactCount = Object.keys(manifest.outputs).length;
		expect(checkArtifacts(outputRoot, manifestPath)).toBe(artifactCount);
		const entry = Object.entries(manifest.outputs).find(
			([path, output]) => path.endsWith(".js") && output.entryPoint === manifest.entrypoint,
		)?.[0];
		expect(entry).toBeString();
		const builtIndex = await Bun.file(join(outputRoot, "index.html")).text();
		expect(builtIndex).toContain(`src="/${entry}"`);
		expect(
			Object.values(manifest.outputs).some((output) => output.precompressedFor !== undefined),
		).toBeTrue();
	} finally {
		await rm(root, { recursive: true, force: true });
	}
}, 20_000);

test("the image build never depends on checkout-volatile web output", async () => {
	// `bun run build` wipes package/webui/dist (including the tracked .gitkeep),
	// so a COPY of anything under dist/ breaks every container build that follows
	// a local web build. The Go embed only needs the directory to exist.
	const dockerfile = await Bun.file(new URL("../../Dockerfile", import.meta.url)).text();
	expect(dockerfile).toContain("mkdir -p webui/dist");
	// COPY sources resolve against the build context (the repo root), one level
	// above this package.
	const context = new URL("../../../", import.meta.url);
	for (const line of dockerfile.split("\n")) {
		expect(line.startsWith("COPY package/webui/dist/")).toBeFalse();
		// Every context COPY source must exist in the checkout: the committed
		// assistant migration dropped assistant/scripts/ while the Dockerfile
		// still copied it, breaking all image builds with a checksum error.
		const tokens = line.trim().split(/\s+/);
		if (tokens[0] !== "COPY" || tokens.some((token) => token.startsWith("--from"))) continue;
		const operands = tokens.slice(1).filter((token) => !token.startsWith("--"));
		for (const source of operands.slice(0, -1)) {
			if (source.includes("*")) continue;
			const present = await stat(new URL(source, context)).then(
				() => true,
				() => false,
			);
			expect(present, `Dockerfile copies missing path: ${source}`).toBeTrue();
		}
	}
});
