#!/usr/bin/env bun

/**
 * AUX-09 — Portable anti-slop convention subset.
 *
 * This ports a small, dependency-free subset of `pi-ui`'s oxlint anti-slop
 * rules to a TypeScript-compiler check. The rules are:
 *
 *   1. `object-parameters`  — no `object` function parameters (direct or via a
 *                             same-file type alias); accept a named owner type.
 *   2. `unknown-parameters` — no `unknown` parameters except a parameter named
 *                             `cause`; decode at the I/O boundary. Suppressed
 *                             inside the explicit boundary roots.
 *   3. `unknown-returns`    — no explicit `unknown`/`Promise<unknown>` return
 *                             contract; return a parsed domain type. Suppressed
 *                             inside the explicit boundary roots.
 *   4. `chained-assertions` — no nested non-const type assertions; they erase
 *                             the evidence the type system preserved.
 *   5. `safety-comment`     — every non-const type assertion needs a nearby
 *                             `SAFETY:` comment stating the unwritten
 *                             invariant.
 *   6. `array-filter-map`   — no `filter().map()` second pass; project in one
 *                             traversal or use a fused helper.
 *   7. `accumulating-spread`— no object/array spread of an accumulator inside
 *                             a loop or a `reduce` callback.
 *
 * Explicit boundaries are architectural, not a loophole: the assistant SDK /
 * process adapter and the shared wire-protocol roots legitimately carry
 * unparsed values until they narrow them, so `unknown` is allowed there.
 * Existing adapter-local occurrences outside those roots are recorded in
 * `conventions-boundaries.json` as a reviewed baseline with a per-file
 * ceiling. The gate fails whenever a file exceeds its ceiling, so it fails on
 * any seeded violation and passes on the current tree while the backlog is
 * burned down.
 *
 * Run `bun scripts/check-conventions.ts --print-baseline` to emit the current
 * ceilings for review; the default invocation only reads the committed file.
 * The check is deterministic and bounded to production sources under
 * `assistant/src`, `shared/src`, `web/webui/src` and `web/scripts`.
 */

import { readdir, readFile } from "node:fs/promises";
import { relative, resolve, sep } from "node:path";
import ts from "typescript";

const repositoryRoot = resolve(import.meta.dir, "../..");
const boundariesPath = resolve(import.meta.dir, "conventions-boundaries.json");

const scanRoots = ["assistant/src", "shared/src", "web/webui/src", "web/scripts"] as const;

const ignoredDirectories = new Set([".build", ".tmp-work", "coverage", "dist", "node_modules"]);

type RuleId =
	| "object-parameters"
	| "unknown-parameters"
	| "unknown-returns"
	| "chained-assertions"
	| "safety-comment"
	| "array-filter-map"
	| "accumulating-spread";

const ruleIds: readonly RuleId[] = [
	"object-parameters",
	"unknown-parameters",
	"unknown-returns",
	"chained-assertions",
	"safety-comment",
	"array-filter-map",
	"accumulating-spread",
];

/** Rules that may carry raw values inside an explicit architectural boundary. */
const boundaryRules: readonly RuleId[] = ["unknown-parameters", "unknown-returns"];
const boundaryRoots: readonly string[] = ["assistant/src", "shared/src"];

interface BoundariesFile {
	readonly schemaVersion: number;
	readonly $comment?: string;
	readonly boundaryRules: readonly string[];
	readonly boundaryRoots: readonly string[];
	readonly baseline: Readonly<Record<string, Readonly<Record<string, number>>>>;
}

interface Violation {
	readonly rule: RuleId;
	readonly file: string;
	readonly line: number;
	readonly detail: string;
}

function displayPath(path: string): string {
	return relative(repositoryRoot, path).split(sep).join("/");
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
	return error instanceof Error ? error.message : String(error);
}

function isErrnoLike(value: unknown): value is { readonly code?: unknown } {
	return isRecord(value) && "code" in value;
}

