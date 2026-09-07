import { afterEach, expect, test } from "bun:test";
import type { UserMessage } from "@pixie/contracts";
import { messagesToRuntime } from "@/chat/runtime/hydrate";
import { createSessionRuntime, reduceSessionEvent } from "@/chat/runtime/session-runtime";
import { appStoreApi } from "@/store";
import { renderSvelte } from "./svelte-render";

afterEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

const reviewResources = [
	{ type: "resource" as const, name: "review.ts", mimeType: "text/x-typescript" },
	{ type: "resource" as const, name: "notes.md", mimeType: "text/markdown" },
] as const;

test("keeps text resource attachment markers through replay without resource contents", async () => {
	const resourceOnly: UserMessage = { role: "user", content: [reviewResources[1]] };
	const replay = messagesToRuntime([
		{ role: "user", content: [{ type: "text", text: "Review these" }, ...reviewResources] },
		{ role: "assistant", content: [{ type: "text", text: "Done" }] },
		resourceOnly,
	]);
	expect(replay.turns).toHaveLength(3);
	const finalTurn = replay.turns[2];
	if (finalTurn?.kind !== "user")
		throw new Error("resource-only replay did not create a user turn");
	const markup = await renderSvelte("src/chat/render/turns.svelte", {
		row: { kind: "user", id: "resource-only", message: finalTurn.message },
		onOpenChange: () => {},
	});
	expect(markup).toContain('data-testid="chat-message-text-attachments"');
	expect(markup).toContain("notes.md");
	expect(markup).not.toContain("attachment source text");
});

test("replaces an optimistic resource turn and merges replay marker updates without duplicates", () => {
	const optimistic: UserMessage = {
		role: "user",
		content: [{ type: "text", text: "Review these" }, ...reviewResources],
	};
	let runtime = createSessionRuntime(null, "off");
	runtime = {
		...runtime,
		turns: [
			{ kind: "user", id: "optimistic", message: optimistic, optimistic: { transcriptTotal: 4 } },
		],
	};
	const firstReplay: UserMessage = {
		role: "user",
		content: [{ type: "text", text: "Review these" }, reviewResources[0]],
	};
	runtime = reduceSessionEvent(runtime, { type: "message_start", message: firstReplay });
	runtime = reduceSessionEvent(runtime, { type: "message_start", message: optimistic });
	runtime = reduceSessionEvent(runtime, { type: "message_start", message: optimistic });
	expect(runtime.turns).toHaveLength(1);
	const turn = runtime.turns[0];
	if (turn?.kind !== "user" || typeof turn.message.content === "string") {
		throw new Error("resource replay did not remain a user turn");
	}
	expect(turn.message.content.filter((block) => block.type === "resource")).toEqual([
		...reviewResources,
	]);
});

test("reconnect replay replaces an optimistic text attachment without retaining its source", () => {
	const sessionId = "text-resource-reconnect";
	const state = appStoreApi.getState();
	state.openChatSession("project", sessionId, null, "off");
	state.appendUserMessage(sessionId, "Review this", [
		{
			kind: "text",
			name: "review.ts",
			content: {
				type: "text",
				name: "review.ts",
				mimeType: "text/x-typescript",
				text: "const secret = true",
			},
		},
	]);
	const hydrated = messagesToRuntime(
		[
			{
				role: "user",
				content: [
					{ type: "text", text: "Review this" },
					{ type: "resource", name: "review.ts", mimeType: "text/x-typescript" },
				],
			},
		],
		{ page: { projectionId: "reconnect", start: 0, total: 1 } },
	);
	state.replaceTranscriptSnapshot(
		sessionId,
		{
			sessionId,
			projectId: "project",
			cwd: "/workspace",
			title: "Reconnect",
			model: null,
			thinkingLevel: "off",
			isStreaming: false,
			messageCount: 1,
			updatedAt: 1,
			live: true,
			archived: false,
		},
		hydrated,
		null,
		null,
	);
	const turns = appStoreApi.getState().sessions[sessionId]?.turns ?? [];
	expect(turns).toHaveLength(1);
	const turn = turns[0];
	if (turn?.kind !== "user" || typeof turn.message.content === "string") {
		throw new Error("reconnect did not retain the text attachment marker");
	}
	expect(turn.message.content).toEqual([
		{ type: "text", text: "Review this" },
		{ type: "resource", name: "review.ts", mimeType: "text/x-typescript" },
	]);
});
