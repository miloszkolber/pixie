import { describe, expect, it } from "bun:test";
import type { ToolRenderProps } from "@/chat/render/tool-registry";
import { subagentSummary } from "@/chat/tools/subagent/subagent-card";
import { renderSvelte } from "../../svelte-render";

const props = (result: unknown, status: ToolRenderProps["status"] = "done"): ToolRenderProps => ({
	toolCallId: "subagent-call",
	toolName: "subagent",
	args: { task: "Inspect the patch" },
	result,
	status,
	streaming: false,
});

describe("subagent renderer parsing", () => {
	it("renders child status and escapes untrusted result text", async () => {
		const markup = await renderSvelte(
			"src/chat/tools/subagent/subagent-card.svelte",
			props({
				content: [{ type: "text", text: '<script>alert("x")</script>' }],
				details: {
					mode: "single",
					status: "completed",
					childSessionId: "child-1",
					results: [{ runId: "child-1", agent: "child", status: "completed" }],
				},
			}),
		);
		expect(markup).toContain("Child completed");
		expect(markup).toContain('&lt;script>alert("x")&lt;/script>');
		expect(markup).not.toContain("<script>");
	});

	it("shows bounded recent activity without treating it as a child transcript", async () => {
		const markup = await renderSvelte("src/chat/tools/subagent/subagent-card.svelte", {
			...props(undefined, "running"),
			toolName: "delegate",
			subagentActivity: {
				events: [
					{ childSessionId: "child-1", toolName: "developer__shell" },
					{ childSessionId: "child-2", toolName: '<script>alert("x")</script>' },
				],
				truncated: true,
			},
		});
		expect(markup).toContain("Recent child activity");
		expect(markup).toContain("child-1");
		expect(markup).toContain("child-2");
		expect(markup).toContain("developer__shell");
		expect(markup).toContain("Earlier activity omitted");
		expect(markup).toContain('&lt;script>alert("x")&lt;/script>');
		expect(markup).not.toContain("<script>");
	});
});

describe("subagent renderer registration", () => {
	it("registers only supported subagent identities", async () => {
		for (const name of ["subagent", "delegate", "load"]) {
			const probe = await renderSvelte("tests/webui/chat/fixtures/tool-registry-probe.svelte", {
				name,
			});
			expect(probe).toContain('data-default="false"');
		}
		for (const name of ["other__delegate", "subagent_wait", "contact_supervisor"]) {
			const probe = await renderSvelte("tests/webui/chat/fixtures/tool-registry-probe.svelte", {
				name,
			});
			expect(probe).toContain('data-default="true"');
		}
		expect(subagentSummary({ task: "Inspect" })).toBe("subagent · Inspect");
	});
});
