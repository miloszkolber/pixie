import { describe, expect, it } from "bun:test";
import type { ToolRenderProps } from "@/chat/render/tool-registry";
import { childStatus, subagentDetails, subagentSummary } from "@/chat/tools/subagent/subagent-card";
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

describe("pi-subagent result projection", () => {
	const piResult = (results: unknown) => ({
		content: [{ type: "text", text: "summary" }],
		details: { kind: "pi-subagent", projectAgentsDir: null, results },
	});
	const single = {
		callIndex: 0,
		agent: "reviewer",
		agentSource: "user",
		prompt: "Review the patch",
		initialContext: "empty",
		session: {
			handle: "iter-1",
			id: "subagent.abc123",
			name: "subagent: reviewer · iter-1",
			cwd: "/tmp",
			created: true,
			initialContextApplied: "empty",
		},
		exitCode: 0,
		messages: [
			{
				role: "assistant",
				content: [{ type: "text", text: "Looks good." }],
			},
		],
		stderr: "",
		usage: {
			input: 10,
			output: 5,
			cacheRead: 0,
			cacheWrite: 0,
			cost: 0,
			contextTokens: 0,
			turns: 1,
		},
		model: "reviewer/model",
	};

	it("maps upstream calls onto the shared child-run shape", () => {
		const details = subagentDetails(piResult([single]));
		expect(details.kind).toBe("pi-subagent");
		expect(childStatus(details)).toBe("completed");
		expect(details.results?.[0]).toMatchObject({
			agent: "reviewer",
			task: "Review the patch",
			model: { id: "reviewer/model" },
			sessionHandle: "iter-1",
			sessionId: "subagent.abc123",
			finalOutput: "Looks good.",
			usage: { input: 10, output: 5 },
		});
		expect(subagentSummary({ calls: [{ agent: "reviewer", prompt: "Review the patch" }] })).toBe(
			"subagent · reviewer: Review the patch",
		);
	});

	it("renders completed children with session and model", async () => {
		const markup = await renderSvelte("src/chat/tools/subagent/subagent-card.svelte", {
			...props(undefined),
			args: { calls: [{ agent: "reviewer", prompt: "Review the patch" }] },
			result: piResult([single]),
		});
		expect(markup).toContain("Child completed");
		expect(markup).toContain("reviewer");
		expect(markup).toContain("Review the patch");
		expect(markup).toContain("subagent.abc123");
		expect(markup).toContain("reviewer/model");
	});

	it("derives running, failed and cancelled states without status fields", () => {
		expect(
			childStatus(subagentDetails(piResult([{ ...single, exitCode: -1, messages: [] }]))),
		).toBe("running");
		expect(
			childStatus(subagentDetails(piResult([{ ...single, exitCode: 1, errorMessage: "boom" }]))),
		).toBe("failed");
		expect(
			childStatus(subagentDetails(piResult([{ ...single, stopReason: "aborted", exitCode: 1 }]))),
		).toBe("cancelled");
		expect(childStatus(subagentDetails(piResult([])))).toBeUndefined();
	});

	it("lists parallel children with per-child status and escaped errors", async () => {
		const markup = await renderSvelte("src/chat/tools/subagent/subagent-card.svelte", {
			...props(undefined, "running"),
			args: {
				calls: [
					{ agent: "a", prompt: "first" },
					{ agent: "b", prompt: "second" },
				],
			},
			result: piResult([
				{ ...single, agent: "a", prompt: "first" },
				{
					...single,
					callIndex: 1,
					agent: "b",
					prompt: "second",
					exitCode: 1,
					errorMessage: '<script>alert("x")</script>',
				},
			]),
		});
		expect(markup).toContain("2 children");
		expect(markup).toContain("1 completed · 1 failed");
		expect(markup).toContain("Child failed");
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
