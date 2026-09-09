import { expect, test } from "bun:test";
import {
	createDraft,
	preserveDraftOnExternalReplacement,
	updateDraft,
} from "../../../assistant/src/drafts/continuity.ts";

test("draft revisions advance on edits and stale edits are rejected", () => {
	const draft = createDraft({ sessionKey: "session-key", clientId: "client-a", content: "first" });
	const updated = updateDraft(draft, 0, "second");
	expect(updated).toMatchObject({ ok: true, draft: { revision: 1, version: 1, content: "second" } });
	if (!updated.ok) return;
	expect(updateDraft(updated.draft, 0, "stale")).toMatchObject({ ok: false, reason: "revision-conflict" });
});

test("external native replacement preserves the draft content and edit revision", () => {
	const draft = createDraft({
		sessionKey: "old-key",
		content: "keep this text",
		revision: 4,
		identity: { sessionKey: "old-key", nativeSessionId: "native-old", childGeneration: 1 },
	});
	const replaced = preserveDraftOnExternalReplacement(draft, {
		sessionKey: "new-key",
		identity: { sessionKey: "new-key", nativeSessionId: "native-new", childGeneration: 0 },
	});
	expect(replaced).toMatchObject({ preserved: true, previousRevision: 4, draft: { content: "keep this text", revision: 4, sessionKey: "new-key" } });
	expect(replaced.draft.continuityRevision).toBe(1);
});
