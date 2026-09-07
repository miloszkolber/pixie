import { expect, test } from "bun:test";
import type { TranscriptMessage } from "@pixie/contracts";
import { messagesToRuntime } from "@/chat/runtime/hydrate";
import { deriveRows } from "@/chat/runtime/rows";
import { renderSvelte } from "./svelte-render";

test("assistant image replays hydrate, project to an image row, and render an attachment chip", async () => {
	const messages: TranscriptMessage[] = [
		{
			role: "assistant",
			content: [
				{ type: "text", text: "Here is the result." },
				{ type: "image", data: "AA==", mimeType: "image/png" },
			],
		},
	];
	const runtime = messagesToRuntime(messages);
	const assistant = runtime.turns[0];
	if (assistant?.kind !== "assistant") throw new Error("expected assistant turn");
	expect(assistant.message.content).toEqual(
		messages[0]?.role === "assistant" ? messages[0].content : [],
	);

	const rows = deriveRows(runtime.turns, runtime.toolResults, false);
	const image = rows.find((row) => row.kind === "image");
	if (image?.kind !== "image") throw new Error("expected image row");
	expect(image.image).toEqual({ type: "image", data: "AA==", mimeType: "image/png" });
	const markup = await renderSvelte("src/chat/render/turns.svelte", {
		row: image,
		onOpenChange: () => {},
	});
	expect(markup).toContain("chat-attachment-chip");
	expect(markup).toContain('aria-haspopup="dialog"');
});
