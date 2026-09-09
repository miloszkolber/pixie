/**
 * Separate-module boundary for the assistant.
 *
 * The assistant is its own module: it imports nothing controller-internal and
 * composes with the host through the public facade plus an exact local
 * replacement. This file implements the check so regressions prove the
 * boundary instead of repeating ad-hoc string searches in tests.
 *
 * Forbidden controller surfaces:
 * - `package/internal/` (controller implementation)
 * - `package/webui/` (controller UI)
 * - `package/cmd/` (controller entrypoints)
 *
 * Obsolete source roots (`pi/` or `pixie/` beside `assistant/` and `package/`)
 * are also forbidden. Shared schemas under `package/contracts/` are not
 * imported by the current assistant; this module treats them as forbidden for
 * the same reason (no assistant-internal import of another package's source).
 * If a future contract needs sharing it must move through the facade with an
 * explicit allowlist change, not a silent import.
 *
 * Exact local replacement means the workspace resolves `assistant/` and
 * `package/` from this checkout (Bun workspaces) and any Go `require` of an
 * assistant module carries a `replace` to the exact local directory. A
 * published version without a local replace is not exact.
 */

export const ASSISTANT_MODULE_NAME = "@pixie_ai/pixie-assistant";

export const FORBIDDEN_ASSISTANT_IMPORT_SUBSTRINGS: readonly string[] = [
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
];

const COMMENT_OR_STRING_SAFE = true;

export interface ForbiddenAssistantImport {
	specifier: string;
	matched: string;
}

export interface ReplacementCheck {
	ok: boolean;
	details: string[];
}

/**
 * Extract static import specifiers without executing the module.
 * Handles `import ... from "x"`, `import "x"`, `export ... from "x"`,
 * `require("x")`, `require.resolve("x")` and dynamic `import("x")` with a
 * string literal. Comments are stripped so commented examples do not fail.
 */
