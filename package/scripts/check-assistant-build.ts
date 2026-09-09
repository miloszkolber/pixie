#!/usr/bin/env bun

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { dirname, join, normalize, relative, resolve } from "node:path";

const IGNORED_DIRECTORIES = new Set([".git", "coverage", "dist", "node_modules"]);
const SOURCE_EXTENSIONS = new Set([".js", ".jsx", ".mjs", ".ts", ".tsx"]);
const RUNTIME_DIRECTORY = "dist";
const DOCTOR_SOURCE = "src/doctor.ts";
const BUILD_ENTRY = "src/main.ts";
const ALLOWED_PACKAGE_FILES = new Set(["dist", "patches"]);
const FORBIDDEN_PATH = /(?:^|\/)(?:package|webui|worker|controller|tests?|secrets?)(?:\/|\.|$)/i;
const FORBIDDEN_BOUNDARY_SPECIFIER = /(?:^|[/@-])(?:controller|webui|worker)(?:[/@-]|$)/i;
const FORBIDDEN_SECRET_PATH = /(?:^|\/)(?:\.env(?:\.|$)|.*(?:secret|credential|token|password).*)/i;
const SECRET_LITERAL =
	/\b(?:[A-Za-z0-9]+[_-])*?(?:secret|token|password|passwd|api[-_]?key|authorization|credential)(?:[_-][A-Za-z0-9]+)*\b\s*[:=]\s*["'][^"'\n]{8,}["']/i;

export interface AssistantManifest {
	bin?: Readonly<Record<string, string>>;
	files?: readonly string[];
	exports?: Readonly<Record<string, string | Readonly<Record<string, string>>>>;
	scripts?: Readonly<Record<string, string>>;
	dependencies?: Readonly<Record<string, string>>;
	devDependencies?: Readonly<Record<string, string>>;
}

export interface AssistantBuildInput {
	manifest: AssistantManifest;
	sourceFiles: Readonly<Record<string, string>>;
	artifactFiles?: readonly string[];
	artifactSources?: Readonly<Record<string, string>>;
}

export interface AssistantBuildFacts {
	buildEntries: readonly string[];
	artifactRoots: readonly string[];
	closure: readonly string[];
	checks: readonly string[];
}

export interface AssistantBuildReport {
	ok: boolean;
	violations: readonly string[];
	facts: AssistantBuildFacts;
}

