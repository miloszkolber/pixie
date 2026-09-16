#!/usr/bin/env bun

import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { relative, resolve } from "node:path";

/**
 * Static documentation checks only.  This checker verifies that the operating
 * docs point at files that exist and describe the commands that the checkout
 * actually exposes.  It deliberately does not turn prose, source inspection,
 * or an example command into runtime/deployment evidence.
 */
export interface DocumentationInput {
	files: Readonly<Record<string, string>>;
	packageScripts?: Readonly<Record<string, string>>;
}

export interface DocumentationFacts {
	documentCount: number;
	localLinks: number;
	pathReferences: number;
	environmentVariables: number;
	checkedCommands: readonly string[];
	documentedMethods: number;
}

export interface DocumentationReport {
	ok: boolean;
	violations: readonly string[];
	facts: DocumentationFacts;
}

const REQUIRED_DOCUMENTS = [
	"README.md",
	"docs/architecture.md",
	"docs/pi.md",
	"docs/deployment.md",
	"docs/development.md",
	"docs/security.md",
] as const;

const REQUIRED_COMMANDS = [
	"check:deps",
	"check:docs",
	"check:coverage",
	"lint",
	"typecheck",
	"test",
	"build",
] as const;

// Environment variables are read by these source roots and must be named in an
// operating document or the example configuration.
const ENVIRONMENT_SOURCE_ROOTS = [
	"assistant/src/",
	"web/cmd/",
	"web/internal/controller/",
	"shared/piprotocol/",
] as const;

// Test-only fixtures and build-time defines that are deliberately not operator
// configuration. The probe variables only cross a spawned test helper, the
// test-mode flag is set by the integration harness, and the assistant
// version/revision are bundler defines rather than process environment.
const NON_OPERATOR_ENVIRONMENT = new Set([
	"PIXIE_ASSISTANT_REVISION",
	"PIXIE_ASSISTANT_VERSION",
	"PIXIE_BUNDLED_PI_TEST_MODE",
	"PIXIE_PI_SDK_PROBE_CHECKER",
	"PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_MARKER",
	"PIXIE_PI_SDK_PROBE_WORKER_SUCCESS_TOKEN",
]);

// The SDK coverage table is hand-maintained, so it drifts from the generated
// controller-method status map. The guard below reads both the generated
// TypeScript module and the Markdown table as text, which keeps the check pure
// over the collected repository snapshot and testable with small fixtures.
const SDK_COVERAGE_DOCUMENT = "docs/sdk-coverage.md";
const GENERATED_PROTOCOL_CATALOG = "shared/src/generated/protocol-catalog.ts";
const HOST_OPERATION_STATUSES = ["available", "unavailable", "absent"] as const;
type HostOperationStatus = (typeof HOST_OPERATION_STATUSES)[number];

