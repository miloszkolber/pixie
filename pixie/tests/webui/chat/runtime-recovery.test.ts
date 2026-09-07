import { expect, test } from "bun:test";
import { messagesToRuntime } from "../../../webui/src/chat/runtime/hydrate";
import { deriveRows } from "../../../webui/src/chat/runtime/rows";
import {
	createSessionRuntime,
	reduceSessionEvent,
} from "../../../webui/src/chat/runtime/session-runtime";

test("live cancellation and replay never mark unfinished tool work successful", () => {
	let runtime = createSessionRuntime(null, "off");
	runtime = reduceSessionEvent(runtime, {
		type: "tool-start",
		toolCallId: "write",
		toolName: "write",
		toolCall: { type: "toolCall", id: "write", name: "write", arguments: { path: "a" } },
	});
	runtime = reduceSessionEvent(runtime, { type: "complete", status: "cancelled" });
	expect(runtime.toolResults.write?.status).toBe("interrupted");
	expect(runtime.turns.at(-1)).toMatchObject({ kind: "system", text: "Stopped" });
	const replay = messagesToRuntime(
		[
			{
				role: "assistant",
				content: [{ type: "toolCall", id: "write", name: "write", arguments: { path: "a" } }],
				timestamp: 1,
			},
		],
		{ lastSettlement: { stopReason: "cancelled" } },
	);
	expect(replay.toolResults.write?.status).toBe("interrupted");
	expect(replay.turns.at(-1)).toMatchObject({ kind: "system", text: "Stopped" });
});

test("message identity, partial tool input and display title survive live projection", () => {
	let runtime = createSessionRuntime(null, "off");
	runtime = reduceSessionEvent(runtime, { type: "text", messageId: "one", text: "First" });
	runtime = reduceSessionEvent(runtime, { type: "text", messageId: "two", text: "Second" });
	expect(runtime.turns).toHaveLength(2);
	runtime = reduceSessionEvent(runtime, { type: "tool-start", toolCallId: "t", toolName: "tool" });
	runtime = reduceSessionEvent(runtime, {
		type: "tool-update",
		toolCallId: "t",
		toolCall: {
			type: "toolCall",
			id: "t",
			name: "tool",
			title: "Read requested file",
			arguments: { path: "actual" },
		},
	});
	const row = deriveRows(runtime.turns, runtime.toolResults, true).find(
		(row) => row.kind === "activity",
	);
	expect(row?.kind === "activity" ? row.steps[0] : null).toMatchObject({
		title: "Read requested file",
		args: { path: "actual" },
	});
});

test("limits, refusal and live progress stay distinct from successful completion", () => {
	for (const status of ["max_tokens", "max_turn_requests", "refusal"]) {
		const runtime = reduceSessionEvent(createSessionRuntime(null, "off"), {
			type: "complete",
			status,
		});
		expect(runtime.turns.at(-1)?.kind).toBe("error");
	}
	let runtime = reduceSessionEvent(createSessionRuntime(null, "off"), {
		type: "activity",
		status: "progress",
		text: "Compacting context",
	});
	expect(runtime.activity).toBe("Compacting context");
	expect(runtime.turns).toHaveLength(0);
	runtime = reduceSessionEvent(runtime, { type: "complete", status: "end_turn" });
	expect(runtime.activity).toBeNull();
});

test("a reply without message IDs continues its hydrated assistant block", () => {
	const hydrated = messagesToRuntime(
		[{ role: "assistant", messageId: "", content: [{ type: "text", text: "Partial reply " }] }],
		{ isStreaming: true },
	);
	const runtime = reduceSessionEvent(
		{ ...createSessionRuntime(null, "off"), ...hydrated, isStreaming: true },
		{ type: "text", messageId: null, text: "complete." },
	);
	expect(runtime.turns).toHaveLength(1);
	expect(runtime.turns[0]?.kind === "assistant" ? runtime.turns[0].message.content : null).toEqual([
		{ type: "text", text: "Partial reply complete." },
	]);
});
