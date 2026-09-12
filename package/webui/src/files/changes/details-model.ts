import type { ContextUsage, SessionSummary, ThinkingLevel, WireModel } from "@pixie/contracts";
import { formatTokens } from "../../chat/session/session-stats";

/**
 * Read-only session details helpers for the secondary sidebar.
 * The backend remains the authority for runtime release; these helpers only
 * format reported values. Unknown usage stays "Unknown", never zero.
 */

export function formatSessionModel(model: WireModel | null | undefined): string {
	if (!model) return "Unknown model";
	const provider = model.provider?.trim() ?? "";
	const id = model.id?.trim() ?? "";
	if (provider && id) return `${provider}/${id}`;
	if (id) return id;
	if (provider) return provider;
	return "Unknown model";
}

export function formatThinkingLevel(level: ThinkingLevel | null | undefined): string {
	if (typeof level !== "string" || level.trim() === "") return "Unknown thinking level";
	return level;
}

export function describeTokenCount(value: number, reported: boolean | undefined): string {
	const known = reported === true || (reported === undefined && value !== 0);
	if (!known) return "Unknown";
	return formatTokens(value);
}

export function describeContextUsage(usage: ContextUsage | null | undefined): string {
	if (!usage) return "Unknown";
	const window = formatTokens(usage.contextWindow);
	if (usage.percent === null || usage.percent === undefined) return `Unknown of ${window}`;
	return `${usage.percent.toFixed(1)}% of ${window}`;
}

export type SessionStatusText = "Running" | "Archived" | "Live" | "Unloaded";

export function sessionStatusText(
	summary: Pick<SessionSummary, "isStreaming" | "archived" | "live">,
): SessionStatusText {
	if (summary.isStreaming) return "Running";
	if (summary.archived) return "Archived";
	if (summary.live) return "Live";
	return "Unloaded";
}

export function shouldShowReleaseAffordance(input: {
	backendEligible: boolean | undefined;
	isStreaming: boolean;
}): boolean {
	return input.backendEligible === true && !input.isStreaming;
}

export function releaseAffordanceReason(input: {
	backendEligible: boolean | undefined;
	backendReason?: string | null;
	isStreaming: boolean;
}): string | null {
	if (input.isStreaming) return "Session is running";
	if (input.backendEligible === true) return null;
	if (typeof input.backendReason === "string" && input.backendReason.trim() !== "") {
		return input.backendReason;
	}
	return "Session is not idle";
}
