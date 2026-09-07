import { existsSync, statSync } from "node:fs";
import { dirname, extname, join, resolve } from "node:path";
import { compile } from "svelte/compiler";

const repositoryRoot = resolve(import.meta.dir, "../../..");
const webuiRoot = join(repositoryRoot, "webui");
const modules = new Map<string, Promise<unknown>>();

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
		return import(`data:text/javascript;base64,${Buffer.from(source).toString("base64")}`);
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
