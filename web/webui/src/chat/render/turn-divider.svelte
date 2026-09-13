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
	<div data-testid="turn-divider" class="my-sm h-px bg-border-muted"></div>
{:else}
	<div data-testid="turn-divider" class="my-sm flex flex-col gap-xs text-text-muted tr-text-metadata">
		<div class="flex items-center gap-sm">
			<span class="h-px flex-1 bg-border-muted"></span>
			{#if data.toolCount > 0}
				<span class="flex items-center gap-xs"><Icon name="wrench" size={12} />{data.toolCount} {data.toolCount === 1 ? "tool call" : "tool calls"}</span>
			{/if}
			{#if data.changedFiles.length > 0}
				<button
					type="button"
					data-testid="turn-divider-files"
					data-expanded={expanded || undefined}
					aria-expanded={many ? expanded : undefined}
					aria-controls={expanded ? listId : undefined}
					onclick={activateFiles}
					class={`flex items-center gap-xs rounded-[var(--radius-sm)] px-xs text-primary hover:bg-control-bg-hovered ${expanded ? "bg-control-bg-selected" : ""}`}
				>
					<Icon name="file-diff" size={12} />
					{data.changedFiles.length} {data.changedFiles.length === 1 ? "file changed" : "files changed"}
					{#if many}<Icon name={expanded ? "chevron-down" : "chevron-right"} size={12} />{/if}
				</button>
			{/if}
			{#if data.elapsedMs != null && data.elapsedMs >= 1000}
				<span class="flex items-center gap-xs"><Icon name="clock" size={12} />{formatElapsed(data.elapsedMs)}</span>
			{/if}
			<span class="h-px flex-1 bg-border-muted"></span>
		</div>
		{#if expanded}
			<ul id={listId} data-testid="turn-divider-files-list" class="flex flex-col">
				{#each data.changedFiles as path (path)}
					<li>
						<button
							type="button"
							data-testid="turn-divider-files-list-item"
							onclick={() => onOpenChange(path)}
							title={path}
							class="flex w-full items-center gap-xs rounded-[var(--radius-sm)] px-xs py-0.5 text-left hover:bg-control-bg-hovered"
						><Icon name="file-diff" size={12} class="shrink-0 text-text-muted" /><span class="min-w-0 flex-1 truncate text-text-muted">{projectRelativePath(path, projectAreaRoot)}</span></button>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
{/if}