export function extractStaticImportSpecifiers(source: string): string[] {
	const withoutBlockComments = source.replace(/\/\*[\s\S]*?\*\//g, "");
	const withoutLineComments = withoutBlockComments.replace(/(^|\s)\/\/.*$/gm, "$1");
	const specifiers: string[] = [];
	const patterns: RegExp[] = [
		/\bimport\s+(?:[^'"]+?\sfrom\s+)?["']([^"']+)["']/g,
		/\bexport\s+[^'"]+?\sfrom\s+["']([^"']+)["']/g,
		/\brequire\s*\(\s*["']([^"']+)["']\s*\)/g,
		/\brequire\s*\.\s*resolve\s*\(\s*["']([^"']+)["']\s*\)/g,
		/\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
	];
	for (const pattern of patterns) {
		pattern.lastIndex = 0;
		let match: RegExpExecArray | null;
		while ((match = pattern.exec(withoutLineComments)) !== null) {
			const specifier = match[1];
			if (specifier && COMMENT_OR_STRING_SAFE) specifiers.push(specifier);
		}
	}
	return [...new Set(specifiers)];
}

function matchesForbidden(specifier: string): string | null {
	for (const forbidden of FORBIDDEN_ASSISTANT_IMPORT_SUBSTRINGS) {
		if (specifier.includes(forbidden)) return forbidden;
	}
	// Obsolete sibling roots appear as relative imports that escape assistant/.
	// `../pi/...`, `../../pi`, `../pixie/...` are never valid assistant imports.
	// The Pi SDK package name contains "pi-" and is allowed; only a path
	// segment match counts here, handled above via "/pi/" and "/pixie/".
	if (/(^|\/)pi(\/|$)/.test(specifier) && !specifier.startsWith("@earendil-works/pi")) {
		// Relative escapes that resolve outside assistant/ are the obsolete roots.
		// Bare "pi" executable names are not import specifiers and never reach here.
		if (specifier.startsWith(".") && (specifier.includes("/pi/") || specifier.endsWith("/pi"))) {
			return "../pi/";
		}
	}
	return null;
}

/** Return every forbidden import specifier found in the list. */
export function findForbiddenAssistantImports(specifiers: readonly string[]): ForbiddenAssistantImport[] {
	const found: ForbiddenAssistantImport[] = [];
	for (const specifier of specifiers) {
		const matched = matchesForbidden(specifier);
		if (matched) found.push({ specifier, matched });
	}
	return found;
}

/**
 * Assert a map of file path to source contains no controller-internal imports.
 * Throws an Error listing every violation; passes silently when clean.
 */
export function assertAssistantImportSurface(files: Readonly<Record<string, string>>): void {
	const violations: string[] = [];
	for (const [path, source] of Object.entries(files)) {
		const specifiers = extractStaticImportSpecifiers(source);
		for (const violation of findForbiddenAssistantImports(specifiers)) {
			violations.push(`${path} imports ${JSON.stringify(violation.specifier)} (matched ${JSON.stringify(violation.matched)})`);
		}
	}
	if (violations.length > 0) {
		throw new Error(`Assistant module boundary violated:\n- ${violations.join("\n- ")}`);
	}
}

function parseJsonSafe(text: string): Record<string, unknown> | null {
	try {
		const data: unknown = JSON.parse(text);
		if (data && typeof data === "object" && !Array.isArray(data)) return data as Record<string, unknown>;
		return null;
	} catch {
		return null;
	}
}

/**
 * Verify exact local replacement for the separate assistant module.
 *
 * Checks the Bun workspace linkage and, when Go module text is supplied, that
 * any `require` of an assistant module is paired with a `replace` pointing at
 * the exact local directory. Missing Go text means "Go separation not yet
 * present in this checkout" and does not fail; a versioned require without a
 * local replace does fail.
 */
export function checkExactLocalReplacement(input: {
	rootPackageJsonText?: string;
	assistantPackageJsonText?: string;
	packageGoModText?: string;
	assistantGoModText?: string;
} = {}): ReplacementCheck {
	const details: string[] = [];
	let ok = true;
	const fail = (detail: string): void => {
		ok = false;
		details.push(detail);
	};
	const pass = (detail: string): void => {
		details.push(detail);
	};

	if (input.rootPackageJsonText !== undefined) {
		const root = parseJsonSafe(input.rootPackageJsonText);
		const workspaces = (root?.["workspaces"] as { packages?: unknown } | undefined)?.packages;
		const packages = Array.isArray(workspaces) ? workspaces : [];
		const hasAssistant = packages.includes("assistant");
		const hasPackage = packages.includes("package");
		if (!hasAssistant || !hasPackage) {
			fail(`root workspaces must list exact local "assistant" and "package" (found ${JSON.stringify(packages)})`);
		} else {
			pass(`root workspaces resolve exact local assistant and package`);
		}
	} else {
		pass("root workspace check skipped (no root manifest supplied)");
	}

	if (input.assistantPackageJsonText !== undefined) {
		const manifest = parseJsonSafe(input.assistantPackageJsonText);
		const name = manifest?.["name"];
		if (name !== ASSISTANT_MODULE_NAME) {
			fail(`assistant manifest name must be ${JSON.stringify(ASSISTANT_MODULE_NAME)} (found ${JSON.stringify(name)})`);
		} else {
			pass(`assistant manifest name is ${ASSISTANT_MODULE_NAME}`);
		}
		const deps = {
			...((manifest?.["dependencies"] as Record<string, string> | undefined) ?? {}),
			...((manifest?.["devDependencies"] as Record<string, string> | undefined) ?? {}),
		};
		if ("pixie" in deps) {
			fail(`assistant must not depend on controller package "pixie" (exact local facade only)`);
		} else {
			pass(`assistant has no controller package dependency`);
		}
	} else {
		pass("assistant manifest check skipped (no assistant manifest supplied)");
	}

	const goModChecks: Array<{ label: string; text: string | undefined }> = [
		{ label: "package/go.mod", text: input.packageGoModText },
		{ label: "assistant/go.mod", text: input.assistantGoModText },
	];
	for (const { label, text } of goModChecks) {
		if (text === undefined) continue;
		const requiresAssistant = text
			.split("\n")
			.filter((line) => line.includes("assistant") && line.match(/\brequire\b|\bassistant\//));
		const hasReplace = /^\s*replace\s+.*assistant.*=>\s*(\.\.?\/[^\s]+)/m.test(text);
		const mentionsLocalAssistant = /\.\.\/assistant|\.\/assistant/.test(text);
		if (requiresAssistant.length > 0 && !hasReplace) {
			fail(`${label} requires an assistant module without an exact local replace (=> ../assistant)`);
		} else if (hasReplace && !mentionsLocalAssistant) {
			fail(`${label} replace must point at the exact local checkout (=> ../assistant)`);
		} else {
			pass(`${label} has no published-only assistant require`);
		}
		// Assistant internals are never importable across modules.
		if (/assistant\/internal\//.test(text)) {
			fail(`${label} must not reference assistant/internal across modules`);
		}
	}

	return { ok, details };
}
