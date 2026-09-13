import { expect, test } from "bun:test";
import { messagesToRuntime } from "../../../webui/src/chat/runtime/hydrate";
import { createRowDeriver, deriveRows } from "../../../webui/src/chat/runtime/rows";
import {
	createSessionRuntime,
	reduceSessionEvent,
	type SessionRuntime,
} from "../../../webui/src/chat/runtime/session-runtime";

test("older paging preserves mounted row identities while changed live content remains visible", () => {
	const project = createRowDeriver();
	let runtime: SessionRuntime = {
		...createSessionRuntime(null, "off"),
		...messagesToRuntime(
			[
				{ role: "user", content: "Current request", timestamp: 1 },
				{
					role: "assistant",
					messageId: "answer",
					content: [{ type: "text", text: "Current answer" }],
					timestamp: 2,
				},
			],
			{ isStreaming: true },
		),
	};
	const original = project(runtime.turns, runtime.toolResults, false);
	const older = messagesToRuntime([{ role: "user", content: "Earlier request", timestamp: 0 }]);
	runtime = {
		...runtime,
		turns: [...older.turns.map((turn) => ({ ...turn, id: `older-${turn.id}` })), ...runtime.turns],
	};
	const paged = project(runtime.turns, runtime.toolResults, false);
	for (const row of original) expect(paged.find((next) => next.id === row.id)).toBe(row);
	runtime = reduceSessionEvent(runtime, { type: "text", messageId: "answer", text: " continued" });
	const live = project(runtime.turns, runtime.toolResults, true);
	expect(live).toEqual(deriveRows(runtime.turns, runtime.toolResults, true));
	expect(live.find((row) => row.kind === "markdown")).toMatchObject({
		text: "Current answer continued",
	});
	expect(live.find((row) => row.id === original[0]?.id)).toBe(original[0]);
});

test("row reuse preserves tool, title, file summary and settlement updates", () => {
	const project = createRowDeriver();
	let runtime = createSessionRuntime(null, "off");
	const events = [
		{
			type: "tool-start",
			toolCallId: "write",
			toolName: "write",
			toolCall: { type: "toolCall", id: "write", name: "write", arguments: { path: "one" } },
		},
		{
			type: "tool-update",
			toolCallId: "write",
			toolCall: {
				type: "toolCall",
				id: "write",
				name: "write",
				title: "Write the requested file",
				arguments: { path: "two" },
			},
		},
		{ type: "tool-end", toolCallId: "write", status: "done", tool: { output: "Saved" } },
		{ type: "complete", status: "end_turn" },
	] as const;
	for (const event of events) {
		runtime = reduceSessionEvent(runtime, event);
		const actual = project(runtime.turns, runtime.toolResults, runtime.isStreaming);
		expect(actual).toEqual(deriveRows(runtime.turns, runtime.toolResults, runtime.isStreaming));
		const unchanged = project(runtime.turns, runtime.toolResults, runtime.isStreaming);
		for (let index = 0; index < actual.length; index++)
			expect(unchanged[index]).toBe(actual[index]);
	}
});
