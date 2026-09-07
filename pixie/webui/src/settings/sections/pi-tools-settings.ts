import type { PiExtensionSummary, PiToolSummary } from "@pixie/contracts";

export function uniqueExtensions(extensions: PiExtensionSummary[]): PiExtensionSummary[] {
	const seen = new Set<string>();
	return extensions.filter(
		(extension) => !seen.has(extension.name) && Boolean(seen.add(extension.name)),
	);
}

export function filterTools(tools: readonly PiToolSummary[], query: string): PiToolSummary[] {
	const needle = query.trim().toLocaleLowerCase();
	if (!needle) return [...tools];
	return tools.filter((tool) =>
		`${tool.name} ${tool.description} ${tool.parameters.join(" ")}`
			.toLocaleLowerCase()
			.includes(needle),
	);
}

export function extensionWarningText(warningCount: number): string | null {
	if (warningCount === 0) return null;
	return `${warningCount} Pi configuration ${warningCount === 1 ? "warning" : "warnings"} reported.`;
}

export function isSessionInventoryCurrent(
	loadedTarget: string | null,
	activeTarget: string,
	loading: boolean,
): boolean {
	return !loading && loadedTarget === activeTarget;
}
