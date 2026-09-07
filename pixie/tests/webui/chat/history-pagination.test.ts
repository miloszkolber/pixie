import { expect, test } from "bun:test";
import type { TranscriptMessage } from "@pixie/contracts";
import { loadTranscriptUntil } from "@/chat/history/history-loading";
import { messagesToRuntime, prependTranscriptPage } from "@/chat/runtime/hydrate";
import { deriveRows } from "@/chat/runtime/rows";
import {
	createSessionRuntime,
	reduceSessionEvent,
	type SessionRuntime,
} from "@/chat/runtime/session-runtime";

test("transcript pages hydrate and prepend without changing identity, result precedence, or row keys", () => {
	const olderApp = {
		toolName: "read",
		extensionName: "older-extension",
		resourceUri: "ui://older/result",
	};
	const newerApp = {
		toolName: "read",
		extensionName: "newer-extension",
		resourceUri: "ui://newer/result",
	};
	const olderSubagent = { events: [{ childSessionId: "older-child", toolName: "read" }] };
	const newerSubagent = { events: [{ childSessionId: "newer-child", toolName: "read" }] };
	const olderMessages: TranscriptMessage[] = [
		{
			role: "assistant",
			content: [
				{ type: "toolCall", id: "reused", name: "read", arguments: { path: "/older" } },
				{ type: "image", data: "AA==", mimeType: "image/png" },
			],
		},
		{
			role: "toolResult",
			toolCallId: "reused",
			content: "older result",
			app: olderApp,
			subagentActivity: olderSubagent,
		},
		{ role: "user", content: "Continue" },
	];
	const tailMessages: TranscriptMessage[] = [
		{ role: "user", content: "Latest prompt" },
		{
			role: "assistant",
			content: [
				{ type: "toolCall", id: "reused", name: "read", arguments: { path: "/newer" } },
				{ type: "text", text: "Working" },
			],
		},
	];
	const tail = messagesToRuntime(tailMessages, {
		page: { projectionId: "projection", start: 3, total: 5 },
		pendingTools: [
			{
				toolCallId: "reused",
				output: "newer result",
				app: newerApp,
				subagentActivity: newerSubagent,
			},
		],
		isStreaming: true,
	});
	const repeatedTail = messagesToRuntime(tailMessages, {
		page: { projectionId: "projection", start: 3, total: 5 },
		isStreaming: true,
	});
	expect(tail.turns.map((turn) => turn.id)).toEqual(repeatedTail.turns.map((turn) => turn.id));
	expect(tail.currentAssistantId).toBe("transcript:projection:4");

	const runtime = {
		...createSessionRuntime(null, "off"),
		turns: tail.turns,
		toolResults: tail.toolResults,
		turnIdByMessageIndex: tail.turnIdByMessageIndex,
		currentAssistantId: tail.currentAssistantId,
		isStreaming: true,
		transcript: tail.transcript,
	};
	const older = messagesToRuntime(olderMessages, {
		page: { projectionId: "projection", start: 0, total: 3 },
	});
	const combined = prependTranscriptPage(runtime, older);
	if (!combined) throw new Error("expected a contiguous page to prepend");

	expect(combined.transcript).toEqual({ projectionId: "projection", start: 0, total: 5 });
	expect(combined.turnIdByMessageIndex).toMatchObject({
		0: "transcript:projection:0",
		1: null,
		2: "transcript:projection:2",
		3: "transcript:projection:3",
		4: "transcript:projection:4",
	});
	expect(combined.toolResults.reused?.raw).toBe("newer result");

	const rows = deriveRows(combined.turns, combined.toolResults, combined.isStreaming);
	const rowKeys = rows.flatMap((row) =>
		row.kind === "activity" ? [row.id, ...row.steps.map((step) => step.id)] : [row.id],
	);
	expect(new Set(rowKeys).size).toBe(rowKeys.length);
	const toolRows = rows.flatMap((row) => {
		if (row.kind === "tool") return [row];
		if (row.kind === "activity") return row.steps.filter((step) => step.kind === "tool");
		return [];
	});
	expect(
		toolRows.map((row) => ({
			path: row.args.path,
			result: row.tool?.raw,
			app: row.tool?.app,
			subagentActivity: row.tool?.subagentActivity,
		})),
	).toEqual([
		{
			path: "/older",
			result: "older result",
			app: olderApp,
			subagentActivity: olderSubagent,
		},
		{
			path: "/newer",
			result: "newer result",
			app: newerApp,
			subagentActivity: newerSubagent,
		},
	]);
	expect(rows.find((row) => row.kind === "image")).toMatchObject({
		kind: "image",
		image: { type: "image", data: "AA==", mimeType: "image/png" },
	});

	const trailingResult = messagesToRuntime(
		[
			tailMessages[1] as TranscriptMessage,
			{ role: "toolResult", toolCallId: "reused", content: "done" },
		],
		{
			page: { projectionId: "projection-2", start: 0, total: 2 },
			isStreaming: true,
		},
	);
	expect(trailingResult.currentAssistantId).toBeNull();
});

test("an explicit history jump loads each page until its message is available", async () => {
	let transcript = { projectionId: "projection", start: 180, total: 240 };
	const starts = [120, 60, 0];
	let requests = 0;
	await loadTranscriptUntil(
		0,
		() => transcript,
		async () => {
			const start = starts[requests++];
			if (start === undefined) return "failed";
			transcript = { ...transcript, start };
			return "loaded";
		},
		() => true,
	);
	expect(requests).toBe(3);
	expect(transcript.start).toBe(0);
});

test("a hydrated pending tool settles in its invocation before the ID is reused", () => {
	const hydrated = messagesToRuntime(
		[
			{
				role: "assistant",
				content: [{ type: "toolCall", id: "reused", name: "read", arguments: { path: "/older" } }],
			},
			{ role: "toolResult", toolCallId: "reused", content: "older result" },
			{ role: "user", content: "next" },
			{
				role: "assistant",
				content: [
					{ type: "toolCall", id: "reused", name: "read", arguments: { path: "/pending" } },
				],
			},
		],
		{
			page: { projectionId: "projection", start: 0, total: 4 },
			pendingTools: [{ toolCallId: "reused", output: "partial" }],
			isStreaming: true,
		},
	);
	let runtime: SessionRuntime = {
		...createSessionRuntime(null, "off"),
		...hydrated,
		isStreaming: true,
	};
	runtime = reduceSessionEvent(runtime, {
		type: "tool-end",
		toolCallId: "reused",
		status: "completed",
		tool: "final",
	});
	runtime = reduceSessionEvent(runtime, {
		type: "message_start",
		message: { role: "assistant", content: [] },
	});
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "reused",
		toolName: "read",
		tool: { path: "/live" },
	});
	const tools = deriveRows(runtime.turns, runtime.toolResults, true).flatMap((row) => {
		if (row.kind === "tool") return [row];
		if (row.kind === "activity") return row.steps.filter((step) => step.kind === "tool");
		return [];
	});
	expect(
		tools.map((tool) => ({
			path: tool.args.path,
			status: tool.tool?.status,
			result: tool.tool?.raw,
		})),
	).toEqual([
		{ path: "/older", status: "done", result: "older result" },
		{ path: "/pending", status: "done", result: "final" },
		{ path: "/live", status: "running", result: undefined },
	]);
});
