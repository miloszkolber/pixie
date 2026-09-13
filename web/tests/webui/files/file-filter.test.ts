import { expect, test } from "bun:test";
import {
	nodeNameMatchesFilter,
	normalizeFileFilter,
	subtreeHasMatch,
} from "@/files/tree/file-filter";

const webuiRoot = new URL("../../../webui/src/", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, webuiRoot)).text();
}

test("filter queries normalize case and surrounding whitespace", () => {
	expect(normalizeFileFilter("  README  ")).toBe("readme");
	expect(normalizeFileFilter("Dirty.GO")).toBe("dirty.go");
	expect(normalizeFileFilter("   ")).toBe("");
});

test("name matching takes a normalized query and empty queries match everything", () => {
	expect(nodeNameMatchesFilter({ name: "README.md" }, normalizeFileFilter("READ"))).toBeTrue();
	expect(nodeNameMatchesFilter({ name: "dirty.go" }, normalizeFileFilter("  Dirty "))).toBeTrue();
	expect(nodeNameMatchesFilter({ name: "history.txt" }, normalizeFileFilter("md"))).toBeFalse();
	expect(nodeNameMatchesFilter({ name: "anything" }, normalizeFileFilter("  "))).toBeTrue();
});

test("subtree matching recurses into nested children", () => {
	const tree = [
		{
			kind: "dir",
			name: "src",
			children: [
				{ kind: "dir", name: "nested", children: [{ kind: "file", name: "deep-target.ts" }] },
				{ kind: "file", name: "other.ts" },
			],
		},
		{ kind: "file", name: "top.txt" },
	];
	expect(subtreeHasMatch(tree, "deep-target")).toBeTrue();
	expect(subtreeHasMatch(tree, "nested")).toBeTrue();
	expect(subtreeHasMatch(tree, "top")).toBeTrue();
	expect(subtreeHasMatch(tree, "missing")).toBeFalse();
	expect(subtreeHasMatch([], "anything")).toBeFalse();
	expect(subtreeHasMatch(null, "anything")).toBeFalse();
	expect(subtreeHasMatch(tree, "")).toBeTrue();
});

test("filter rows force-expand while filtering, hide only decided leaf branches, and report matches", async () => {
	const row = await source("files/tree/file-node-row.svelte");
	for (const contract of [
		"shownExpanded",
		"filterActive && isDirectory",
		"subtreeHasMatch(children, filter)",
		"nodeNameMatchesFilter(node, filter)",
		'children.every((child) => child.kind === "file")',
		"matchStatus",
		"onMatchChange",
		"lastReported",
		"onDestroy",
		"{#if !hidden}",
		"expanded={shownExpanded}",
		"{onMatchChange}",
	]) {
		expect(row).toContain(contract);
	}
	expect(row).not.toContain("expanded && children}");
});

test("the file tree counts proven matches for an exact no-match message", async () => {
	const tree = await source("files/tree/file-tree.svelte");
	for (const contract of [
		"matchCount",
		"reportMatch",
		"onMatchChange={reportMatch}",
		"No files match this filter.",
		"normalizeFileFilter(filter)",
	]) {
		expect(tree).toContain(contract);
	}
	expect(tree).not.toContain("visibleCount");
});
