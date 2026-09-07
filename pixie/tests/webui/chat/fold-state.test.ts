import { describe, expect, test } from "bun:test";
import { createFoldState } from "@/chat/runtime/fold-state";

const { readFold, writeFold, toggleFold, readSelection, selectValue } = createFoldState();

describe("persistent chat disclosure state", () => {
	test("retains explicit fold choices independently", () => {
		expect(readFold("fold-a", true)).toBe(true);
		expect(writeFold("fold-a", false)).toBe(false);
		expect(readFold("fold-a", true)).toBe(false);
		expect(toggleFold("fold-a", false)).toBe(true);
		expect(readFold("fold-b", false)).toBe(false);
	});

	test("selects and clears a detail row", () => {
		expect(readSelection("selection-a")).toBeNull();
		expect(selectValue("selection-a", null, "tool-1")).toBe("tool-1");
		expect(readSelection("selection-a")).toBe("tool-1");
		expect(selectValue("selection-a", "tool-1", "tool-1")).toBeNull();
	});
});

test("identical tool IDs in different sessions keep independent disclosure choices", () => {
	const first = createFoldState();
	const second = createFoldState();
	first.writeFold("tool", true);
	expect(second.readFold("tool")).toBe(false);
});
