import { describe, expect, it } from "bun:test";
import type { ToolRenderProps } from "@/chat/render/tool-registry";
import { signetSummary } from "@/chat/tools/signet/signet-card";
import { renderSvelte } from "../../svelte-render";

const props = (result: unknown, status: ToolRenderProps["status"] = "done"): ToolRenderProps => ({
	toolCallId: "signet-call",
	toolName: "signet_recall",
	args: { query: "project decision" },
	result,
	status,
	streaming: false,
});

describe("Signet renderer parsing", () => {
	it("renders safe memory results without exposing raw diagnostics or markup", async () => {
		const offline = await renderSvelte(
			"src/chat/tools/signet/signet-card.svelte",
			props({
				content: [{ type: "text", text: "Signet daemon not running. Memories unavailable." }],
				details: { error: "daemon_offline" },
			}),
		);
		expect(offline).toContain("Signet daemon unavailable");
		expect(offline).not.toContain("Memories unavailable.");
		const markup = await renderSvelte(
			"src/chat/tools/signet/signet-card.svelte",
			props({
				content: [{ type: "text", text: '<script>alert("x")</script>' }],
			}),
		);
		expect(markup).toContain('&lt;script>alert("x")&lt;/script>');
		expect(markup).not.toContain("<script>");
	});
});

describe("Signet renderer registration", () => {
	it("registers all connector tool names with query summaries", async () => {
		for (const name of [
			"signet_recall",
			"signet_source_search",
			"signet_session_search",
			"signet_remember",
		]) {
			const probe = await renderSvelte("tests/webui/chat/fixtures/tool-registry-probe.svelte", {
				name,
				args: { query: "project decision" },
			});
			expect(probe).toContain('data-default="false"');
			expect(probe).toContain('data-summary="project decision"');
		}
		expect(signetSummary({ query: "remember this" })).toBe("remember this");
	});
});
