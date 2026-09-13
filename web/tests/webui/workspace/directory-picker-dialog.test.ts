import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";
import { DIRECTORY_PAGE_SIZE, parentPath } from "@/workspace/projects/directory-picker";

const componentUrl = new URL(
	"../../../webui/src/workspace/projects/directory-picker-dialog.svelte",
	import.meta.url,
);

test("directory navigation stays inside bounded pages", () => {
	expect(parentPath("/mount/work")).toBe("/mount");
	expect(parentPath("/mount/work/")).toBe("/mount");
	expect(parentPath("/mount")).toBeNull();
	expect(parentPath("/")).toBeNull();
	expect(DIRECTORY_PAGE_SIZE).toBe(100);
});

test("directory picker keeps its navigable, selectable, and failure states", async () => {
	const source = await Bun.file(componentUrl).text();
	const result = compile(source, { filename: componentUrl.pathname, generate: false });
	expect(result.warnings).toEqual([]);
	for (const contract of [
		'aria-label="Go to parent directory"',
		'data-testid="directory-picker-path"',
		'role="alert"',
		'role="status"',
		"Select this directory",
		"Show hidden directories",
	]) {
		expect(source).toContain(contract);
	}
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
});

test("only the latest concurrent project-open result can navigate or report an error", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/workspace/projects/open-project-dialogs.svelte", import.meta.url),
	).text();
	expect(source).toContain("const sequence = ++openSequence");
	expect(source).toContain("if (sequence !== openSequence) return;");
	expect(source).toContain("if (sequence === openSequence) openError =");
});
