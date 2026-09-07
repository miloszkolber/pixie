import type { Dirent } from "node:fs";
import { readdir } from "node:fs/promises";
import { relative, resolve } from "node:path";

const projectRoot = resolve(import.meta.dir, "..");
const repositoryRoot = resolve(projectRoot, "..");
const roots = [
	resolve(projectRoot, "cmd"),
	resolve(projectRoot, "internal"),
	resolve(repositoryRoot, "pi"),
	resolve(projectRoot, "contracts"),
	resolve(projectRoot, "scripts"),
	resolve(projectRoot, "webui", "scripts"),
	resolve(projectRoot, "webui", "src"),
	resolve(projectRoot, "tests"),
];

const ignoredDirectories = new Set(["coverage", "dist", "node_modules"]);
const checkedExtensions = new Set(["cjs", "js", "jsx", "mjs", "sh", "svelte", "ts", "tsx"]);
const kebabSegment = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function checkDirectoryName(name: string, path: string, errors: string[]): void {
	if (!kebabSegment.test(name)) {
		errors.push(`${path}: directory names must use lowercase kebab-case`);
	}
}

function checkFileName(name: string, path: string, errors: string[]): void {
	const parts = name.split(".");
	const extension = parts.at(-1);
	if (extension === "go") {
		if (!/^[a-z0-9]+(?:[-_][a-z0-9]+)*\.go$/.test(name)) {
			errors.push(`${path}: Go filenames must use lowercase words`);
		}
		return;
	}
	if (!extension || !checkedExtensions.has(extension)) return;

	for (const part of parts.slice(0, -1)) {
		if (!kebabSegment.test(part)) {
			errors.push(`${path}: source filenames must use lowercase kebab-case`);
			return;
		}
	}
}

async function walk(directory: string, errors: string[]): Promise<void> {
	let entries: Dirent[];
	try {
		entries = await readdir(directory, { withFileTypes: true });
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
		throw error;
	}

	for (const entry of entries) {
		if (entry.isDirectory() && ignoredDirectories.has(entry.name)) continue;
		const path = resolve(directory, entry.name);
		const displayPath = relative(repositoryRoot, path).replaceAll("\\", "/");

		if (entry.isDirectory()) {
			checkDirectoryName(entry.name, displayPath, errors);
			await walk(path, errors);
		} else if (entry.isFile()) {
			checkFileName(entry.name, displayPath, errors);
		}
	}
}

const errors: string[] = [];
for (const root of roots) await walk(root, errors);

if (errors.length > 0) {
	console.error(errors.join("\n"));
	process.exit(1);
}

console.log(`check-filenames: OK (${roots.length} source roots)`);
