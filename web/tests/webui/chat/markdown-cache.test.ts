import { expect, test } from "bun:test";
import {
	hasOpenCodeFence,
	PrefixMarkdownCache,
	splitMarkdownBlocks,
} from "@/chat/runtime/markdown-cache";
import { renderMarkdown } from "@/lib/markdown";

test("AUX-17 completed blocks are not re-parsed as the tail streams", () => {
	const stream = [
		"# Plan\n\n",
		"first paragraph",
		" keeps growing",
		"...\n\n",
		"second block is final",
	];
	const cache = new PrefixMarkdownCache();
	const parseCounts: number[] = [];
	let rendered = "";
	let last = { html: "", reusedBlocks: 0, renderedRanges: 0 };
	for (const chunk of stream) {
		rendered += chunk;
		last = cache.render(rendered);
		parseCounts.push(last.renderedRanges);
		// Every call parses at most the frozen heading plus the mutable tail,
		// never the whole message.
		expect(last.renderedRanges).toBeLessThanOrEqual(2);
	}
	// The heading was rendered once, then served from the prefix cache while
	// the paragraphs streamed.
	expect(parseCounts.at(-1)).toBe(1);
	expect(last.reusedBlocks).toBeGreaterThanOrEqual(1);
	expect(last.html).toContain("first paragraph keeps growing");
	expect(last.html).toContain("second block is final");
});

test("AUX-17 an open code fence is not highlighted until it closes", () => {
	const opening = "```ts\nconst value = 1;\n";
	expect(hasOpenCodeFence(opening)).toBe(true);
	const cache = new PrefixMarkdownCache();
	const midStream = cache.render(opening);
	// An unterminated fence has no language-class code block to enhance yet.
	expect(midStream.html).not.toContain("language-ts");

	const closed = `${opening}\`\`\`\n`;
	expect(hasOpenCodeFence(closed)).toBe(false);
	const complete = cache.render(closed);
	expect(complete.html).toContain("language-ts");
});

test("AUX-17 block splitting never freezes inside an open fence", () => {
	const open = "intro paragraph\n\n```bash\necho one\n\nstill code\n";
	const splitOpen = splitMarkdownBlocks(open);
	// The open fence and the paragraph before it are both tail text: nothing
	// inside an unclosed fence may be frozen.
	expect(splitOpen.blocks).toEqual(["intro paragraph\n"]);
	expect(splitOpen.tail).toContain("```bash");
	expect(hasOpenCodeFence(splitOpen.tail)).toBe(true);

	// Once the fence closes and a blank line separates the next block, the
	// closed fence is frozen and only the trailing block remains mutable.
	const closed = "intro paragraph\n\n```bash\necho one\n\nstill code\n```\n\noutro";
	const splitClosed = splitMarkdownBlocks(closed);
	expect(splitClosed.blocks).toEqual([
		"intro paragraph\n",
		"```bash\necho one\n\nstill code\n```\n",
	]);
	expect(splitClosed.tail).toBe("outro");
});

test("AUX-17 the prefix cache matches full-document rendering", () => {
	const source =
		"# Title\n\nA paragraph with **bold** and a [link](/x).\n\n- one\n- two\n\n```js\nconst x = 1;\n```\n\nFinal line.";
	const cache = new PrefixMarkdownCache();
	expect(cache.render(source).html).toBe(renderMarkdown(source));
});
