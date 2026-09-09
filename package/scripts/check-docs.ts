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
	checkedCommands: readonly string[];
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
			path.startsWith("package/") ||
			path.startsWith("docs/") ||
			path.startsWith("roadmap/")
		)
	)
		return null;
	return normalizePath(path);
}

function checkLinks(files: Readonly<Record<string, string>>, violations: string[]): number {
	let count = 0;
	for (const [source, markdown] of Object.entries(files)) {
		if (!source.endsWith(".md") || !(source === "README.md" || source.startsWith("docs/")))
			continue;
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
		if (!source.endsWith(".md") || !(source === "README.md" || source.startsWith("docs/")))
			continue;
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
			checkedCommands,
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
		return `check-docs: OK (${report.facts.documentCount} Markdown documents, ${report.facts.localLinks} local links, ${report.facts.checkedCommands.length} commands)`;
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
