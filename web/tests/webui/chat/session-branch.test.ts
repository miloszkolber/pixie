import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { renderSvelte } from "./svelte-render";

const turns = new URL("../../../webui/src/chat/render/turns.svelte", import.meta.url);
const workArea = new URL(
	"../../../webui/src/workspace/views/project-work-area.svelte",
	import.meta.url,
);
const context = new URL(
	"../../../webui/src/chat/session/session-branch-context.ts",
	import.meta.url,
);

// AUX-14: the per-turn renderer offers "Edit from here" only when the render
// context carries a native entry id, and delegates the branch to the work area
// instead of sending a display/row id to session.fork.
test("the per-turn renderer offers edit-from-here exactly when a native entry exists", async () => {
	const present = await renderSvelte("tests/webui/chat/fixtures/session-branch-probe.svelte", {
		entryId: "entry-42",
	});
	expect(present).toContain('data-testid="turn-edit-from-here"');
	const absent = await renderSvelte("tests/webui/chat/fixtures/session-branch-probe.svelte", {});
	expect(absent).not.toContain('data-testid="turn-edit-from-here"');

	const source = await Bun.file(turns).text();
	expect(compile(source, { filename: turns.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain("messageEntryId(row.message)");
	expect(source).toContain("branch.editFromHere(entryId)");
	expect(source).toContain("{#if branch && entryId}");
});

test("the work area owns the in-file branch and supplies the native entry", async () => {
	const source = await Bun.file(workArea).text();
	expect(compile(source, { filename: workArea.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain("setSessionBranchContext(");
	expect(source).toContain("editFromHere: (entryId: string) => void editFromHere(entryId)");
	expect(source).toContain('"session.fork",');
	expect(source).toContain("sessionForkParams(target, nativeEntry)");
	expect(source).toContain("openChatInTab(projectAreaId, summary.sessionId)");
});

test("the branch context is the single transcript-to-work-area channel", async () => {
	const source = await Bun.file(context).text();
	expect(source).toContain("export function setSessionBranchContext(");
	expect(source).toContain("export function getSessionBranchContext(");
	expect(source).toContain("editFromHere(entryId: string): void;");
});
