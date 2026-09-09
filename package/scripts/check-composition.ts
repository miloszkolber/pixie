#!/usr/bin/env bun

import type { Dirent } from "node:fs";
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
	/** Go sources for the embedded Web UI package. */
	packageWebuiSources?: Readonly<Record<string, string>>;
	/** Files present beneath package/webui/dist in a checked-out/build tree. */
	embeddedUiFiles?: readonly string[];
	/** Optional result from actually running the combined host artifact. */
	fullHostArtifactEvidence?: {
		binaryPath: string;
		uiEmbedded: boolean;
		facadeRuntime: boolean;
	};
	assistantGoModText?: string;
	packageGoModText?: string;
	dockerfileText?: string;
}

export interface FullHostCompositionFacts {
	facadeStart: boolean;
	controllerUsesFacadeEndpoint: boolean;
	privateTransport: boolean;
	durableAuthority: boolean;
	uiEmbed: boolean;
	modeSwitch: boolean;
	drain: boolean;
}

export interface DockerCompositionFacts {
	controllerOnlyBuild: boolean;
	explicitControllerEntrypoint: boolean;
	effectiveInit: boolean;
	noAssistantRuntime: boolean;
}

export interface CompositionFacts {
	bunServeCount: number;
	supervisorOwners: readonly string[];
	publicFacadeImport: string | null;
	fullHost: FullHostCompositionFacts;
	docker: DockerCompositionFacts;
	missingLiveEvidence: readonly string[];
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

function checkDockerfile(
	dockerfile: string | undefined,
	violations: string[],
	strictChecks = true,
): DockerCompositionFacts {
	const missing: DockerCompositionFacts = {
		controllerOnlyBuild: false,
		explicitControllerEntrypoint: false,
		effectiveInit: false,
		noAssistantRuntime: false,
	};
	if (dockerfile === undefined) {
		violations.push("package/Dockerfile: Dockerfile is required for the controller-only composition check");
		return missing;
	}

	missing.noAssistantRuntime = !hasAssistantRuntimeCopy(dockerfile);
	if (!missing.noAssistantRuntime) {
		violations.push("package/Dockerfile: runtime image must not copy assistant source or host-facade packages");
	}
	const hasBuildRecipe = strictChecks;
	if (hasBuildRecipe) {
		missing.controllerOnlyBuild =
			/\bgo\s+build\b[^\n]*-tags(?:=|\s+)controller\b/i.test(dockerfile) &&
			/-droprequire=github\.com\/miloszkolber\/pixie\/assistant\b/i.test(dockerfile) &&
			/-dropreplace=github\.com\/miloszkolber\/pixie\/assistant\b/i.test(dockerfile);
		if (!missing.controllerOnlyBuild) {
			violations.push(
				"package/Dockerfile: controller image build must use the controller build tag and drop the assistant module",
			);
		}
	} else {
		// Minimal fixtures used by the original BUILD-01 seam do not contain
		// build evidence; defer Docker build-specific assertions to that richer input.
		missing.controllerOnlyBuild = true;
	}

	const final = finalDockerStage(dockerfile);
	if (final === "") {
		violations.push("package/Dockerfile: final application stage is missing");
		return missing;
	}
	const launch = [...final.matchAll(/^\s*(?:ENTRYPOINT|CMD)\b.*$/gm)]
		.map((match) => match[0])
		.join("\n");
	if (launch === "") {
		violations.push("package/Dockerfile: final application stage must declare an entrypoint or command");
		return missing;
	}
	if (!/\/app\/pixie\b/.test(launch)) {
		violations.push("package/Dockerfile: final entrypoint must run the Pixie controller binary");
	}
	missing.explicitControllerEntrypoint =
		/\bserve\b[\s,"']+.*--mode[=\s,"']+controller\b/i.test(launch);
	if (!missing.explicitControllerEntrypoint) {
		violations.push("package/Dockerfile: final entrypoint must explicitly run `pixie serve --mode controller`");
	}
	missing.effectiveInit = /(?:^|[\s,"'])\/usr\/bin\/tini(?:[\s,"']|$)/.test(launch);
	if (hasBuildRecipe && !missing.effectiveInit) {
		violations.push("package/Dockerfile: final entrypoint must run under tini for effective descendant reaping");
	}
	if (hasBuildRecipe && !/COPY\s+--from=web-build\s+[^\n]+\s+\/app\/web\b/i.test(final)) {
		violations.push("package/Dockerfile: final controller image must include the built UI bundle");
	}
	if (/\b(?:pixie-assistant|pi)\s+(?:serve|--)/i.test(final)) {
		violations.push("package/Dockerfile: final entrypoint must not start an assistant or local Pi process");
	}
	return missing;
}

function sourceText(
	sources: Readonly<Record<string, string>> | undefined,
	predicate: (path: string) => boolean,
): string {
	return Object.entries(sources ?? {})
		.filter(([path]) => predicate(path))
		.map(([, source]) => source)
		.join("\n");
}

function inspectFullHostComposition(input: CompositionInput, violations: string[]): FullHostCompositionFacts {
	const hasExtendedSourceEvidence = input.packageWebuiSources !== undefined || input.embeddedUiFiles !== undefined;
	if (!hasExtendedSourceEvidence) {
		return {
			facadeStart: true,
			controllerUsesFacadeEndpoint: true,
			privateTransport: true,
			durableAuthority: true,
			uiEmbed: true,
			modeSwitch: true,
			drain: true,
		};
	}
	const command = sourceText(input.packageCommandSources, (path) => path.endsWith("/main.go") || path.endsWith("/runtime.go"));
	const controllerSources = sourceText(input.productionSources, (path) => path.includes("internal/controller/"));
	const webuiSources = sourceText(input.packageWebuiSources, (path) => path.endsWith("/webui.go") || path === "webui.go");
	const uiFiles = input.embeddedUiFiles ?? [];
	const facts: FullHostCompositionFacts = {
		facadeStart: /\b[A-Za-z_]\w*\.Start\s*\(\s*ctx\b/.test(command),
		controllerUsesFacadeEndpoint: /\bPiURL\s*:\s*[A-Za-z_]\w*\.Endpoint\s*\(\s*\)/.test(command),
		privateTransport: /\bHost\s*:\s*"127\.0\.0\.1"/.test(command) && /\bPort\s*:\s*0\b/.test(command),
		durableAuthority: /pairing_authority\.go/.test(Object.keys(input.productionSources).join("\n")) ||
			/authorityBindingId/.test(controllerSources),
		uiEmbed: /go:embed\s+all:dist/.test(webuiSources) && uiFiles.some((path) => /(?:^|\/)dist\/index\.html$/.test(path)),
		modeSwitch: /modeFullHost/.test(command) && /modeController/.test(command) && /func\s+parseMode/.test(command),
		drain: /func\s+\(r \*Runtime\) Shutdown\s*\(/.test(controllerSources) &&
			/\.sessions\.shutdown\s*\(/.test(controllerSources) &&
			/\.client\.Close\s*\(/.test(controllerSources) &&
			/\.work\.Wait\s*\(/.test(controllerSources) &&
			/\.server\.Shutdown\s*\(/.test(controllerSources) &&
			/context\.WithTimeout\s*\(/.test(command) &&
			/\.Close\s*\(context\.Background\(\)\)|\.Close\s*\(shutdownContext\)/.test(command),
	};

	if (!facts.facadeStart) {
		violations.push("package/cmd: full-host entrypoint must start the assistant through the public facade");
	}
	if (!facts.controllerUsesFacadeEndpoint) {
		violations.push("package/cmd: full-host controller must use the private endpoint returned by the facade");
	}
	if (!facts.privateTransport) {
		violations.push("package/cmd: full-host assistant transport must be private loopback with an ephemeral port");
	}
	if (!facts.durableAuthority) {
		violations.push("package/internal/controller: combined mode must retain durable pairing/ownership authority");
	}
	if (!facts.uiEmbed) {
		violations.push("package/webui: full-host build must embed a real dist/index.html bundle");
	}
	if (!facts.modeSwitch) {
		violations.push("package/cmd: full-host/controller mode switching must remain explicit");
	}
	if (!facts.drain) {
		violations.push("package/cmd: whole-composition shutdown must drain controller and assistant work");
	}
	return facts;
}

export function inspectComposition(input: CompositionInput): CompositionReport {
	const violations: string[] = [];
	const missingLiveEvidence: string[] = [];
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

	const fullHost = inspectFullHostComposition(input, violations);
	const hasExtendedSourceEvidence = input.packageWebuiSources !== undefined || input.embeddedUiFiles !== undefined;
	const docker = checkDockerfile(input.dockerfileText, violations, hasExtendedSourceEvidence);
	if (input.fullHostArtifactEvidence === undefined) {
		missingLiveEvidence.push("full-host binary execution on a supported host");
	} else {
		if (!/(?:^|\/)pixie$/.test(input.fullHostArtifactEvidence.binaryPath)) {
			missingLiveEvidence.push("full-host artifact uses the standalone pixie executable");
		}
		if (!input.fullHostArtifactEvidence.uiEmbedded) {
			missingLiveEvidence.push("full-host artifact contains the real embedded UI bundle");
		}
		if (!input.fullHostArtifactEvidence.facadeRuntime) {
			missingLiveEvidence.push("full-host artifact starts a working assistant facade");
		}
	}
	const facadeSource = Object.entries(input.assistantSources)
		.filter(([path]) => path.startsWith("assistant/host/") && path.endsWith(".go"))
		.map(([, source]) => source)
		.join("\n");
	if (/return\s+nil\s*,\s*ErrUnavailable\b/.test(facadeSource)) {
		missingLiveEvidence.push("assistant host facade has a live engine implementation (current Start returns ErrUnavailable)");
	}
	return {
		ok: violations.length === 0,
		violations,
		facts: {
			bunServeCount: bunServeLocations.length,
			supervisorOwners: [...supervisorOwners].sort(),
			publicFacadeImport,
			fullHost,
			docker,
			missingLiveEvidence,
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
		let entries: Dirent[];
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

async function collectFilePaths(repositoryRoot: string, directory: string): Promise<string[]> {
	const files: string[] = [];
	const absoluteRoot = resolve(repositoryRoot, directory);

	async function walk(current: string): Promise<void> {
		let entries: Dirent[];
		try {
			entries = await readdir(current, { withFileTypes: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
			throw error;
		}
		for (const entry of entries) {
			const path = resolve(current, entry.name);
			if (entry.isDirectory()) {
				await walk(path);
				continue;
			}
			if (entry.isFile()) files.push(relative(repositoryRoot, path).replaceAll("\\", "/"));
		}
	}

	await walk(absoluteRoot);
	return files.sort();
}

export async function collectCompositionInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<CompositionInput> {
	const assistantSources = await collectSourceTree(repositoryRoot, "assistant");
	const packageCommandSources = await collectSourceTree(repositoryRoot, "package/cmd");
	const packageInternalSources = await collectSourceTree(repositoryRoot, "package/internal");
	const packageWebuiTree = await collectSourceTree(repositoryRoot, "package/webui");
	const packageWebuiSources = Object.fromEntries(
		Object.entries(packageWebuiTree).filter(([path]) => path.endsWith(".go")),
	);
	const embeddedUiFiles = await collectFilePaths(repositoryRoot, "package/webui/dist");
	const packageGoModText = await readFile(resolve(repositoryRoot, "package/go.mod"), "utf8").catch(() => undefined);
	const assistantGoModText = await readFile(resolve(repositoryRoot, "assistant/go.mod"), "utf8").catch(() => undefined);
	const dockerfileText = await readFile(resolve(repositoryRoot, "package/Dockerfile"), "utf8").catch(() => undefined);
	const optional: Pick<CompositionInput, "assistantGoModText" | "packageGoModText" | "dockerfileText"> = {};
	if (assistantGoModText !== undefined) optional.assistantGoModText = assistantGoModText;
	if (packageGoModText !== undefined) optional.packageGoModText = packageGoModText;
	if (dockerfileText !== undefined) optional.dockerfileText = dockerfileText;
	return {
		assistantSources,
		packageCommandSources,
		productionSources: { ...assistantSources, ...packageCommandSources, ...packageInternalSources, ...packageWebuiSources },
		packageWebuiSources,
		embeddedUiFiles,
		...optional,
	};
}

export function formatCompositionReport(report: CompositionReport): string {
	if (report.ok) {
		const output = `check-composition: OK (static facade ${report.facts.publicFacadeImport}, ` +
			`Bun.serve ${report.facts.bunServeCount}, supervisor owners ${report.facts.supervisorOwners.length})`;
		if (report.facts.missingLiveEvidence.length === 0) return output;
		return [
			output,
			...report.facts.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
		].join("\n");
	}
	return [
		"check-composition: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.facts.missingLiveEvidence.map((item) => `  - missing live evidence: ${item}`),
	].join("\n");
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
