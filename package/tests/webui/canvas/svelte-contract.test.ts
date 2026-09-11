import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { canvasToolCardViewModel } from "@/canvas";

const root = new URL("../../../webui/src/canvas/", import.meta.url);
const components = [
	"canvas-preview.svelte",
	"canvas-tool-card.svelte",
	"canvas-sidebar.svelte",
	"canvas-viewer.svelte",
] as const;

test("Canvas components compile without executable HTML replay", async () => {
	for (const relativePath of components) {
		const url = new URL(relativePath, root);
		const source = await Bun.file(url).text();
		const result = compile(source, { filename: url.pathname, generate: false });
		expect(result.warnings).toEqual([]);
		expect(source).not.toContain("{@html}");
		expect(source).not.toContain("<iframe");
		expect(source).not.toContain("srcdoc");
		expect(source).not.toContain("innerHTML");
	}
});

test("Canvas tool cards expose compact metadata without executable replay", async () => {
	const view = canvasToolCardViewModel({
		toolName: "canvas_read",
		result: { outcome: "completed", version: 1, warnings: ['<script>alert("x")</script>'] },
	});
	expect(view.operation).toBe("read");
	expect(view.version).toBe(1);
	expect(view.warnings).toEqual(['<script>alert("x")</script>']);
});

test("Canvas sidebar exposes session-scoped version, rendering, update, and action controls", async () => {
	const source = await Bun.file(new URL("canvas-sidebar.svelte", root)).text();
	for (const testid of [
		'data-testid="canvas-sidebar"',
		'data-testid="canvas-version-label"',
		'data-testid="canvas-rendering-label"',
		'data-testid="canvas-updated-label"',
		'data-testid="canvas-screenshot-button"',
		'data-testid="canvas-refresh-button"',
		'data-testid="canvas-remove-button"',
		'data-testid="canvas-removal-confirmation"',
	]) {
		expect(source).toContain(testid);
	}
	expect(source).toContain('data-scope="session"');
	expect(source).toContain("scopeLabel");
	expect(source).toContain("canvasSidebarViewModel");
	// Two owned Mewa templates are demonstrated with Mewa card/button primitives.
	expect(source).toContain("CANVAS_TEMPLATES");
	expect(source).toContain('data-testid={`canvas-template-');
	expect(source).toContain("Use {template.id}");
	expect(source).toContain('class="card"');
	expect(source).toContain('class="btn"');
	// Raster-first: sidebar delegates imagery to the shared raster preview.
	expect(source).not.toContain("<video");
	expect(source).not.toContain("<object");
});

test("Canvas viewer shows stale version identity through the raster preview", async () => {
	const source = await Bun.file(new URL("canvas-viewer.svelte", root)).text();
	for (const testid of [
		'data-testid="canvas-viewer"',
		'data-testid="canvas-viewer-version"',
		'data-testid="canvas-viewer-rendering"',
		'data-testid="canvas-viewer-updated"',
		'data-testid="canvas-viewer-screenshot"',
		'data-testid="canvas-viewer-refresh"',
	]) {
		expect(source).toContain(testid);
	}
	expect(source).toContain('data-scope="session"');
	expect(source).toContain("CanvasPreview");
	expect(source).toContain("canvasViewerViewModel");
	expect(source).toContain("canvas-viewer-stale");
});
