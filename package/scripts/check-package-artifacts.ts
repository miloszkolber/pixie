#!/usr/bin/env bun

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { basename, relative, resolve } from "node:path";

export const PACKAGE_VARIANTS = ["assistant", "host"] as const;
export const PACKAGE_ARCHITECTURES = ["amd64", "arm64"] as const;
export type PackageVariant = (typeof PACKAGE_VARIANTS)[number];
export type PackageArchitecture = (typeof PACKAGE_ARCHITECTURES)[number];

export interface PackageArchiveEvidence {
	name: string;
	entries: readonly string[];
}

export interface PackageBinaryEvidence {
	variant: PackageVariant;
	architecture: PackageArchitecture;
	path: string;
	version?: string;
	doctor?: boolean;
	readiness?: boolean;
	lifecycle?: boolean;
	uninstall?: boolean;
	uiEmbedded?: boolean;
}

export interface PackageArtifactInput {
	releaseId?: string;
	archives?: readonly PackageArchiveEvidence[];
	units?: Readonly<Record<string, string>>;
	configs?: Readonly<Record<string, string>>;
	commandSources?: Readonly<Record<string, string>>;
	webuiSources?: Readonly<Record<string, string>>;
	embeddedUiFiles?: readonly string[];
	facadeSources?: Readonly<Record<string, string>>;
	binaries?: readonly PackageBinaryEvidence[];
}

export interface PackageArtifactFacts {
	expectedArchives: readonly string[];
	observedArchives: readonly string[];
	unitFiles: readonly string[];
	configFiles: readonly string[];
	staticChecks: readonly string[];
	missingLiveEvidence: readonly string[];
}

export interface PackageArtifactReport {
	ok: boolean;
	staticOk: boolean;
	complete: boolean;
	violations: readonly string[];
	missingLiveEvidence: readonly string[];
	facts: PackageArtifactFacts;
}

const RELEASE_ID_PATTERN = /^sha-[0-9a-f]{12}$/;
const ARCHIVE_PATTERN = /^pixie(?:-assistant)?-sha-[0-9a-f]{12}-linux-(?:amd64|arm64)\.tar\.gz$/;
const SECRET_PATTERN = /(?:secret|token|password|passwd|credential|api[-_]?key)/i;
const INSTALL_ENTRY_PATTERN = /^(?:INSTALL|INSTALL\.md|README(?:\.install)?(?:\.md)?)$/i;

function stripComments(source: string): string {
	return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|\s)\/\/.*$/gm, "$1");
}

function expectedArchiveName(variant: PackageVariant, architecture: PackageArchitecture, releaseId: string): string {
	const binary = variant === "assistant" ? "pixie-assistant" : "pixie";
	return `${binary}-${releaseId}-linux-${architecture}.tar.gz`;
}

function expectedBinaryName(variant: PackageVariant): string {
	return variant === "assistant" ? "pixie-assistant" : "pixie";
}

