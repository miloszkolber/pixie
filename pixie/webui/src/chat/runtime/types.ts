import type {
	AssistantMessage,
	ImageContent,
	McpAppAttachment,
	SubagentActivity,
	TextResourceAttachment,
	UserMessage,
} from "@pixie/contracts";

export type ChatAttachment =
	| { kind: "image"; name: string; content: ImageContent }
	| { kind: "text"; name: string; content: TextResourceAttachment };

export type ChatTurn =
	| {
			kind: "user";
			id: string;
			message: UserMessage;
			imageAttachmentNames?: string[];
			optimistic?: { transcriptTotal: number | null };
	  }
	| {
			kind: "assistant";
			id: string;
			message: AssistantMessage;
			streaming: boolean;
			toolResultsByBlock?: Record<number, ToolResultState | null>;
	  }
	| { kind: "system"; id: string; text: string; endedAt?: number }
	| ({ kind: "compaction"; id: string } & CompactionState)
	| { kind: "error"; id: string; text: string }
	| {
			kind: "retry";
			id: string;
			source: "turn" | "summarization";
			attempt: number;
			maxAttempts: number;
			delayMs: number;
	  };

export interface CompactionState {
	status: "running" | "done" | "failed" | "cancelled";
	detail?: string;
	summary?: string;
	tokensBefore?: number;
	tokensAfter?: number;
	resuming?: boolean;
}

export type ToolStatus = "running" | "done" | "error" | "interrupted";

export interface ToolResultState {
	status: ToolStatus;
	raw: unknown;
	app?: McpAppAttachment;
	subagentActivity?: SubagentActivity;
}

export interface ChatSubmission {
	text: string;
	attachments: ChatAttachment[];
	behavior: "send" | "steer" | "queue" | "interrupt";
	busy: boolean;
	error?: string;
	optimisticTurnId?: string;
}
