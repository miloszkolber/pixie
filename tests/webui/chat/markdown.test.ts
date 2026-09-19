import { expect, test } from "bun:test";
import { renderMarkdown, sanitizeMarkdownHtml } from "@/lib/markdown";
import { renderSvelte } from "./svelte-render";

test("chat Markdown retains GFM output while escaping embedded HTML", () => {
	const html = renderMarkdown(
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
	expect(source).toContain('import("@/lib/markdown")');
	expect(source).toContain('import("../../lib/highlighter")');

	const markup = await renderSvelte("src/chat/render/markdown.svelte", {
		text: "<script>alert('unsafe')</script>",
	});
	expect(markup).toContain('aria-busy="true"');
	expect(markup).toContain("&lt;script>alert('unsafe')&lt;/script>");
	expect(markup).not.toContain("<script>");
});

test("AUX-01 remote Markdown images are never emitted as a live src", () => {
	const remote = renderMarkdown("![beacon](https://evil.example/pixel.png)");
	expect(remote).not.toContain("src=");
	expect(remote).not.toContain("evil.example");
	expect(remote).toContain('data-pixie-remote-image="blocked"');

	// Protocol-relative sources are blocked too. Raw HTML is escaped by
	// micromark and therefore never becomes live markup in the first place.
	expect(renderMarkdown("![beacon](//evil.example/pixel.png)")).not.toContain("evil.example");
	expect(renderMarkdown('<img src="https://evil.example/pixel.png">')).not.toContain("<img");

	// Same-origin paths stay usable. micromark already scrubs `data:` URIs to
	// an empty src, and the sanitizer keeps blob: sources.
	const local = renderMarkdown("![ok](/files/project/photo.png)");
	expect(local).toContain('src="/files/project/photo.png"');
	expect(sanitizeMarkdownHtml('<img src="blob:http://localhost/abc">')).toContain(
		'src="blob:http://localhost/abc"',
	);
	expect(renderMarkdown("![ok](data:image/png;base64,iVBORw0KGgo=")).toContain("data:image/png");
});
