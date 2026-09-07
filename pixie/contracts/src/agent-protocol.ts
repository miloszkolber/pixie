/** Browser-safe data projected by the Go controller. Pi stays controller-side. */
export type ThinkingLevel = "off" | "minimal" | "low" | "medium" | "high" | "xhigh" | string;

export interface ImageContent {
	type: "image";
	data: string;
	mimeType: string;
}

export interface TextContent {
	type: "text";
	text: string;
}
/** Metadata-only projection of a browser text attachment. Resource text never reaches the Web UI. */
export interface TextResourceAttachmentMarker {
	type: "resource";
	name: string;
	mimeType: string;
}
export interface ThinkingContent {
	type: "thinking";
	thinking: string;
}
export interface ToolCall {
	type: "toolCall";
	id: string;
	/** Exact upstream tool identity, when Pi provides one. */
	toolName?: string;
	/** Legacy display/renderer name. Prefer toolName when available. */
	name: string;
	arguments: unknown;
	title?: string;
	kind?: string;
	locations?: unknown;
}

/** Trusted MCP Apps metadata projected from Pi for one completed tool call. */
export interface McpAppAttachment {
	toolName: string;
	extensionName: string;
	resourceUri: string;
}

export interface McpAppCsp {
	connectDomains?: string[];
	resourceDomains?: string[];
	frameDomains?: string[];
	baseUriDomains?: string[];
}

export interface McpAppPermissions {
	camera?: Record<string, never>;
	microphone?: Record<string, never>;
	geolocation?: Record<string, never>;
	clipboardWrite?: Record<string, never>;
}

export interface McpAppResourceContent {
	uri: string;
	mimeType?: string;
	text?: string;
	blob?: string;
	_meta?: Record<string, unknown>;
}

export interface McpAppResourceResult {
	contents: McpAppResourceContent[];
	_meta?: Record<string, unknown>;
}

export interface McpAppOpenResult {
	viewId: string;
	url: string;
	resource: {
		byteLength: number;
		csp?: McpAppCsp;
		permissions?: McpAppPermissions;
	};
}

export interface McpAppContentChunk {
	offset: number;
	data: string;
	nextOffset: number;
}

export interface McpAppToolResult {
	content: unknown[];
	structuredContent?: Record<string, unknown>;
	isError?: boolean;
	_meta?: Record<string, unknown>;
}

/** Best-effort child tool requests reported by Pi for an outer Summon call. */
export interface SubagentActivityEvent {
	childSessionId: string;
	toolName: string;
}

export interface SubagentActivity {
	events: readonly SubagentActivityEvent[];
	truncated?: boolean;
}
export type StopReason = string;

export interface UserMessage {
	messageId?: string;
	role: "user";
	content: string | (TextContent | ImageContent | TextResourceAttachmentMarker)[];
	timestamp?: number;
}

export interface AssistantMessage {
	messageId?: string;
	role: "assistant";
	content: (TextContent | ImageContent | ThinkingContent | ToolCall)[];
	thinking?: string;
	stopReason?: string;
	errorMessage?: string;
	timestamp?: number;
}

export interface ToolResultMessage {
	role: "toolResult";
	toolCallId: string;
	isError?: boolean;
	content?: unknown;
	details?: unknown;
	app?: McpAppAttachment;
	subagentActivity?: SubagentActivity;
}

export interface PendingToolPreview {
	toolCallId: string;
	output?: unknown;
	app?: McpAppAttachment;
	subagentActivity?: SubagentActivity;
}

export type TranscriptMessage = UserMessage | AssistantMessage | ToolResultMessage;

export interface WireModelCostRates {
	input: number;
	output: number;
	cacheRead?: number;
	cacheWrite?: number;
}
export interface WireModelCost extends WireModelCostRates {
	/** Currency symbol reported by Pi for these per-million-token rates. */
	currency: string;
	tiers?: (WireModelCostRates & { inputTokensAbove: number })[];
}
export type WireModelCostTier = WireModelCostRates & { inputTokensAbove: number };

export interface WireModel {
	/** Whether the optional metadata lookup completed, including an authoritative no-match. */
	metadataComplete?: boolean;
	id: string;
	name: string;
	provider: string;
	contextWindow?: number;
	maxTokens?: number;
	reasoning?: boolean;
	thinkingLevels?: ThinkingLevel[];
	input?: ("text" | "image")[];
	cost?: WireModelCost;
	available: boolean;
	hidden: boolean;
}

export interface RefreshedModels {
	models: WireModel[];
	complete: boolean;
}
export interface AgentSettlement {
	stopReason: string;
	errorMessage?: string;
}
export interface ContextUsage {
	tokens: number | null;
	contextWindow: number;
	percent: number | null;
}
export interface SessionStats {
	sessionId: string;
	totalMessages: number;
	tokens: { input: number; output: number; cacheRead: number; cacheWrite: number; total: number };
	cost: number;
	costCurrency?: string;
	reported?: Partial<
		Record<"input" | "output" | "cacheRead" | "cacheWrite" | "total" | "cost", boolean>
	>;
	contextUsage?: ContextUsage;
}

export interface SessionModeState {
	currentModeId: string;
	availableModes: { id: string; name: string; description?: string }[];
}

export interface SessionPlanState {
	entries: {
		content: string;
		priority: "high" | "medium" | "low";
		status: "pending" | "in_progress" | "completed";
	}[];
	truncated?: boolean;
}

export interface SessionConfigOption {
	id: string;
	name?: string;
	type?: string;
	category?: string;
	currentValue?: string | boolean;
	options?: readonly { value: string; name: string }[];
}

