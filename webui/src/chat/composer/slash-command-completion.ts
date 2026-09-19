import type { SlashCommandInfo } from "@pixie/shared";
import { completionNavigationIndex } from "./composer-state";

const MAX_MATCHES = 8;

export function slashCommandQuery(value: string): string | null {
	return value.startsWith("/") && !/\s/.test(value) ? value.slice(1) : null;
}

export function matchSlashCommands(
	value: string,
	commands: readonly SlashCommandInfo[],
): SlashCommandInfo[] {
	const query = slashCommandQuery(value);
	if (query === null) return [];
	const normalized = query.toLowerCase();
	return commands
		.filter((command) => command.name.toLowerCase().includes(normalized))
		.slice(0, MAX_MATCHES);
}

export function selectedSlashCommandValue(command: SlashCommandInfo): string {
	return `/${command.name} `;
}

export function clampedSlashActiveIndex(activeIndex: number, matchCount: number): number {
	return matchCount > 0 ? Math.min(Math.max(activeIndex, 0), matchCount - 1) : 0;
}

export type SlashCompletionKeyAction =
	| { type: "none" }
	| { type: "move"; index: number }
	| { type: "select"; index: number }
	| { type: "dismiss" };

export function slashCompletionKeyAction(
	key: string,
	open: boolean,
	activeIndex: number,
	matchCount: number,
): SlashCompletionKeyAction {
	if (!open || matchCount === 0) return { type: "none" };
	const visibleIndex = clampedSlashActiveIndex(activeIndex, matchCount);
	const navigation = completionNavigationIndex(key, visibleIndex, matchCount);
	if (navigation !== null) return { type: "move", index: navigation };
	if (key === "Enter" || key === "Tab") return { type: "select", index: visibleIndex };
	if (key === "Escape") return { type: "dismiss" };
	return { type: "none" };
}

export function slashCommandResetSignal(
	value: string,
	commands: readonly SlashCommandInfo[],
): string {
	return JSON.stringify([slashCommandQuery(value), commands.map((command) => command.name)]);
}
