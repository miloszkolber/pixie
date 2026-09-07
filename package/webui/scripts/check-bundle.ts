import { readFileSync, statSync } from "node:fs";
import { dirname, join, normalize } from "node:path";

interface OutputImport {
	path: string;
	kind: string;
}

interface ManifestOutput {
	entryPoint?: string;
	imports?: OutputImport[];
}

interface BundleManifest {
	schemaVersion: number;
	entrypoint: string;
	outputs: Record<string, ManifestOutput>;
}

const MAX_INITIAL_JAVASCRIPT_BYTES = 500_000;
const manifest = JSON.parse(
	readFileSync(join(".build", "bundle-manifest.json"), "utf8"),
) as BundleManifest;
if (manifest.schemaVersion !== 1 || manifest.entrypoint !== "index.html") {
	throw new Error("Unsupported Bun bundle manifest");
}

const entries = Object.entries(manifest.outputs).filter(
	([path, output]) => path.endsWith(".js") && output.entryPoint === manifest.entrypoint,
);
if (entries.length !== 1) {
	throw new Error(`Expected one Web UI JavaScript entry bundle, found ${entries.length}`);
}

function importedPath(owner: string, imported: string): string {
	return normalize(join(dirname(owner), imported))
		.replaceAll("\\", "/")
		.replace(/^\.\//, "");
}

const visited = new Set<string>();
function javascriptBytes(path: string): number {
	if (visited.has(path)) return 0;
	visited.add(path);
	const output = manifest.outputs[path];
	if (!output) throw new Error(`Bundle manifest references missing output: ${path}`);
	const own = path.endsWith(".js") ? statSync(join("dist", path)).size : 0;
	return (
		own +
		(output.imports ?? [])
			.filter((dependency) => dependency.kind !== "dynamic-import")
			.reduce(
				(total, dependency) => total + javascriptBytes(importedPath(path, dependency.path)),
				0,
			)
	);
}

const initialBytes = javascriptBytes(entries[0]?.[0] ?? "");
if (initialBytes > MAX_INITIAL_JAVASCRIPT_BYTES) {
	throw new Error(
		`Initial JavaScript is ${initialBytes} bytes; budget is ${MAX_INITIAL_JAVASCRIPT_BYTES} bytes`,
	);
}
console.log(`bundle-budget: ${initialBytes}/${MAX_INITIAL_JAVASCRIPT_BYTES} initial JS bytes`);