function isBoundary(file: string, boundaries: BoundariesFile, rule: RuleId): boolean {
	if (!boundaries.boundaryRules.includes(rule)) return false;
	return boundaries.boundaryRoots.some((root) => file === root || file.startsWith(`${root}/`));
}

/** Replaces everything outside `<script>` blocks with spaces, preserving lines. */
function maskSvelte(raw: string): string {
	const chars = raw.split("");
	let index = 0;
	while (index < raw.length) {
		const open = raw.indexOf("<script", index);
		if (open < 0) break;
		const openEnd = raw.indexOf(">", open);
		if (openEnd < 0) break;
		const close = raw.indexOf("</script>", openEnd);
		if (close < 0) break;
		for (let cursor = open; cursor < openEnd + 1; cursor += 1) {
			if (chars[cursor] !== "\n") chars[cursor] = " ";
		}
		for (let cursor = close; cursor < close + "</script>".length; cursor += 1) {
			if (chars[cursor] !== "\n") chars[cursor] = " ";
		}
		index = close + "</script>".length;
	}
	return chars.join("");
}

function lineOf(source: ts.SourceFile, node: ts.Node): number {
	return source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
}

function parameterAnnotation(parameter: ts.ParameterDeclaration): ts.TypeNode | undefined {
	return parameter.type;
}

function isConstAssertion(node: ts.AsExpression | ts.TypeAssertion): boolean {
	return (
		ts.isTypeReferenceNode(node.type) &&
		ts.isIdentifier(node.type.typeName) &&
		node.type.typeName.text === "const"
	);
}

function unwrapExpression(expression: ts.Expression): ts.Expression {
	let current = expression;
	while (ts.isParenthesizedExpression(current)) current = current.expression;
	return current;
}

function isAssertion(node: ts.Node): node is ts.AsExpression | ts.TypeAssertion {
	return ts.isAsExpression(node) || ts.isTypeAssertionExpression(node);
}

function resolvesToObject(
	type: ts.TypeNode,
	aliases: ReadonlyMap<string, ts.TypeNode>,
	visited: ReadonlySet<string> = new Set(),
): boolean {
	if (type.kind === ts.SyntaxKind.ObjectKeyword) return true;
	if (ts.isParenthesizedTypeNode(type)) return resolvesToObject(type.type, aliases, visited);
	if (ts.isUnionTypeNode(type)) {
		return type.types.some((member) => resolvesToObject(member, aliases, visited));
	}
	if (ts.isTypeReferenceNode(type) && ts.isIdentifier(type.typeName)) {
		const name = type.typeName.text;
		if (type.typeArguments !== undefined || visited.has(name)) return false;
		const alias = aliases.get(name);
		if (alias === undefined) return false;
		return resolvesToObject(alias, aliases, new Set([...visited, name]));
	}
	return false;
}

function resolvesToUnknown(
	type: ts.TypeNode,
	aliases: ReadonlyMap<string, ts.TypeNode>,
	visited: ReadonlySet<string> = new Set(),
): boolean {
	if (type.kind === ts.SyntaxKind.UnknownKeyword) return true;
	if (ts.isParenthesizedTypeNode(type)) return resolvesToUnknown(type.type, aliases, visited);
	if (ts.isUnionTypeNode(type)) {
		return type.types.some((member) => resolvesToUnknown(member, aliases, visited));
	}
	if (ts.isTypeReferenceNode(type) && ts.isIdentifier(type.typeName)) {
		const name = type.typeName.text;
		if (name === "Promise" || name === "PromiseLike") {
			const value = type.typeArguments?.[0];
			return value !== undefined && resolvesToUnknown(value, aliases, visited);
		}
		if (type.typeArguments !== undefined || visited.has(name)) return false;
		const alias = aliases.get(name);
		if (alias === undefined) return false;
		return resolvesToUnknown(alias, aliases, new Set([...visited, name]));
	}
	return false;
}

