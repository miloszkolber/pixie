export type ChildStatus = "starting" | "running" | "completed" | "failed" | "cancelled";

export interface ChildModel {
	provider?: string;
	id?: string;
}

export interface ChildUsage {
	input?: number;
	output?: number;
	cacheRead?: number;
	cacheWrite?: number;
	cost?: number;
}

export interface ChildResult {
	runId?: string;
	callIndex?: number;
	agent?: string;
	agentSource?: string;
	task?: string;
	status?: ChildStatus | undefined;
	model?: ChildModel | null;
	thinkingLevel?: string;
	currentTool?: string;
	finalOutput?: string;
	outputState?: "present" | "absent";
	truncated?: boolean;
	error?: string;
	stopReason?: string;
	exitCode?: number;
	sessionHandle?: string;
	sessionId?: string;
	usage?: ChildUsage;
}

export interface SubagentDetails {
	kind?: "pi-subagent";
	mode?: "single";
	runId?: string;
	parentSessionId?: string;
	childSessionId?: string;
	projectAgentsDir?: string | null;
	failed?: boolean;
	status?: ChildStatus | undefined;
	results?: ChildResult[];
}

function recordOf(value: unknown): Record<string, unknown> | undefined {
	return typeof value === "object" && value !== null && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: undefined;
}

function statusOf(value: unknown): ChildStatus | undefined {
	return value === "starting" ||
		value === "running" ||
		value === "completed" ||
		value === "failed" ||
		value === "cancelled"
		? value
		: undefined;
}

// Upstream `pi-subagent` results carry no status field: derive one from the
// process outcome so parallel and finished calls render like `delegate`
// children. Delegate results already carry status and never carry exitCode,
// so this derivation never alters them.
function statusFromExit(result: {
	exitCode?: unknown;
	stopReason?: unknown;
	errorMessage?: unknown;
}): ChildStatus | undefined {
	const stopReason = typeof result.stopReason === "string" ? result.stopReason : undefined;
	if (stopReason === "aborted") return "cancelled";
	if (typeof result.exitCode !== "number") return undefined;
	if (result.exitCode === -1) return "running";
	if (result.errorMessage !== undefined || result.exitCode !== 0) return "failed";
	return "completed";
}

function assistantTexts(messages: unknown): string[] {
	if (!Array.isArray(messages)) return [];
	const texts: string[] = [];
	for (const message of messages) {
		const record = recordOf(message);
		if (record?.role !== "assistant") continue;
		const content = record.content;
		if (typeof content === "string") {
			texts.push(content);
			continue;
		}
		if (!Array.isArray(content)) continue;
		for (const block of content) {
			const item = recordOf(block);
			if (item?.type === "text" && typeof item.text === "string") texts.push(item.text);
		}
	}
	return texts;
}

function lastToolName(messages: unknown): string | undefined {
	if (!Array.isArray(messages)) return undefined;
	let current: string | undefined;
	for (const message of messages) {
		const content = recordOf(message)?.content;
		if (!Array.isArray(content)) continue;
		for (const block of content) {
			const item = recordOf(block);
			if (
				(item?.type === "toolCall" || item?.type === "tool_call") &&
				typeof item.name === "string"
			) {
				current = item.name;
			}
		}
	}
	return current;
}

function modelOf(value: unknown): ChildModel | null | undefined {
	if (value === null) return null;
	if (typeof value === "string") return value ? { id: value } : undefined;
	const model = recordOf(value);
	if (!model) return undefined;
	return {
		...(typeof model.provider === "string" ? { provider: model.provider } : {}),
		...(typeof model.id === "string" ? { id: model.id } : {}),
	};
}

function usageOf(value: unknown): ChildUsage | undefined {
	const record = recordOf(value);
	if (!record) return undefined;
	const usage: ChildUsage = {};
	for (const key of ["input", "output", "cacheRead", "cacheWrite", "cost"] as const) {
		const amount = record[key];
		if (typeof amount === "number") usage[key] = amount;
	}
	return Object.keys(usage).length > 0 ? usage : undefined;
}

