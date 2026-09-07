import type { ImageContent, UserMessage } from "@pixie/contracts";
import { resolveProminence } from "../render/tool-registry";
import { strArg } from "../tools/tool-helpers";
import type { ChatTurn, CompactionState, ToolResultState } from "./types";

export interface ToolCallData {
	toolCallId: string;
	toolName: string;
	title?: string;
	args: Record<string, unknown>;
	tool: ToolResultState | undefined;
	dead: boolean;
	streaming: boolean;
}

export type ActivityStep =
	| ({ kind: "tool"; id: string } & ToolCallData)
	| { kind: "thinking"; id: string; text: string; streaming: boolean };

export type ChatRow =
	| { kind: "user"; id: string; message: UserMessage; imageAttachmentNames?: string[] }
	| { kind: "system"; id: string; text: string }
	| { kind: "error"; id: string; text: string }
	| ({ kind: "compaction"; id: string } & CompactionState)
	| {
			kind: "retry";
			id: string;
			source: "turn" | "summarization";
			attempt: number;
			maxAttempts: number;
			delayMs: number;
	  }
	| { kind: "markdown"; id: string; text: string }
	| { kind: "image"; id: string; image: ImageContent }
	| ({ kind: "tool"; id: string } & ToolCallData)
	| {
			kind: "activity";
			id: string;
			steps: ActivityStep[];
			live: boolean;
	  }
	| { kind: "divider"; id: string; data: TurnDividerData };

function sameFields(first: object, second: object): boolean {
	const keys = Object.keys(first);
	return (
		keys.length === Object.keys(second).length &&
		keys.every(
			(key) => Object.hasOwn(second, key) && Reflect.get(first, key) === Reflect.get(second, key),
		)
	);
}

function sameItems<T>(
	first: readonly T[] | undefined,
	second: readonly T[] | undefined,
	same: (a: T, b: T) => boolean,
): boolean {
	return (
		first === second ||
		(first !== undefined &&
			second !== undefined &&
			first.length === second.length &&
			first.every((item, index) => same(item, second[index] as T)))
	);
}

function sameRow(first: ChatRow, second: ChatRow): boolean {
	if (first.kind !== second.kind || first.id !== second.id) return false;
	if (first.kind === "divider" && second.kind === "divider") {
		return (
			first.data.toolCount === second.data.toolCount &&
			first.data.elapsedMs === second.data.elapsedMs &&
			sameItems(first.data.changedFiles, second.data.changedFiles, Object.is)
		);
	}
	if (first.kind === "activity" && second.kind === "activity") {
		return first.live === second.live && sameItems(first.steps, second.steps, sameFields);
	}
	if (first.kind === "image" && second.kind === "image")
		return sameFields(first.image, second.image);
	if (first.kind === "user" && second.kind === "user") {
		return (
			first.message === second.message &&
			sameItems(first.imageAttachmentNames, second.imageAttachmentNames, Object.is)
		);
	}
	return sameFields(first, second);
}

// The runtime is immutable, but projecting it creates fresh display objects.
// Reuse unchanged rows so paging and streaming do not invalidate every mounted
// component. The cache contains only the current chat's current rows.
export function createRowDeriver(): typeof deriveRows {
	let previous = new Map<string, ChatRow>();
	return (turns, tools, streaming) => {
		const next = new Map<string, ChatRow>();
		const result = deriveRows(turns, tools, streaming).map((row) => {
			const old = previous.get(row.id);
			const stable = old && sameRow(old, row) ? old : row;
			next.set(row.id, stable);
			return stable;
		});
		previous = next;
		return result;
	};
}