function archiveEntryNames(entries: readonly string[]): Set<string> {
	const names = new Set<string>();
	for (const entry of entries) {
		const normalized = entry.replaceAll("\\", "/").replace(/^\.\//, "").toLowerCase();
		names.add(normalized);
		names.add(basename(normalized));
	}
	return names;
}

function checkUnit(
	name: string,
	source: string,
	violations: string[],
	staticChecks: string[],
): void {
	const required: readonly [RegExp, string][] = [
		[/^\s*\[Unit\]/m, "[Unit] section"],
		[/^\s*\[Service\]/m, "[Service] section"],
		[/^\s*StartLimitIntervalSec=60\s*$/m, "60-second start limit window"],
		[/^\s*StartLimitBurst=5\s*$/m, "start limit burst"],
		[/^\s*Type=exec\s*$/m, "Type=exec"],
		[/^\s*Restart=on-failure\s*$/m, "Restart=on-failure"],
		[/^\s*RestartSec=\d+/m, "RestartSec"],
		[/^\s*TimeoutStopSec=30\s*$/m, "30-second stop ceiling"],
		[/^\s*KillMode=mixed\s*$/m, "KillMode=mixed"],
		[/^\s*UMask=0077\s*$/m, "private UMask"],
		[/^\s*StandardOutput=journal\s*$/m, "journal stdout"],
		[/^\s*StandardError=journal\s*$/m, "journal stderr"],
		[/^\s*\[Install\]/m, "[Install] section"],
		[/^\s*WantedBy=default\.target\s*$/m, "default.target installation"],
	];
	for (const [pattern, label] of required) {
		if (!pattern.test(source)) violations.push(`${name}: missing ${label}`);
	}

	if (name === "pixie-assistant.service") {
		if (!/^\s*ExecStart=.*\bpixie-assistant\b.*\bserve\b.*--config\s+\S+/m.test(source)) {
			violations.push(`${name}: ExecStart must run pixie-assistant with an explicit config`);
		}
	} else if (name === "pixie.service") {
		if (!/^\s*ExecStart=.*\bpixie\b.*\bserve\b.*--config\s+\S+/m.test(source)) {
			violations.push(`${name}: ExecStart must run the full-host pixie binary with an explicit config`);
		}
		if (/^\s*(?:Requires|BindsTo)=.*pixie-assistant\.service/m.test(source)) {
			violations.push(`${name}: full-host unit must not depend on a separate assistant service`);
		}
	}
	if (!/^\s*RestartForceExitStatus=75\s*$/m.test(source)) {
		violations.push(`${name}: requested restart must use exit status 75`);
	}
	staticChecks.push(`${name}: service lifetime directives`);
}

function checkConfig(name: string, source: string | undefined, violations: string[], staticChecks: string[]): void {
	if (source === undefined) {
		violations.push(`package/systemd: missing ${name} configuration example`);
		return;
	}
	if (SECRET_PATTERN.test(source)) {
		violations.push(`${name}: configuration example contains a secret-shaped key`);
	}
	try {
		const config = JSON.parse(source) as Record<string, unknown>;
		if (config === null || typeof config !== "object" || Array.isArray(config)) {
			violations.push(`${name}: configuration example must be a JSON object`);
		}
	} catch {
		violations.push(`${name}: configuration example is not valid JSON`);
	}
	staticChecks.push(`${name}: non-secret JSON configuration`);
}

function checkArchives(
	input: PackageArtifactInput,
	violations: string[],
	missingLiveEvidence: string[],
): { expected: string[]; observed: string[] } {
	const expected: string[] = [];
	const archives = input.archives ?? [];
	const observedReleaseIds = [
		...new Set(
			archives
				.map((archive) => archive.name.match(/-sha-([0-9a-f]{12})-linux-/)?.[1])
				.filter((release): release is string => release !== undefined),
		),
	];
	let releaseId = input.releaseId;
	if (releaseId === undefined && observedReleaseIds.length === 1) releaseId = `sha-${observedReleaseIds[0]}`;
	if (observedReleaseIds.length > 1) violations.push("archives must use one commit-based release ID");
	if (releaseId !== undefined) {
		if (!RELEASE_ID_PATTERN.test(releaseId)) {
			violations.push(`release ID must be sha- plus 12 lowercase hexadecimal characters (${releaseId})`);
		} else {
			for (const variant of PACKAGE_VARIANTS) {
				for (const architecture of PACKAGE_ARCHITECTURES) {
					expected.push(expectedArchiveName(variant, architecture, releaseId));
				}
			}
		}
	}

	const byName = new Map<string, PackageArchiveEvidence>();
	for (const archive of archives) {
		if (byName.has(archive.name)) violations.push(`archive ${archive.name}: duplicate archive evidence`);
		byName.set(archive.name, archive);
		if (!ARCHIVE_PATTERN.test(archive.name)) {
			violations.push(`archive ${archive.name}: name must be commit-based for linux amd64/arm64`);
			continue;
		}
		const entries = archiveEntryNames(archive.entries);
		const isAssistant = archive.name.startsWith("pixie-assistant-");
		const variant: PackageVariant = isAssistant ? "assistant" : "host";
		const binary = expectedBinaryName(variant);
		const unit = `${binary}.service`;
		const config = variant === "assistant" ? "assistant.json" : "pixie.json";
		if (!entries.has(binary)) violations.push(`archive ${archive.name}: missing ${binary} executable`);
		if (!entries.has(unit)) violations.push(`archive ${archive.name}: missing ${unit}`);
		if (!entries.has(config)) violations.push(`archive ${archive.name}: missing ${config}`);
		if (![...entries].some((entry) => INSTALL_ENTRY_PATTERN.test(entry))) {
			violations.push(`archive ${archive.name}: missing concise install/uninstall instructions`);
		}
		for (const legal of ["license", "notice.md"]) {
			if (!entries.has(legal)) violations.push(`archive ${archive.name}: missing ${legal.toUpperCase()} notice`);
		}
		if (variant === "host" && [...entries].some((entry) => entry === "pixie-assistant" || entry === "pixie-assistant.service")) {
			violations.push(`archive ${archive.name}: full-host archive must not ship a separate assistant runtime`);
		}
		if ([...entries].some((entry) => entry === "web" || entry.startsWith("web/"))) {
			violations.push(`archive ${archive.name}: full-host UI must be embedded, not a required web asset directory`);
		}
	}
	if (expected.length === 0) {
		missingLiveEvidence.push("four commit-named host archives (assistant and full-host, linux amd64 and arm64)");
	} else {
		for (const name of expected) {
			if (!byName.has(name)) missingLiveEvidence.push(`release archive ${name}`);
		}
	}
	return { expected, observed: archives.map(({ name }) => name).sort() };
}

function checkStaticCommands(
	input: PackageArtifactInput,
	violations: string[],
	staticChecks: string[],
): void {
	const source = stripComments(Object.values(input.commandSources ?? {}).join("\n"));
	const checks: readonly [RegExp, string][] = [
		[/--version\b|\bversion\b/, "version"],
		[/\/readyz\b|\breadyz\b/, "readiness"],
		[/signal\.NotifyContext|Shutdown\s*\(/, "stop"],
		[/serveController|func\s+\(r \*Runtime\) Start/, "start"],
		[/doctor\b/, "doctor"],
		[/uninstall\b/, "uninstall"],
	];
	for (const [pattern, label] of checks) {
		if (!pattern.test(source)) violations.push(`package binaries: ${label} command/check is not present in the current source`);
		else staticChecks.push(`binary ${label} command/check`);
	}

	const units = Object.values(input.units ?? {}).join("\n");
	if (!/^\s*Restart=on-failure\s*$/m.test(units) || !/^\s*RestartForceExitStatus=75\s*$/m.test(units)) {
		violations.push("package units: restart policy evidence is missing");
	} else {
		staticChecks.push("restart policy and requested-restart status");
	}
}

function checkBinaries(
	binaries: readonly PackageBinaryEvidence[] | undefined,
	input: PackageArtifactInput,
	missingLiveEvidence: string[],
): void {
	const inferredReleaseId = input.releaseId ?? input.archives?.[0]?.name.match(/-sha-([0-9a-f]{12})-linux-/)?.[1];
	const expectedReleaseId = inferredReleaseId === undefined
		? undefined
		: inferredReleaseId.startsWith("sha-") ? inferredReleaseId : `sha-${inferredReleaseId}`;
	const observed = new Map<string, PackageBinaryEvidence>();
	for (const binary of binaries ?? []) {
		const key = `${binary.variant}/${binary.architecture}`;
		if (observed.has(key)) missingLiveEvidence.push(`duplicate live binary evidence ${key}`);
		observed.set(key, binary);
		if (!new RegExp(`(?:^|/)${expectedBinaryName(binary.variant)}$`).test(binary.path ?? "")) {
			missingLiveEvidence.push(`${key} binary path is not the expected executable`);
		}
		if (!binary.version) missingLiveEvidence.push(`${key} --version output`);
		if (expectedReleaseId !== undefined && binary.version !== expectedReleaseId) {
			missingLiveEvidence.push(`${key} --version output matches ${expectedReleaseId}`);
		}
		if (!binary.doctor) missingLiveEvidence.push(`${key} doctor check`);
		if (!binary.readiness) missingLiveEvidence.push(`${key} readiness check`);
		if (!binary.lifecycle) missingLiveEvidence.push(`${key} start/stop/restart check`);
		if (!binary.uninstall) missingLiveEvidence.push(`${key} uninstall check`);
		if (binary.variant === "host" && !binary.uiEmbedded) missingLiveEvidence.push(`${key} real embedded UI check`);
	}
	for (const variant of PACKAGE_VARIANTS) {
		for (const architecture of PACKAGE_ARCHITECTURES) {
			const key = `${variant}/${architecture}`;
			if (!observed.has(key)) missingLiveEvidence.push(`built executable ${key}`);
		}
	}
	if (binaries === undefined || binaries.length === 0) {
		missingLiveEvidence.push("live version/doctor/readiness/lifecycle checks for both binaries on both architectures");
	}
	if (input.facadeSources !== undefined && Object.values(input.facadeSources).some((source) => /ErrUnavailable/.test(source))) {
		missingLiveEvidence.push("full-host binary starts a working assistant engine through the facade");
	}
	if (input.webuiSources !== undefined && !Object.values(input.webuiSources).some((source) => /go:embed\s+all:dist/.test(source))) {
		missingLiveEvidence.push("full-host binary embeds the UI through package/webui");
	}
	if (
		input.embeddedUiFiles !== undefined &&
		!input.embeddedUiFiles.some((path) => /(?:^|\/)dist\/index\.html$/.test(path))
	) {
		missingLiveEvidence.push("full-host binary contains a real dist/index.html bundle");
	}
}

export function inspectPackageArtifacts(input: PackageArtifactInput): PackageArtifactReport {
	const violations: string[] = [];
	const staticChecks: string[] = [];
	const missingLiveEvidence: string[] = [];
	for (const [name, source] of Object.entries(input.units ?? {})) checkUnit(basename(name), source, violations, staticChecks);
	const units = Object.keys(input.units ?? {}).map((name) => basename(name).toLowerCase());
	for (const required of ["pixie-assistant.service", "pixie.service"]) {
		if (!units.includes(required)) violations.push(`package/systemd: missing ${required}`);
	}
	checkConfig("assistant.json", Object.entries(input.configs ?? {}).find(([name]) => basename(name) === "assistant.json")?.[1], violations, staticChecks);
	checkConfig("pixie.json", Object.entries(input.configs ?? {}).find(([name]) => basename(name) === "pixie.json")?.[1], violations, staticChecks);
	checkStaticCommands(input, violations, staticChecks);
	const archives = checkArchives(input, violations, missingLiveEvidence);
	checkBinaries(input.binaries, input, missingLiveEvidence);

	const staticOk = violations.length === 0;
	const complete = missingLiveEvidence.length === 0;
	return {
		ok: staticOk && complete,
		staticOk,
		complete,
		violations,
		missingLiveEvidence,
		facts: {
			expectedArchives: archives.expected,
			observedArchives: archives.observed,
			unitFiles: units.sort(),
			configFiles: Object.keys(input.configs ?? {}).map((name) => basename(name)).sort(),
			staticChecks,
			missingLiveEvidence,
		},
	};
}

async function collectTextTree(repositoryRoot: string, directory: string, extensions: ReadonlySet<string>): Promise<Record<string, string>> {
	const files: Record<string, string> = {};
	const root = resolve(repositoryRoot, directory);
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
			const extension = path.slice(path.lastIndexOf("."));
			if (!entry.isFile() || !extensions.has(extension)) continue;
			files[relative(repositoryRoot, path).replaceAll("\\", "/")] = await readFile(path, "utf8");
		}
	}
	await walk(root);
	return files;
}

