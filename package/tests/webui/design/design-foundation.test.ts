import { describe, expect, test } from "bun:test";
import {
	advanceDesignUpload,
	applyDesignDraftReference,
	applySharedDesignFocus,
	buildDesignDraftReference,
	cancelDesignUpload,
	DESIGN_CONTRIBUTION,
	DESIGN_UPLOAD_LIMITS,
	type DesignNode,
	type DesignPage,
	designArtifactUrl,
	designDocumentMetadata,
	designDraftReferenceAction,
	designDraftTargetFromState,
	designFrameCandidates,
	designInspectorModel,
	designPreviewFromResult,
	designRemovalConfirmation,
	designRemovalRequest,
	designRevisionRecovery,
	designSharedFocusPublish,
	designStateFromStatus,
	designToolCardViewModel,
	designUploadPercent,
	designUploadStatusLabel,
	emptyDesignState,
	failDesignUpload,
	formatDesignFileSize,
	framePreviewUnavailable,
	recoverDesignRevision,
	setPrivateDesignFocus,
	setSharedDesignFocusOptimistic,
	startDesignUpload,
	validateDesignUploadFile,
} from "@/design";

const documentId = "abcdefghijklmnopqrstuvwxyz234567";

function node(id: string, frameCandidate?: boolean): DesignNode {
	return {
		id,
		pageId: "page",
		parentId: null,
		position: 0,
		depth: 1,
		type: "FRAME",
		name: id,
		visible: true,
		x: 0,
		y: 0,
		width: 100,
		height: 100,
		rotation: 0,
		text: null,
		children: [],
		style: {},
		overrides: {},
		...(frameCandidate === undefined ? {} : { frameCandidate }),
	};
}

