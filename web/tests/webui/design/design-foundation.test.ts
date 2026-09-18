import { describe, expect, test } from "bun:test";
import {
	DESIGN_CONTRIBUTION,
	designArtifactUrl,
	designStateFromStatus,
	emptyDesignState,
} from "@/design";

const documentID = "abcdefghijklmnopqrstuvwxyz234567";

describe("Design UI foundation", () => {
	test("describes an instance-wide, read-only contribution", () => {
		expect(DESIGN_CONTRIBUTION.scope).toBe("instance");
		expect(DESIGN_CONTRIBUTION.agentReadsAreReadOnly).toBe(true);
		expect(DESIGN_CONTRIBUTION.framePreviewAvailable).toBe(false);
	});

	test("builds authenticated artifact paths without tokens", () => {
		const reference = `pixie://design/artifacts/${documentID}/cover.png`;
		expect(designArtifactUrl(reference)).toBe(`/api/design/artifacts/${documentID}/cover.png`);
		expect(designArtifactUrl(reference, "https://pixie.test/")).toBe(
			`https://pixie.test/api/design/artifacts/${documentID}/cover.png`,
		);
		expect(designArtifactUrl(`${reference}?token=secret`)).toBeNull();
		expect(designArtifactUrl(documentID, "../secret")).toBeNull();
	});

	test("projects only the status-provided cover preview", () => {
		const state = designStateFromStatus(emptyDesignState(), {
			enabled: true,
			availability: "ready",
			documentId: documentID,
			generation: 4,
			name: "Library",
			pageCount: 2,
			nodeCount: 9,
			coverAvailable: true,
			coverArtifact: `pixie://design/artifacts/${documentID}/cover.png`,
		});
		expect(state.document?.name).toBe("Library");
		expect(state.preview.status).toBe("ready");
		expect(state.preview.url).toBe(`/api/design/artifacts/${documentID}/cover.png`);
	});
});
