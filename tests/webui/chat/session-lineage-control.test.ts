import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const component = new URL(
	"../../../webui/src/chat/session/session-lineage-control.svelte",
	import.meta.url,
);

test("fork lineage compiles as Svelte and preserves available and deleted-parent states", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain("{#if parentSessionId}");
	expect(source).toContain("disabled={parentDeleted}");
	expect(source).toContain(
		'aria-label={parentDeleted ? "Forked from an unavailable chat" : "Open parent chat"}',
	);
	expect(source).toContain(
		'title={parentDeleted ? "Parent chat is unavailable" : "Open parent chat"}',
	);
	expect(source).toContain("Forked from chat");
	expect(source).toContain("openChatInTab(projectAreaId, parentSessionId)");
});
