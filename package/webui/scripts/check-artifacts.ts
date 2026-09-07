import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, extname, join, normalize, relative } from "node:path";
import { gunzipSync } from "node:zlib";

interface ManifestOutput {
	imports?: { path: string }[];
	precompressedFor?: string;
}

interface BundleManifest {
	outputs: Record<string, ManifestOutput>;
}

const defaultOutputRoot = join(import.meta.dir, "..", "dist");
const defaultManifestPath = join(import.meta.dir, "..", ".build", "bundle-manifest.json");

export function checkArtifacts(
	outputRoot = defaultOutputRoot,
	manifestPath = defaultManifestPath,
): number {
	const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as BundleManifest;
	const failures: string[] = [];

	function filesBelow(root: string): string[] {
		const files: string[] = [];
		for (const entry of readdirSync(root, { withFileTypes: true })) {
			const path = join(root, entry.name);
			if (entry.isDirectory()) files.push(...filesBelow(path));
			else if (entry.isFile()) files.push(relative(outputRoot, path).replaceAll("\\", "/"));
		}
		return files;
	}

	function localReference(owner: string, reference: string): string | null {
		if (/^(?:data:|#)/i.test(reference)) return null;
		const clean = reference.split(/[?#]/, 1)[0]?.replace(/^\/+/, "");
		if (!clean) return null;
		const path = normalize(join(dirname(owner), clean))
			.replaceAll("\\", "/")
			.replace(/^\.\//, "");
		if (path === ".." || path.startsWith("../")) {
			failures.push(`${owner} references a path outside the artifact: ${reference}`);
			return null;
		}
		return path;
	}

	function checkReference(owner: string, reference: string): void {
		const value = reference.trim();
		if (!value) return;
		if (/^(?:https?:)?\/\//i.test(value)) {
			failures.push(`${owner} contains an external runtime URL: ${value}`);
			return;
		}
		if (owner === "index.html" && !/^(?:data:|#|\/)/i.test(value)) {
			failures.push(
				`${owner} contains a document-relative asset that breaks nested application routes: ${value}`,
			);
		}
		const path = localReference(owner, value);
		if (path && !existsSync(join(outputRoot, path)))
			failures.push(`${owner} references missing ${path}`);
	}

	const files = filesBelow(outputRoot);
	if (!files.includes("index.html")) failures.push("missing top-level index.html");
	if (files.some((file) => /\.(?:ts|tsx|svelte)$/.test(file))) {
		failures.push("source TypeScript or Svelte files leaked into dist");
	}
	if (files.some((file) => /(?:^|\/)bundle-manifest\.json$/.test(file))) {
		failures.push("build metadata leaked into the public artifact");
	}

	const expected = new Set(Object.keys(manifest.outputs));
	for (const file of files) {
		if (!expected.has(file)) failures.push(`unexpected output file: ${file}`);
	}
	for (const file of expected) {
		if (!files.includes(file)) failures.push(`manifest output is missing: ${file}`);
	}

	for (const [owner, output] of Object.entries(manifest.outputs)) {
		for (const imported of output.imports ?? []) {
			const path = localReference(owner, imported.path);
			if (path && !expected.has(path)) failures.push(`${owner} imports missing ${path}`);
		}
	}

	for (const [file, output] of Object.entries(manifest.outputs)) {
		if (output.precompressedFor) {
			const original = output.precompressedFor;
			if (!file.endsWith(".gz") || file !== `${original}.gz` || !expected.has(original)) {
				failures.push(`${file} has invalid precompressed source: ${original}`);
				continue;
			}
			try {
				const compressed = readFileSync(join(outputRoot, file));
				const source = readFileSync(join(outputRoot, original));
				if (compressed.byteLength >= source.byteLength)
					failures.push(`${file} does not reduce ${original}`);
				if (!gunzipSync(compressed).equals(source))
					failures.push(`${file} does not decode to ${original}`);
			} catch {
				failures.push(`${file} is not a valid gzip companion for ${original}`);
			}
		}
		if (
			/\.(?:css|js)$/.test(file) &&
			statSync(join(outputRoot, file)).size >= 1_024 &&
			manifest.outputs[`${file}.gz`]?.precompressedFor !== file
		) {
			failures.push(`${file} is missing its gzip companion`);
		}
	}

	for (const owner of files.filter((file) => [".html", ".css"].includes(extname(file)))) {
		const source = readFileSync(join(outputRoot, owner), "utf8");
		const patterns =
			extname(owner) === ".html"
				? [/\b(?:href|src)=["']([^"']+)["']/g]
				: [
						/url\(\s*["']?([^"')]+)["']?\s*\)/g,
						/@import\s+(?:url\(\s*)?["']?([^"')\s;]+)["']?\s*\)?[^;]*;/g,
					];
		for (const pattern of patterns) {
			for (const match of source.matchAll(pattern)) {
				checkReference(owner, match[1] ?? "");
			}
		}
	}

	for (const file of files) {
		if (statSync(join(outputRoot, file)).size === 0) failures.push(`empty output file: ${file}`);
	}

	if (failures.length > 0) throw new Error(`Invalid Web UI artifact:\n${failures.join("\n")}`);
	return files.length;
}

if (import.meta.main) {
	const files = checkArtifacts();
	console.log(`artifact-check: OK (${files} files)`);
}