function normalizePath(path: string): string {
	return path.replaceAll("\\", "/").replace(/^\.\//, "");
}

function headingSlug(value: string): string {
	return value
		.toLowerCase()
		.replace(/<[^>]*>/g, "")
		.replace(/[`*_~]/g, "")
		.replace(/[^\p{Letter}\p{Number}\s-]/gu, "")
		.trim()
		.replace(/\s+/g, "-");
}

function headingAnchors(markdown: string): Set<string> {
	const anchors = new Set<string>();
	for (const match of markdown.matchAll(/^#{1,6}\s+(.+?)\s*#*\s*$/gm)) {
		const slug = headingSlug(match[1] ?? "");
		if (slug !== "") anchors.add(slug);
	}
	return anchors;
}

function anchorMatches(anchors: Set<string>, fragment: string): boolean {
	const normalized = fragment.toLowerCase();
	if (anchors.has(normalized)) return true;
	const collapsed = normalized.replace(/-+/g, "-");
	return [...anchors].some((anchor) => anchor.replace(/-+/g, "-") === collapsed);
}

function localPathReference(value: string): string | null {
	const candidate = value
		.trim()
		.replace(/^['"]|['"]$/g, "")
		.replace(/[),.;]+$/, "");
	if (
		candidate === "" ||
		candidate.startsWith("http://") ||
		candidate.startsWith("https://") ||
		candidate.startsWith("mailto:") ||
		candidate.startsWith("#") ||
		candidate.includes(" ") ||
		candidate.includes("<") ||
		candidate.includes(">")
	)
		return null;
	const path = candidate.split(":", 1)[0] ?? candidate;
	if (
		!(
			path.startsWith("assistant/") ||
			path.startsWith("web/") ||
			path.startsWith("shared/") ||
			path.startsWith("docs/") ||
			path.startsWith("roadmap/")
		)
	)
		return null;
	return normalizePath(path);
}

// Checked sources are the operating docs plus the planning roadmap. Planning
// copies must not hide broken links or stale paths.
function isCheckedSource(path: string): boolean {
	return path === "README.md" || path.startsWith("docs/") || path.startsWith("roadmap/");
}

function checkLinks(files: Readonly<Record<string, string>>, violations: string[]): number {
	let count = 0;
	for (const [source, markdown] of Object.entries(files)) {
		if (!source.endsWith(".md") || !isCheckedSource(source)) continue;
		for (const match of markdown.matchAll(/!?\[[^\]]*\]\(([^)]+)\)/g)) {
			const target = (match[1] ?? "").trim().split(/\s+/, 1)[0] ?? "";
			if (
				target === "" ||
				target.startsWith("http://") ||
				target.startsWith("https://") ||
				target.startsWith("mailto:")
			)
				continue;
			count += 1;
			const [pathPart, fragment] = target.split("#", 2);
			if (pathPart === "") {
				if (fragment !== undefined && !anchorMatches(headingAnchors(markdown), fragment)) {
					violations.push(`${source}: missing heading anchor #${fragment}`);
				}
				continue;
			}
			const targetPath = normalizePath(
				resolve("/", source, "../", pathPart ?? "").replace(/^\//, ""),
			);
			const destination = files[targetPath];
			if (destination === undefined) {
				violations.push(`${source}: local link target does not exist: ${target}`);
				continue;
			}
			if (fragment !== undefined && !anchorMatches(headingAnchors(destination), fragment)) {
				violations.push(`${source}: target ${pathPart} has no heading anchor #${fragment}`);
			}
		}
	}
	return count;
}