function hasSafetyComment(source: ts.SourceFile, text: string, node: ts.Node): boolean {
	const owners = new Set<ts.SyntaxKind>([
		ts.SyntaxKind.ExpressionStatement,
		ts.SyntaxKind.PropertyDeclaration,
		ts.SyntaxKind.ReturnStatement,
		ts.SyntaxKind.ThrowStatement,
		ts.SyntaxKind.VariableStatement,
	]);
	let current: ts.Node | undefined = node;
	while (current !== undefined) {
		const comments = ts.getLeadingCommentRanges(text, current.getFullStart()) ?? [];
		for (const comment of comments) {
			if (
				comment.end <= node.getStart(source) &&
				/\bSAFETY\s*:/.test(text.slice(comment.pos, comment.end))
			) {
				return true;
			}
		}
		if (owners.has(current.kind) || current.parent === undefined) return false;
		current = current.parent;
	}
	return false;
}

/** True when the spread's literal is reassigned into the same binding inside a loop. */
function isLoopAccumulator(literal: ts.Node, name: string): boolean {
	const parent = literal.parent;
	if (
		!ts.isBinaryExpression(parent) ||
		parent.operatorToken.kind !== ts.SyntaxKind.EqualsToken ||
		!ts.isIdentifier(parent.left) ||
		parent.left.text !== name
	) {
		return false;
	}
	let current: ts.Node | undefined = parent.parent;
	while (current !== undefined) {
		switch (current.kind) {
			case ts.SyntaxKind.ForStatement:
			case ts.SyntaxKind.ForInStatement:
			case ts.SyntaxKind.ForOfStatement:
			case ts.SyntaxKind.WhileStatement:
			case ts.SyntaxKind.DoStatement:
				return true;
			case ts.SyntaxKind.FunctionDeclaration:
			case ts.SyntaxKind.FunctionExpression:
			case ts.SyntaxKind.ArrowFunction:
			case ts.SyntaxKind.MethodDeclaration:
				return false;
			default:
				current = current.parent;
		}
	}
	return false;
}

/** True when the spread literal is returned from a reduce callback's accumulator. */
function isReduceAccumulator(literal: ts.Node, name: string): boolean {
	if (!ts.isReturnStatement(literal.parent)) return false;
	let current: ts.Node | undefined = literal.parent.parent;
	while (current !== undefined) {
		if (
			ts.isFunctionDeclaration(current) ||
			ts.isFunctionExpression(current) ||
			ts.isArrowFunction(current) ||
			ts.isMethodDeclaration(current)
		) {
			const first = current.parameters[0];
			return (
				first !== undefined &&
				ts.isIdentifier(first.name) &&
				first.name.text === name &&
				isReduceCallback(current)
			);
		}
		current = current.parent;
	}
	return false;
}

function isAccumulatingSpread(node: ts.SpreadElement): boolean {
	const literal = node.parent;
	if (!ts.isObjectLiteralExpression(literal) && !ts.isArrayLiteralExpression(literal)) {
		return false;
	}
	const argument = node.expression;
	if (!ts.isIdentifier(argument)) return false;
	return isLoopAccumulator(literal, argument.text) || isReduceAccumulator(literal, argument.text);
}

function isReduceCallback(fn: ts.Node): boolean {
	const parent = fn.parent;
	if (parent === undefined || !ts.isCallExpression(parent)) return false;
	const callee = parent.expression;
	return (
		ts.isPropertyAccessExpression(callee) &&
		(callee.name.text === "reduce" || callee.name.text === "reduceRight")
	);
}