describe("Design UI foundation", () => {
	test("describes an instance-wide, read-only contribution", () => {
		expect(DESIGN_CONTRIBUTION.scope).toBe("instance");
		expect(DESIGN_CONTRIBUTION.agentReadsAreReadOnly).toBe(true);
		expect(DESIGN_CONTRIBUTION.framePreviewAvailable).toBe(false);
	});

	test("builds authenticated artifact paths without tokens", () => {
		const reference = `pixie://design/artifacts/${documentId}/cover.png`;
		expect(designArtifactUrl(reference)).toBe(`/api/design/artifacts/${documentId}/cover.png`);
		expect(designArtifactUrl(reference, "https://pixie.test/")).toBe(
			`https://pixie.test/api/design/artifacts/${documentId}/cover.png`,
		);
		expect(designArtifactUrl(documentId, "cover", "/pixie")).toBe(
			`/pixie/api/design/artifacts/${documentId}/cover.png`,
		);
		expect(designArtifactUrl(`${reference}?token=secret`)).toBeNull();
		expect(designArtifactUrl(documentId, "../secret")).toBeNull();
		expect(designArtifactUrl(documentId, "https://evil.test/cover")).toBeNull();
		expect(designArtifactUrl(reference, "//evil.example")).toBeNull();
	});

	test("labels the slot instance-wide and separates private from shared focus", () => {
		let state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId,
			generation: 4,
			selectionRevision: 2,
			name: "Library",
			coverAvailable: true,
			coverArtifact: `pixie://design/artifacts/${documentId}/cover.png`,
		});
		state = setPrivateDesignFocus(state, {
			documentId,
			generation: 4,
			pageId: "page",
			nodeId: "private",
		});
		expect(state.scope.scope).toBe("instance");
		expect(state.focus.privateFocus?.nodeId).toBe("private");
		expect(state.focus.sharedFocus).toBeNull();
		expect(state.preview.status).toBe("ready");
		expect(state.preview.artifactUrl).toBe(`/api/design/artifacts/${documentId}/cover.png`);
		const optimistic = setSharedDesignFocusOptimistic(state, { pageId: "page", nodeId: "shared" });
		expect(optimistic?.request.expectedRevision).toBe(2);
		expect(optimistic?.state.focus.sharedFocus?.nodeId).toBe("shared");
		expect(optimistic?.state.focus.privateFocus?.nodeId).toBe("private");
		if (!optimistic) throw new Error("expected an optimistic focus update");
		const applied = applySharedDesignFocus(optimistic.state, optimistic.state.focus.sharedFocus);
		expect(applied.focus.sharedRevision).toBe(3);
	});

	test("keeps cover and unsupported frame previews distinct", () => {
		const frame = framePreviewUnavailable(documentId, 4, "node");
		expect(frame.kind).toBe("frame");
		expect(frame.status).toBe("unavailable");
		expect(frame.url).toBeNull();
		const result = designPreviewFromResult(
			{ documentId, generation: 4, selectionRevision: 2, previewKind: "frame", mime: "image/png" },
			null,
		);
		expect(result.label).toBe("Frame preview unavailable");
		expect(result.kind).not.toBe("cover");
	});

	test("uses explicit frame candidates instead of treating every FRAME as a frame", () => {
		expect(designFrameCandidates([node("plain"), node("candidate", true)])).toHaveLength(1);
		expect(designFrameCandidates([node("plain"), node("candidate", true)])[0]?.id).toBe(
			"candidate",
		);
	});

	test("recovers stale document and selection revisions fail-closed", () => {
		const state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId,
			generation: 4,
			selectionRevision: 2,
		});
		expect(designRevisionRecovery({ code: "stale_selection" }).action).toBe("refresh-selection");
		expect(recoverDesignRevision(state, { code: "stale_document" }).availability).toBe("stale");
		expect(recoverDesignRevision(state, { code: "stale_document" }).pages).toEqual([]);
	});

	test("keeps removal identity available while Design is disabled", () => {
		const state = designStateFromStatus(emptyDesignState(), {
			enabled: false,
			availability: "disabled",
			documentId,
			generation: 4,
			selectionRevision: 7,
		});
		expect(designRemovalRequest(state)).toEqual({
			documentId,
			expectedGeneration: 4,
			selectionRevision: 7,
		});
	});

	test("tool cards state instance scope and frame unavailability compactly", () => {
		const view = designToolCardViewModel({
			toolName: "design_preview",
			result: {
				outcome: "completed",
				documentId,
				generation: 4,
				selectionRevision: 2,
				previewKind: "frame",
			},
		});
		expect(view.instanceWide).toBe(true);
		expect(view.preview?.label).toBe("Frame preview unavailable");
		expect(view.message).toContain("Frame preview unavailable");
	});

	test("inserts an explicit compact draft reference without auto-submit or file attach", () => {
		const state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId,
			generation: 4,
			selectionRevision: 2,
			name: "Library",
		});
		const target = {
			documentId,
			documentName: "Library",
			pageId: "page-a",
			pageName: "Cover",
			nodeId: "node-a",
			nodeName: "Hero",
		};
		const reference = buildDesignDraftReference(target);
		expect(reference).toContain("Library");
		expect(reference).toContain("page");
		expect(reference).toContain("node");
		expect(buildDesignDraftReference(null)).toBeNull();
		expect(buildDesignDraftReference({ documentId: "  " })).toBeNull();
		expect(buildDesignDraftReference({ documentId })).toContain(documentId.slice(0, 8));

		const action = designDraftReferenceAction(state, target);
		expect(action.enabled).toBe(true);
		expect(action.autoSubmit).toBe(false);
		expect(action.reference).toBe(reference);
		expect(action.hint).toContain("Never submits");
		expect(action.hint).toContain("attaches the whole file");

		// Insertion only splices draft text; submission and prompt stay with the caller.
		const inserted = applyDesignDraftReference("Review this", reference);
		expect(inserted?.value).toContain("Review this");
		expect(inserted?.value).toContain(reference ?? "");
		expect(inserted?.caret).toBe(inserted?.value.length);
		const atCaret = applyDesignDraftReference("ab", reference, 1);
		expect(atCaret?.value).toContain(reference ?? "");
		expect(applyDesignDraftReference("draft", null)).toBeNull();
		expect(applyDesignDraftReference("draft", "  ")).toBeNull();

		// Wrong document or unavailable Design disables the explicit action.
		expect(designDraftReferenceAction(state, { documentId: "other-document" }).enabled).toBe(false);
		const disabled = designStateFromStatus(emptyDesignState(), {
			enabled: false,
			availability: "disabled",
			documentId,
			generation: 4,
			selectionRevision: 2,
		});
		expect(designDraftReferenceAction(disabled, { documentId }).enabled).toBe(false);
		expect(designDraftTargetFromState(emptyDesignState())).toBeNull();
	});

	test("publishes shared focus explicitly while private focus stays local", () => {
		const pages: DesignPage[] = [
			{ id: "page-a", name: "Cover", position: 1, nodeCount: 2 },
			{ id: "page-b", name: "Flow", position: 0, nodeCount: 1 },
		];
		const nodes: DesignNode[] = [node("plain"), { ...node("candidate", true), pageId: "page-a" }];
		let state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId,
			generation: 4,
			selectionRevision: 2,
			name: "Library",
		});
		state = { ...state, pages, nodes };
		const inspector = designInspectorModel(state, "page-a");
		expect(inspector.availability.available).toBe(true);
		expect(inspector.pages.map((page) => page.id)).toEqual(["page-b", "page-a"]);
		expect(inspector.selectedPageId).toBe("page-a");
		expect(inspector.layers.length).toBeGreaterThan(0);
		expect(inspector.frameCandidates).toHaveLength(1);
		expect(inspector.frameCandidates[0]?.id).toBe("candidate");
		expect(inspector.sharedRevision).toBe(2);

		const publish = designSharedFocusPublish(state, { pageId: "page-a", nodeId: "candidate" });
		expect(publish.enabled).toBe(true);
		expect(publish.request?.documentId).toBe(documentId);
		expect(publish.request?.expectedRevision).toBe(2);
		expect(publish.request?.pageId).toBe("page-a");
		expect(publish.optimisticRevision).toBe(3);
		expect(publish.hint).toContain("Private browsing never changes shared focus");

		// Fail-closed when the committed source is not inspectable.
		const disabled = designStateFromStatus(emptyDesignState(), {
			enabled: false,
			availability: "disabled",
			documentId,
			generation: 4,
			selectionRevision: 2,
		});
		const closed = designInspectorModel(disabled, null);
		expect(closed.availability.available).toBe(false);
		expect(closed.availability.reason).toContain("disabled");
		expect(closed.availability.diagnostics.length).toBeGreaterThan(0);
		expect(closed.pages).toEqual([]);
		expect(closed.layers).toEqual([]);
		expect(designSharedFocusPublish(disabled, {}).enabled).toBe(false);
		expect(designInspectorModel(emptyDesignState(), null).availability.available).toBe(false);
	});

	test("covers upload progress, cancel, format, limits, metadata, and removal confirmation", () => {
		expect(DESIGN_UPLOAD_LIMITS.acceptedExtension).toBe(".fig");
		expect(DESIGN_UPLOAD_LIMITS.maxBytes).toBe(50 * 1024 * 1024);
		expect(validateDesignUploadFile("library.fig", 1024).ok).toBe(true);
		expect(validateDesignUploadFile("LIBRARY.FIG", 1024).ok).toBe(true);
		expect(validateDesignUploadFile("library.png", 1024).code).toBe("invalid-format");
		expect(validateDesignUploadFile("", 1024).code).toBe("invalid-format");
		expect(validateDesignUploadFile("library.fig", DESIGN_UPLOAD_LIMITS.maxBytes + 1).code).toBe(
			"too-large",
		);
		expect(validateDesignUploadFile("library.fig", 0).code).toBe("empty");

		const started = startDesignUpload("library.fig", 2048);
		expect("failure" in started).toBe(false);
		if ("failure" in started) throw new Error("expected upload to start");
		expect(started.phase).toBe("uploading");
		expect(started.cancellable).toBe(true);
		expect(designUploadPercent(1024, 2048)).toBe(50);
		expect(designUploadPercent(9999, 2048)).toBe(100);
		expect(designUploadPercent(0, null)).toBe(0);
		const advanced = advanceDesignUpload(started, 1024);
		expect(advanced.percent).toBe(50);
		expect(designUploadStatusLabel(advanced)).toContain("50%");
		const cancelled = cancelDesignUpload(advanced);
		expect(cancelled.phase).toBe("cancelling");
		expect(cancelled.cancellable).toBe(true);
		const failed = failDesignUpload(started, "too-large", "File exceeds the limit.");
		expect(failed.phase).toBe("failed");
		expect(failed.error).toContain("limit");
		expect(startDesignUpload("library.png", 10)).toHaveProperty("failure");

		expect(formatDesignFileSize(512)).toBe("512 B");
		expect(formatDesignFileSize(2048)).toContain("KiB");
		expect(formatDesignFileSize(50 * 1024 * 1024)).toContain("MiB");
		expect(formatDesignFileSize(null)).toBe("—");

		const state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId,
			generation: 4,
			selectionRevision: 7,
			name: "Library",
			sourceName: "library.fig",
			sourceBytes: 2048,
			uploadedAt: "2026-09-11T10:00:00.000Z",
			pageCount: 2,
			nodeCount: 9,
		});
		const metadata = designDocumentMetadata(state.document);
		expect(metadata?.sourceName).toBe("library.fig");
		expect(metadata?.sizeLabel).toContain("KiB");
		expect(metadata?.dateLabel).toBe("2026-09-11T10:00:00.000Z");
		expect(metadata?.countsLabel).toContain("2 pages");
		expect(metadata?.scopeNotice).toContain("instance-wide");
		expect(designDocumentMetadata(null)).toBeNull();

		const removal = designRemovalConfirmation(state);
		expect(removal.requiresConfirmation).toBe(true);
		expect(removal.request?.selectionRevision).toBe(7);
		expect(removal.copiesNotice).toContain("cannot erase copies");
		expect(removal.message).toContain("tombstones");
	});
});
