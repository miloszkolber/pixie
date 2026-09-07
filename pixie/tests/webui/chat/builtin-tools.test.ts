import { expect, test } from "bun:test";
import { messagesToRuntime } from "@/chat/runtime/hydrate";
import { deriveRows } from "@/chat/runtime/rows";
import { createSessionRuntime, reduceSessionEvent } from "@/chat/runtime/session-runtime";
import {
	canOpenMcpApp,
	MCP_APP_IFRAME_SANDBOX,
	mcpAppPermissionLabels,
	toMcpToolResult,
} from "@/chat/tools/apps/mcp-app-view";
import { resultText } from "@/chat/tools/tool-helpers";
import { renderSvelte } from "./svelte-render";

function props(
	toolName: string,
	result: unknown,
	args: Record<string, unknown> = {},
	status: "running" | "done" | "error" = "done",
) {
	return { toolCallId: "builtin-tool", toolName, args, result, status, streaming: false };
}

test("generic builtin output preserves ordered text, image and resource blocks without executing HTML", async () => {
	const blocks = [
		{ type: "text", text: "Loaded image" },
		{ type: "image", mimeType: "image/png", data: "YWJjZA==" },
		{
			type: "resource",
			resource: {
				uri: "ui://apps/example",
				mimeType: "text/html;profile=mcp-app",
				text: "<script>unsafe()</script>",
			},
		},
		{ type: "future-output", value: "kept for compatibility" },
	];
	for (const result of [
		blocks,
		{ content: blocks },
		{
			structuredContent: { path: "/repo/image.png", width: 12 },
			content: blocks.map((content) => ({ type: "content", content })),
		},
	]) {
		const markup = await renderSvelte(
			"src/chat/render/default-tool-renderer.svelte",
			props("read_image", result, {}, "running"),
		);
		expect(markup).toContain("Loaded image");
		expect(markup).toContain("chat-attachment-chip");
		expect(markup).not.toContain("YWJjZA==");
		expect(markup).toContain("ui://apps/example");
		expect(markup).toContain("&lt;script>unsafe()&lt;/script>");
		expect(markup).not.toContain("<script");
		expect(markup).not.toContain("<iframe");
		expect(markup).toContain("kept for compatibility");
		expect(markup.indexOf("Loaded image")).toBeLessThan(markup.indexOf("chat-attachment-chip"));
	}
	expect(
		await renderSvelte(
			"src/chat/render/default-tool-renderer.svelte",
			props("orchestrator__view_session", { content: [], structuredContent: { status: "done" } }),
		),
	).toContain("done");
	const failure = {
		structuredContent: { error: "permission denied" },
		content: blocks.slice(0, 2),
	};
	const genericFailure = await renderSvelte(
		"src/chat/render/default-tool-renderer.svelte",
		props("shell", failure, {}, "error"),
	);
	const bashFailure = await renderSvelte(
		"src/chat/tools/bash-card.svelte",
		props("shell", failure, {}, "error"),
	);
	for (const markup of [genericFailure, bashFailure]) {
		expect(markup).toContain("permission denied");
		expect(markup).toContain("Loaded image");
	}
	expect(genericFailure).toContain("chat-attachment-chip");
});

test("Apps creation and iteration keep their concise saved state", async () => {
	for (const toolName of ["apps__create_app", "apps__iterate_app"]) {
		const markup = await renderSvelte(
			"src/chat/render/default-tool-renderer.svelte",
			props(toolName, "Opened in a new window"),
		);
		expect(markup).toContain("Opened in a new window");
		expect(markup).toContain("App saved in the agent session");
		expect(markup).not.toContain("not supported");
	}
	for (const toolName of ["apps__list_apps", "other__create_app"]) {
		expect(
			await renderSvelte("src/chat/render/default-tool-renderer.svelte", props(toolName, "Result")),
		).not.toContain("App saved");
	}
	expect(
		await renderSvelte(
			"src/chat/render/default-tool-renderer.svelte",
			props("apps__create_app", "Failed", {}, "error"),
		),
	).not.toContain("App saved");
});

