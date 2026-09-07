import { expect, test } from "bun:test";
import type { AgentMentionInfo } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import type { MentionCandidate } from "@/chat/composer/composer-state";
import { deriveRows } from "@/chat/runtime/rows";
import type { ChatTurn } from "@/chat/runtime/types";
import {
	locateChatRow,
	mentionCandidatesForQuery,
	resolveSendBehavior,
	uniqueRecentPrompts,
} from "@/chat/view/chat-view-state";

const chatRoot = new URL("../../../webui/src/chat/", import.meta.url);

test("send resolution preserves steer fallback and existing follow-up ordering", () => {
	expect(resolveSendBehavior("steer", false, false)).toEqual({
		effectiveBehavior: "queue",
		heldByQueue: false,
	});
	expect(resolveSendBehavior("send", true, true)).toEqual({
		effectiveBehavior: "queue",
		heldByQueue: true,
	});
	expect(resolveSendBehavior("steer", true, true)).toEqual({
		effectiveBehavior: "steer",
		heldByQueue: false,
	});
});

test("agent mentions lead flat queries while path queries remain file-only", () => {
	const agents: AgentMentionInfo[] = [
		{
			name: "Reviewer",
			description: "Review the current change",
			sourceType: "agent",
			mention: "@reviewer",
		},
	];
	const file: MentionCandidate = { kind: "file", name: "review.ts", path: "src/review.ts" };
	const files = [file];
	expect(mentionCandidatesForQuery("rev", agents, files)).toEqual([
		{
			kind: "agent",
			name: "Reviewer",
			description: "Review the current change",
			sourceType: "agent",
			mention: "@reviewer",
		},
		file,
	]);
	expect(mentionCandidatesForQuery("src/", agents, files)).toEqual(files);
	expect(mentionCandidatesForQuery(null, agents, files)).toEqual([]);
});

test("history location uses indexed turns first and recovers from stale turn mappings", () => {
	const turns: ChatTurn[] = [
		{ kind: "user", id: "user-1", message: { role: "user", content: "First prompt" } },
		{
			kind: "assistant",
			id: "assistant-1",
			message: { role: "assistant", content: [{ type: "text", text: "Recovered answer" }] },
			streaming: false,
		},
		{ kind: "user", id: "user-2", message: { role: "user", content: "Latest prompt" } },
	];
	const rows = deriveRows(turns, {}, false);
	expect(uniqueRecentPrompts(turns)).toEqual(["Latest prompt", "First prompt"]);
	expect(locateChatRow(2, "Latest prompt", { 2: "user-2" }, turns, rows)?.id).toBe("user-2");
	expect(locateChatRow(1, "Recovered answer", { 1: "missing" }, turns, rows)?.id).toBe(
		"assistant-1:text:0",
	);
});

test("chat view is React-free Svelte and retains the complete interaction contract", async () => {
	const viewUrl = new URL("chat-view.svelte", chatRoot);
	const transcriptUrl = new URL("view/chat-transcript.svelte", chatRoot);
	const [view, transcript] = await Promise.all([
		Bun.file(viewUrl).text(),
		Bun.file(transcriptUrl).text(),
	]);
	for (const [url, source] of [
		[viewUrl, view],
		[transcriptUrl, transcript],
	] as const) {
		expect(source).not.toMatch(
			/from ["'](?:react|react-dom|react-virtuoso|lucide-react|@radix-ui)/,
		);
		expect(compile(source, { filename: url.pathname, generate: false }).warnings).toEqual([]);
	}

	for (const contract of [
		"session.getMessages",
		"STALE_TRANSCRIPT_PROJECTION",
		"replaceTranscriptSnapshot",
		"prependTranscriptPage",
		"session.getStats",
		"session.getAgentMentions",
		"fs.readDir",
		"session.prompt",
		"session.steer",
		"session.queueAdd",
		"session.abort",
		"session.queueEdit",
		"session.queueRemove",
		"session.queueRetry",
		"session.questionReply",
		"session.delete",
		"loadTranscriptUntil",
		"setAskStatesContext",
		"setChatActionsContext",
		"setMcpAppSessionContext",
		"createHistorySearch",
		"createSessionCommandSync",
		"bind:this={composer}",
		"<HistoryOverlay",
		"<QueueStrip",
		"<ChatHeader",
	]) {
		expect(view).toContain(contract);
	}

	for (const contract of [
		'role="log"',
		'tabindex="0"',
		"data-default-pinned",
		"beginPrepend",
		"finishPrepend",
		"restoredChatScrollTop",
		"ResizeObserver",
		'scrollIntoView({ block: "center" })',
		"content-visibility: auto",
		"contain-intrinsic-size: auto 12rem",
	]) {
		expect(transcript).toContain(contract);
	}

	expect(await Bun.file(new URL("chat-view.tsx", chatRoot)).exists()).toBeFalse();
	expect(await Bun.file(new URL("view/use-chat-scroll.ts", chatRoot)).exists()).toBeFalse();
});
