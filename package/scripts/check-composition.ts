#!/usr/bin/env bun

import { readdir, readFile } from "node:fs/promises";
import { relative, resolve } from "node:path";

const SOURCE_EXTENSIONS = new Set([".go", ".js", ".jsx", ".mjs", ".ts", ".tsx"]);
const IGNORED_DIRECTORIES = new Set([".git", "coverage", "dist", "node_modules", "vendor"]);
const FORBIDDEN_ASSISTANT_IMPORT_SUBSTRINGS = [
	"package/internal/",
	"package/webui/",
	"package/cmd/",
	"package/contracts/",
	"/internal/",
	"/webui/",
	"/contracts/",
	"/cmd/",
	"/pi/",
	"/pixie/",
] as const;

export interface CompositionInput {
	assistantSources: Readonly<Record<string, string>>;
	packageCommandSources: Readonly<Record<string, string>>;
	productionSources: Readonly<Record<string, string>>;
	assistantGoModText?: string;
	packageGoModText?: string;
	dockerfileText?: string;
}

export interface CompositionFacts {
	bunServeCount: number;
	supervisorOwners: readonly string[];
	publicFacadeImport: string | null;
}

export interface CompositionReport {
	ok: boolean;
	violations: readonly string[];
	facts: CompositionFacts;
}

function stripComments(source: string): string {
	return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|\s)\/\/.*$/gm, "$1");
}