function checkSource(absolutePath: string, text: string, violations: Violation[]): void {
	const file = displayPath(absolutePath);
	const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
	const aliases = new Map<string, ts.TypeNode>();
	for (const statement of source.statements) {
		if (ts.isTypeAliasDeclaration(statement) && statement.typeParameters === undefined) {
			aliases.set(statement.name.text, statement.type);
		}
	}

	const visit = (node: ts.Node): void => {
		if (ts.isFunctionLike(node)) {
			for (const parameter of node.parameters) {
				const annotation = parameterAnnotation(parameter);
				if (annotation === undefined) continue;
				if (resolvesToObject(annotation, aliases)) {
					violations.push({
						rule: "object-parameters",
						file,
						line: lineOf(source, annotation),
						detail: "",
					});
				}
				if (annotation.kind === ts.SyntaxKind.UnknownKeyword) {
					const name = ts.isIdentifier(parameter.name) ? parameter.name.text : "";
					if (name !== "cause") {
						violations.push({
							rule: "unknown-parameters",
							file,
							line: lineOf(source, annotation),
							detail: "",
						});
					}
				}
			}
			if (node.type !== undefined && resolvesToUnknown(node.type, aliases)) {
				violations.push({
					rule: "unknown-returns",
					file,
					line: lineOf(source, node.type),
					detail: "",
				});
			}
		}

		if (isAssertion(node)) {
			let count = 0;
			let hasNonConst = false;
			let cursor: ts.Expression = node;
			while (isAssertion(cursor)) {
				count += 1;
				if (!isConstAssertion(cursor)) hasNonConst = true;
				cursor = unwrapExpression(cursor.expression);
			}
			const parent = node.parent;
			const outermost = !isAssertion(parent) || parent.expression !== node;
			if (outermost && count > 1 && hasNonConst) {
				violations.push({
					rule: "chained-assertions",
					file,
					line: lineOf(source, node),
					detail: `${count} assertions`,
				});
			}
			if (!isConstAssertion(node) && !hasSafetyComment(source, text, node)) {
				violations.push({
					rule: "safety-comment",
					file,
					line: lineOf(source, node),
					detail: "",
				});
			}
		}

		if (
			ts.isCallExpression(node) &&
			ts.isPropertyAccessExpression(node.expression) &&
			node.expression.name.text === "map"
		) {
			const inner = unwrapExpression(node.expression.expression);
			if (
				ts.isCallExpression(inner) &&
				ts.isPropertyAccessExpression(inner.expression) &&
				inner.expression.name.text === "filter"
			) {
				violations.push({
					rule: "array-filter-map",
					file,
					line: lineOf(source, node),
					detail: "",
				});
			}
		}

		if (ts.isSpreadElement(node) && isAccumulatingSpread(node)) {
			violations.push({
				rule: "accumulating-spread",
				file,
				line: lineOf(source, node),
				detail: "",
			});
		}

		ts.forEachChild(node, visit);
	};
	visit(source);
}

async function walk(directory: string, files: string[]): Promise<void> {
	let entries: Awaited<ReturnType<typeof readdir>>;
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
		if (!/\.(?:ts|tsx|svelte)$/.test(entry.name)) continue;
		files.push(resolve(directory, entry.name));
	}
}

async function collectViolations(): Promise<Violation[]> {
	const files: string[] = [];
	for (const root of scanRoots) await walk(resolve(repositoryRoot, root), files);
	files.sort();
	const violations: Violation[] = [];
	for (const file of files) {
		const raw = await readFile(file, "utf8");
		const text = file.endsWith(".svelte") ? maskSvelte(raw) : raw;
		checkSource(file, text, violations);
	}
	return violations;
}

function loadBoundaries(raw: string): BoundariesFile {
	const parsed: unknown = JSON.parse(raw);
	if (!isRecord(parsed)) throw new Error("conventions boundaries must be a JSON object");
	if (parsed.schemaVersion !== 1) throw new Error("conventions boundaries schemaVersion must be 1");
	const rawRules: unknown = parsed.boundaryRules;
	const rawRoots: unknown = parsed.boundaryRoots;
	if (!Array.isArray(rawRules) || !rawRules.every((rule) => typeof rule === "string")) {
		throw new Error("conventions boundaries must list string boundaryRules");
	}
	if (!Array.isArray(rawRoots) || !rawRoots.every((root) => typeof root === "string")) {
		throw new Error("conventions boundaries must list string boundaryRoots");
	}
	const rawBaseline: unknown = parsed.baseline;
	if (!isRecord(rawBaseline)) {
		throw new Error("conventions boundaries must carry a baseline object");
	}
	const baseline: Record<string, Record<string, number>> = {};
	for (const [rule, files] of Object.entries(rawBaseline)) {
		if (!isRecord(files)) throw new Error(`baseline.${rule} must be an object`);
		const perFile: Record<string, number> = {};
		for (const [file, count] of Object.entries(files)) {
			if (typeof count !== "number" || !Number.isInteger(count) || count < 0) {
				throw new Error(`baseline.${rule}.${file} must be a non-negative integer`);
			}
			perFile[file] = count;
		}
		baseline[rule] = perFile;
	}
	return {
		schemaVersion: 1,
		boundaryRules: rawRules.map((rule) => String(rule)),
		boundaryRoots: rawRoots.map((root) => String(root)),
		baseline,
	};
}

