import { expect, test } from "bun:test";
import type { HistorySearchResult, PromptHit } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import {
	buildHistoryScope,
	highlightHistoryText,
	historySelectionAnnouncement,
	historySelectionCount,
	jumpTarget,
	resolveHistorySelection,
} from "@/chat/history/history-search";

const component = new URL(
	"../../../webui/src/chat/history/history-overlay.svelte",
	import.meta.url,
);
const prompt: PromptHit = {
	text: "Deploy the service",
	timestamp: 1,
	sessionId: "chat-1",
	cwd: "/project",
	projectId: "project-1",
	messageIndex: 4,
	anchorText: "Deploy",
};
const result: HistorySearchResult = {
	prompts: [prompt],
	messages: [],
	promptTotal: 1,
	messageTotal: 0,
	indexing: false,
	incomplete: false,
};

test("history selection keeps its keyboard announcement and jump target", () => {
	expect(historySelectionCount("compact", result)).toBe(1);
	expect(resolveHistorySelection("compact", result, 0)).toEqual({ kind: "prompt", hit: prompt });
	expect(historySelectionAnnouncement("compact", result, 0)).toBe(
		"Selected 1 of 1: Deploy the service",
	);
	expect(jumpTarget(prompt)).toEqual({
		projectAreaId: "project-1",
		projectId: "project-1",
		sessionId: "chat-1",
		messageIndex: 4,
		anchorText: "Deploy",
	});
	expect(buildHistoryScope("chat", "chat-1", "area-1", "project-1")).toEqual({
		kind: "chat",
		sessionId: "chat-1",
	});
});

test("history highlighting is literal, case-insensitive, and prefers longer terms", () => {
	expect(
		highlightHistoryText("Deploy deployment DEPLOY", "deploy deployment")
			.filter((part) => part.highlighted)
			.map((part) => part.text),
	).toEqual(["Deploy", "deployment", "DEPLOY"]);
	expect(
		highlightHistoryText("literal [term]", "[term]").some((part) => part.highlighted),
	).toBeTrue();
});

test("history overlay compiles as Svelte and retains its complete interaction contract", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	for (const testId of [
		"history-overlay",
		"history-query",
		"history-scope",
		"history-scope-option",
		"history-results",
		"history-item",
		"history-delete-chat",
		"history-jump",
		"history-jump-shortcut",
		"history-preview",
		"history-error",
		"history-indexing",
		"history-counts",
		"history-expand-hint",
	])
		expect(source).toContain(`data-testid="${testId}"`);
	expect(source).toContain('type="search"');
	expect(source).toContain('<ul aria-label="Prompt history results"');
	expect(source).toContain("<li");
	expect(source).toContain("aria-current={isSelected}");
	expect(source).toContain('role="status"');
	expect(source).toContain("historySelectionAnnouncement(");
	expect(source).toContain('event.key === "ArrowDown"');
	expect(source).toContain('event.key === "ArrowUp"');
	expect(source).toContain('event.key === "Tab"');
	expect(source).toContain('event.key !== "Enter"');
	expect(source).toContain("event.metaKey || event.ctrlKey");
	expect(source).toContain("event.shiftKey");
});
