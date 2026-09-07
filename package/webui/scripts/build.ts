import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { gzipSync } from "node:zlib";
import type { BunPlugin } from "bun";
import { sveltePlugin } from "../vendor/mewa-svelte/bun-plugin.js";

const webRoot = resolve(import.meta.dir, "..");
const sourceCss = join(webRoot, "src", "index.css");
const sourceMewaCss = join(webRoot, "src", "mewa.css");

export interface WebBuildOptions {
	outputRoot?: string;
	intermediateRoot?: string;
	development?: boolean;
}

async function compileCss(
	intermediateRoot: string,
	compiledCss: string,
	development: boolean,
): Promise<void> {
	await mkdir(intermediateRoot, { recursive: true });
	const tailwindCli = join(
		dirname(Bun.resolveSync("@tailwindcss/cli/package.json", webRoot)),
		"dist/index.mjs",
	);
	const compiler = Bun.spawn(
		[
			process.execPath,
			tailwindCli,
			"-i",
			sourceCss,
			"-o",
			compiledCss,
			...(development ? [] : ["--minify"]),
		],
		{ cwd: webRoot, stdout: "inherit", stderr: "inherit" },
	);
	if ((await compiler.exited) !== 0) throw new Error("Tailwind CSS compilation failed");
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
	const compiledCss = join(intermediateRoot, "index.css");
	const compiledMewaCss = join(intermediateRoot, "mewa.css");
	const development = options.development ?? false;
	const compiledCssPlugin: BunPlugin = {
		name: "pixie-css",
		setup(build) {
			build.onLoad({ filter: /\/src\/index\.css$/ }, async () => ({
				contents: await readFile(compiledCss, "utf8"),
				loader: "css",
			}));
		},
	};
	const mewaCssPlugin: BunPlugin = {
		name: "mewa-css",
		setup(build) {
			build.onLoad({ filter: /\/src\/mewa\.css$/ }, async () => ({
				contents: await readFile(compiledMewaCss, "utf8"),
				loader: "css",
			}));
		},
	};

	await rm(outputRoot, { force: true, recursive: true });
	await rm(intermediateRoot, { force: true, recursive: true });
	await compileCss(intermediateRoot, compiledCss, development);
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
		plugins: [sveltePlugin({ dev: development }), compiledCssPlugin, mewaCssPlugin],
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
