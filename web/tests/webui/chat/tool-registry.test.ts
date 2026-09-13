import { expect, test } from "bun:test";
import type { ToolRenderProps } from "@/chat/render/tool-registry";
import {
	DefaultToolRenderer,
	getToolChrome,
	getToolRenderer,
	getToolSummary,
	registerToolRenderer,
	resolveProminence,
	type ToolRenderer,
} from "@/chat/render/tool-registry";

const props = (args: Record<string, unknown>): ToolRenderProps => ({
	toolCallId: "tc1",
	toolName: "x",
	args,
	result: undefined,
	status: "running",
	streaming: false,
});

test("unknown tools use the safe fallback presentation", () => {
	expect(getToolSummary("never-registered", props({}))).toBe("");
	expect(getToolRenderer("never-registered")).toBe(DefaultToolRenderer);
	expect(getToolChrome("never-registered")).toBe("card");
	expect(resolveProminence("never-registered")).toEqual({
		prominence: "routine",
		defaultExpanded: false,
	});
});

test("registration applies the renderer, summary, chrome and prominence precedence", () => {
	const renderer = (() => {}) as unknown as ToolRenderer;
	registerToolRenderer("self-framed-tool", renderer, {
		summary: ({ args }) => `ran ${String(args.command)}`,
		chrome: "bare",
		prominence: "routine",
		defaultExpanded: true,
	});
	expect(getToolRenderer("self-framed-tool")).toBe(renderer);
	expect(getToolSummary("self-framed-tool", props({ command: "echo hi" }))).toBe("ran echo hi");
	expect(getToolChrome("self-framed-tool")).toBe("bare");
	expect(resolveProminence("self-framed-tool")).toEqual({
		prominence: "primary",
		defaultExpanded: true,
	});
});
