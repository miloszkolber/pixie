import { expect, test } from "bun:test";
import {
	applyDraftMutation,
	createDraft,
	createDraftState,
	replaceDraftState,
} from "../../../assistant/src/drafts/continuity.ts";

test("draft mutation IDs make retries replay-free", () => {
	let state = createDraftState(createDraft({ sessionKey: "s", content: "one" }));
	const mutation = { mutationId: "edit-1", expectedRevision: 0, content: "two" };
	const applied = applyDraftMutation(state, mutation);
	expect(applied.kind).toBe("applied");
	if (applied.kind !== "applied") return;
	state = applied.state;
	expect(applyDraftMutation(state, mutation)).toMatchObject({ kind: "replayed", state });
	expect(applyDraftMutation(state, { ...mutation, content: "forged retry" })).toMatchObject({
		kind: "conflict",
		reason: "mutation-reuse",
	});
});

test("external replacement does not clear the mutation ledger", () => {
	const original = createDraftState(createDraft({ sessionKey: "s", content: "one" }));
	const applied = applyDraftMutation(original, {
		mutationId: "edit-1",
		expectedRevision: 0,
		content: "two",
	});
	if (applied.kind !== "applied") throw new Error("fixture mutation did not apply");
	const replaced = replaceDraftState(applied.state, {
		sessionKey: "s",
		identity: { sessionKey: "s", nativeSessionId: "native-replacement" },
	});
	expect(
		applyDraftMutation(replaced, { mutationId: "edit-1", expectedRevision: 0, content: "two" }),
	).toMatchObject({
		kind: "replayed",
	});
});
