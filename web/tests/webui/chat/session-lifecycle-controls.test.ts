import { expect, test } from "bun:test";
import type { UserMessage } from "@pixie/shared";
import { compile } from "svelte/compiler";
import {
	messageEntryId,
	type SessionLifecycleTarget,
	sessionForkParams,
} from "@/chat/session/session-lifecycle";

const component = new URL(
	"../../../webui/src/chat/session/session-lifecycle-controls.svelte",
	import.meta.url,
);
const lifecycle = new URL("../../../webui/src/chat/session/session-lifecycle.ts", import.meta.url);

const target: SessionLifecycleTarget = {
	projectId: "area-1",
	sessionId: "chat-1",
	title: "Chat",
};

// AUX-14: the plain new-session action must stay an entryId-free new-file fork
// so it can never be mistaken for an in-file edit.
test("new-session fork omits entryId", () => {
	expect(sessionForkParams(target)).toEqual({ projectId: "area-1", sessionId: "chat-1" });
	// A blank selection is not an edit; it must not silently become one.
	expect(sessionForkParams(target, "   ")).toEqual({ projectId: "area-1", sessionId: "chat-1" });
});

// AUX-14: "Edit from here" must forward the selected native entryId so the
// controller routes session.fork to an in-file sibling branch.
test("edit-from-here sends the selected native entry id", () => {
	expect(sessionForkParams(target, "entry-42")).toEqual({
		projectId: "area-1",
		sessionId: "chat-1",
		entryId: "entry-42",
	});
	expect(sessionForkParams(target, "  entry-42  ")).toEqual({
		projectId: "area-1",
		sessionId: "chat-1",
		entryId: "entry-42",
	});
});

// The render context only exposes the native entry when the host projected it.
// A display/row id or a non-string must never be forwarded as the branch entry.
test("messageEntryId accepts only a projected string entry id", () => {
	// The value is intentionally cast: a hostile or pre-projection message can
	// carry a non-string entryId at runtime, and the helper must ignore it.
	const projected = (entryId?: unknown): UserMessage => {
		const message: Record<string, unknown> = { role: "user", content: "hi" };
		if (entryId !== undefined) message.entryId = entryId;
		return message as unknown as UserMessage;
	};
	expect(messageEntryId(projected("entry-42"))).toBe("entry-42");
	expect(messageEntryId(projected(""))).toBeUndefined();
	expect(messageEntryId(projected(42))).toBeUndefined();
	expect(messageEntryId(projected())).toBeUndefined();
});

test("the session controls route fork and edit through the shared params", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	expect(source).toContain('data-testid="session-edit-from-here"');
	expect(source).toContain("{#if canEditFromHere}");
	expect(source).toContain("const entryId = target.entryId;");
	expect(source).toContain('if (entryId === undefined || entryId === "") return;');
	expect(source).toContain('.request("session.fork", sessionForkParams(target, entryId))');
	expect(source).toContain("openChatInTab(target.projectId, summary.sessionId)");
	// The plain fork stays entryId-free, so edit-from-here cannot degrade into a
	// new-file fork by sending no entry.
	expect(source).toContain('.request("session.fork", sessionForkParams(target))');
});

test("session lifecycle target and branch state carry the entry affordance", async () => {
	const source = await Bun.file(lifecycle).text();
	expect(source).toContain("entryId?: string | undefined;");
	expect(source).toContain("export function sessionForkParams(");
	expect(source).toContain("export function messageEntryId(");
	expect(source).toContain("export function editFromHereActionState(");
	expect(source).toContain('label: busy ? "Branching…" : "Edit from here"');
	expect(source).toContain("does not support branching chats");
	expect(source).toContain("Stop the running chat before branching it");
});