test("settled session-bound MCP Apps preserve errors and declared permission labels", async () => {
	const app = {
		toolName: "weather",
		extensionName: "weather-server",
		resourceUri: "ui://weather/dashboard",
	};
	expect(canOpenMcpApp(app, "done")).toBe(true);
	expect(canOpenMcpApp(app, "error")).toBe(true);
	expect(canOpenMcpApp(app, "running")).toBe(false);
	expect(canOpenMcpApp({ ...app, resourceUri: "https://unsafe.example" }, "done")).toBe(false);
	expect(MCP_APP_IFRAME_SANDBOX).toBe("allow-scripts allow-same-origin allow-forms");
	expect(mcpAppPermissionLabels({ clipboardWrite: {}, camera: {}, geolocation: {} })).toEqual([
		"Camera",
		"Location",
		"Clipboard write",
	]);
	const appProps = {
		toolCallId: "tool-1",
		args: { city: "Warsaw" },
		result: { content: [{ type: "text", text: "Clear" }] },
		app,
		status: "done",
	};
	expect(await renderSvelte("src/chat/tools/apps/mcp-app-view.svelte", appProps)).not.toContain(
		"Open app",
	);
	expect(
		await renderSvelte("tests/webui/chat/fixtures/mcp-app-view-host.svelte", {
			...appProps,
			projectId: "project-1",
			sessionId: "session-1",
		}),
	).toContain("Open app");
	expect(toMcpToolResult("legacy result")).toEqual({
		content: [{ type: "text", text: "legacy result" }],
	});
	expect(
		toMcpToolResult({ content: [{ type: "text", text: "MCP result" }], isError: true }),
	).toEqual({
		content: [{ type: "text", text: "MCP result" }],
		isError: true,
	});
	expect(toMcpToolResult([{ type: "text", text: "Failed" }], true)).toEqual({
		content: [{ type: "text", text: "Failed" }],
		isError: true,
	});
	expect(
		await renderSvelte("tests/webui/chat/fixtures/mcp-app-view-host.svelte", {
			...appProps,
			toolCallId: "tool-error",
			result: [{ type: "text", text: "Provider failed" }],
			status: "error",
			projectId: "project-1",
			sessionId: "session-1",
		}),
	).toContain("Open app");
});

test("official developer and summon results use their real arguments and show returned output", async () => {
	const shellProbe = await renderSvelte("tests/webui/chat/fixtures/tool-registry-probe.svelte", {
		name: "shell",
		compareWith: "bash",
	});
	expect(shellProbe).toContain('data-default="false"');
	expect(shellProbe).toContain('data-same="true"');
	const thirdPartyProbe = await renderSvelte(
		"tests/webui/chat/fixtures/tool-registry-probe.svelte",
		{
			name: "third_party__shell",
		},
	);
	expect(thirdPartyProbe).toContain('data-default="true"');
	const shellResult = [
		{ type: "text", text: "stdout" },
		{ type: "text", text: "stderr" },
	];
	expect(resultText(shellResult)).toBe("stdout\nstderr");
	const edit = await renderSvelte(
		"src/chat/tools/edit-card.svelte",
		props("edit", "Updated file", {
			path: "/repo/file.ts",
			before: "old value",
			after: "new value",
		}),
	);
	expect(edit).toContain("old value");
	expect(edit).toContain("new value");
	expect(edit).toContain("Updated file");
	for (const toolName of ["delegate", "load"]) {
		const returned = await renderSvelte(
			"src/chat/tools/subagent/subagent-card.svelte",
			props(toolName, { content: [{ type: "text", text: "Actual Pi result" }] }),
		);
		expect(returned).toContain("Actual Pi result");
		expect(returned).not.toContain("Subagent running");
		const failed = await renderSvelte(
			"src/chat/tools/subagent/subagent-card.svelte",
			props(toolName, { content: [{ type: "text", text: "Actual Pi failure" }] }, {}, "error"),
		);
		expect(failed).toContain("Actual Pi failure");
		expect(failed).toContain("text-feedback-error");
	}
	const replay = messagesToRuntime([
		{
			role: "assistant",
			content: [
				{
					type: "toolCall",
					id: "summon-call",
					toolName: "summon__delegate",
					name: "delegate",
					arguments: {},
				},
			],
		},
	]);
	const activity = deriveRows(replay.turns, replay.toolResults, false).find(
		(row) => row.kind === "activity",
	);
	const projected = activity?.kind === "activity" ? activity.steps[0] : undefined;
	expect(projected?.kind === "tool" ? projected.toolName : undefined).toBe("summon__delegate");
});

