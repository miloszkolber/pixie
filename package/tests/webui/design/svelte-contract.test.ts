import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { designToolCardViewModel } from "@/design";

const root = new URL("../../../webui/src/design/", import.meta.url);
const components = [
	"design-preview.svelte",
	"design-tool-card.svelte",
	"design-sidebar.svelte",
	"design-viewer.svelte",
] as const;

test("Design components compile without executable HTML replay", async () => {
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

test("Design cards expose instance-wide scope and safe frame fallback metadata", async () => {
	const view = designToolCardViewModel({
		toolName: "design_preview",
		result: {
			outcome: "completed",
			documentId: "abcdefghijklmnopqrstuvwxyz234567",
			generation: 1,
			selectionRevision: 1,
			previewKind: "frame",
		},
	});
	expect(view.instanceWide).toBe(true);
	expect(view.message).toContain("Frame preview unavailable");
	expect(view.preview?.status).toBe("unavailable");
});

test("Design sidebar keeps cover versus frame, shared focus, upload, and draft controls explicit", async () => {
	const source = await Bun.file(new URL("design-sidebar.svelte", root)).text();
	for (const testid of [
		'data-testid="design-sidebar"',
		'data-testid="design-scope-notice"',
		'data-testid="design-upload-status"',
		'data-testid="design-upload-progress"',
		'data-testid="design-upload-cancel"',
		'data-testid="design-document-metadata"',
		'data-testid="design-removal-confirmation"',
		'data-testid="design-shared-focus-publish"',
		'data-testid="design-draft-reference-insert"',
		'data-testid="design-inspector-counts"',
	]) {
		expect(source).toContain(testid);
	}
	expect(source).toContain('data-scope="instance"');
	expect(source).toContain("Instance-wide");
	expect(source).toContain("every authorized Design reader");
	// Upload uses streamed binary HTTP wording, format/limit handling, and explicit removal.
	expect(source).toContain("designRemovalConfirmation");
	expect(source).toContain("removal.copiesNotice");
	expect(source).toContain("removal.title");
	expect(source).toContain('class="progress"');
	expect(source).toContain("Cancel upload");
	// Inspector presence with fail-closed unavailability and diagnostics.
	expect(source).toContain("designInspectorModel");
	expect(source).toContain("design-inspector-unavailable");
	expect(source).toContain("diagnostic");
	expect(source).toContain("design-page-row-");
	expect(source).toContain("design-layer-row-");
	expect(source).toContain("design-frame-candidate-row-");
	// Explicit draft reference only on user action.
	expect(source).toContain("designDraftReferenceAction");
	expect(source).toContain("draftAction.label");
	expect(source).toContain("draftAction.hint");
	// Mewa primitives only; no second visual system.
	expect(source).toContain('class="btn"');
});

test("Design viewer preserves Document thumbnail versus Frame preview unavailable", async () => {
	const source = await Bun.file(new URL("design-viewer.svelte", root)).text();
	for (const testid of [
		'data-testid="design-viewer"',
		'data-testid="design-viewer-metadata"',
		'data-testid="design-viewer-frame-note',
	]) {
		expect(source).toContain(testid);
	}
	expect(source).toContain('data-scope="instance"');
	expect(source).toContain("DesignPreview");
	expect(source).toContain("Document thumbnail");
	expect(source).toContain("Frame preview unavailable");
	expect(source).toContain("never repeated as a frame render");
	expect(source).toContain("designInspectorModel");
});
