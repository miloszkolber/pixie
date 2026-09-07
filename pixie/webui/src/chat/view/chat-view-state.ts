import type { AgentMentionInfo } from "@pixie/contracts";
import type { MentionCandidate, SubmitBehavior } from "../composer/composer-state";
import { type ChatRow, rowIndexForTurn } from "../runtime/rows";
import type { ChatTurn } from "../runtime/types";

export function turnAnchorText(turn: ChatTurn): string {
	if (turn.kind === "user") {
		const { content } = turn.message;
		return typeof content === "string"
			? content
			: content
					.filter((block) => block.type === "text")
					.map((block) => block.text)
					.join("\n");
	}
	if (turn.kind === "assistant") {
		return turn.message.content
			.filter((block) => block.type === "text")
			.map((block) => block.text)
			.join("\n");
	}
	return "";
}

export function uniqueRecentPrompts(turns: ChatTurn[]): string[] {
	const texts = turns
		.filter((turn) => turn.kind === "user")
		.map(turnAnchorText)
		.filter(Boolean)
		.reverse();
	return [...new Set(texts)];
}

export function projectAreaNameMap(
	projectAreas: Record<string, readonly { id: string; name: string }[]>,
): Record<string, string> {
	const names: Record<string, string> = {};
	for (const areas of Object.values(projectAreas)) {
		for (const area of areas) names[area.id] = area.name;
	}
	return names;
}

export function mentionCandidatesForQuery(
	query: string | null,
	agentMentions: readonly AgentMentionInfo[],
	fileMentions: readonly MentionCandidate[],
): MentionCandidate[] {
	if (query === null) return [];
	if (query.includes("/")) return fileMentions.slice(0, 12);
	const normalized = query.toLocaleLowerCase();
	const agents: MentionCandidate[] = agentMentions
		.filter(({ name, mention }) =>
			[name, mention.startsWith("@") ? mention.slice(1) : mention].some((value) =>
				value.toLocaleLowerCase().startsWith(normalized),
			),
		)
		.map(({ name, description, sourceType, mention }) => ({
			name,
			description,
			sourceType,
			mention,
			kind: "agent",
		}));
	return [...agents, ...fileMentions].slice(0, 12);
}

export interface SendResolution {
	effectiveBehavior: Exclude<SubmitBehavior, "interrupt">;
	heldByQueue: boolean;
}

export function resolveSendBehavior(
	behavior: Exclude<SubmitBehavior, "interrupt">,
	canSteer: boolean,
	hasQueuedFollowUps: boolean,
): SendResolution {
	const supportedBehavior = behavior === "steer" && !canSteer ? "queue" : behavior;
	const heldByQueue = supportedBehavior === "send" && hasQueuedFollowUps;
	return {
		effectiveBehavior: heldByQueue ? "queue" : supportedBehavior,
		heldByQueue,
	};
}

export function locateChatRow(
	messageIndex: number,
	anchorText: string,
	turnIdByMessageIndex: Record<number, string | null>,
	turns: ChatTurn[],
	rows: ChatRow[],
): ChatRow | null {
	const prefix = anchorText.slice(0, 40);
	const mappedId = turnIdByMessageIndex[messageIndex];
	const mapped = mappedId ? turns.find((turn) => turn.id === mappedId) : undefined;
	const target =
		mapped && turnAnchorText(mapped).includes(prefix)
			? mapped
			: turns.findLast((turn) => turnAnchorText(turn).includes(prefix));
	if (!target) return null;
	const index = rowIndexForTurn(rows, target.id);
	return index < 0 ? null : (rows[index] ?? null);
}