function checkPathReferences(
	files: Readonly<Record<string, string>>,
	violations: string[],
): number {
	let count = 0;
	for (const [source, markdown] of Object.entries(files)) {
		if (!source.endsWith(".md") || !isCheckedSource(source)) continue;
		for (const match of markdown.matchAll(/`([^`]+)`/g)) {
			const reference = localPathReference(match[1] ?? "")?.replace(/\/+$/, "");
			if (reference === undefined || reference === null) continue;
			count += 1;
			if (
				files[reference] !== undefined ||
				Object.keys(files).some((path) => path.startsWith(`${reference}/`))
			)
				continue;
			// A source reference may include a line/range suffix.  The source file
			// itself is the factual part and is checked without that suffix.
			const sourcePath = reference.replace(/:\d+(?:-\d+)?$/, "");
			if (files[sourcePath] !== undefined) continue;
			violations.push(`${source}: referenced path does not exist: ${reference}`);
		}
	}
	return count;
}

function isEnvironmentSource(path: string): boolean {
	if (!ENVIRONMENT_SOURCE_ROOTS.some((root) => path.startsWith(root))) return false;
	if (path.endsWith("_test.go") || path.endsWith(".test.ts") || path.endsWith(".test.tsx")) {
		return false;
	}
	return !path.split("/").some((segment) => segment === "tests" || segment === "testdata");
}

// Every PIXIE_* name read by the scanned source must appear in the operating
// docs or the example configuration. A name that is only used as a prefix
// (trailing underscore) is not a variable and is ignored. The check is a
// documentation-consistency gate only: naming a variable in prose does not
// establish its runtime behavior.
function checkEnvironmentDocumentation(
	files: Readonly<Record<string, string>>,
	violations: string[],
): number {
	const documented = Object.entries(files)
		.filter(([path]) => path === ".pixie.example" || path.startsWith("docs/"))
		.map(([, text]) => text)
		.join("\n");
	// First source occurrence per name keeps one violation per distinct
	// variable even when a symbol or reject-list repeats it. Files are visited
	// in path order so the reported location is deterministic.
	const referenced = new Map<string, string>();
	for (const [source, text] of Object.entries(files).sort(([a], [b]) => a.localeCompare(b))) {
		if (!isEnvironmentSource(source)) continue;
		for (const match of text.matchAll(/PIXIE_[A-Z0-9_]+/g)) {
			const name = match[0] ?? "";
			if (name.endsWith("_") || referenced.has(name)) continue;
			referenced.set(name, source);
		}
	}
	for (const [name, source] of [...referenced].sort(([a], [b]) => a.localeCompare(b))) {
		if (NON_OPERATOR_ENVIRONMENT.has(name) || documented.includes(name)) continue;
		violations.push(`${source}: undocumented environment variable ${name}`);
	}
	return referenced.size;
}

interface GeneratedControllerMethods {
	order: readonly string[];
	status: ReadonlyMap<string, HostOperationStatus>;
}

interface DocumentedMethodSection {
	status: HostOperationStatus;
	headingCount: number | null;
	methods: readonly string[];
}

interface DocumentedMethodCoverage {
	declaredTotal: number | null;
	sections: readonly DocumentedMethodSection[];
}

function isHostOperationStatus(value: string): value is HostOperationStatus {
	return HOST_OPERATION_STATUSES.some((status) => status === value);
}

// Reads CONTROLLER_METHODS and CONTROLLER_METHOD_STATUS from the generated
// TypeScript module. The generated file is the single source of truth for the
// controller method set, and the doc table must not invent or omit methods.
function parseGeneratedControllerMethods(catalog: string): GeneratedControllerMethods | null {
	const methodsAnchor = catalog.indexOf("export const CONTROLLER_METHODS");
	const statusAnchor = catalog.indexOf("export const CONTROLLER_METHOD_STATUS");
	if (methodsAnchor < 0 || statusAnchor < 0) return null;
	const methodsBlock = /\[([\s\S]*?)\]\s*as const/.exec(catalog.slice(methodsAnchor));
	if (methodsBlock === null) return null;
	const order = [...(methodsBlock[1] ?? "").matchAll(/"([^"]+)"/g)].map((match) => match[1] ?? "");
	const statusBlock = /=\s*\{([\s\S]*?)\n\};/.exec(catalog.slice(statusAnchor));
	if (statusBlock === null) return null;
	const status = new Map<string, HostOperationStatus>();
	for (const match of (statusBlock[1] ?? "").matchAll(
		/"([^"]+)"\s*:\s*"(available|unavailable|absent)"/g,
	)) {
		const name = match[1] ?? "";
		const value = match[2] ?? "";
		if (isHostOperationStatus(value)) status.set(name, value);
	}
	return { order, status };
}

// Restricts parsing to the "Method coverage" section so later prose cannot add
// or remove rows, and stops at the next level-two heading.
function methodCoverageSection(markdown: string): string | null {
	const heading = "## Method coverage";
	const start = markdown.indexOf(heading);
	if (start < 0) return null;
	const remainder = markdown.slice(start + heading.length);
	const nextHeading = remainder.search(/\n##\s/);
	return nextHeading < 0 ? remainder : remainder.slice(0, nextHeading);
}

function parseDocumentedMethodCoverage(section: string): DocumentedMethodCoverage {
	const totalMatch = /enumerates all\s+(\d+)\s+methods/i.exec(section);
	const declaredTotal = totalMatch === null ? null : Number.parseInt(totalMatch[1] ?? "", 10);
	const headings = [...section.matchAll(/^###\s+([A-Za-z-]+)\s*(?:\((\d+)\))?\s*$/gm)];
	const sections: DocumentedMethodSection[] = [];
	for (let index = 0; index < headings.length; index += 1) {
		const heading = headings[index];
		const status = (heading?.[1] ?? "").toLowerCase();
		if (!isHostOperationStatus(status)) continue;
		const bodyStart = (heading?.index ?? 0) + (heading?.[0]?.length ?? 0);
		const bodyEnd = headings[index + 1]?.index ?? section.length;
		const body = section.slice(bodyStart, bodyEnd);
		const methods = [...body.matchAll(/^\|\s*`([^`]+)`\s*\|/gm)].map((match) => match[1] ?? "");
		const headingValue = heading?.[2];
		sections.push({
			status,
			headingCount: headingValue === undefined ? null : Number.parseInt(headingValue, 10),
			methods,
		});
	}
	return { declaredTotal, sections };
}

