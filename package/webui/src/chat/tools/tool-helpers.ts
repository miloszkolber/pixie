export function toText(value: unknown): string {
	if (value == null) return "";
	if (typeof value === "string") return value;
	try {
		return JSON.stringify(value, null, 2);
	} catch {
		return String(value);
	}
}

/** MCP result envelopes and Pi's wrapped content carry the same ordered blocks. */
export function toolContent(result: unknown, includeStructured = false): unknown[] {
	if (result == null) return [];
	const content = typeof result === "object" ? Reflect.get(result, "content") : undefined;
	let blocks: unknown[] = Array.isArray(result) ? result : [result];
	if (Array.isArray(content)) {
		const structured = Reflect.get(result as object, "structuredContent");
		blocks = content.length > 0 || structured === undefined ? content : [structured];
		// A final failure may supply only rawOutput while retaining earlier Pi content.
		if (includeStructured && content.length > 0 && structured !== undefined) {
			blocks = [...content, structured];
		}
	}
	return blocks.map((block: unknown) =>
		block && typeof block === "object" && Reflect.get(block, "type") === "content"
			? Reflect.get(block, "content")
			: block,
	);
}

export function resultText(result: unknown, error = false): string {
	return toolContent(result, error)
		.map((block) => {
			if (block && typeof block === "object") {
				if (Reflect.get(block, "type") === "text" && typeof Reflect.get(block, "text") === "string")
					return Reflect.get(block, "text") as string;
				if (Reflect.get(block, "type") === "image") return "";
			}
			return toText(block);
		})
		.filter(Boolean)
		.join("\n");
}

export function strArg(args: Record<string, unknown>, key: string): string {
	const v = args[key];
	return typeof v === "string" ? v : "";
}

export function numArg(args: Record<string, unknown>, key: string): number | null {
	const v = args[key];
	return typeof v === "number" ? v : null;
}

export { languageForPath as languageFromPath } from "../../lib/language";
