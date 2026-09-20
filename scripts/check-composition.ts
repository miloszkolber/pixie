#!/usr/bin/env bun

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { isAbsolute, relative, resolve } from "node:path";

const SOURCE_EXTENSIONS = new Set([".go", ".js", ".jsx", ".mjs", ".ts", ".tsx"]);
const IGNORED_DIRECTORIES = new Set([".git", "coverage", "dist", "node_modules", "vendor"]);
const FORBIDDEN_ASSISTANT_IMPORT_SUBSTRINGS = [
	"web/internal/",
	"web/webui/",
	"web/cmd/",
	"/internal/",
	"/webui/",
	"/contracts/",
	"/cmd/",
	"/pi/",
	"/pixie/",
] as const;
// Removed with the Go host and the administration bridge sidecar. Any source
// reappearing under these prefixes fails the composition check.
const REMOVED_ASSISTANT_PREFIXES = [
	"assistant/host/",
	"assistant/cmd/",
	"assistant/bridge/",
] as const;
const REMOVED_ASSISTANT_MODULE = "github.com/miloszkolber/pixie/assistant";

export interface CompositionInput {
	assistantSources: Readonly<Record<string, string>>;
	packageCommandSources: Readonly<Record<string, string>>;
	productionSources: Readonly<Record<string, string>>;
	/** Go sources for the embedded Web UI package. */
	packageWebuiSources?: Readonly<Record<string, string>>;
	/** Files present beneath web/webui/dist in a checked-out/build tree. */
	embeddedUiFiles?: readonly string[];
	/** Optional result from actually running the controller artifact. */
	controllerArtifactEvidence?: {
		binaryPath: string;
		uiEmbedded: boolean;
	};
	assistantGoModText?: string;
	packageGoModText?: string;
	dockerfileText?: string;
	composeFileText?: string;
	/** Release runtime staging source used to verify the pinned Bun download. */
	releaseRuntimeText?: string;
	systemdUnitSources?: Readonly<Record<string, string>>;
	systemdConfigSources?: Readonly<Record<string, string>>;
}

export interface ControllerCompositionFacts {
	controllerDefault: boolean;
	rejectsLocalAssistant: boolean;
	uiEmbed: boolean;
	drain: boolean;
}

export interface DockerCompositionFacts {
	controllerOnlyBuild: boolean;
	explicitControllerEntrypoint: boolean;
	effectiveInit: boolean;
	noAssistantRuntime: boolean;
	noCombinedImage: boolean;
	/** The release archive stages a verified pinned Bun instead of Node. */
	pinnedBunRuntime: boolean;
	/** No Node runtime payload or staging helper survives in the container topology. */
	noNodeRuntime: boolean;
}

export interface DeploymentCompositionFacts {
	composeUsesWebImage: boolean;
	composeHasNoCombinedImage: boolean;
	composeWebServiceIsNonRoot: boolean;
	composeWebUsesDataMount: boolean;
	composeHasNoFixedContainerNames: boolean;
	archiveServiceUsesHostCommand: boolean;
	noLegacyCliUnit: boolean;
	noPublicAssistantUnit: boolean;
	ownerLockDoesNotRestart: boolean;
	secretsInherited: boolean;
	absoluteConfigPaths: boolean;
	noShellInterpolation: boolean;
}