// Ties docs/sdk-coverage.md to shared/src/generated/protocol-catalog.ts. A
// method table that was corrected by hand and never re-read from the generated
// status map fails here with the exact count, split, name or status mismatch.
function checkSdkCoverage(files: Readonly<Record<string, string>>, violations: string[]): number {
	const coverage = files[SDK_COVERAGE_DOCUMENT];
	if (coverage === undefined) return 0;
	const catalogText = files[GENERATED_PROTOCOL_CATALOG];
	const catalog = catalogText === undefined ? null : parseGeneratedControllerMethods(catalogText);
	if (catalog === null) {
		violations.push(
			`${SDK_COVERAGE_DOCUMENT}: cannot read the generated controller-method status map from ${GENERATED_PROTOCOL_CATALOG}`,
		);
		return 0;
	}
	const section = methodCoverageSection(coverage);
	if (section === null) {
		violations.push(`${SDK_COVERAGE_DOCUMENT}: missing the Method coverage section`);
		return 0;
	}
	const documented = parseDocumentedMethodCoverage(section);
	const catalogCount = catalog.order.length;
	const expectedByStatus = new Map<HostOperationStatus, number>(
		HOST_OPERATION_STATUSES.map((status) => [status, 0]),
	);
	for (const method of catalog.order) {
		const status = catalog.status.get(method);
		if (status === undefined) continue;
		expectedByStatus.set(status, (expectedByStatus.get(status) ?? 0) + 1);
	}
	if (documented.declaredTotal !== null && documented.declaredTotal !== catalogCount) {
		violations.push(
			`${SDK_COVERAGE_DOCUMENT}: declared method count ${documented.declaredTotal} does not match the generated catalog count ${catalogCount}`,
		);
	}
	for (const status of HOST_OPERATION_STATUSES) {
		const expected = expectedByStatus.get(status) ?? 0;
		const entry = documented.sections.find((candidate) => candidate.status === status);
		const listed = entry?.methods.length ?? 0;
		if (entry?.headingCount != null && entry.headingCount !== expected) {
			violations.push(
				`${SDK_COVERAGE_DOCUMENT}: ${status} heading reports ${entry.headingCount} but the generated catalog has ${expected}`,
			);
		}
		if (listed !== expected) {
			violations.push(
				`${SDK_COVERAGE_DOCUMENT}: ${status} table lists ${listed} methods but the generated catalog has ${expected}`,
			);
		}
	}
	const seen = new Set<string>();
	for (const entry of documented.sections) {
		for (const method of entry.methods) {
			const catalogStatus = catalog.status.get(method);
			if (catalogStatus === undefined) {
				violations.push(
					`${SDK_COVERAGE_DOCUMENT}: documented method ${method} is not a generated controller method`,
				);
				continue;
			}
			if (seen.has(method)) {
				violations.push(
					`${SDK_COVERAGE_DOCUMENT}: documented method ${method} is listed more than once`,
				);
				continue;
			}
			seen.add(method);
			if (catalogStatus !== entry.status) {
				violations.push(
					`${SDK_COVERAGE_DOCUMENT}: documented method ${method} is listed under ${entry.status} but the generated catalog marks it ${catalogStatus}`,
				);
			}
		}
	}
	for (const method of catalog.order) {
		if (!seen.has(method)) {
			violations.push(
				`${SDK_COVERAGE_DOCUMENT}: generated controller method ${method} is not documented`,
			);
		}
	}
	return documented.sections.reduce((total, entry) => total + entry.methods.length, 0);
}

