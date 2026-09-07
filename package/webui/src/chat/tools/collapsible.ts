export const COLLAPSIBLE_LINE_THRESHOLD = 24;

export function countLines(text: string): number {
	if (!text) return 0;
	const count = text.split("\n").length;
	return text.endsWith("\n") ? count - 1 : count;
}
