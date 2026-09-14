<script lang="ts">
import type { SlashCommandInfo } from "@pixie/shared";
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
	class={`slash-command-menu ${className}`}
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
			class={`u-flex u-w-full u-items-center u-gap-sm u-rounded u-px-sm u-py-xs u-text-left tr-text-ui slash-command-option ${
				index === activeIndex
					? "slash-command-selected u-text-text-default"
					: "u-text-text-muted"
			}`}
		>
			<span class="u-min-w-0 u-flex-1">
				<span data-testid="slash-command-name" class="slash-command-block slash-command-break tr-code-text u-text-text-default">
					/{command.name}
				</span>
				{#if command.inputHint}
					<span class="slash-command-block u-truncate u-text-text-muted tr-text-metadata">{command.inputHint}</span>
				{/if}
				{#if command.description}
					<span class="slash-command-block u-truncate tr-text-metadata">{command.description}</span>
				{/if}
			</span>
			<span class="slash-command-source u-shrink-0 u-text-text-muted tr-text-metadata">
				{command.source}/{command.sourceInfo.scope}
			</span>
		</button>
	{/each}
	{@render footer?.()}
</div>

<style>
	.slash-command-menu { max-height: 40vh; width: min(28rem, 90%); overflow-y: auto; border: 1px solid var(--border-default); border-radius: var(--radius-md); background: var(--container-elevated-bg); padding: var(--space-xs); box-shadow: var(--shadow-md); }
	.slash-command-option { border: 0; background: transparent; }
	.slash-command-selected { background: var(--control-bg-selected); }
	.slash-command-block { display: block; }
	.slash-command-break { overflow-wrap: anywhere; }
	.slash-command-source { margin-inline-start: auto; }
</style>