export interface CompositionFacts {
	bunServeCount: number;
	supervisorOwners: readonly string[];
	controller: ControllerCompositionFacts;
	docker: DockerCompositionFacts;
	deployment?: DeploymentCompositionFacts;
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
		for (const match of block[1]?.matchAll(/^\s*(?:(?:[A-Za-z_]\w*|\.)\s+)?["']([^"']+)["']/gm) ??
			[]) {
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

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function hasGoRequire(goMod: string, module: string): boolean {
	const escaped = escapeRegExp(module);
	return new RegExp(`^\\s*(?:require\\s+)?${escaped}\\s+\\S+`, "m").test(goMod);
}

function sourceOwner(path: string): string {
	if (path.startsWith("assistant/") || path.startsWith("src/assistant/")) return "assistant";
	if (
		path.startsWith("shared/") ||
		path.startsWith("src/shared/") ||
		path.startsWith("schema/") ||
		path.startsWith("piprotocol/")
	)
		return "shared";
	if (path.startsWith("web/")) return "web";
	if (
		path.startsWith("cmd/") ||
		path.startsWith("internal/") ||
		path.startsWith("scripts/") ||
		path.startsWith("webui/") ||
		path.startsWith("systemd/")
	)
		return "web";
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

function dockerStage(dockerfile: string, name: string): string {
	const stage = new RegExp(`^\\s*FROM\\s+[^\\n]+\\s+AS\\s+${escapeRegExp(name)}\\s*$`, "gmi");
	const match = stage.exec(dockerfile);
	if (match?.index === undefined) return "";
	const next = /^\s*FROM\b.*$/gim;
	next.lastIndex = match.index + match[0].length;
	const nextMatch = next.exec(dockerfile);
	return dockerfile.slice(match.index, nextMatch?.index);
}

function hasAssistantRuntimeCopy(stage: string): boolean {
	for (const match of stage.matchAll(/^\s*COPY\s+(?:--from=\S+\s+)?(\S+)/gm)) {
		const source = match[1];
		if (
			(source?.startsWith("assistant/") && source !== "assistant/package.json") ||
			source?.startsWith("src/assistant/")
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
	releaseRuntimeText?: string,
): DockerCompositionFacts {
	const missing: DockerCompositionFacts = {
		controllerOnlyBuild: false,
		explicitControllerEntrypoint: false,
		effectiveInit: false,
		noAssistantRuntime: false,
		noCombinedImage: false,
		pinnedBunRuntime: false,
		noNodeRuntime: false,
	};
	if (dockerfile === undefined) {
		violations.push("Dockerfile: Dockerfile is required for the controller-only composition check");
		return missing;
	}

	const controller = dockerStage(dockerfile, "pixie_web");
	if (controller === "") {
		violations.push("Dockerfile: pixie_web controller image stage is missing");
		return missing;
	}
	missing.noAssistantRuntime = !hasAssistantRuntimeCopy(controller);
	if (!missing.noAssistantRuntime) {
		violations.push("Dockerfile: controller-only runtime image must not copy assistant source");
	}
	const hasBuildRecipe = strictChecks;
	if (hasBuildRecipe) {
		missing.controllerOnlyBuild = /\bgo\s+build\b[^\n]*-tags(?:=|\s+)controller\b/i.test(
			dockerfile,
		);
		if (!missing.controllerOnlyBuild) {
			violations.push("Dockerfile: controller image build must use the controller build tag");
		}
	} else {
		// Minimal fixtures used by the original BUILD-01 seam do not contain
		// build evidence; defer Docker build-specific assertions to that richer input.
		missing.controllerOnlyBuild = true;
	}

	const launch = [...controller.matchAll(/^\s*(?:ENTRYPOINT|CMD)\b.*$/gm)]
		.map((match) => match[0])
		.join("\n");
	if (launch === "") {
		violations.push("Dockerfile: final application stage must declare an entrypoint or command");
		return missing;
	}
	if (!/\/app\/pixie_web\b/.test(launch)) {
		violations.push("Dockerfile: final entrypoint must run the pixie_web controller binary");
	}
	missing.explicitControllerEntrypoint = /\bserve\b[\s,"']+.*--mode[=\s,"']+controller\b/i.test(
		launch,
	);
	if (!missing.explicitControllerEntrypoint) {
		violations.push(
			"Dockerfile: final entrypoint must explicitly run `pixie_web serve --mode controller`",
		);
	}
	missing.effectiveInit = /(?:^|[\s,"'])\/usr\/bin\/tini(?:[\s,"']|$)/.test(launch);
	if (hasBuildRecipe && !missing.effectiveInit) {
		violations.push(
			"Dockerfile: final entrypoint must run under tini for effective descendant reaping",
		);
	}
	if (hasBuildRecipe && !/COPY\s+--from=web-build\s+[^\n]+\s+\/app\/web\b/i.test(controller)) {
		violations.push("Dockerfile: final controller image must include the built UI bundle");
	}
	if (
		/(?:\/app\/runtime\b|node_modules|@earendil-works\/pi-coding-agent|\bbun\b|\bnode\b|pixie_assistant|\/app\/libexec\/pixie)/i.test(
			controller,
		)
	) {
		violations.push(
			"Dockerfile: pixie_web runtime must contain no Bun, Node, Pi package, assistant, or Pi launcher",
		);
	}
	if (!hasBuildRecipe) return missing;

	const combined = dockerStage(dockerfile, "pixie");
	missing.noCombinedImage = combined === "";
	if (!missing.noCombinedImage) {
		violations.push(
			"Dockerfile: combined pixie image stage was removed; pixie_web is the only published container",
		);
	}
	const releasePinsVerifiedBun =
		releaseRuntimeText === undefined
			? true
			: /BUNDLED_BUN_VERSION\s*=\s*"1\.4\.0"/.test(releaseRuntimeText) &&
				/\bstageBundledBunRuntime\b/.test(releaseRuntimeText) &&
				/\bstageVerifiedBunArchive\b/.test(releaseRuntimeText) &&
				/archive:\s*"bun-linux-x64\.zip"/.test(releaseRuntimeText) &&
				/2d03fb5fb83ac8b567aca0a281b2ce1a1a19d488f56c2968d88c3f25e92fe452/.test(
					releaseRuntimeText,
				) &&
				/archive:\s*"bun-linux-aarch64\.zip"/.test(releaseRuntimeText) &&
				/4b1a332ee861983eb93bcfe6f770fff94e3e31b2c388bdaea3c8ed35e58eed0e/.test(releaseRuntimeText);
	missing.pinnedBunRuntime = releasePinsVerifiedBun;
	if (!missing.pinnedBunRuntime) {
		violations.push(
			"release runtime: host archive must stage and verify pinned Bun 1.4.0 at runtime/bin/bun",
		);
	}
	const nodeRuntimeMarkers = [
		/\bruntime\/node\/bin\/node\b/,
		/\bruntime\/node\b/,
		/\bnode-v\d+\.\d+\.\d+\b/i,
		/\bBUNDLED_NODE_VERSION\b/,
		/\bstageBundledNodeRuntime\b/,
		/\bparseVerifiedNodeArchive\b/,
		/\bstageVerifiedNodeArchive\b/,
	];
	missing.noNodeRuntime =
		!nodeRuntimeMarkers.some((marker) => marker.test(dockerfile)) &&
		(releaseRuntimeText === undefined ||
			!nodeRuntimeMarkers.some((marker) => marker.test(releaseRuntimeText)));
	if (!missing.noNodeRuntime) {
		violations.push(
			"Dockerfile: the Pi-bearing runtime must not reintroduce a bundled Node runtime",
		);
	}
	return missing;
}

function sourceByBasename(
	sources: Readonly<Record<string, string>> | undefined,
	name: string,
): string | undefined {
	return Object.entries(sources ?? {}).find(([path]) => path.split("/").at(-1) === name)?.[1];
}

function execStart(source: string | undefined): string | undefined {
	return source?.match(/^\s*ExecStart=(.+?)\s*$/m)?.[1];
}

function isSystemdAbsolutePath(value: string): boolean {
	return (value.startsWith("/") || value.startsWith("%h/")) && !/[`$~]/.test(value);
}

function isLiteralAbsolutePath(value: unknown): value is string {
	return typeof value === "string" && isAbsolute(value) && !/[`$~]/.test(value);
}

function isLiteralLoopback(value: unknown): boolean {
	return value === "127.0.0.1" || value === "localhost";
}

function isPort(value: unknown): boolean {
	return typeof value === "number" && Number.isInteger(value) && value >= 1 && value <= 65535;
}

function configObject(
	name: string,
	source: string | undefined,
	violations: string[],
): Record<string, unknown> | undefined {
	if (source === undefined) {
		violations.push(`systemd: missing ${name} configuration example`);
		return undefined;
	}
	try {
		const config: unknown = JSON.parse(source);
		if (config === null || typeof config !== "object" || Array.isArray(config)) {
			violations.push(`${name}: configuration example must be a JSON object`);
			return undefined;
		}
		return config as Record<string, unknown>;
	} catch {
		violations.push(`${name}: configuration example is not valid JSON`);
		return undefined;
	}
}

function inspectDeploymentComposition(
	input: CompositionInput,
	violations: string[],
): DeploymentCompositionFacts | undefined {
	if (
		input.composeFileText === undefined &&
		input.systemdUnitSources === undefined &&
		input.systemdConfigSources === undefined
	) {
		return undefined;
	}

	const facts: DeploymentCompositionFacts = {
		composeUsesWebImage: false,
		composeHasNoCombinedImage: false,
		composeWebServiceIsNonRoot: false,
		composeWebUsesDataMount: false,
		composeHasNoFixedContainerNames: false,
		archiveServiceUsesHostCommand: false,
		noLegacyCliUnit: false,
		noPublicAssistantUnit: false,
		ownerLockDoesNotRestart: false,
		secretsInherited: false,
		absoluteConfigPaths: false,
		noShellInterpolation: false,
	};

	const compose = input.composeFileText;
	if (compose === undefined) {
		violations.push(
			"docker-compose.yaml: compose source is required for deployment composition checks",
		);
	} else {
		const service = (name: string): string => {
			const match = compose.match(
				new RegExp(
					`^ {4}${escapeRegExp(name)}:\\n[\\s\\S]*?(?=^ {4}[A-Za-z0-9_-]+:|^configs:|^volumes:|(?![\\s\\S]))`,
					"m",
				),
			);
			return match?.[0] ?? "";
		};
		const web = service("pixie_web");
		const webImage = web.match(/^\s*image:\s*["']?\$\{PIXIE_IMAGE:-([^}"']+)\}["']?\s*$/m)?.[1];
		facts.composeUsesWebImage =
			web !== "" &&
			webImage !== undefined &&
			/^ghcr\.io\/[^/]+\/pixie_web:sha-[0-9a-f]{12}$/.test(webImage);
		if (!facts.composeUsesWebImage) {
			violations.push(
				"docker-compose.yaml: pixie_web must default to an immutable GHCR pixie_web image",
			);
		}
		facts.composeHasNoCombinedImage =
			service("pixie") === "" &&
			service("pixie_pi_state_init") === "" &&
			!/PIXIE_FULL_IMAGE/.test(compose) &&
			!/pixie-pi-state/.test(compose) &&
			!/pixie_full/.test(compose);
		if (!facts.composeHasNoCombinedImage) {
			violations.push(
				"docker-compose.yaml: combined pixie topology was removed; only pixie_web may be configured",
			);
		}
		const imageLines = [...compose.matchAll(/^\s*image:\s*([^\n]+)$/gm)].map(
			(match) => match[1] ?? "",
		);
		if (
			imageLines.length !== 1 ||
			/^\s*build\s*:/m.test(compose) ||
			imageLines.some(
				(image) =>
					!image.includes("ghcr.io/") ||
					!image.includes(":sha-") ||
					/\b(?:latest|main|edge)\b/i.test(image),
			)
		) {
			violations.push(
				"docker-compose.yaml: only the immutable GHCR pixie_web image may be configured (no build or unpinned image)",
			);
		}
		facts.composeHasNoFixedContainerNames = !/^\s*container_name\s*:/m.test(compose);
		if (!facts.composeHasNoFixedContainerNames) {
			violations.push("docker-compose.yaml: fixed container_name values are not allowed");
		}
		facts.composeWebServiceIsNonRoot =
			/^\s*profiles:\s*\["web"\]\s*$/m.test(web) &&
			/^\s*network_mode:\s*host\s*$/m.test(web) &&
			/^\s*user:\s*["']1000:1000["']\s*$/m.test(web) &&
			web.includes('PIXIE_CONTROLLER_PORT: "${PIXIE_CONTROLLER_PORT:-7312}"');
		if (!facts.composeWebServiceIsNonRoot) {
			violations.push(
				"docker-compose.yaml: pixie_web must use the web profile with host networking as UID/GID 1000",
			);
		}
		facts.composeWebUsesDataMount =
			web.includes("${PIXIE_DATA_PATH}:/var/lib/pixie/data") &&
			web.includes('PIXIE_DATA_DIR: "/var/lib/pixie/data"') &&
			!web.includes("PI_CODING_AGENT_DIR");
		if (!facts.composeWebUsesDataMount) {
			violations.push(
				"docker-compose.yaml: pixie_web must mount PIXIE_DATA_PATH at /var/lib/pixie/data and must not mount Pi state",
			);
		}
	}

	const combinedUnit = sourceByBasename(input.systemdUnitSources, "pixie.service");
	const cliUnit = sourceByBasename(input.systemdUnitSources, "pixie_cli.service");
	const combinedCommand = execStart(combinedUnit);
	const combinedMatch = combinedCommand?.match(
		/^%h\/\.local\/bin\/pixie\s+serve\s+--config\s+(\S+)$/,
	);
	facts.archiveServiceUsesHostCommand =
		combinedMatch !== undefined &&
		combinedMatch !== null &&
		isSystemdAbsolutePath(combinedMatch[1] ?? "");
	if (!facts.archiveServiceUsesHostCommand) {
		violations.push("pixie.service: host archive must run `pixie serve --config ABS` directly");
	}
	facts.noLegacyCliUnit = cliUnit === undefined;
	if (!facts.noLegacyCliUnit) {
		violations.push("pixie_cli.service was removed: the single pixie host binary owns serve");
	}
	const staleAssistantUnits = Object.keys(input.systemdUnitSources ?? {}).filter((path) =>
		/(?:^|\/)(?:pixie_assistant|pixie-assistant)\.service$/.test(path),
	);
	facts.noPublicAssistantUnit = staleAssistantUnits.length === 0;
	if (!facts.noPublicAssistantUnit) {
		violations.push(
			`systemd: public pixie_assistant service unit is removed (${staleAssistantUnits.join(", ")})`,
		);
	}

	const units = [combinedUnit];
	facts.secretsInherited = units.every(
		(unit) => unit?.includes("EnvironmentFile=%h/.config/pixie/pixie.env") === true,
	);
	if (!facts.secretsInherited) {
		violations.push("systemd: the service unit must inherit secrets from pixie.env");
	}
	facts.ownerLockDoesNotRestart = units.every(
		(unit) =>
			unit?.includes("Restart=on-failure") === true &&
			unit.includes("RestartForceExitStatus=75") &&
			unit.includes("RestartPreventExitStatus=73") &&
			unit.includes("Environment=PI_CODING_AGENT_DIR=%h/") &&
			!unit.includes("Restart=always"),
	);
	if (!facts.ownerLockDoesNotRestart) {
		violations.push(
			"systemd: pixie must not restart owner-lock exit 73 while retaining restart exit 75",
		);
	}
	facts.noShellInterpolation = [combinedCommand].every(
		(command) =>
			command !== undefined && !/(?:^|\s)(?:\/bin\/)?(?:sh|bash)\s+-c\b|[`$]/.test(command),
	);
	if (!facts.noShellInterpolation) {
		violations.push("systemd: service commands must not use a shell or shell interpolation");
	}

	const assistantConfig = configObject(
		"assistant.json",
		sourceByBasename(input.systemdConfigSources, "assistant.json"),
		violations,
	);
	const webConfig = configObject(
		"pixie.json",
		sourceByBasename(input.systemdConfigSources, "pixie.json"),
		violations,
	);
	const validAssistantConfig =
		assistantConfig !== undefined &&
		assistantConfig.schemaVersion === 2 &&
		isLiteralLoopback(assistantConfig.host) &&
		isPort(assistantConfig.port) &&
		isLiteralAbsolutePath(assistantConfig.agentDir) &&
		assistantConfig.allowSelfRestart === true &&
		assistantConfig.piPackage === undefined;
	if (!validAssistantConfig) {
		violations.push(
			"assistant.json: schemaVersion 2, host/port, absolute agentDir, allowSelfRestart, and no public piPackage are required",
		);
	}
	const validWebConfig =
		webConfig !== undefined &&
		isLiteralLoopback(webConfig.host) &&
		isPort(webConfig.port) &&
		isLiteralAbsolutePath(webConfig.dataDir) &&
		webConfig.agentDir === undefined &&
		webConfig.piPackage === undefined &&
		webConfig.piExecutable === undefined;
	if (!validWebConfig) {
		violations.push(
			"pixie.json: controller host/port and absolute dataDir are required; assistant settings are not allowed",
		);
	}
	facts.absoluteConfigPaths =
		facts.archiveServiceUsesHostCommand &&
		facts.noLegacyCliUnit &&
		validAssistantConfig &&
		validWebConfig;

	return facts;
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

function inspectControllerComposition(
	input: CompositionInput,
	violations: string[],
): ControllerCompositionFacts {
	const hasExtendedSourceEvidence =
		input.packageWebuiSources !== undefined || input.embeddedUiFiles !== undefined;
	if (!hasExtendedSourceEvidence) {
		return {
			controllerDefault: true,
			rejectsLocalAssistant: true,
			uiEmbed: true,
			drain: true,
		};
	}
	const command = sourceText(
		input.packageCommandSources,
		(path) => path.endsWith("/main.go") || path.endsWith("/runtime.go"),
	);
	const config = sourceText(input.packageCommandSources, (path) => path === "cmd/config.go");
	const configFields = /type runtimeConfigFile struct \{([\s\S]*?)\n\}/.exec(config)?.[1];
	const webuiSources = sourceText(
		input.packageWebuiSources,
		(path) => path.endsWith("/webui.go") || path === "webui.go",
	);
	const uiFiles = input.embeddedUiFiles ?? [];
	const facts: ControllerCompositionFacts = {
		// cmd is controller-only: it defaults to controller mode and
		// rejects every other serve mode.
		controllerDefault:
			/mode\s*:?=\s*modeController/.test(command) &&
			/mode\s*!=\s*modeController/.test(command) &&
			/func\s+parseMode/.test(command),
		// The controller never selects a local Pi: agentDir/piExecutable
		// settings are absent from its strict configuration surface.
		rejectsLocalAssistant:
			/rejectControllerAssistantSettings/.test(command) &&
			configFields !== undefined &&
			!/json:"(?:agentDir|piExecutable)"/.test(configFields) &&
			/unknown field/.test(config),
		uiEmbed:
			/go:embed\s+all:dist/.test(webuiSources) &&
			uiFiles.some((path) => /(?:^|\/)dist\/index\.html$/.test(path)),
		// The controller entrypoint drains through the runtime shutdown
		// before the process exits.
		drain:
			/func\s+serveController/.test(command) &&
			/\.Shutdown\s*\(/.test(command) &&
			/context\.WithTimeout\s*\(/.test(command),
	};

	if (!facts.controllerDefault) {
		violations.push("cmd: controller-only entrypoint must default to controller mode");
	}
	if (!facts.rejectsLocalAssistant) {
		violations.push("cmd: controller mode must explicitly reject local assistant settings");
	}
	if (!facts.uiEmbed) {
		violations.push("webui: controller build must embed a real dist/index.html bundle");
	}
	if (!facts.drain) {
		violations.push("cmd: controller shutdown must drain runtime work");
	}
	return facts;
}

export function inspectComposition(input: CompositionInput): CompositionReport {
	const violations: string[] = [];
	const missingLiveEvidence: string[] = [];

	// The assistant is Bun-only: the Go assistant module, its host facade,
	// entrypoint and administration bridge were removed. Any of them
	// reappearing fails the composition check.
	if (input.assistantGoModText !== undefined) {
		violations.push(
			"assistant/go.mod: assistant Go module was removed; the Bun host in assistant/src owns Pi interaction",
		);
	}
	if (input.packageGoModText !== undefined) {
		if (hasGoRequire(input.packageGoModText, REMOVED_ASSISTANT_MODULE)) {
			violations.push(
				`go.mod: must not require the removed assistant Go module ${REMOVED_ASSISTANT_MODULE}`,
			);
		}
		if (/=>\s*\.\.\/assistant(?:\s|$)/m.test(input.packageGoModText)) {
			violations.push("go.mod: must not replace a module with the removed ../assistant tree");
		}
	}

	const assistantGoSources = Object.keys(input.assistantSources).filter((path) =>
		path.endsWith(".go"),
	);
	if (assistantGoSources.length > 0) {
		violations.push(
			`assistant/: Go sources were removed with the Go host (${assistantGoSources.length} remain); Pi interaction lives in assistant/src`,
		);
	}
	for (const prefix of REMOVED_ASSISTANT_PREFIXES) {
		const remaining = Object.keys(input.assistantSources).filter((path) => path.startsWith(prefix));
		if (remaining.length > 0) {
			violations.push(
				`${prefix}: removed assistant tree must not reappear (${remaining.length} file(s))`,
			);
		}
	}

	const commandImports = Object.entries(input.packageCommandSources).flatMap(([path, source]) =>
		extractImportSpecifiers(path, source),
	);
	if (commandImports.some((specifier) => specifier.includes("/pixie/assistant"))) {
		violations.push("cmd: must not import the removed assistant Go module");
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
			// The assistant is Bun-only, so no Go source under assistant/
			// may reach into another module's internals.
			if (specifier.includes("/internal/")) {
				violations.push(
					`${path}: forbidden controller-internal import ${JSON.stringify(specifier)}`,
				);
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
		violations.push(
			`production sources: duplicate supervisor owners (${[...supervisorOwners].sort().join(", ")})`,
		);
	}

	const controller = inspectControllerComposition(input, violations);
	const hasExtendedSourceEvidence =
		input.packageWebuiSources !== undefined || input.embeddedUiFiles !== undefined;
	const docker = checkDockerfile(
		input.dockerfileText,
		violations,
		hasExtendedSourceEvidence,
		input.releaseRuntimeText,
	);
	const deployment = inspectDeploymentComposition(input, violations);
	// Static composition never proves live execution. The controller artifact
	// result is optional evidence a live run can supply; without it the gap
	// stays reported rather than passing silently.
	if (input.controllerArtifactEvidence === undefined) {
		missingLiveEvidence.push("controller binary execution on a supported host");
	} else {
		if (!/(?:^|\/)pixie_web$/.test(input.controllerArtifactEvidence.binaryPath)) {
			missingLiveEvidence.push("controller artifact uses the pixie_web executable");
		}
		if (!input.controllerArtifactEvidence.uiEmbedded) {
			missingLiveEvidence.push("controller artifact contains the real embedded UI bundle");
		}
	}
	return {
		ok: violations.length === 0,
		violations,
		facts: {
			bunServeCount: bunServeLocations.length,
			supervisorOwners: [...supervisorOwners].sort(),
			controller,
			docker,
			...(deployment === undefined ? {} : { deployment }),
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
	repositoryRoot = resolve(import.meta.dir, ".."),
): Promise<CompositionInput> {
	const assistantSources = await collectSourceTree(repositoryRoot, "src/assistant");
	const packageCommandSources = await collectSourceTree(repositoryRoot, "cmd");
	const packageInternalSources = await collectSourceTree(repositoryRoot, "internal");
	const packageWebuiTree = await collectSourceTree(repositoryRoot, "webui");
	const packageWebuiSources = Object.fromEntries(
		Object.entries(packageWebuiTree).filter(([path]) => path.endsWith(".go")),
	);
	const embeddedUiFiles = await collectFilePaths(repositoryRoot, "webui/dist");
	const packageGoModText = await readFile(resolve(repositoryRoot, "go.mod"), "utf8").catch(
		() => undefined,
	);
	const assistantGoModText = await readFile(
		resolve(repositoryRoot, "assistant/go.mod"),
		"utf8",
	).catch(() => undefined);
	const dockerfileText = await readFile(resolve(repositoryRoot, "Dockerfile"), "utf8").catch(
		() => undefined,
	);
	const composeFileText = await readFile(
		resolve(repositoryRoot, "docker-compose.yaml"),
		"utf8",
	).catch(() => undefined);
	const releaseRuntimeText = await readFile(
		resolve(repositoryRoot, "scripts/release-runtime.ts"),
		"utf8",
	).catch(() => undefined);
	const systemdRoot = resolve(repositoryRoot, "systemd");
	const systemdFiles = await readdir(systemdRoot, { withFileTypes: true })
		.then((entries) =>
			Promise.all(
				entries
					.filter((entry) => entry.isFile())
					.map(
						async (entry) =>
							[
								`systemd/${entry.name}`,
								await readFile(resolve(systemdRoot, entry.name), "utf8"),
							] as const,
					),
			),
		)
		.catch(() => [] as const);
	const systemdUnits: Record<string, string> = {};
	const systemdConfigs: Record<string, string> = {};
	for (const [path, source] of systemdFiles) {
		if (source === undefined) continue;
		if (path.endsWith(".service")) systemdUnits[path] = source;
		if (path.endsWith(".json")) systemdConfigs[path] = source;
	}
	// The Go assistant tree is removed. Production-source checks (single
	// Bun.serve, single supervisor owner) apply to the Bun assistant in
	// src/assistant and the controller/UI sources.
	const assistantProductionSources = Object.fromEntries(
		Object.entries(assistantSources).filter(([path]) => !path.endsWith(".go")),
	);
	const optional: Pick<
		CompositionInput,
		| "assistantGoModText"
		| "packageGoModText"
		| "dockerfileText"
		| "composeFileText"
		| "releaseRuntimeText"
	> = {};
	if (assistantGoModText !== undefined) optional.assistantGoModText = assistantGoModText;
	if (packageGoModText !== undefined) optional.packageGoModText = packageGoModText;
	if (dockerfileText !== undefined) optional.dockerfileText = dockerfileText;
	if (composeFileText !== undefined) optional.composeFileText = composeFileText;
	if (releaseRuntimeText !== undefined) optional.releaseRuntimeText = releaseRuntimeText;
	return {
		assistantSources,
		packageCommandSources,
		productionSources: {
			...assistantProductionSources,
			...packageCommandSources,
			...packageInternalSources,
			...packageWebuiSources,
		},
		packageWebuiSources,
		embeddedUiFiles,
		systemdUnitSources: systemdUnits,
		systemdConfigSources: systemdConfigs,
		...optional,
	};
}

export function formatCompositionReport(report: CompositionReport): string {
	if (report.ok) {
		const output =
			`check-composition: OK (Bun-only assistant, ` +
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
	repositoryRoot = resolve(import.meta.dir, ".."),
): Promise<number> {
	const report = inspectComposition(await collectCompositionInput(repositoryRoot));
	const output = formatCompositionReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runCompositionCheck());