function childOf(value: unknown): ChildResult | undefined {
	const record = recordOf(value);
	if (!record) return undefined;
	const model = modelOf(record.model);
	const status = statusOf(record.status) ?? statusFromExit(record);
	const session = recordOf(record.session);
	const prompt = typeof record.prompt === "string" ? record.prompt : undefined;
	const task = typeof record.task === "string" ? record.task : (prompt ?? undefined);
	const error =
		typeof record.error === "string"
			? record.error
			: typeof record.errorMessage === "string"
				? record.errorMessage
				: undefined;
	const texts = assistantTexts(record.messages);
	const finalOutput =
		typeof record.finalOutput === "string"
			? record.finalOutput
			: texts.join("\n").trim() || undefined;
	const currentTool =
		typeof record.currentTool === "string" ? record.currentTool : lastToolName(record.messages);
	const usage = usageOf(record.usage);
	return {
		...(typeof record.runId === "string" ? { runId: record.runId } : {}),
		...(typeof record.callIndex === "number" ? { callIndex: record.callIndex } : {}),
		...(typeof record.agent === "string" ? { agent: record.agent } : {}),
		...(typeof record.agentSource === "string" ? { agentSource: record.agentSource } : {}),
		...(task ? { task } : {}),
		...(status ? { status } : {}),
		...(model !== undefined ? { model } : {}),
		...(typeof record.thinkingLevel === "string" ? { thinkingLevel: record.thinkingLevel } : {}),
		...(currentTool !== undefined ? { currentTool } : {}),
		...(finalOutput ? { finalOutput } : {}),
		...(record.outputState === "present" || record.outputState === "absent"
			? { outputState: record.outputState }
			: {}),
		...(record.truncated === true ? { truncated: true } : {}),
		...(error ? { error } : {}),
		...(typeof record.stopReason === "string" ? { stopReason: record.stopReason } : {}),
		...(typeof record.exitCode === "number" ? { exitCode: record.exitCode } : {}),
		...(typeof session?.handle === "string" ? { sessionHandle: session.handle } : {}),
		...(typeof session?.id === "string" ? { sessionId: session.id } : {}),
		...(usage ? { usage } : {}),
	};
}

export function subagentDetails(result: unknown): SubagentDetails {
	const raw = recordOf(recordOf(result)?.details);
	if (!raw) return {};
	const rawResults = Array.isArray(raw.results) ? raw.results : [];
	const status = statusOf(raw.status);
	return {
		...(raw.kind === "pi-subagent" ? { kind: "pi-subagent" as const } : {}),
		...(raw.mode === "single" ? { mode: "single" as const } : {}),
		...(typeof raw.runId === "string" ? { runId: raw.runId } : {}),
		...(typeof raw.parentSessionId === "string" ? { parentSessionId: raw.parentSessionId } : {}),
		...(typeof raw.childSessionId === "string" ? { childSessionId: raw.childSessionId } : {}),
		...("projectAgentsDir" in raw &&
		(raw.projectAgentsDir === null || typeof raw.projectAgentsDir === "string")
			? { projectAgentsDir: raw.projectAgentsDir as string | null }
			: {}),
		...(raw.failed === true ? { failed: true as const } : {}),
		...(status ? { status } : {}),
		...(rawResults.length > 0
			? {
					results: rawResults
						.map(childOf)
						.filter((child): child is ChildResult => child !== undefined),
				}
			: {}),
	};
}

export function childStatus(details: SubagentDetails): ChildStatus | undefined {
	return details.status ?? details.results?.[0]?.status ?? (details.failed ? "failed" : undefined);
}

export function childrenSummary(details: SubagentDetails): string | undefined {
	const results = details.results ?? [];
	if (results.length < 2) return undefined;
	const counts = new Map<ChildStatus, number>();
	for (const result of results) {
		const status = result.status ?? "running";
		counts.set(status, (counts.get(status) ?? 0) + 1);
	}
	return [...counts].map(([status, count]) => `${count} ${status}`).join(" · ");
}

export function childStatusLabel(
	status: ChildStatus | undefined,
	currentTool?: string,
	error?: string,
): string {
	switch (status) {
		case "starting":
			return "Starting child…";
		case "running":
			return currentTool ? `Child running · ${currentTool}` : "Child running";
		case "completed":
			return "Child completed";
		case "failed":
			return error ? `Child failed · ${error}` : "Child failed";
		case "cancelled":
			return "Child cancelled";
		default:
			return "Subagent running…";
	}
}

export function childModelLabel(
	model: ChildModel | null | undefined,
	thinkingLevel?: string,
): string {
	const modelName =
		model?.provider && model.id ? `${model.provider}/${model.id}` : (model?.id ?? undefined);
	return [modelName, thinkingLevel].filter(Boolean).join(" · ");
}

function firstCall(args: Record<string, unknown>): Record<string, unknown> | undefined {
	const calls = args.calls;
	if (!Array.isArray(calls)) return undefined;
	return recordOf(calls[0]);
}

export function subagentSummary(args: Record<string, unknown>): string {
	const task = typeof args.task === "string" ? args.task : "";
	const instructions = typeof args.instructions === "string" ? args.instructions : "";
	const source = typeof args.source === "string" ? args.source : "";
	const call = firstCall(args);
	const callLabel =
		call && typeof call.agent === "string"
			? `subagent · ${call.agent}${typeof call.prompt === "string" && call.prompt ? `: ${call.prompt.slice(0, 80)}` : ""}`
			: "";
	const calls = args.calls;
	const extra =
		Array.isArray(calls) && calls.length > 1 && callLabel ? ` (+${calls.length - 1} more)` : "";
	const bare = task || instructions || source;
	if (bare) return `subagent · ${bare}`;
	return callLabel ? `${callLabel}${extra}` : "subagent";
}
