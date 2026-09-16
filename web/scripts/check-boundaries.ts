#!/usr/bin/env bun

/**
 * AUX-22 — Module-boundary and banned-dependency gate.
 *
 * Enumerates the allowed edges between the three source roots (`assistant/`,
 * `shared/`, `web/`) and fails when a checked file crosses a forbidden edge:
 *
 *   assistant -> assistant, shared   (never web)
 *   shared    -> shared               (never web or assistant)
 *   web       -> web, shared          (never assistant)
 *
 * The check parses relative and workspace-package imports in TypeScript,
 * Svelte and Go sources, plus JSON dependency manifests. Every violation names
 * the file and the import line. External packages (anything not one of the
 * three roots) are always allowed; this gate owns only the internal edges and
 * the banned-dependency list.
 *
 * Banned dependencies are matched against manifests, import specifiers and
 * style directives. Tailwind is forbidden by AGENTS.md: new frontend work must
 * not add Tailwind dependencies or utilities.
 *
 * Exact-pin drift is already enforced by `check-catalog.ts`, and generated
 * artifact drift in `shared/` by `check-contracts.ts`; this gate does not
 * duplicate them. It does add the cross-module half those checks lack: a
 * generated artifact must not be reachable across a forbidden edge, so an
 * assistant import of `shared/src/generated` is allowed while an assistant
 * import of any `web/` internal is not.
 *
 * Deterministic and bounded: it never reads `node_modules`, `dist` or
 * `.build`, and exits non-zero when any violation is reported.
 */

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { dirname, relative, resolve, sep } from "node:path";

const repositoryRoot = resolve(import.meta.dir, "../..");
const sourceRootNames = ["assistant", "shared", "web"] as const;
type SourceRoot = (typeof sourceRootNames)[number];

const ignoredDirectories = new Set([
	".build",
	".git",
	".tmp-work",
	"coverage",
	"dist",
	"node_modules",
]);

const checkedExtensions = new Set([
	".cjs",
	".css",
	".go",
	".js",
	".jsx",
	".mjs",
	".pcss",
	".svelte",
	".ts",
	".tsx",
]);

/** Workspace package names map to their owning source root. */
const workspacePackages: readonly { readonly base: string; readonly root: SourceRoot }[] = [
	{ base: "@pixie_ai/pixie-assistant", root: "assistant" },
	{ base: "@pixie/web", root: "web" },
	{ base: "@pixie/shared", root: "shared" },
];

/** Go module paths map to their owning source root. The shared module is a
 * subpath of the web module path, so it must be tested first. */
const goModulePaths: readonly { readonly base: string; readonly root: SourceRoot }[] = [
	{ base: "github.com/miloszkolber/pixie/shared", root: "shared" },
	{ base: "github.com/miloszkolber/pixie", root: "web" },
];

const allowedEdges: Readonly<Record<SourceRoot, readonly SourceRoot[]>> = {
	assistant: ["assistant", "shared"],
	shared: ["shared"],
	web: ["shared", "web"],
};

const bannedDependencyPrefixes = ["tailwindcss", "@tailwindcss/", "tailwind"];

