<script lang="ts">
import type { SlashCommandInfo } from "@pixie/contracts";
import type { Snippet } from "svelte";
import { slashCommandKey } from "./composer-state";

interface Props {
	commands: readonly SlashCommandInfo[];
	activeIndex: number;
	onSelect: (command: SlashCommandInfo) => void;
	class?: string;
	footer?: Snippet;
	listboxId?: string;
}

let {
	commands,
	activeIndex,
	onSelect,
	class: className = "",
	footer,
	listboxId = "slash-command-menu",
}: Props = $props();
</script>

<div
	id={listboxId}
	role="listbox"
	data-testid="slash-menu"
	class={`max-h-[40vh] w-[min(28rem,90%)] overflow-y-auto rounded-[var(--radius-md)] border border-border-default bg-container-elevated-bg p-xs shadow-[var(--shadow-md)] ${className}`}
>
	{#each commands as command, index (slashCommandKey(command))}
		<button
			id={`${listboxId}-option-${index}`}
			role="option"
			aria-selected={index === activeIndex}
			type="button"
			data-testid="slash-command"
			data-source={command.source}
			onclick={() => onSelect(command)}
			class={`flex w-full items-center gap-sm rounded-[var(--radius-sm)] px-sm py-xs text-left tr-text-ui ${
				index === activeIndex
					? "bg-control-bg-selected text-text-default"
					: "text-text-muted"
			}`}
		>
			<span class="min-w-0 flex-1">
				<span data-testid="slash-command-name" class="block break-all tr-code-text text-text-default">
					/{command.name}
				</span>
				{#if command.inputHint}
					<span class="block truncate text-text-muted tr-text-metadata">{command.inputHint}</span>
				{/if}
				{#if command.description}
					<span class="block truncate tr-text-metadata">{command.description}</span>
				{/if}
			</span>
			<span class="ml-auto shrink-0 text-text-muted tr-text-metadata">
				{command.source}/{command.sourceInfo.scope}
			</span>
		</button>
	{/each}
	{@render footer?.()}
</div>
