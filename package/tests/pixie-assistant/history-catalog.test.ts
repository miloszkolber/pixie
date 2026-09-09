import { expect, test } from "bun:test";
import {
	buildBoundedCatalog,
	compareNativeFile,
	isExternalReplacement,
	pageNativeHistory,
	scanNativeHistory,
} from "../../../assistant/src/history/catalog.ts";

test("history indexing is bounded, read-only, and retains unknown native records", () => {
	const source = '{"type":"session","id":"one"}\n{"type":"future","payload":7}\nnot-json\n{"type":"tail"';
	const result = scanNativeHistory(source, { knownTypes: ["session"], maxRecords: 10, maxBytes: 1024 });
	expect(result.records).toHaveLength(3);
	expect(result.records[0]).toMatchObject({ kind: "known", type: "session" });
	expect(result.unknownRecords.map((record) => record.reason)).toEqual(["unknown-type", "malformed"]);
	expect(result.unknownRecords[0]?.value).toEqual({ type: "future", payload: 7 });
	expect(result.incompleteTrailing?.raw).toBe('{"type":"tail"');
	expect(source).toContain('"future"');
});

test("history pages reject a cursor from an externally replaced source", () => {
	const result = scanNativeHistory('{"type":"session","id":"one"}\n{"type":"session","id":"two"}\n', {
		knownTypes: ["session"],
	});
	const first = pageNativeHistory(result.records, { sourceRevision: "rev-a", limit: 1 });
	expect(first.nextCursor).toEqual({ sourceRevision: "rev-a", index: 1 });
	expect(
		pageNativeHistory(result.records, { sourceRevision: "rev-b", cursor: first.nextCursor, limit: 1 }),
	).toMatchObject({ invalidCursor: true, records: [] });
});

test("catalog bounds output while retaining duplicate and unknown entries", () => {
	const result = buildBoundedCatalog(
		[
			{ sessionId: "same", path: "/one" },
			{ sessionId: "same", path: "/two" },
			{ path: "/future", known: false, record: { future: true } },
		],
		{ maxEntries: 3, maxBytes: 4096 },
	);
	expect(result.entries.map((entry) => entry.key)).toEqual(["same#1", "same#2", "/future#1"]);
	expect(result.entries[1]).toMatchObject({ duplicateOf: "same#1" });
	expect(result.entries[2]).toMatchObject({ kind: "unknown", record: { future: true } });
});

test("file identity distinguishes modification from replacement", () => {
	const before = { path: "/sessions/a.jsonl", device: 1, inode: 2, size: 10, mtimeMs: 1 };
	expect(compareNativeFile(before, { ...before, size: 20, mtimeMs: 2 })).toBe("modified");
	expect(isExternalReplacement(before, { ...before, inode: 3 })).toBe(true);
	expect(compareNativeFile(before, null)).toBe("missing");
});