async function collectFilePaths(repositoryRoot: string, directory: string): Promise<string[]> {
	const files: string[] = [];
	const root = resolve(repositoryRoot, directory);
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
	await walk(root);
	return files.sort();
}

export async function collectPackageArtifactInput(repositoryRoot = resolve(import.meta.dir, "../..")): Promise<PackageArtifactInput> {
	const commandSources = {
		...(await collectTextTree(repositoryRoot, "package/cmd", new Set([".go"]))),
		...(await collectTextTree(repositoryRoot, "package/internal", new Set([".go"]))),
	};
	const webuiSources = await collectTextTree(repositoryRoot, "package/webui", new Set([".go"]));
	const embeddedUiFiles = await collectFilePaths(repositoryRoot, "package/webui/dist");
	const facadeSources = await collectTextTree(repositoryRoot, "assistant/host", new Set([".go"]));
	const units = await collectTextTree(repositoryRoot, "package/systemd", new Set([".service"]));
	const configs = await collectTextTree(repositoryRoot, "package/systemd", new Set([".json"]));
	return { commandSources, webuiSources, embeddedUiFiles, facadeSources, units, configs };
}

export function formatPackageArtifactReport(report: PackageArtifactReport): string {
	if (report.ok) {
		return `check-package-artifacts: OK (${report.facts.observedArchives.length} archives, ` +
			`${report.facts.unitFiles.length} units, ${report.facts.staticChecks.length} static checks)`;
	}
	return [
		"check-package-artifacts: FAILED",
		...report.violations.map((violation) => `  - ${violation}`),
		...report.missingLiveEvidence.map((item) => `  - missing live artifact evidence: ${item}`),
	].join("\n");
}

export async function runPackageArtifactCheck(repositoryRoot = resolve(import.meta.dir, "../..")): Promise<number> {
	const report = inspectPackageArtifacts(await collectPackageArtifactInput(repositoryRoot));
	const output = formatPackageArtifactReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runPackageArtifactCheck());
