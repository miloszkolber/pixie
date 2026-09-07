import { expect, test } from "bun:test";
import { renderSvelte } from "../svelte-render";

test("web fetch links only safe browser URLs", async () => {
	const safe = await renderSvelte("src/chat/tools/web/web-fetch-card.svelte", {
		args: { url: "https://www.example.com/docs" },
		result: "Fetched",
		status: "done",
		toolCallId: "fetch-safe",
		toolName: "web_fetch",
		streaming: false,
	});
	expect(safe).toContain('href="https://www.example.com/docs"');
	expect(safe).toContain(">example.com<");

	const unsafe = await renderSvelte("src/chat/tools/web/web-fetch-card.svelte", {
		args: { url: "javascript:alert(1)" },
		result: "Fetched",
		status: "done",
		toolCallId: "fetch-unsafe",
		toolName: "web_fetch",
		streaming: false,
	});
	expect(unsafe).toContain("javascript:alert(1)");
	expect(unsafe).not.toContain("href=");
});
