<script lang="ts">
import type { FileNode } from "@pixie/contracts";
import { getTransport } from "../../connection";
import type { TabIntent } from "../../store";
import { appStore, selectProjectAreaTick } from "../../store";
import { openFileInTab } from "../tabs/open-tabs";
import { loadExpandedFolderChain } from "./directory-loader";
import FileNodeRow from "./file-node-row.svelte";
import type { ResolvedFolderChain } from "./folder-chains";
import TreeRow from "./tree-row.svelte";

interface Props {
	node: FileNode;
	projectAreaId: string;
	expandedPaths: ReadonlySet<string>;
	setPathsExpanded: (paths: readonly string[], expanded: boolean) => void;
	onOpen?: (() => void) | undefined;
}

let { node, projectAreaId, expandedPaths, setPathsExpanded, onOpen }: Props = $props();
let directory = $state<ResolvedFolderChain<FileNode> | null>(null);
let loadedTick = $state<number | null>(null);
const readState = { identity: "", generation: 0 };
let isDirectory = $derived(node.kind === "dir");
let label = $derived(directory?.label ?? node.name);
let representedPaths = $derived(directory?.paths ?? [node.path]);
let expanded = $derived(expandedPaths.has(directory?.path ?? node.path));
let children = $derived(directory?.children ?? null);
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));

$effect(() => {
	const currentIdentity = `${projectAreaId}\0${node.kind}\0${node.path}`;
	const tick = projectTick;
	const rowExpanded = expanded;
	if (readState.identity !== currentIdentity) {
		readState.identity = currentIdentity;
		directory = null;
		loadedTick = null;
	}
	if (!isDirectory) return;
	if (!rowExpanded) return;
	const mine = ++readState.generation;
	void loadExpandedFolderChain(node, {
		expanded: rowExpanded,
		projectTick: tick,
		loadedTick,
		readChildren: (path) =>
			getTransport()
				.request("fs.readDir", { projectId: projectAreaId, path })
				.then((listing) => listing.nodes),
	})
		.then((result) => {
			if (mine !== readState.generation || result === null) return;
			setPathsExpanded(result.directory.paths, true);
			directory = result.directory;
			loadedTick = result.loadedTick;
		})
		.catch(() => {});
	return () => {
		if (mine === readState.generation) readState.generation += 1;
	};
});

function toggleDirectory(): void {
	const nextExpanded = !expanded;
	if (!nextExpanded) readState.generation += 1;
	setPathsExpanded(representedPaths, nextExpanded);
}

function open(intent: TabIntent): void {
	void openFileInTab(projectAreaId, node.path, intent).then((opened) => {
		if (opened) onOpen?.();
	});
}
</script>

<li class="tree-item">
	<TreeRow
		testid="file-node"
		kind={isDirectory ? "dir" : "file"}
		{expanded}
		{label}
		onclick={isDirectory ? toggleDirectory : () => open("preview")}
		ondblclick={isDirectory ? undefined : () => open("keep")}
	/>
	{#if isDirectory && expanded && children}
		<ul class="tree-group flex flex-col pl-md">
			{#each children as child (child.path)}
				<FileNodeRow
					node={child}
					{projectAreaId}
					{expandedPaths}
					{setPathsExpanded}
					{onOpen}
				/>
			{/each}
		</ul>
	{/if}
</li>