function extractTypeScriptImportSpecifiers(source: string): string[] {
	const clean = stripComments(source);
	const specifiers: string[] = [];
	const patterns = [
		/\bimport\s+(?:[^'"]+?\sfrom\s+)?["']([^"']+)["']/g,
		/\bexport\s+[^'"]+?\sfrom\s+["']([^"']+)["']/g,
		/\brequire\s*\(\s*["']([^"']+)["']\s*\)/g,
		/\brequire\s*\.\s*resolve\s*\(\s*["']([^"']+)["']\s*\)/g,
		/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
	];
	for (const pattern of patterns) {
		for (const match of clean.matchAll(pattern)) {
			if (match[1]) specifiers.push(match[1]);
		}
	}
	return [...new Set(specifiers)];
}

function forbiddenAssistantImport(specifier: string): string | null {
	for (const forbidden of FORBIDDEN_ASSISTANT_IMPORT_SUBSTRINGS) {
		if (specifier.includes(forbidden)) return forbidden;
	}
	if (
		/(^|\/)pi(\/|$)/.test(specifier) &&
		!specifier.startsWith("@earendil-works/pi") &&
		specifier.startsWith(".") &&
		(specifier.includes("/pi/") || specifier.endsWith("/pi"))
	) {
		return "../pi/";
	}
	return null;
}

function extractGoImportSpecifiers(source: string): string[] {
	const clean = stripComments(source);
	const specifiers: string[] = [];
	const singleImport = /^\s*import\s+(?:(?:[A-Za-z_]\w*|\.)\s+)?["']([^"']+)["']/gm;
	const importBlocks = /\bimport\s*\(([\s\S]*?)\)/g;

	for (const match of clean.matchAll(singleImport)) {
		if (match[1]) specifiers.push(match[1]);
	}
	for (const block of clean.matchAll(importBlocks)) {
		for (const match of block[1]?.matchAll(/^\s*(?:(?:[A-Za-z_]\w*|\.)\s+)?["']([^"']+)["']/gm) ?? []) {
			if (match[1]) specifiers.push(match[1]);
		}
	}
	return [...new Set(specifiers)];
}

function extractImportSpecifiers(path: string, source: string): string[] {
	return path.endsWith(".go")
		? extractGoImportSpecifiers(source)
		: extractTypeScriptImportSpecifiers(source);
}

function modulePath(goMod: string | undefined): string | null {
	return goMod?.match(/^\s*module\s+(\S+)\s*$/m)?.[1] ?? null;
}

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function hasGoRequire(goMod: string, module: string): boolean {
	const escaped = escapeRegExp(module);
	return new RegExp(`^\\s*(?:require\\s+)?${escaped}\\s+\\S+`, "m").test(goMod);
}

function hasExactLocalReplace(goMod: string, module: string): boolean {
	const escaped = escapeRegExp(module);
	return new RegExp(
		`^\\s*replace\\s+${escaped}(?:\\s+\\S+)?\\s*=>\\s*\\.\\.?/assistant(?:\\s|$)`,
		"m",
	).test(goMod);
}

function sourceOwner(path: string): string {
	if (path.startsWith("assistant/")) return "assistant";
	if (path.startsWith("package/")) return "package";
	return path.split("/", 1)[0] ?? path;
}

function hasSupervisorImplementation(path: string, source: string): boolean {
	const pathLooksLikeSupervisor = /(^|\/)supervis(?:or|ion)(?:\/|[-_.]|$)/i.test(path);
	const code = stripComments(source);
	const sourceDeclaresSupervisor =
		/\b(?:type|class|interface)\s+[A-Za-z_]\w*Supervisor\b/.test(code) ||
		/\b(?:new|function)\s+[A-Za-z_]\w*Supervisor\b/.test(code) ||
		/\b[A-Za-z_]\w*Supervisor\s*[:=]/.test(code);
	return pathLooksLikeSupervisor || sourceDeclaresSupervisor;
}

function finalDockerStage(dockerfile: string): string {
	const stages = [...dockerfile.matchAll(/^\s*FROM\b.*$/gm)];
	const last = stages.at(-1);
	return last?.index === undefined ? "" : dockerfile.slice(last.index);
}

function hasAssistantRuntimeCopy(dockerfile: string): boolean {
	for (const match of dockerfile.matchAll(/^\s*COPY\s+(?:--from=\S+\s+)?(\S+)/gm)) {
		const source = match[1];
		if (
			source?.startsWith("assistant/") &&
			source !== "assistant/package.json" &&
			source !== "assistant/patches/"
		) {
			return true;
		}
	}
	return false;
}

function checkDockerfile(dockerfile: string | undefined, violations: string[]): void {
	if (dockerfile === undefined) {
		violations.push("package/Dockerfile: Dockerfile is required for the controller-only composition check");
		return;
	}

	if (hasAssistantRuntimeCopy(dockerfile)) {
		violations.push("package/Dockerfile: runtime image must not copy assistant source or host-facade packages");
	}

	const final = finalDockerStage(dockerfile);
	if (final === "") {
		violations.push("package/Dockerfile: final application stage is missing");
		return;
	}
	const launch = [...final.matchAll(/^\s*(?:ENTRYPOINT|CMD)\b.*$/gm)]
		.map((match) => match[0])
		.join("\n");
	if (launch === "") {
		violations.push("package/Dockerfile: final application stage must declare an entrypoint or command");
		return;
	}
	if (!/\/app\/pixie\b/.test(launch)) {
		violations.push("package/Dockerfile: final entrypoint must run the Pixie controller binary");
	}
	if (!/\bserve\b[\s,"']+.*--mode[=\s,"']+controller\b/i.test(launch)) {
		violations.push("package/Dockerfile: final entrypoint must explicitly run `pixie serve --mode controller`");
	}
	if (/\b(?:pixie-assistant|pi)\s+(?:serve|--)/i.test(launch)) {
		violations.push("package/Dockerfile: final entrypoint must not start an assistant or local Pi process");
	}
}

export function inspectComposition(input: CompositionInput): CompositionReport {
	const violations: string[] = [];
	const packageModule = modulePath(input.packageGoModText);
	const assistantModule = modulePath(input.assistantGoModText);
	const publicFacadeImport = assistantModule ? `${assistantModule}/host` : null;

	if (assistantModule === null) {
		violations.push("assistant/go.mod: separate assistant Go module is required");
	}
	if (packageModule === null) {
		violations.push("package/go.mod: controller Go module declaration is required");
	}
	if (assistantModule !== null && packageModule !== null) {
		if (!hasGoRequire(input.packageGoModText ?? "", assistantModule)) {
			violations.push(`package/go.mod: must require the assistant module ${assistantModule}`);
		}
		if (!hasExactLocalReplace(input.packageGoModText ?? "", assistantModule)) {
			violations.push(`package/go.mod: assistant module must use the exact local replacement => ../assistant`);
		}
	}

	const assistantHostFiles = Object.keys(input.assistantSources).filter(
		(path) => path.startsWith("assistant/host/") && path.endsWith(".go"),
	);
	if (assistantHostFiles.length === 0) {
		violations.push("assistant/host: public assistant host facade package is missing");
	}

	const commandImports = Object.entries(input.packageCommandSources).flatMap(([path, source]) =>
		extractImportSpecifiers(path, source),
	);
	if (
		publicFacadeImport === null ||
		!commandImports.includes(publicFacadeImport)
	) {
		violations.push(
			`package/cmd: full-host entrypoint must import the public assistant facade${publicFacadeImport ? ` ${publicFacadeImport}` : ""}`,
		);
	}

	const assistantCommandFiles = Object.entries(input.assistantSources).filter(
		([path]) => path.startsWith("assistant/cmd/") && path.endsWith(".go"),
	);
	if (
		publicFacadeImport !== null &&
		assistantCommandFiles.length > 0 &&
		!assistantCommandFiles.some(([path, source]) =>
			extractImportSpecifiers(path, source).includes(publicFacadeImport),
		)
	) {
		violations.push(`assistant/cmd: assistant entrypoint must use the same public facade ${publicFacadeImport}`);
	}

	for (const [path, source] of Object.entries(input.assistantSources)) {
		const specifiers = extractImportSpecifiers(path, source);
		if (!path.endsWith(".go")) {
			for (const specifier of specifiers) {
				const matched = forbiddenAssistantImport(specifier);
				if (matched === null) continue;
				violations.push(
					`${path}: forbidden controller-internal import ${JSON.stringify(specifier)} (matched ${JSON.stringify(matched)})`,
				);
			}
			continue;
		}
		for (const specifier of specifiers) {
			const isOwnAssistantImport = assistantModule !== null &&
				(specifier === assistantModule || specifier.startsWith(`${assistantModule}/`));
			if (specifier.includes("/internal/") && !isOwnAssistantImport) {
				violations.push(`${path}: forbidden controller-internal import ${JSON.stringify(specifier)}`);
			}
		}
	}

	const bunServeLocations: string[] = [];
	const supervisorOwners = new Set<string>();
	for (const [path, source] of Object.entries(input.productionSources)) {
		const code = stripComments(source);
		const bunServeCount = code.match(/\bBun\.serve\s*\(/g)?.length ?? 0;
		for (let index = 0; index < bunServeCount; index += 1) bunServeLocations.push(path);
		if (hasSupervisorImplementation(path, source)) supervisorOwners.add(sourceOwner(path));
	}
	if (bunServeLocations.length > 1) {
		violations.push(
			`production sources: duplicate Bun.serve implementations (${bunServeLocations.join(", ")})`,
		);
	}
	if (supervisorOwners.size > 1) {
		violations.push(`production sources: duplicate supervisor owners (${[...supervisorOwners].sort().join(", ")})`);
	}

	checkDockerfile(input.dockerfileText, violations);
	return {
		ok: violations.length === 0,
		violations,
		facts: {
			bunServeCount: bunServeLocations.length,
			supervisorOwners: [...supervisorOwners].sort(),
			publicFacadeImport,
		},
	};
}

async function collectSourceTree(
	repositoryRoot: string,
	directory: string,
): Promise<Record<string, string>> {
	const files: Record<string, string> = {};
	const absoluteRoot = resolve(repositoryRoot, directory);

	async function walk(current: string): Promise<void> {
		let entries;
		try {
			entries = await readdir(current, { withFileTypes: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
			throw error;
		}
		for (const entry of entries) {
			if (entry.isDirectory() && IGNORED_DIRECTORIES.has(entry.name)) continue;
			const path = resolve(current, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
				continue;
			}
			if (!entry.isFile() || !SOURCE_EXTENSIONS.has(path.slice(path.lastIndexOf(".")))) continue;
			const displayPath = relative(repositoryRoot, path).replaceAll("\\", "/");
			files[displayPath] = await readFile(path, "utf8");
		}
	}

	await walk(absoluteRoot);
	return files;
}

export async function collectCompositionInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<CompositionInput> {
	const assistantSources = await collectSourceTree(repositoryRoot, "assistant");
	const packageCommandSources = await collectSourceTree(repositoryRoot, "package/cmd");
	const packageInternalSources = await collectSourceTree(repositoryRoot, "package/internal");
	const packageGoModText = await readFile(resolve(repositoryRoot, "package/go.mod"), "utf8").catch(() => undefined);
	const assistantGoModText = await readFile(resolve(repositoryRoot, "assistant/go.mod"), "utf8").catch(() => undefined);
	const dockerfileText = await readFile(resolve(repositoryRoot, "package/Dockerfile"), "utf8").catch(() => undefined);
	return {
		assistantSources,
		packageCommandSources,
		productionSources: { ...assistantSources, ...packageCommandSources, ...packageInternalSources },
		assistantGoModText,
		packageGoModText,
		dockerfileText,
	};
}

export function formatCompositionReport(report: CompositionReport): string {
	if (report.ok) {
		return `check-composition: OK (facade ${report.facts.publicFacadeImport}, ` +
			`Bun.serve ${report.facts.bunServeCount}, supervisor owners ${report.facts.supervisorOwners.length})`;
	}
	return ["check-composition: FAILED", ...report.violations.map((violation) => `  - ${violation}`)].join("\n");
}

export async function runCompositionCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const report = inspectComposition(await collectCompositionInput(repositoryRoot));
	const output = formatCompositionReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runCompositionCheck());
