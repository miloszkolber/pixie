<script lang="ts">
import { untrack } from "svelte";
import Icon from "../../components/icon.svelte";
import { projectRelativePath } from "../../lib";
import { useFoldState } from "../runtime/fold-state";
import type { TurnDividerData } from "../runtime/rows";
import { formatElapsed } from "./turns";

const { readSelection, selectValue } = useFoldState();

interface Props {
	id: string;
	data: TurnDividerData;
	projectAreaRoot?: string | undefined;
	onOpenChange: (path: string) => void;
}
let { id, data, projectAreaRoot, onOpenChange }: Props = $props();
let selected = $state(untrack(() => readSelection(`${id}:artifacts`)));
let listId = $derived(`${id}-files-list`);
let many = $derived(data.changedFiles.length > 1);
let expanded = $derived(many && selected === "files");
let showMetadata = $derived(
	data.toolCount > 0 ||
		data.changedFiles.length > 0 ||
		(data.elapsedMs != null && data.elapsedMs >= 1000),
);

function activateFiles(): void {
	if (!many) {
		const first = data.changedFiles[0];
		if (first) onOpenChange(first);
		return;
	}
	selected = selectValue(`${id}:artifacts`, selected, "files");
}
</script>

{#if !showMetadata}
	<div data-testid="turn-divider" class="turn-divider-rule"></div>
{:else}
	<div data-testid="turn-divider" class="u-flex u-flex-col u-gap-xs turn-divider u-text-text-muted tr-text-metadata">
		<div class="u-flex u-items-center u-gap-sm">
			<span class="u-flex-1 turn-divider-rule-inline"></span>
			{#if data.toolCount > 0}
				<span class="u-flex u-items-center u-gap-xs"><Icon name="wrench" size={12} />{data.toolCount} {data.toolCount === 1 ? "tool call" : "tool calls"}</span>
			{/if}
			{#if data.changedFiles.length > 0}
				<button
					type="button"
					data-testid="turn-divider-files"
					data-expanded={expanded || undefined}
					aria-expanded={many ? expanded : undefined}
					aria-controls={expanded ? listId : undefined}
					onclick={activateFiles}
					class={`u-flex u-items-center u-gap-xs u-rounded u-px-xs turn-divider-files ${expanded ? "turn-divider-files-expanded" : ""}`}
				>
					<Icon name="file-diff" size={12} />
					{data.changedFiles.length} {data.changedFiles.length === 1 ? "file changed" : "files changed"}
					{#if many}<Icon name={expanded ? "chevron-down" : "chevron-right"} size={12} />{/if}
				</button>
			{/if}
			{#if data.elapsedMs != null && data.elapsedMs >= 1000}
				<span class="u-flex u-items-center u-gap-xs"><Icon name="clock" size={12} />{formatElapsed(data.elapsedMs)}</span>
			{/if}
			<span class="u-flex-1 turn-divider-rule-inline"></span>
		</div>
		{#if expanded}
			<ul id={listId} data-testid="turn-divider-files-list" class="u-flex u-flex-col">
				{#each data.changedFiles as path (path)}
					<li>
						<button
							type="button"
							data-testid="turn-divider-files-list-item"
							onclick={() => onOpenChange(path)}
							title={path}
							class="u-flex u-w-full u-items-center u-gap-xs u-rounded u-px-xs turn-divider-file"
						><Icon name="file-diff" size={12} class="u-shrink-0 u-text-text-muted" /><span class="u-min-w-0 u-flex-1 u-truncate u-text-text-muted">{projectRelativePath(path, projectAreaRoot)}</span></button>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
{/if}

<style>
	.turn-divider-rule { height: 1px; margin-block: var(--space-sm); background: var(--border-muted); }
	.turn-divider { margin-block: var(--space-sm); }
	.turn-divider-rule-inline { height: 1px; background: var(--border-muted); }
	.turn-divider-files { border: 0; background: transparent; color: var(--primary); }
	.turn-divider-files:hover { background: var(--control-bg-hovered); }
	.turn-divider-files-expanded { background: var(--control-bg-selected); }
	.turn-divider-file { border: 0; background: transparent; padding-block: var(--space-2xs); text-align: start; }
	.turn-divider-file:hover { background: var(--control-bg-hovered); }
</style>
