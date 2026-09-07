<script lang="ts">
import type { GitFileChange } from "@pixie/contracts";
import type { TabIntent } from "../../store";
import TreeRow from "../tree/tree-row.svelte";
import ChangeRowActions, { ROW_MENU_SLOT } from "./change-row-actions.svelte";
import { buildChangesTree, type ChangeTreeNode, statusNameClass } from "./changes-model";
import DiffStatBadge from "./diff-stat-badge.svelte";

interface Props {
	changes: readonly GitFileChange[];
	onOpen: (path: string, intent: TabIntent) => void;
	isActive: (path: string) => boolean;
}

let { changes, onOpen, isActive }: Props = $props();
let nodes = $derived(buildChangesTree(changes));
let collapsedPaths = $state<ReadonlySet<string>>(new Set());

function toggle(path: string): void {
	const next = new Set(collapsedPaths);
	if (next.has(path)) next.delete(path);
	else next.add(path);
	collapsedPaths = next;
}
</script>

{#snippet nodeRow(node: ChangeTreeNode)}
	{#if node.kind === "file"}
		<li>
			{#snippet trailing()}
				<DiffStatBadge added={node.added} removed={node.removed} />
			{/snippet}
			{#snippet row(oncontextmenu: (event: MouseEvent) => void)}
				<TreeRow
					testid="change-node"
					{oncontextmenu}
					kind="file"
					highlight="wrapper"
					active={isActive(node.path)}
					dataStatus={node.status}
					label={node.name}
					labelClassName={statusNameClass(node.status)}
					onclick={() => onOpen(node.path, "preview")}
					ondblclick={() => onOpen(node.path, "keep")}
					{trailing}
				/>
			{/snippet}
			<ChangeRowActions
				path={node.path}
				active={isActive(node.path)}
				onView={() => onOpen(node.path, "preview")}
				children={row}
			/>
		</li>
	{:else}
		{@const expanded = !collapsedPaths.has(node.path)}
		<li>
			<div class="flex min-w-0 items-center">
				{#snippet trailing()}
					<DiffStatBadge added={node.added} removed={node.removed} />
				{/snippet}
				<TreeRow
					testid="change-tree-folder"
					kind="dir"
					{expanded}
					label={node.name}
					onclick={() => toggle(node.path)}
					{trailing}
				/>
				<span class={ROW_MENU_SLOT}></span>
			</div>
			{#if expanded}
				<ul class="tree-group flex flex-col pl-md">
					{#each node.children as child (child.path)}
						{@render nodeRow(child)}
					{/each}
				</ul>
			{/if}
		</li>
	{/if}
{/snippet}

<ul class="tree tree-group flex flex-col">
	{#each nodes as node (node.path)}
		{@render nodeRow(node)}
	{/each}
</ul>
