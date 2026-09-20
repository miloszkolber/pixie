import { mkdir, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { gzipSync } from "node:zlib";
import type { BunPlugin } from "bun";
import { sveltePlugin } from "../vendor/mewa-svelte/bun-plugin.js";

const webRoot = resolve(import.meta.dir, "..");
const sourceMewaCss = join(webRoot, "src", "mewa.css");

export interface WebBuildOptions {
	outputRoot?: string;
	intermediateRoot?: string;
	development?: boolean;
}

async function flattenMewaCss(path: string, visited = new Set<string>()): Promise<string> {
	const canonicalPath = resolve(path);
	if (visited.has(canonicalPath)) return "";
	visited.add(canonicalPath);
	const source = await readFile(canonicalPath, "utf8");
	const imports = [...source.matchAll(/^\s*@import\s+["']([^"']+)["'];\s*$/gm)];
	let flattened = "";
	for (const match of imports) {
		const specifier = match[1];
		if (!specifier?.startsWith(".")) {
			throw new Error(`Mewa CSS may only import local package entries: ${specifier ?? ""}`);
		}
		flattened += await flattenMewaCss(resolve(dirname(canonicalPath), specifier), visited);
	}
	const body = source.replace(/^\s*@import\s+["'][^"']+["'];\s*$/gm, "").trim();
	return `${flattened}${body ? `${body}\n` : ""}`;
}

export async function buildWeb(options: WebBuildOptions = {}): Promise<number> {
	const outputRoot = resolve(options.outputRoot ?? join(webRoot, "dist"));
	const intermediateRoot = resolve(options.intermediateRoot ?? join(webRoot, ".build"));
	const compiledMewaCss = join(intermediateRoot, "mewa.css");
	const development = options.development ?? false;
	const mewaCssPlugin: BunPlugin = {
		name: "mewa-css",
		setup(build) {
			build.onLoad({ filter: /\/src\/mewa\.css$/ }, async () => ({
				contents: await readFile(compiledMewaCss, "utf8"),
				loader: "css",
			}));
		},
	};

	// Keep the tracked embed placeholder: building must not dirty the checkout
	// or temporarily remove the directory required by Go's //go:embed pattern.
	await mkdir(outputRoot, { recursive: true });
	for (const name of await readdir(outputRoot)) {
		if (name !== ".gitkeep") await rm(join(outputRoot, name), { force: true, recursive: true });
	}
	await rm(intermediateRoot, { force: true, recursive: true });
	await mkdir(intermediateRoot, { recursive: true });
	await writeFile(compiledMewaCss, await flattenMewaCss(sourceMewaCss));

	const result = await Bun.build({
		entrypoints: [join(webRoot, "index.html")],
		outdir: outputRoot,
		publicPath: "/",
		target: "browser",
		format: "esm",
		loader: { ".svg": "file", ".woff2": "file" },
		minify: !development,
		splitting: true,
		metafile: true,
		plugins: [sveltePlugin({ dev: development }), mewaCssPlugin],
		throw: false,
	});

	if (!result.success || !result.metafile) {
		for (const log of result.logs) console.error(log);
		throw new AggregateError(result.logs, "Web UI build failed");
	}

	const outputs: Record<string, { entryPoint?: string; [key: string]: unknown }> =
		Object.fromEntries(
			result.outputs.map((artifact) => {
				const path = relative(outputRoot, artifact.path).replaceAll("\\", "/");
				const sourceMetadata = (result.metafile?.outputs[path] ??
					result.metafile?.outputs[`./${path}`] ??
					result.metafile?.outputs[artifact.path] ??
					{}) as { entryPoint?: string; [key: string]: unknown };
				const metadata = {
					...sourceMetadata,
					...(sourceMetadata.entryPoint &&
					resolve(sourceMetadata.entryPoint) === join(webRoot, "index.html")
						? { entryPoint: "index.html" }
						: {}),
				};
				return [path, metadata];
			}),
		);
	// Bun's HTML entry rewriting can point the document at a non-entry chunk when
	// splitting emits many outputs; that chunk evaluates cleanly but never mounts the
	// application, so the shipped page stays blank while every existence check passes.
	// Pin the document to the manifest entry before hashing companions.
	const entryScripts = Object.entries(outputs).filter(
		([path, metadata]) => path.endsWith(".js") && metadata.entryPoint === "index.html",
	);
	if (entryScripts.length !== 1) {
		throw new Error(`Expected one Web UI JavaScript entry bundle, found ${entryScripts.length}`);
	}
	const entryScript = entryScripts[0]?.[0] ?? "";
	const builtIndex = join(outputRoot, "index.html");
	const builtDocument = await readFile(builtIndex, "utf8");
	if (!/<script[^>]*\btype="module"[^>]*\bsrc="[^"]*"[^>]*>/.test(builtDocument)) {
		throw new Error("Web UI entry script is missing from the built index.html");
	}
	const rewrittenDocument = builtDocument.replace(
		/<script([^>]*)\bsrc="[^"]*"([^>]*)>/g,
		(match, before, after) =>
			/\btype="module"/.test(match) ? `<script${before}src="/${entryScript}"${after}>` : match,
	);
	if (!rewrittenDocument.includes(`src="/${entryScript}"`)) {
		throw new Error(`Web UI index.html does not load the entry bundle ${entryScript}`);
	}
	await writeFile(builtIndex, rewrittenDocument);

	for (const path of Object.keys(outputs)) {
		if (!/\.(?:css|js)$/.test(path)) continue;
		const source = await readFile(join(outputRoot, path));
		if (source.byteLength < 1_024) continue;
		const compressed = gzipSync(source, { level: 9 });
		if (compressed.byteLength >= source.byteLength) continue;
		const compressedPath = `${path}.gz`;
		await writeFile(join(outputRoot, compressedPath), compressed);
		outputs[compressedPath] = { precompressedFor: path };
	}

	const manifest = {
		schemaVersion: 1,
		entrypoint: "index.html",
		outputs,
	};
	await writeFile(
		join(intermediateRoot, "bundle-manifest.json"),
		`${JSON.stringify(manifest, null, 2)}\n`,
	);
	console.log(`web-build: ${Object.keys(outputs).length} artifacts written with Bun`);
	return Object.keys(outputs).length;
}

if (import.meta.main) await buildWeb();
