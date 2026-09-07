import { describe, expect, it } from "bun:test";
import type { ToolRenderProps } from "@/chat/render/tool-registry";
import {
	browserArtifactUrl,
	browserDetails,
	browserImages,
	browserSummary,
} from "@/chat/tools/browser/browser-card";
import { renderSvelte } from "../../svelte-render";

const props = (result: unknown, status: ToolRenderProps["status"] = "done"): ToolRenderProps => ({
	toolCallId: "browser-call",
	toolName: "browser",
	args: { command: "snapshot", session: "qa" },
	result,
	status,
	streaming: false,
});

const renderCard = (result: unknown, status: ToolRenderProps["status"] = "done") =>
	renderSvelte("src/chat/tools/browser/browser-card.svelte", props(result, status));

describe("browser renderer parsing", () => {
	it("extracts text, image, and guarded artifact details", () => {
		const result = {
			content: [
				{ type: "text", text: "snapshot output" },
				{ type: "image", data: "iVBORw==", mimeType: "image/png" },
				{ type: "image", data: "<svg>", mimeType: "image/svg+xml" },
			],
			details: {
				session: "qa",
				command: "screenshot",
				artifact: { name: "screen.png", url: "/v1/artifacts/qa/screen.png" },
			},
		};
		expect(browserDetails(result)).toEqual({
			session: "qa",
			command: "screenshot",
			artifact: { name: "screen.png", url: "/v1/artifacts/qa/screen.png" },
		});
		expect(browserImages(result)).toHaveLength(1);
		expect(browserArtifactUrl("/v1/artifacts/qa/screen.png")).toBe("/v1/artifacts/qa/screen.png");
		expect(browserArtifactUrl("/v1/artifacts/qa/screen.png?token=secret")).toBeUndefined();
		expect(browserArtifactUrl("https://evil.example/screen.png")).toBeUndefined();
	});

	it("renders failures and screenshots as safe text and image elements", async () => {
		const markup = await renderCard(
			{ content: [{ type: "text", text: '<script>alert("x")</script>' }] },
			"error",
		);
		expect(markup).toContain('&lt;script>alert("x")&lt;/script>');
		expect(markup).not.toContain("<script>");

		const screenshot = await renderCard({
			content: [{ type: "image", data: "iVBORw==", mimeType: "image/png" }],
			details: { artifact: "screen.png" },
		});
		expect(screenshot).toContain('data-testid="tool-browser-images"');
		expect(screenshot).toContain("data:image/png;base64,iVBORw==");
		expect(screenshot).toContain("Artifact:");
	});

	it("shows a bounded running state even before a result arrives", async () => {
		expect(await renderCard(undefined, "running")).toContain("Running browser command");
	});

	it("preserves MCP screenshot details through structured results and Pi text replay", async () => {
		const payload = {
			outcome: "completed",
			command: "screenshot",
			session: "qa",
			code: 0,
			stdout: "Screenshot saved",
			stderr: "",
			artifact: { name: "screen.png", url: "/v1/artifacts/qa/screen.png" },
		};
		const text = { type: "text", text: JSON.stringify(payload) };
		for (const result of [
			{ structuredContent: payload, content: [text] },
			{ content: [text] },
			[{ type: "content", content: text }],
		]) {
			expect(browserDetails(result).artifact).toEqual(payload.artifact);
			const markup = await renderCard(result);
			expect(markup).toContain('href="/v1/artifacts/qa/screen.png"');
			expect(markup).toContain("Screenshot saved");
			expect(markup).not.toContain("&quot;outcome&quot;");
		}
		const failed = await renderCard({
			structuredContent: {
				outcome: "rejected",
				warnings: ["Invalid command"],
				hints: ["Read browser guidance"],
			},
		});
		expect(failed).toContain("Invalid command");
		expect(failed).toContain("Read browser guidance");
		expect(failed).toContain("text-feedback-error");
	});
});

describe("browser renderer registration", () => {
	it("registers browser identities with compact command/session summaries", async () => {
		expect(browserSummary({ command: "open", session: "qa" })).toBe("open in qa");
		for (const name of [
			"browser",
			"pixie_browser__browser_command",
			"private_browser__browser_command",
		]) {
			const probe = await renderSvelte("tests/webui/chat/fixtures/tool-registry-probe.svelte", {
				name,
				compareWith: "browser",
				args: { command: "snapshot", session: "qa" },
			});
			expect(probe).toContain('data-default="false"');
			expect(probe).toContain('data-same="true"');
			expect(probe).toContain('data-summary="snapshot in qa"');
		}
	});
});
