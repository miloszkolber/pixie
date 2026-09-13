import type { ChatTurn } from "../runtime/types";

export type StreamPhase = "working" | "thinking" | "running-tool" | "writing" | "compacting";

export interface StreamStatus {
	phase: StreamPhase;
	toolName?: string;
}

export function streamStatus(turns: ChatTurn[], currentAssistantId: string | null): StreamStatus {
	const lastTurn = turns.at(-1);
	if (lastTurn?.kind === "compaction" && lastTurn.status === "running")
		return { phase: "compacting" };
	const active =
		turns.find(
			(turn): turn is Extract<ChatTurn, { kind: "assistant" }> =>
				turn.kind === "assistant" && turn.id === currentAssistantId,
		) ?? (currentAssistantId === null && lastTurn?.kind === "assistant" ? lastTurn : undefined);
	const last = active?.message.content.at(-1);
	if (!last) return { phase: "working" };
	if (last.type === "toolCall") return { phase: "running-tool", toolName: last.name };
	if (last.type === "text") return last.text.trim() ? { phase: "writing" } : { phase: "working" };
	if (last.type === "thinking")
		return last.thinking.trim() ? { phase: "thinking" } : { phase: "working" };
	return { phase: "working" };
}

export function phaseLabel({ phase, toolName }: StreamStatus): string {
	switch (phase) {
		case "thinking":
			return "Thinking…";
		case "writing":
			return "Writing…";
		case "running-tool":
			return toolName ? `Running ${toolName}…` : "Running tool…";
		case "compacting":
			return "Compacting context…";
		default:
			return "Working…";
	}
}
