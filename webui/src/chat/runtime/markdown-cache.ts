import { renderMarkdown } from "../../lib/markdown";

/**
 * AUX-17 streaming render discipline.
 *
 * A streaming assistant message arrives one token at a time. Re-parsing the
 * whole accumulating source on every token is O(n) per token and O(n²) per
 * message, and mid-stream code fences flicker into highlighted blocks before
 * they close. This cache freezes the rendered HTML for completed top-level
 * blocks and only re-renders the mutable tail, so a completed block is parsed
 * exactly once for the lifetime of one message.
 */

export interface RenderedMarkdown {
	html: string;
	/** Number of completed blocks served from the prefix cache. */
	reusedBlocks: number;
	/** Number of source ranges parsed during this call. */
	renderedRanges: number;
}

const CACHE_LIMIT = 512;

/**
 * Splits source into complete top-level blocks plus a trailing partial block.
 * Blocks are separated by blank lines; a blank line inside an open code fence
 * never counts as a boundary, so nothing inside an unclosed fence is frozen.
 * The returned tail is the final unterminated block, which may still grow.
 */
export function splitMarkdownBlocks(source: string): { blocks: string[]; tail: string } {
	const blocks: string[] = [];
	const lines = source.split("\n");
	let fence: "```" | "~~~" | null = null;
	let start = 0;
	// A trailing empty element only exists because the source ended in "\n";
	// that newline does not begin a new block.
	const last = lines[lines.length - 1] === "" ? lines.length - 1 : lines.length;
	for (let index = 0; index < last; index += 1) {
		const line = lines[index] ?? "";
		const marker = fenceMarker(line);
		if (marker && fence === null) {
			fence = marker;
		} else if (marker && marker === fence) {
			fence = null;
		}
		if (line.trim() !== "" || fence !== null) continue;
		const block = lines.slice(start, index).join("\n");
		start = index + 1;
		if (block.trim().length > 0) blocks.push(`${block}\n`);
	}
	const tail = lines.slice(start).join("\n");
	const frozen = blocks.slice();
	if (tail.trim().length === 0) return { blocks: frozen, tail: "" };
	return { blocks: frozen, tail };
}

/** Returns the opening/closing fence marker for a line, if any. */
function fenceMarker(line: string): "```" | "~~~" | null {
	const trimmed = line.trimStart();
	if (trimmed.startsWith("```")) return "```";
	if (trimmed.startsWith("~~~")) return "~~~";
	return null;
}

/**
 * PrefixMarkdownCache renders an accumulating markdown source while reusing the
 * rendered HTML of frozen blocks. It is owned by one streaming message; call
 * `render` with the latest full source and it returns the assembled HTML.
 */
export class PrefixMarkdownCache {
	private readonly rendered = new Map<string, string>();
	private ordered: string[] = [];

	render(source: string): RenderedMarkdown {
		const { blocks, tail } = splitMarkdownBlocks(source);
		const parts: string[] = [];
		let reusedBlocks = 0;
		let renderedRanges = 0;

		for (const block of blocks) {
			const cached = this.rendered.get(block);
			if (cached !== undefined) {
				parts.push(cached);
				reusedBlocks += 1;
				continue;
			}
			const html = renderMarkdown(block);
			this.remember(block, html);
			parts.push(html);
			renderedRanges += 1;
		}
		if (tail.length > 0) {
			// The tail is the only range re-parsed as tokens arrive. While a
			// code fence is still open the tail must not advertise a language
			// class, so the highlighter cannot promote a half-written block;
			// the completed block is only frozen once the fence closes.
			const tailHtml = renderMarkdown(tail);
			parts.push(hasOpenCodeFence(tail) ? stripCodeLanguage(tailHtml) : tailHtml);
			renderedRanges += 1;
		}
		return { html: joinBlocks(parts), reusedBlocks, renderedRanges };
	}

	private remember(block: string, html: string): void {
		if (!this.rendered.has(block)) {
			this.ordered.push(block);
			if (this.ordered.length > CACHE_LIMIT) {
				const oldest = this.ordered.shift();
				if (oldest !== undefined) this.rendered.delete(oldest);
			}
		}
		this.rendered.set(block, html);
	}
}

/**
 * Joins rendered block fragments. Each fragment already carries the block
 * separator micromark emits, so no extra whitespace is inserted.
 */
function joinBlocks(parts: string[]): string {
	return parts.filter((part) => part.length > 0).join("");
}

/**
 * Reports whether the tail of a streaming source contains an unterminated code
 * fence. Used by the UI to keep the tail in a neutral, unhighlighted form.
 */
export function hasOpenCodeFence(source: string): boolean {
	let fence: "```" | "~~~" | null = null;
	for (const line of source.split("\n")) {
		const marker = fenceMarker(line);
		if (marker && fence === null) fence = marker;
		else if (marker && marker === fence) fence = null;
	}
	return fence !== null;
}

/** Removes the language hint so an unfinished block has nothing to highlight. */
function stripCodeLanguage(html: string): string {
	return html.replace(/(<code)\s+class="language-[^"]*"/gi, "$1");
}