test("status-only tool completion retains the right streamed result and matches history hydration", () => {
	const image = [{ type: "image", mimeType: "image/png", data: "YWJjZA==" }];
	const subagentActivity = {
		events: [{ childSessionId: "child-1", toolName: "shell" }],
		truncated: true,
	};
	const app = {
		toolName: "read_image",
		extensionName: "images",
		resourceUri: "ui://images/viewer",
	};
	const pending = messagesToRuntime([], {
		pendingTools: [{ toolCallId: "image", output: image, app, subagentActivity }],
	});
	expect(pending.toolResults.image).toEqual({
		status: "running",
		raw: image,
		app,
		subagentActivity,
	});
	const precedence = messagesToRuntime(
		[
			{ role: "toolResult", toolCallId: "image", content: "completed" },
			{ role: "toolResult", toolCallId: "__proto__", content: "safe", isError: true },
			{ role: "toolResult", toolCallId: "without-output", content: "old result" },
			{
				role: "assistant",
				content: [
					{ type: "toolCall", id: "image", name: "image", arguments: {} },
					{ type: "toolCall", id: "__proto__", name: "reserved", arguments: {} },
					{ type: "toolCall", id: "without-output", name: "read", arguments: {} },
				],
			},
		],
		{
			pendingTools: [
				{ toolCallId: "image", output: "current pending output" },
				{ toolCallId: "__proto__", output: "current reserved output" },
				{ toolCallId: "toString", output: "still running" },
			],
		},
	);
	expect(Object.getPrototypeOf(precedence.toolResults)).toBeNull();
	expect(precedence.toolResults.image).toEqual({
		status: "running",
		raw: "current pending output",
	});
	expect(Reflect.get(precedence.toolResults, "__proto__")).toEqual({
		status: "running",
		raw: "current reserved output",
	});
	expect(Reflect.get(precedence.toolResults, "toString")).toEqual({
		status: "running",
		raw: "still running",
	});
	expect(Object.hasOwn(precedence.toolResults, "without-output")).toBe(false);
	let reloaded = createSessionRuntime(null, "off");
	reloaded = { ...reloaded, ...pending };
	reloaded = reduceSessionEvent(reloaded, {
		type: "tool-end",
		toolCallId: "image",
		status: "completed",
	});
	expect(reloaded.toolResults.image).toEqual({ status: "done", raw: image, app, subagentActivity });
	let runtime = createSessionRuntime(null, "off");
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "image",
		toolName: "read_image",
		tool: { path: "/repo/image.png" },
	});
	expect(runtime.toolResults.image?.raw).toBeUndefined();
	runtime = reduceSessionEvent(runtime, {
		type: "tool-update",
		toolCallId: "image",
		tool: image,
		app,
		subagentActivity,
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-update",
		toolCallId: "other",
		tool: "other result",
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-end",
		toolCallId: "image",
		status: "completed",
	});
	const history = messagesToRuntime([
		{ role: "toolResult", toolCallId: "image", content: image, app, subagentActivity },
	]);
	expect(runtime.toolResults.image).toEqual(history.toolResults.image);
	expect(runtime.toolResults.other?.raw).toBe("other result");
	for (const tool of ["", false]) {
		runtime = reduceSessionEvent(runtime, {
			type: "tool-end",
			toolCallId: "image",
			status: "completed",
			tool,
		});
		expect(runtime.toolResults.image?.raw).toBe(tool);
	}
});