export function deriveRows(
	turns: ChatTurn[],
	toolResults: Record<string, ToolResultState>,
	isStreaming: boolean,
): ChatRow[] {
	const rows: ChatRow[] = [];
	let run: ActivityStep[] = [];

	const flushRun = (live = false) => {
		const first = run[0];
		if (!first) return;
		rows.push({ kind: "activity", id: `activity:${first.id}`, steps: run, live });
		run = [];
	};

	for (let i = 0; i < turns.length; i++) {
		const turn = turns[i];
		if (!turn) continue;
		if (turn.kind === "assistant") {
			const { message } = turn;
			const dead = message.stopReason === "aborted" || message.stopReason === "error";
			for (let b = 0; b < message.content.length; b++) {
				const block = message.content[b];
				if (!block) continue;
				if (block.type === "thinking") {
					if (block.thinking.trim().length === 0) continue;
					run.push({
						kind: "thinking",
						id: `${turn.id}:thinking:${b}`,
						text: block.thinking,
						streaming: turn.streaming,
					});
				} else if (block.type === "text") {
					if (block.text.trim().length === 0) continue;
					flushRun();
					rows.push({ kind: "markdown", id: `${turn.id}:text:${b}`, text: block.text });
				} else if (block.type === "image") {
					flushRun();
					rows.push({ kind: "image", id: `${turn.id}:image:${b}`, image: block });
				} else if (block.type === "toolCall") {
					const toolName = block.toolName ?? block.name;
					const rowId = `${turn.id}:tool:${b}`;
					const hasReplayResult = Object.hasOwn(turn.toolResultsByBlock ?? {}, b);
					const data: ToolCallData = {
						toolCallId: block.id,
						toolName,
						...(block.title ? { title: block.title } : {}),
						args: (typeof block.arguments === "object" && block.arguments !== null
							? block.arguments
							: {}) as Record<string, unknown>,
						tool: hasReplayResult
							? (turn.toolResultsByBlock?.[b] ?? undefined)
							: toolResults[block.id],
						dead,
						streaming: turn.streaming,
					};
					if (resolveProminence(toolName).prominence === "primary") {
						flushRun();
						rows.push({ kind: "tool", id: rowId, ...data });
					} else {
						run.push({ kind: "tool", id: rowId, ...data });
					}
				}
			}
		} else {
			flushRun();
			switch (turn.kind) {
				case "user":
					rows.push({
						kind: "user",
						id: turn.id,
						message: turn.message,
						...(turn.imageAttachmentNames
							? { imageAttachmentNames: turn.imageAttachmentNames }
							: {}),
					});
					break;
				case "system":
					rows.push({ kind: "system", id: turn.id, text: turn.text });
					break;
				case "error":
					rows.push({ kind: "error", id: turn.id, text: turn.text });
					break;
				case "compaction":
					rows.push(turn);
					break;
				case "retry":
					rows.push({
						kind: "retry",
						id: turn.id,
						source: turn.source,
						attempt: turn.attempt,
						maxAttempts: turn.maxAttempts,
						delayMs: turn.delayMs,
					});
					break;
			}
		}
		const roundEnded =
			turn.kind !== "user" &&
			(turns[i + 1]?.kind === "user" || (i === turns.length - 1 && !isStreaming));
		if (roundEnded) {
			flushRun();
			const data = turnDivider(turns, i);
			if (data) rows.push({ kind: "divider", id: `${turn.id}:divider`, data });
		}
	}
	flushRun(isStreaming);
	return rows;
}

export interface TurnDividerData {
	elapsedMs: number | null;
	toolCount: number;
	changedFiles: string[];
}

const FILE_WRITER_TOOLS = new Set(["write", "edit"]);

export function turnDivider(turns: ChatTurn[], endIndex: number): TurnDividerData | null {
	let userIdx = -1;
	for (let i = endIndex; i >= 0; i--) {
		if (turns[i]?.kind === "user") {
			userIdx = i;
			break;
		}
	}
	if (userIdx < 0) return null;

	let toolCount = 0;
	const written = new Set<string>();
	let endMs: number | null = null;
	for (let i = userIdx + 1; i <= endIndex; i++) {
		const turn = turns[i];
		if (turn?.kind === "assistant") {
			if (turn.message.timestamp) endMs = turn.message.timestamp;
			for (const block of turn.message.content) {
				if (block.type !== "toolCall") continue;
				toolCount++;
				if (!FILE_WRITER_TOOLS.has(block.name)) continue;
				const path = strArg(
					(typeof block.arguments === "object" && block.arguments !== null
						? block.arguments
						: {}) as Record<string, unknown>,
					"path",
				);
				if (!path) continue;
				written.add(path);
			}
		} else if (turn?.kind === "system" && turn.endedAt != null) {
			endMs = turn.endedAt;
		}
	}

	const user = turns[userIdx];
	const startMs = user?.kind === "user" ? user.message.timestamp : null;
	const elapsedMs = startMs != null && endMs != null ? endMs - startMs : null;

	return { elapsedMs, toolCount, changedFiles: [...written] };
}

export function rowIndexForTurn(rows: ChatRow[], turnId: string): number {
	return rows.findIndex((r) => r.id === turnId || r.id.startsWith(`${turnId}:text:`));
}