export function inspectDocumentation(input: DocumentationInput): DocumentationReport {
	const violations: string[] = [];
	const files = Object.fromEntries(
		Object.entries(input.files).map(([path, text]) => [normalizePath(path), text]),
	);
	for (const required of REQUIRED_DOCUMENTS) {
		if (files[required] === undefined) violations.push(`missing operating document ${required}`);
	}
	const localLinks = checkLinks(files, violations);
	const pathReferences = checkPathReferences(files, violations);
	const environmentVariables = checkEnvironmentDocumentation(files, violations);
	const documentedMethods = checkSdkCoverage(files, violations);
	const checkedCommands: string[] = [];
	const development = files["docs/development.md"] ?? "";
	for (const command of REQUIRED_COMMANDS) {
		if (!development.includes(`bun run ${command}`)) {
			violations.push(`docs/development.md: missing documented command bun run ${command}`);
			continue;
		}
		if (input.packageScripts?.[command] === undefined) {
			violations.push(`package.json: documented command ${command} has no package script`);
			continue;
		}
		checkedCommands.push(command);
	}
	const security = files["docs/security.md"] ?? "";
	for (const phrase of ["verified-from-code", "same-UID", "not a sandbox"]) {
		if (!security.toLowerCase().includes(phrase.toLowerCase())) {
			violations.push(
				`docs/security.md: missing factual security qualifier ${JSON.stringify(phrase)}`,
			);
		}
	}
	const operatingDocs = Object.entries(files)
		.filter(([path]) => path === "README.md" || path.startsWith("docs/"))
		.map(([, text]) => text);
	for (const forbidden of ["roadmap/MCP.md", "roadmap/mcp.md", "roadmap/review-pass.md"]) {
		if (operatingDocs.some((text) => text.includes(forbidden))) {
			violations.push(`documentation references removed planning copy ${forbidden}`);
		}
	}
	return {
		ok: violations.length === 0,
		violations,
		facts: {
			documentCount: Object.keys(files).filter((path) => path.endsWith(".md")).length,
			localLinks,
			pathReferences,
			environmentVariables,
			checkedCommands,
			documentedMethods,
		},
	};
}

async function collectRepositoryFiles(repositoryRoot: string): Promise<Record<string, string>> {
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
			const path = resolve(directory, entry.name);
			if (entry.name === ".git" || entry.name === "node_modules" || entry.name === "dist") continue;
			if (entry.isDirectory()) {
				await walk(path);
			} else if (entry.isFile()) {
				try {
					files[normalizePath(relative(repositoryRoot, path))] = await readFile(path, "utf8");
				} catch {
					// Binary/unreadable files still count as an existing path target.
					files[normalizePath(relative(repositoryRoot, path))] = "";
				}
			}
		}
	}
	await walk(repositoryRoot);
	return files;
}

export async function collectDocumentationInput(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<DocumentationInput> {
	const files = await collectRepositoryFiles(repositoryRoot);
	const packageText = await readFile(resolve(repositoryRoot, "package.json"), "utf8").catch(
		() => "{}",
	);
	let packageScripts: Readonly<Record<string, string>> = {};
	try {
		const parsed = JSON.parse(packageText) as { scripts?: Record<string, string> };
		packageScripts = parsed.scripts ?? {};
	} catch {
		packageScripts = {};
	}
	return { files, packageScripts };
}

export function formatDocumentationReport(report: DocumentationReport): string {
	if (report.ok) {
		return `check-docs: OK (${report.facts.documentCount} Markdown documents, ${report.facts.localLinks} local links, ${report.facts.environmentVariables} environment variables, ${report.facts.checkedCommands.length} commands, ${report.facts.documentedMethods} documented SDK methods)`;
	}
	return ["check-docs: FAILED", ...report.violations.map((violation) => `  - ${violation}`)].join(
		"\n",
	);
}

export async function runDocumentationCheck(
	repositoryRoot = resolve(import.meta.dir, "../.."),
): Promise<number> {
	const report = inspectDocumentation(await collectDocumentationInput(repositoryRoot));
	const output = formatDocumentationReport(report);
	if (report.ok) console.log(output);
	else console.error(output);
	return report.ok ? 0 : 1;
}

if (import.meta.main) process.exit(await runDocumentationCheck());
