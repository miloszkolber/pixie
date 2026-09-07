import { expect, test } from "bun:test";
import { classifyHref, projectFileUrl, resolveRelativePath } from "@/files/markdown/markdown-links";
import { parseAlertMarker, renderMarkdown, slugify } from "@/lib/markdown";

test("resolves Markdown files inside the originating project root", () => {
	expect(resolveRelativePath("docs/guide.md", "../images/diagram one.png")).toBe(
		"images/diagram one.png",
	);
	expect(
		projectFileUrl(
			"http://controller.test:7312",
			"project one",
			"docs/guide.md",
			"../images/diagram one.png",
		),
	).toBe("http://controller.test:7312/files/project%20one/images/diagram%20one.png");
});

test("does not construct a file URL without a target path", () => {
	expect(projectFileUrl("", "project", "README.md", "")).toBeUndefined();
});

test("normalizes already encoded paths and preserves image query or fragment suffixes", () => {
	expect(resolveRelativePath("docs/guide.md", "../images/a%20b.png")).toBe("images/a b.png");
	expect(
		projectFileUrl(
			"http://controller.test",
			"project one",
			"docs/guide.md",
			"../a%20b.png?v=1#top",
		),
	).toBe("http://controller.test/files/project%20one/a%20b.png?v=1#top");
});

test("renders GFM safely and exposes deterministic document transformations", () => {
	const rendered = renderMarkdown(
		"<script>alert(1)</script>\n\n- [x] done\n\n| A | B |\n| - | - |\n| 1 | 2 |",
	);
	expect(rendered).toContain("&lt;script&gt;");
	expect(rendered).toContain('type="checkbox"');
	expect(rendered).toContain("<table>");
	expect(classifyHref("#heading")).toBe("anchor");
	expect(classifyHref("https://example.com")).toBe("external");
	expect(classifyHref("guide.md")).toBe("relative");
	expect(slugify("A repeated heading!")).toBe("a-repeated-heading");
	expect(parseAlertMarker("[!WARNING]\nKeep the markup")).toEqual({
		variant: "warning",
		rest: "Keep the markup",
	});
});