// Built from parts so the checker's own source text cannot match the directive.
const tailwindDirective = new RegExp(`@${"tail"}${"wind"}\\b`);
const bannedDirectives = [tailwindDirective, /@import\s+["']tailwindcss["']/];

interface Violation {
	readonly file: string;
	readonly line: number;
	readonly message: string;
}

/** Optional root override used by the seeded-violation regression test. */
export interface BoundaryCheckOptions {
	readonly root?: string;
}

function displayPath(root: string, path: string): string {
	return relative(root, path).split(sep).join("/");
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isErrnoLike(value: unknown): value is { readonly code?: unknown } {
	return isRecord(value) && "code" in value;
}

/** Returns the source root that owns an absolute path, if any. */
function rootOf(root: string, path: string): SourceRoot | null {
	const rel = relative(root, path).split(sep).join("/");
	for (const sourceRoot of sourceRootNames) {
		if (rel === sourceRoot || rel.startsWith(`${sourceRoot}/`)) return sourceRoot;
	}
	return null;
}

function isRelative(specifier: string): boolean {
	return specifier.startsWith(".") || specifier.startsWith("/");
}

/**
 * Production module edges are the contract. Test directories may import the
 * generator or fixture they verify across roots, so edges are not enforced
 * there; the banned-dependency checks still are.
 */
function isTestPath(root: string, file: string): boolean {
	const rel = relative(root, file).split(sep).join("/");
	return rel.startsWith("tests/") || rel.includes("/tests/");
}

function matchesBase(specifier: string, base: string): boolean {
	return specifier === base || specifier.startsWith(`${base}/`);
}

/** Classifies a non-Go import specifier into a source root, or null. */
function classify(root: string, file: string, specifier: string): SourceRoot | null {
	if (isRelative(specifier)) {
		return rootOf(root, resolve(dirname(file), specifier));
	}
	for (const entry of workspacePackages) {
		if (matchesBase(specifier, entry.base)) return entry.root;
	}
	return null;
}

/** Go imports resolve by module path; relative Go imports are not a thing. */
function classifyGo(specifier: string): SourceRoot | null {
	if (isRelative(specifier)) return null;
	for (const entry of goModulePaths) {
		if (matchesBase(specifier, entry.base)) return entry.root;
	}
	return null;
}

interface ImportReference {
	readonly line: number;
	readonly specifier: string;
}

const tsImportPatterns = [
	/\bfrom\s+["']([^"']+)["']/g,
	/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
	/\bimport\s+["']([^"']+)["']/g,
	/\brequire\s*\(\s*["']([^"']+)["']\s*\)/g,
];

function collectTypeScriptImports(text: string): ImportReference[] {
	const references: ImportReference[] = [];
	const lines = text.split("\n");
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] ?? "";
		for (const pattern of tsImportPatterns) {
			pattern.lastIndex = 0;
			for (const match of line.matchAll(pattern)) {
				if (match[1] !== undefined) {
					references.push({ line: index + 1, specifier: match[1] });
				}
			}
		}
	}
	return references;
}

// Go import blocks span multiple lines; a small state machine attributes each
// quoted path to its physical line.
function collectGoImports(text: string): ImportReference[] {
	const references: ImportReference[] = [];
	const lines = text.split("\n");
	const quoted = /^\s*(?:[\w.]+\s+)?"([^"]+)"\s*$/;
	const inline = /^\s*import\s+(?:[\w.]+\s+)?"([^"]+)"\s*$/;
	let inBlock = false;
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] ?? "";
		const trimmed = line.trim();
		if (!inBlock && /^import\s*\($/.test(trimmed)) {
			inBlock = true;
			continue;
		}
		if (inBlock) {
			if (trimmed === ")") {
				inBlock = false;
				continue;
			}
			const match = quoted.exec(line);
			if (match?.[1] !== undefined) references.push({ line: index + 1, specifier: match[1] });
			continue;
		}
		const match = inline.exec(line);
		if (match?.[1] !== undefined) references.push({ line: index + 1, specifier: match[1] });
	}
	return references;
}

async function walk(directory: string, files: string[]): Promise<void> {
	let entries: Dirent[];
	try {
		entries = await readdir(directory, { withFileTypes: true });
	} catch (error) {
		if (isErrnoLike(error) && error.code === "ENOENT") return;
		throw error;
	}
	for (const entry of entries) {
		if (entry.isDirectory()) {
			if (ignoredDirectories.has(entry.name)) continue;
			await walk(resolve(directory, entry.name), files);
			continue;
		}
		if (!entry.isFile()) continue;
		const dot = entry.name.lastIndexOf(".");
		if (dot < 0 || !checkedExtensions.has(entry.name.slice(dot))) continue;
		files.push(resolve(directory, entry.name));
	}
}

function isBannedDependency(name: string): boolean {
	for (const prefix of bannedDependencyPrefixes) {
		if (prefix.endsWith("/")) {
			if (name.startsWith(prefix)) return true;
			continue;
		}
		if (name === prefix || name.startsWith(`${prefix}/`)) return true;
	}
	return false;
}

