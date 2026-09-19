function lines(value: string): string[] {
	if (!value) return [];
	const result = value.split("\n");
	if (result.at(-1) === "") result.pop();
	return result;
}

function comparable(value: string, ignoreWhitespace: boolean): string {
	return ignoreWhitespace ? value.replace(/\s+/g, " ").trim() : value;
}

/**
 * Produce a bounded, readable unified-style diff without carrying an editor runtime.
 * Common prefix/suffix lines stay as context; the changed middle is represented as
 * removed then added lines. Git already determines which files changed.
 */
export function simpleUnifiedDiff(
	path: string,
	original: string,
	modified: string,
	ignoreWhitespace = false,
	originalPath = path,
): string {
	const before = lines(original);
	const after = lines(modified);
	let prefix = 0;
	while (
		prefix < before.length &&
		prefix < after.length &&
		comparable(before[prefix] ?? "", ignoreWhitespace) ===
			comparable(after[prefix] ?? "", ignoreWhitespace)
	) {
		prefix += 1;
	}
	let suffix = 0;
	while (
		suffix < before.length - prefix &&
		suffix < after.length - prefix &&
		comparable(before[before.length - 1 - suffix] ?? "", ignoreWhitespace) ===
			comparable(after[after.length - 1 - suffix] ?? "", ignoreWhitespace)
	) {
		suffix += 1;
	}

	const context = 3;
	const prefixStart = Math.max(0, prefix - context);
	const suffixStartBefore = Math.max(prefix, before.length - suffix);
	const suffixStartAfter = Math.max(prefix, after.length - suffix);
	const suffixContext = Math.min(context, suffix);
	const output = [`--- a/${originalPath}`, `+++ b/${path}`, "@@"];

	for (const line of before.slice(prefixStart, prefix)) output.push(` ${line}`);
	for (const line of before.slice(prefix, suffixStartBefore)) output.push(`-${line}`);
	for (const line of after.slice(prefix, suffixStartAfter)) output.push(`+${line}`);
	for (const line of after.slice(suffixStartAfter, suffixStartAfter + suffixContext)) {
		output.push(` ${line}`);
	}
	if (output.length === 3) output.push(" (no textual changes)");
	return `${output.join("\n")}\n`;
}
