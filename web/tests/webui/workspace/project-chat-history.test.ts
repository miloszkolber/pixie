import { expect, test } from "bun:test";
import { shouldLoadArchivedChats } from "@/workspace/projects/project-chat-history-state";

test("archived chats load only after the history menu enables them", () => {
	expect(shouldLoadArchivedChats(true, false, "connected", true)).toBeFalse();
	expect(shouldLoadArchivedChats(false, true, "connected", true)).toBeFalse();
	expect(shouldLoadArchivedChats(true, true, "disconnected", true)).toBeFalse();
	expect(shouldLoadArchivedChats(true, true, "connected", false)).toBeFalse();
	expect(shouldLoadArchivedChats(true, true, "connected", true)).toBeTrue();
});