function checkImports(root: string, file: string, text: string, violations: Violation[]): void {
	const sourceRoot = rootOf(root, file);
	if (sourceRoot === null) return;
	const isGo = file.endsWith(".go");
	const references = isGo ? collectGoImports(text) : collectTypeScriptImports(text);
	for (const reference of references) {
		if (isBannedDependency(reference.specifier)) {
			violations.push({
				file: displayPath(root, file),
				line: reference.line,
				message: `banned dependency ${JSON.stringify(reference.specifier)} (Tailwind is forbidden)`,
			});
		}
		const target = isGo
			? classifyGo(reference.specifier)
			: classify(root, file, reference.specifier);
		if (target === null || target === sourceRoot) continue;
		if (allowedEdges[sourceRoot].includes(target)) continue;
		if (isTestPath(root, file)) continue;
		violations.push({
			file: displayPath(root, file),
			line: reference.line,
			message: `forbidden ${sourceRoot} -> ${target} import ${JSON.stringify(reference.specifier)} (allowed: ${allowedEdges[sourceRoot].join(", ")})`,
		});
	}
}

function checkBannedDirectives(
	root: string,
	file: string,
	text: string,
	violations: Violation[],
): void {
	if (!/\.(?:css|pcss|svelte)$/.test(file)) return;
	const lines = text.split("\n");
	for (let index = 0; index < lines.length; index += 1) {
		const line = lines[index] ?? "";
		for (const pattern of bannedDirectives) {
			if (pattern.test(line)) {
				violations.push({
					file: displayPath(root, file),
					line: index + 1,
					message: "banned Tailwind directive; the project must not reintroduce Tailwind",
				});
			}
		}
	}
}

async function checkManifests(root: string, violations: Violation[]): Promise<number> {
	let checked = 0;
	const manifests = [resolve(root, "package.json")];
	for (const sourceRoot of sourceRootNames) {
		try {
			const entries = await readdir(resolve(root, sourceRoot), { withFileTypes: true });
			for (const entry of entries) {
				if (entry.isFile() && entry.name === "package.json") {
					manifests.push(resolve(root, sourceRoot, entry.name));
				}
			}
		} catch {
			// A missing source root is reported by other gates.
		}
	}
	for (const manifestPath of manifests) {
		const text = await readFile(manifestPath, "utf8");
		checked += 1;
		let parsed: unknown;
		try {
			parsed = JSON.parse(text);
		} catch {
			violations.push({
				file: displayPath(root, manifestPath),
				line: 1,
				message: "package manifest is not valid JSON",
			});
			continue;
		}
		if (!isRecord(parsed)) {
			violations.push({
				file: displayPath(root, manifestPath),
				line: 1,
				message: "package manifest must be a JSON object",
			});
			continue;
		}
		for (const section of ["dependencies", "devDependencies", "optionalDependencies"]) {
			const deps = parsed[section];
			if (!isRecord(deps)) continue;
			for (const name of Object.keys(deps)) {
				if (!isBannedDependency(name)) continue;
				const line = text.split("\n").findIndex((entry) => entry.includes(`"${name}"`)) + 1;
				violations.push({
					file: displayPath(root, manifestPath),
					line: line > 0 ? line : 1,
					message: `${section}.${name} is a banned dependency (Tailwind is forbidden)`,
				});
			}
		}
	}
	return checked;
}

export async function runBoundaryCheck(options: BoundaryCheckOptions = {}): Promise<number> {
	const root = options.root ?? repositoryRoot;
	const files: string[] = [];
	for (const sourceRoot of sourceRootNames) await walk(resolve(root, sourceRoot), files);
	files.sort();
	const violations: Violation[] = [];
	for (const file of files) {
		const text = await readFile(file, "utf8");
		checkImports(root, file, text, violations);
		checkBannedDirectives(root, file, text, violations);
	}
	const manifestCount = await checkManifests(root, violations);

	if (violations.length > 0) {
		console.error(`check-boundaries: FAILED (${violations.length} violation(s))`);
		for (const violation of violations) {
			console.error(`  ${violation.file}:${violation.line}: ${violation.message}`);
		}
		return 1;
	}
	console.log(
		`check-boundaries: OK (${files.length} files across ${sourceRootNames.length} roots, ${manifestCount} manifests)`,
	);
	return 0;
}

if (import.meta.main) process.exit(await runBoundaryCheck());
