<script lang="ts">
import type { FileNode } from "@pixie/contracts";
import { onDestroy } from "svelte";
import { getTransport } from "../../connection";
import type { TabIntent } from "../../store";
import { appStore, selectProjectAreaTick } from "../../store";
import { openFileInTab } from "../tabs/open-tabs";
import { loadExpandedFolderChain } from "./directory-loader";
import { nodeNameMatchesFilter, subtreeHasMatch } from "./file-filter";
import FileNodeRow from "./file-node-row.svelte";
import type { ResolvedFolderChain } from "./folder-chains";
import TreeRow from "./tree-row.svelte";

interface Props {
	node: FileNode;
	projectAreaId: string;
	expandedPaths: ReadonlySet<string>;
	setPathsExpanded: (paths: readonly string[], expanded: boolean) => void;
	onOpen?: (() => void) | undefined;
	activePath?: string | null;
	filter?: string;
	onMatchChange?: ((delta: 1 | -1) => void) | undefined;
}

let {
	node,
	projectAreaId,
	expandedPaths,
	setPathsExpanded,
	onOpen,
	activePath = null,
	filter = "",
	onMatchChange = undefined,
}: Props = $props();
let directory = $state<ResolvedFolderChain<FileNode> | null>(null);
let loadedTick = $state<number | null>(null);
const readState = { identity: "", generation: 0 };
let isDirectory = $derived(node.kind === "dir");
let label = $derived(directory?.label ?? node.name);
let representedPaths = $derived(directory?.paths ?? [node.path]);
let expanded = $derived(expandedPaths.has(directory?.path ?? node.path));
let filterActive = $derived(filter !== "");
// While filtering, directories behave expanded so nested matches surface without
// manual expansion. expandedPaths is never mutated for this; clearing the filter
// restores the manual state.
let shownExpanded = $derived(expanded || (filterActive && isDirectory));
let children = $derived(directory?.children ?? null);
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));
let active = $derived(!isDirectory && activePath !== null && node.path === activePath);
let visibleChildren = $derived(
	filter && children
		? children.filter((child) => child.kind === "dir" || child.name.toLowerCase().includes(filter))
		: children,
);
// A loaded leaf directory (no subdirectories) with no match in its own name or
// files hides while filtering. Branch directories always stay mounted while
// filtering: hiding one would unmount still-loading descendants and their
// matches could never surface.
let hidden = $derived(
	filterActive &&
		isDirectory &&
		children !== null &&
		children.every((child) => child.kind === "file") &&
		!nodeNameMatchesFilter(node, filter) &&
		!subtreeHasMatch(children, filter),
);

// Match status drives the parent's exact no-match message (not visibility: branch
// directories stay visible as context but only report proven matches). Unloaded
// directories report a provisional match so the message never flashes while the
// subtree is still loading. Writes go out only on flips, so spurious effect
// re-runs are zero-write no-ops and can never reschedule the parent; unmounts
// retract through onDestroy instead of effect cleanup for the same reason.
let matchStatus = $derived(
	isDirectory
		? nodeNameMatchesFilter(node, filter) ||
			children === null ||
			subtreeHasMatch(children, filter)
		: nodeNameMatchesFilter(node, filter),
);
let lastReported = false;

$effect(() => {
	const status = matchStatus;
	if (status === lastReported) return;
	lastReported = status;
	onMatchChange?.(status ? 1 : -1);
});

onDestroy(() => {
	if (lastReported) {
		lastReported = false;
		onMatchChange?.(-1);
	}
});

$effect(() => {
	const currentIdentity = `${projectAreaId}\0${node.kind}\0${node.path}`;
	const tick = projectTick;
	const rowExpanded = shownExpanded;
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

{#if !hidden}
<li class="tree-item">
	<TreeRow
		testid="file-node"
		kind={isDirectory ? "dir" : "file"}
		expanded={shownExpanded}
		{active}
		{label}
		onclick={isDirectory ? toggleDirectory : () => open("preview")}
		ondblclick={isDirectory ? undefined : () => open("keep")}
	/>
	{#if isDirectory && shownExpanded && children}
		<ul class="tree-group pixie-guide flex flex-col pl-md">
			{#each visibleChildren ?? [] as child (child.path)}
				<FileNodeRow
					node={child}
					{projectAreaId}
					{expandedPaths}
					{setPathsExpanded}
					{onOpen}
					{activePath}
					{filter}
					{onMatchChange}
				/>
			{/each}
		</ul>
	{/if}
</li>
{/if}
