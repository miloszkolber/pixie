import { expect, test } from "bun:test";
import { compile } from "svelte/compiler";

const filesRoot = new URL("../../../webui/src/files/", import.meta.url);

test("every files component compiles as Svelte 5 and retains stable automation hooks", async () => {
	let source = "";
	let count = 0;
	for await (const path of new Bun.Glob("**/*.svelte").scan({ cwd: filesRoot.pathname })) {
		const component = await Bun.file(new URL(path, filesRoot)).text();
		compile(component, { filename: path, generate: "client", modernAst: true });
		source += component;
		count += 1;
	}
	expect(count).toBe(15);
	for (const testid of [
		"change-row",
		"change-row-menu",
		"change-row-actions",
		"change-action-view",
		"change-action-copy-path",
		"change-node",
		"change-tree-folder",
		"change-item",
		"changes-toggle-list",
		"changes-toggle-tree",
		"changes-empty",
		"diff-pane",
		"diff-path",
		"diff-toggle-whitespace",
		"diff-copy",
		"source-diff",
		"file-node",
		"source-preview",
		"file-refresh-error",
		"file-refresh-retry",
		"image-preview-retry",
		"diff-refresh-error",
		"diff-refresh-retry",
		"markdown-view-toggle",
		"md-toggle-preview",
		"md-toggle-source",
		"markdown-preview",
		"md-alert",
	]) {
		expect(source).toContain(testid);
	}
});

test("a reused tree row clears compressed directory state before becoming a file", async () => {
	const source = await Bun.file(new URL("tree/file-node-row.svelte", filesRoot)).text();
	expect(source).toMatch(
		/const currentIdentity = `\$\{projectAreaId\}\\0\$\{node\.kind\}\\0\$\{node\.path\}`;/,
	);
	expect(source.indexOf("directory = null;")).toBeLessThan(
		source.indexOf("if (!isDirectory) return;"),
	);
	expect(source.indexOf("if (!rowExpanded) return;")).toBeLessThan(
		source.indexOf('.request("fs.readDir"'),
	);
});
