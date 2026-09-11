import { createHash } from "node:crypto";
import { existsSync, statSync } from "node:fs";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, extname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { compile } from "svelte/compiler";

const repositoryRoot = resolve(import.meta.dir, "../../..");
const webuiRoot = join(repositoryRoot, "webui");
const modules = new Map<string, Promise<unknown>>();
let ssrDirectory: Promise<string> | undefined;

function ssrTempDirectory(): Promise<string> {
	const pending = ssrDirectory ?? (ssrDirectory = mkdtemp(join(tmpdir(), "pixie-webui-ssr-")));
	return pending.then(async (directory) => {
		await mkdir(directory, { recursive: true });
		return directory;
	});
}

function ssrFileName(specifier: string): string {
	const slug =
		specifier
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, "-")
			.replace(/^-+|-+$/g, "")
			.slice(0, 80) || "component";
	const digest = createHash("sha256").update(specifier).digest("hex").slice(0, 12);
	return `${slug}-${digest}.mjs`;
}

function resolveSource(path: string): string | undefined {
	const candidates = extname(path)
		? [path]
		: [
				path,
				`${path}.ts`,
				`${path}.svelte`,
				`${path}.js`,
				join(path, "index.ts"),
				join(path, "index.svelte"),
				join(path, "index.js"),
			];
	return candidates.find((candidate) => {
		try {
			return existsSync(candidate) && statSync(candidate).isFile();
		} catch {
			return false;
		}
	});
}

function componentModule(path: string): Promise<unknown> {
	let pending = modules.get(path);
	if (pending) return pending;
	pending = (async () => {
		const entrypoint = path.startsWith("tests/")
			? join(repositoryRoot, path)
			: join(webuiRoot, path);
		const virtualEntry = "pixie-svelte-ssr-entry";
		const result = await Bun.build({
			entrypoints: [virtualEntry],
			target: "bun",
			format: "esm",
			plugins: [
				{
					name: "test-svelte-server-entry",
					setup(build) {
						build.onResolve({ filter: /^pixie-svelte-ssr-entry$/ }, () => ({
							path: virtualEntry,
							namespace: "pixie-svelte-test",
						}));
						build.onLoad({ filter: /.*/, namespace: "pixie-svelte-test" }, () => ({
							contents: `import Component from ${JSON.stringify(entrypoint)};\nimport { render } from "svelte/server";\nexport default (props) => render(Component, { props }).body;`,
							loader: "js",
						}));
					},
				},
				{
					name: "test-webui-alias",
					setup(build) {
						build.onResolve({ filter: /^@\// }, ({ path: specifier }) => {
							const resolved = resolveSource(join(webuiRoot, "src", specifier.slice(2)));
							return resolved ? { path: resolved } : undefined;
						});
						build.onResolve({ filter: /^\.{1,2}\// }, ({ importer, path: specifier }) => {
							if (
								!importer.startsWith(webuiRoot) &&
								!importer.startsWith(join(repositoryRoot, "tests"))
							) {
								return undefined;
							}
							const resolved = resolveSource(resolve(dirname(importer), specifier));
							return resolved ? { path: resolved } : undefined;
						});
					},
				},
				{
					name: "test-svelte-server",
					setup(build) {
						// The virtual entry otherwise resolves from cwd and can load a second
						// Svelte runtime with a separate component-context stack.
						build.onResolve({ filter: /^svelte(?:\/|$)/ }, ({ path: specifier }) => ({
							path: Bun.resolveSync(specifier, import.meta.dir),
						}));
						build.onLoad({ filter: /\.svelte$/ }, async ({ path: filename }) => ({
							contents: compile(await Bun.file(filename).text(), {
								filename,
								generate: "server",
								css: "injected",
							}).js.code,
							loader: "js",
						}));
					},
				},
			],
		});
		if (!result.success) throw new Error(result.logs.map(String).join("\n"));
		const output = result.outputs.find((artifact) => artifact.kind === "entry-point");
		if (!output) throw new Error(`No server bundle was produced for ${path}.`);
		const source = await output.text();
		// Bun 1.3.14 fails large data-URL imports with NameTooLong because the
		// bundled SSR output (often 100+ KiB) exceeds the resolvable specifier
		// length. Write the already-bundled output to a disposable temp file and
		// import it by file URL instead. The bundle is self-contained, so the
		// temp location does not change module resolution.
		const directory = await ssrTempDirectory();
		const filename = ssrFileName(path);
		const file = join(directory, filename);
		await writeFile(file, source, "utf8");
		return import(pathToFileURL(file).href);
	})();
	modules.set(path, pending);
	return pending;
}

export async function renderSvelte(path: string, props: object): Promise<string> {
	const module = (await componentModule(path)) as {
		default: (props: object) => string;
	};
	return module.default(props);
}