export async function runConventionsCheck(
	options: { printBaseline?: boolean } = {},
): Promise<number> {
	const violations = await collectViolations();

	if (options.printBaseline === true) {
		const baseline: Record<string, Record<string, number>> = {};
		for (const rule of ruleIds) {
			const perFile = new Map<string, number>();
			for (const violation of violations) {
				if (violation.rule !== rule) continue;
				if (boundaryRules.includes(rule)) {
					if (
						boundaryRoots.some(
							(root) => violation.file === root || violation.file.startsWith(`${root}/`),
						)
					) {
						continue;
					}
				}
				perFile.set(violation.file, (perFile.get(violation.file) ?? 0) + 1);
			}
			if (perFile.size === 0) continue;
			baseline[rule] = Object.fromEntries(
				[...perFile.entries()]
					.filter(([, count]) => count > 0)
					.sort(([a], [b]) => a.localeCompare(b)),
			);
		}
		const document = {
			$comment:
				"Reviewed baseline for check-conventions.ts. Each entry is the maximum allowed count of a rule in one file. It is lint debt to burn down, not evidence that the pattern is correct; a new violation in any file (or a new file) fails the gate. Boundary roots are architectural adapters where unparsed values are legitimate; the rest are adapter-local occurrences outside those roots. Regenerate with `bun scripts/check-conventions.ts --print-baseline` and review the diff.",
			schemaVersion: 1,
			boundaryRules,
			boundaryRoots,
			baseline,
		};
		console.log(JSON.stringify(document, null, "\t"));
		return 0;
	}

	let boundaries: BoundariesFile;
	try {
		boundaries = loadBoundaries(await readFile(boundariesPath, "utf8"));
	} catch (error) {
		console.error(
			`check-conventions: cannot read ${displayPath(boundariesPath)}: ${errorMessage(error)}`,
		);
		console.error("Run `bun scripts/check-conventions.ts --print-baseline` and commit the result.");
		return 1;
	}

	const failures: Violation[] = [];
	const reported = new Map<string, number>();
	for (const violation of violations) {
		if (isBoundary(violation.file, boundaries, violation.rule)) continue;
		const key = `${violation.rule}\u0000${violation.file}`;
		const seen = reported.get(key) ?? 0;
		reported.set(key, seen + 1);
		const allowed = boundaries.baseline[violation.rule]?.[violation.file] ?? 0;
		if (seen >= allowed) failures.push(violation);
	}

	if (failures.length > 0) {
		console.error(`check-conventions: FAILED (${failures.length} new violation(s))`);
		for (const failure of failures) {
			const detail = failure.detail === "" ? "" : ` ${failure.detail}`;
			console.error(`  ${failure.file}:${failure.line}: ${failure.rule}${detail}`);
		}
		console.error("Fix the pattern or record a reviewed baseline update.");
		return 1;
	}
	console.log(
		`check-conventions: OK (${violations.length} known occurrence(s) within baseline, ${scanRoots.length} scanned roots)`,
	);
	return 0;
}

if (import.meta.main) {
	const printBaseline = process.argv.includes("--print-baseline");
	process.exit(await runConventionsCheck({ printBaseline }));
}