function asAssistantPath(path: string): string {
	return normalize(path).replaceAll("\\", "/").replace(/^\.\//, "");
}

function packagePath(path: string): string {
	return asAssistantPath(path).replace(/\/\*$/, "");
}

function sourceExtensions(path: string): string[] {
	if (SOURCE_EXTENSIONS.has(path.slice(path.lastIndexOf(".")))) return [path];
	return [path, `${path}.ts`, `${path}.tsx`, `${path}.js`, `${path}.mjs`, `${path}/index.ts`];
}

function importedSpecifiers(source: string): string[] {
	const clean = source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|\s)\/\/.*$/gm, "$1");
	const patterns = [
		/\bimport\s+(?:[^'"\n]+?\sfrom\s+)?["']([^"']+)["']/g,
		/\bexport\s+[^'"\n]+?\sfrom\s+["']([^"']+)["']/g,
		/\b(?:require|import)\s*\(\s*["']([^"']+)["']\s*\)/g,
	];
	const result: string[] = [];
	for (const pattern of patterns) {
		for (const match of clean.matchAll(pattern)) {
			if (match[1]) result.push(match[1]);
		}
	}
	return [...new Set(result)];
}

function buildEntries(buildScript: string | undefined): string[] {
	if (buildScript === undefined) return [];
	return [...buildScript.matchAll(/(?<!\S)(src\/[A-Za-z0-9._/-]+\.(?:m?js|tsx?))(?!\S)/g)]
		.map((match) => match[1])
		.filter((entry): entry is string => entry !== undefined);
}

function outputRoots(buildScript: string | undefined, manifest: AssistantManifest): string[] {
	const roots: string[] = [];
	if (buildScript?.match(/(?:--outdir(?:=|\s+)|--outfile(?:=|\s+))([^\s]+)/)?.[1]) {
		const match = buildScript.match(/(?:--outdir(?:=|\s+)|--outfile(?:=\s*))([^\s]+)/);
		if (match?.[1]) roots.push(packagePath(match[1]));
	}
	for (const path of Object.values(manifest.bin ?? {})) {
		const normalized = packagePath(path);
		const root = normalized.split("/")[0];
		if (root) roots.push(root);
	}
	return [...new Set(roots)];
}

function forbiddenArtifactPath(path: string): string | null {
	const normalized = packagePath(path);
	if (FORBIDDEN_PATH.test(normalized)) return normalized;
	if (FORBIDDEN_SECRET_PATH.test(normalized)) return normalized;
	if (/\.(?:ts|tsx|map)$/i.test(normalized)) return normalized;
	return null;
}

function resolveLocalSource(
	entry: string,
	specifier: string,
	sourceFiles: Readonly<Record<string, string>>,
): string | null {
	const base = asAssistantPath(join(dirname(entry), specifier));
	if (base.startsWith("../") || base === "..") return null;
	for (const candidate of sourceExtensions(base)) {
		if (candidate in sourceFiles) return candidate;
	}
	return null;
}

function inspectSourceClosure(
	entries: readonly string[],
	sourceFiles: Readonly<Record<string, string>>,
	violations: string[],
): string[] {
	const closure = new Set<string>();
	const pending = [...entries];
	while (pending.length > 0) {
		const entry = pending.pop();
		if (!entry || closure.has(entry)) continue;
		closure.add(entry);
		const source = sourceFiles[entry];
		if (source === undefined) {
			violations.push(`build entry ${entry}: source file is missing`);
			continue;
		}
		for (const specifier of importedSpecifiers(source)) {
			if (FORBIDDEN_BOUNDARY_SPECIFIER.test(specifier)) {
				violations.push(
					`${entry}: assistant build closure imports forbidden controller/UI/worker module ${specifier}`,
				);
				continue;
			}
			if (!specifier.startsWith(".")) continue;
			const local = resolveLocalSource(entry, specifier, sourceFiles);
			if (local === null) {
				const resolved = asAssistantPath(join(dirname(entry), specifier));
				if (resolved === "package.json") continue;
				violations.push(
					`${entry}: local build import escapes assistant runtime (${JSON.stringify(specifier)})`,
				);
				continue;
			}
			if (FORBIDDEN_PATH.test(local)) {
				violations.push(`${entry}: assistant build closure includes forbidden path ${local}`);
				continue;
			}
			pending.push(local);
		}
	}
	return [...closure].sort();
}

function inspectManifest(
	input: AssistantBuildInput,
	violations: string[],
	checks: string[],
): string[] {
	const scripts = input.manifest.scripts ?? {};
	const build = scripts.build;
	const entries = buildEntries(build);
	if (build === undefined) violations.push("assistant/package.json: build script is required");
	else {
		if (!/^\s*bun\s+build\b/.test(build))
			violations.push("assistant/package.json: build must use `bun build`");
		if (!/--target(?:=|\s+)bun\b/.test(build))
			violations.push("assistant/package.json: build must target Bun");
		if (/(?:package\/|webui\/|worker\/|controller\/|tests?\/)/i.test(build)) {
			violations.push(
				"assistant/package.json: assistant build must not reference controller, UI, worker or test paths",
			);
		}
		if (!build.includes(BUILD_ENTRY))
			violations.push(`assistant/package.json: build must include ${BUILD_ENTRY}`);
	}
	if (entries.length === 0)
		violations.push("assistant/package.json: build has no TypeScript runtime entrypoints");
	if (input.sourceFiles[DOCTOR_SOURCE] === undefined)
		violations.push(`assistant/${DOCTOR_SOURCE}: doctor remains available`);
	if (
		typeof scripts.typecheck !== "string" ||
		!/\btsc\b/.test(scripts.typecheck) ||
		!/--noEmit\b/.test(scripts.typecheck)
	) {
		violations.push("assistant/package.json: no-emit typecheck script must remain available");
	}
	checks.push("independent Bun build entrypoint", "no-emit typecheck", "doctor source");
	for (const section of [input.manifest.dependencies, input.manifest.devDependencies]) {
		for (const name of Object.keys(section ?? {})) {
			if (FORBIDDEN_BOUNDARY_SPECIFIER.test(name)) {
				violations.push(
					`assistant/package.json: build dependency crosses controller/UI/worker boundary (${name})`,
				);
			}
		}
	}

	const binEntries = Object.values(input.manifest.bin ?? {});
	if (binEntries.length === 0)
		violations.push("assistant/package.json: assistant runtime bin is required");
	for (const path of binEntries) {
		if (!path.startsWith(`${RUNTIME_DIRECTORY}/`) || !/\.m?js$/.test(path)) {
			violations.push(
				`assistant/package.json: runtime bin must point to bundled ${RUNTIME_DIRECTORY} JavaScript (${path})`,
			);
		}
	}
	if (typeof scripts.start !== "string" || !scripts.start.includes(`${RUNTIME_DIRECTORY}/`)) {
		violations.push(
			`assistant/package.json: start must run the bundled ${RUNTIME_DIRECTORY} artifact`,
		);
	}

	const files = input.manifest.files ?? [];
	if (!files.some((path) => packagePath(path) === RUNTIME_DIRECTORY)) {
		violations.push(
			`assistant/package.json: published files must include only the ${RUNTIME_DIRECTORY} runtime root`,
		);
	}
	for (const path of files) {
		const normalized = packagePath(path);
		if (!ALLOWED_PACKAGE_FILES.has(normalized)) {
			violations.push(`assistant/package.json: published file entry is not runtime-only: ${path}`);
		}
		if (FORBIDDEN_PATH.test(normalized) || FORBIDDEN_SECRET_PATH.test(normalized)) {
			violations.push(
				`assistant/package.json: published files include forbidden source/test/secret path ${path}`,
			);
		}
	}

	const exports = input.manifest.exports ?? {};
	for (const [name, target] of Object.entries(exports)) {
		if (typeof target !== "string") continue;
		if (!target.startsWith(`./${RUNTIME_DIRECTORY}/`)) {
			violations.push(
				`assistant/package.json: export ${name} must point to bundled runtime output (${target})`,
			);
		}
	}
	return entries;
}

export function inspectAssistantBuild(input: AssistantBuildInput): AssistantBuildReport {
	const violations: string[] = [];
	const checks: string[] = [];
	const entries = inspectManifest(input, violations, checks);
	const closure = inspectSourceClosure(entries, input.sourceFiles, violations);
	if (!closure.includes(DOCTOR_SOURCE)) {
		violations.push(`assistant/package.json: build closure must retain ${DOCTOR_SOURCE}`);
	}
	const build = input.manifest.scripts?.build;
	const artifactRoots = outputRoots(build, input.manifest);
	if (!artifactRoots.includes(RUNTIME_DIRECTORY)) {
		violations.push(`assistant/package.json: build output must be rooted at ${RUNTIME_DIRECTORY}`);
	}
	checks.push("runtime-only package allowlist", "bounded source closure");

	for (const path of input.artifactFiles ?? []) {
		const forbidden = forbiddenArtifactPath(path);
		if (forbidden !== null) violations.push(`assistant artifact includes non-runtime file ${path}`);
	}
	for (const [path, source] of Object.entries(input.artifactSources ?? {})) {
		if (forbiddenArtifactPath(path) !== null) {
			violations.push(`assistant artifact includes non-runtime file ${path}`);
			continue;
		}
		if (SECRET_LITERAL.test(source)) {
			violations.push(`assistant artifact contains a secret-shaped literal (${path})`);
		}
	}

	return {
		ok: violations.length === 0,
		violations,
		facts: { buildEntries: entries, artifactRoots, closure, checks },
	};
}

async function collectSourceTree(root: string): Promise<Record<string, string>> {
	const files: Record<string, string> = {};
	async function walk(directory: string): Promise<void> {
		let entries: Dirent[];
		try {
			entries = await readdir(directory, { withFileTypes: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
			throw error;
		}
		for (const entry of entries) {
			if (entry.isDirectory() && IGNORED_DIRECTORIES.has(entry.name)) continue;
			const path = resolve(directory, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
				continue;
			}
			if (!entry.isFile() || !SOURCE_EXTENSIONS.has(path.slice(path.lastIndexOf(".")))) continue;
			files[relative(root, path).replaceAll("\\", "/")] = await readFile(path, "utf8");
		}
	}
	await walk(root);
	return files;
}

export async function collectAssistantBuildInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<AssistantBuildInput> {
	const assistantRoot = resolve(repositoryRoot, "assistant");
	const manifest = JSON.parse(
		await readFile(resolve(assistantRoot, "package.json"), "utf8"),
	) as AssistantManifest;
	const sourceFiles = await collectSourceTree(resolve(assistantRoot, "src"));
	return {
		manifest,
		sourceFiles: Object.fromEntries(
			Object.entries(sourceFiles).map(([path, source]) => [`src/${path}`, source]),
		),
	};
}

export function formatAssistantBuildReport(report: AssistantBuildReport): string {
	if (report.ok) {
		return (
			`check-assistant-build: OK (${report.facts.buildEntries.length} entrypoints, ` +
			`${report.facts.closure.length} runtime modules, ${report.facts.artifactRoots.join(", ")})`
		);
	}
	return [
		"check-assistant-build: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
	].join("\n");
}

export async function runAssistantBuildCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const report = inspectAssistantBuild(await collectAssistantBuildInput(repositoryRoot));
	const output = formatAssistantBuildReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runAssistantBuildCheck());
