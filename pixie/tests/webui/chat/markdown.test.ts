import { expect, test } from "bun:test";
import { renderChatMarkdown } from "@/chat/render/markdown-document";
import { renderSvelte } from "./svelte-render";

test("chat Markdown retains GFM output while escaping embedded HTML", () => {
	const html = renderChatMarkdown(
		"~~removed~~\n\n| A | B |\n| - | - |\n| 1 | 2 |\n\n<script>alert('unsafe')</script>",
	);
	expect(html).toContain("<del>removed</del>");
	expect(html).toContain("<table>");
	expect(html).toContain("&lt;script&gt;alert('unsafe')&lt;/script&gt;");
	expect(html).not.toContain("<script>");
});

test("chat Markdown defers parsing and highlighting behind a safe initial fallback", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/chat/render/markdown.svelte", import.meta.url),
	).text();
	expect(source).not.toMatch(/import\s+[^;]+from\s+["']\.\/markdown-document["']/);
	expect(source).not.toMatch(/import\s+[^;]+from\s+["']\.\.\/\.\.\/lib\/highlighter["']/);
	expect(source).toContain('import("./markdown-document")');
	expect(source).toContain('import("../../lib/highlighter")');

	const markup = await renderSvelte("src/chat/render/markdown.svelte", {
		text: "<script>alert('unsafe')</script>",
	});
	expect(markup).toContain('aria-busy="true"');
	expect(markup).toContain("&lt;script>alert('unsafe')&lt;/script>");
	expect(markup).not.toContain("<script>");
});