export interface SessionSummary {
	capabilities?: Record<string, number>;
	configOptions?: SessionConfigOption[];
	sessionId: string;
	projectId: string;
	cwd: string;
	/** The recorded Pi session from which this chat was forked, when applicable. */
	parentSessionId?: string;
	title: string;
	model: WireModel | null;
	thinkingLevel: ThinkingLevel;
	isStreaming: boolean;
	messageCount: number;
	updatedAt: number;
	live: boolean;
	archived: boolean;
	lastSettlement?: AgentSettlement | null;
	queue?: SessionQueueState;
}

export const SESSION_TITLE_MAX_LENGTH = 200;

export function normalizeSessionTitle(value: unknown): string {
	if (typeof value !== "string") throw new Error("Session title must be text");
	const title = value.trim();
	if (!title) throw new Error("Session title cannot be empty");
	if (title.includes("\0")) throw new Error("Session title contains an invalid character");
	if (title.length > SESSION_TITLE_MAX_LENGTH) {
		throw new Error(`Session title must be ${SESSION_TITLE_MAX_LENGTH} characters or fewer`);
	}
	return title;
}

export interface SessionLifecycleChangedPayload {
	projectId: string;
	sessionId: string;
	operation: "created" | "renamed" | "archived" | "unarchived" | "forked";
	title?: string;
}

export type AgentMessage = UserMessage | AssistantMessage | ToolResultMessage;
export type AgentEvent =
	| {
			type:
				| "activity"
				| "run-start"
				| "text"
				| "image"
				| "thinking"
				| "tool-start"
				| "tool-update"
				| "tool-end"
				| "usage"
				| "context"
				| "config"
				| "complete"
				| "error"
				| "session-info";
			messageId?: string | null;
			text?: string;
			image?: ImageContent;
			toolCallId?: string;
			toolName?: string;
			status?: string;
			usage?: Partial<SessionStats["tokens"]> & { cost?: number };
			reported?: SessionStats["reported"];
			costCurrency?: string;
			contextUsage?: ContextUsage;
			configOptions?: readonly SessionConfigOption[];
			model?: WireModel | null;
			title?: string;
			error?: string;
			tool?: unknown;
			toolCall?: ToolCall;
			app?: McpAppAttachment;
			subagentActivity?: SubagentActivity;
	  }
	| { type: "agent_start" }
	| { type: "commands"; commands: SlashCommandInfo[] }
	| { type: "current-mode"; currentModeId: string }
	| { type: "plan"; planState: SessionPlanState }
	| ({ type: "queue_update" } & SessionQueueState)
	| { type: "message_start"; message: AgentMessage }
	| {
			type: "message_update";
			assistantMessageEvent:
				| { type: "done"; message: AssistantMessage }
				| { type: "error"; error: AssistantMessage }
				| { type: string; partial: AssistantMessage };
	  }
	| { type: "message_end"; message: AgentMessage }
	| { type: "tool_execution_start"; toolCallId: string }
	| { type: "tool_execution_update"; toolCallId: string; partialResult: unknown }
	| { type: "tool_execution_end"; toolCallId: string; isError: boolean; result: unknown }
	| { type: "agent_end"; messages: AgentMessage[]; willRetry: boolean }
	| { type: "agent_settled"; terminal: AgentSettlement | null }
	| { type: "compaction_start"; reason: "manual" | "threshold" | "overflow" }
	| {
			type: "compaction_end";
			reason: "manual" | "threshold" | "overflow";
			result?: { tokensBefore: number; estimatedTokensAfter?: number };
			aborted: boolean;
			willRetry: boolean;
			errorMessage?: string;
	  }
	| { type: "auto_retry_start"; attempt: number; maxAttempts: number; delayMs: number }
	| { type: "auto_retry_end"; success: boolean; attempt: number; finalError?: string }
	| { type: "summarization_retry_scheduled"; attempt: number; maxAttempts: number; delayMs: number }
	| { type: "summarization_retry_finished" }
	| { type: "thinking_level_changed"; level: ThinkingLevel };
export interface SessionEventPayload {
	sessionId: string;
	event: AgentEvent;
}

export interface SlashCommandInfo {
	name: string;
	description?: string;
	inputHint?: string;
	source: "agent" | "pi" | "extension" | "prompt" | "skill";
	sourceInfo: {
		path: string;
		source: string;
		scope: "user" | "project" | "temporary";
		origin: "package" | "top-level";
		baseDir?: string;
	};
}

/** Controller-owned queue state. Pi has no queue-manipulation API. */
export type QueueLane = "steering" | "followUp";
export interface SessionQueueState {
	/** Opaque controller revision required for conditional edit/removal. */
	revision?: string;
	steering: readonly string[];
	followUp: readonly string[];
	blocked?: { lane: QueueLane; index: number; reason: "delivery-uncertain" };
}

export interface AskUserQuestionResult {
	answers: AskUserQuestionAnswer[];
	cancelled: boolean;
}
export interface AskUserQuestionOption {
	label: string;
	description: string;
	preview?: string;
	recommendedReason?: string;
}
export interface AskUserQuestionItem {
	question: string;
	header: string;
	options: AskUserQuestionOption[];
	multiSelect?: boolean;
}
export interface AskUserQuestionArgs {
	questions: AskUserQuestionItem[];
}
export interface PendingUserQuestion extends AskUserQuestionArgs {
	id: string;
	sessionId: string;
}
export interface AskUserQuestionAnswer {
	questionIndex: number;
	question: string;
	kind: "option" | "custom" | "multi";
	answer: string | null;
	selected?: string[];
	notes?: string;
	preview?: string;
}
export function isTranscriptMessageRole(role: string): role is TranscriptMessage["role"] {
	return role === "user" || role === "assistant" || role === "toolResult";
}
