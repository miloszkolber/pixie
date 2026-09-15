import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const component = new URL(
	"../../../webui/src/chat/session/session-lifecycle-controls.svelte",
	import.meta.url,
);
const lifecycle = new URL("../../../webui/src/chat/session/session-lifecycle.ts", import.meta.url);

// AUX-14: "Edit from here" must forward the selected native browser entryId so
// the controller routes session.fork to an in-file sibling branch. A plain
// "Fork" keeps producing an independent new-file session.
test("edit-from-here affordance forwards entryId to session.fork", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain('data-testid="session-edit-from-here"');
	expect(source).toContain("{#if canEditFromHere}");
	expect(source).toContain("const entryId = target.entryId;");
	expect(source).toContain('if (entryId === undefined || entryId === "") return;');
	expect(source).toContain(
		'.request("session.fork", { projectId: target.projectId, sessionId: target.sessionId, entryId })',
	);
	expect(source).toContain("openChatInTab(target.projectId, summary.sessionId)");
	// The plain fork stays entryId-free, so edit-from-here cannot degrade into a
	// new-file fork by sending no entry.
	expect(source).toContain(
		'.request("session.fork", { projectId: target.projectId, sessionId: target.sessionId })',
	);
});

test("session lifecycle target and branch state carry the entry affordance", async () => {
	const source = await Bun.file(lifecycle).text();
	expect(source).toContain("entryId?: string | undefined;");
	expect(source).toContain("export function editFromHereActionState(");
	expect(source).toContain('label: busy ? "Branching…" : "Edit from here"');
	expect(source).toContain("does not support branching chats");
	expect(source).toContain("Stop the running chat before branching it");
});
