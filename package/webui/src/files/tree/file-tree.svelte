<script lang="ts">
import type { FileNode } from "@pixie/contracts";
import { getTransport } from "../../connection";
import { appStore, selectProjectAreaTick } from "../../store";
import { normalizeFileFilter } from "./file-filter";
import FileNodeRow from "./file-node-row.svelte";

interface Props {
	projectAreaId: string;
	onOpen?: (() => void) | undefined;
	filter?: string;
}

let { projectAreaId, onOpen, filter = "" }: Props = $props();
let root = $derived(
	$appStore.projects.find((project) => project.id === projectAreaId)?.roots[0] ?? "",
);
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));
let activeTab = $derived.by(() => {
	const selection = $appStore.workspaceSelection.secondarySelection;
	if (
		(selection?.kind !== "file" && selection?.kind !== "diff") ||
		selection.projectId !== projectAreaId
	)
		return null;
	return ($appStore.tabsByProjectArea[projectAreaId] ?? []).find(
		(tab) => tab.id === selection.resourceId && (tab.kind === "file" || tab.kind === "diff"),
	) ?? null;
});
let activePath = $derived(
	activeTab?.kind === "file" || activeTab?.kind === "diff" ? activeTab.path : null,
);
let filterQuery = $derived(normalizeFileFilter(filter));
let nodes = $state<FileNode[] | null>(null);
let visibleNodes = $derived(
	filterQuery
		? (nodes?.filter(
				(node) => node.kind === "dir" || node.name.toLowerCase().includes(filterQuery),
			) ?? null)
		: nodes,
);
let error = $state<string | null>(null);
let warnings = $state<string[]>([]);
let expandedPaths = $state<ReadonlySet<string>>(new Set());
let reloadRevision = $state(0);
// Exact count of rows with a proven filter match, maintained by flip-only reports
// from FileNodeRow (mount/unmount retract through onDestroy). Branch directories
// stay visible as context without reporting, so this reaches zero exactly when
// nothing matches and the no-match message below is honest.
let matchCount = $state(0);
const readState = { identity: "", generation: 0 };

function reportMatch(delta: 1 | -1): void {
	matchCount += delta;
}

function setPathsExpanded(paths: readonly string[], expanded: boolean): void {
	const next = new Set(expandedPaths);
	for (const path of paths) {
		if (expanded) next.add(path);
		else next.delete(path);
	}
	expandedPaths = next;
}

$effect(() => {
	const id = projectAreaId;
	const selectedRoot = root;
	const tick = projectTick;
	const revision = reloadRevision;
	void tick;
	void revision;
	if (!selectedRoot) return;
	const nextIdentity = `${id}\0${selectedRoot}`;
	if (readState.identity !== nextIdentity) {
		readState.identity = nextIdentity;
		nodes = null;
		error = null;
		warnings = [];
		expandedPaths = new Set();
	}
	const mine = ++readState.generation;
	void getTransport()
		.request("fs.readDir", { projectId: id, path: "." })
		.then((result) => {
			if (mine !== readState.generation) return;
			nodes = result.nodes;
			warnings = result.warnings;
			error = null;
		})
		.catch((cause) => {
			if (mine !== readState.generation) return;
			nodes = null;
			error = cause instanceof Error ? cause.message : "File tree is unavailable.";
		});
	return () => {
		if (mine === readState.generation) readState.generation += 1;
	};
});
</script>

<div class="tree flex flex-col">
	{#if !root}
		<p class="px-xs py-xs tr-text-metadata text-text-muted">No root</p>
	{:else if error}
		<div class="flex flex-col items-start gap-xs px-xs py-xs">
			<p role="alert" class="tr-text-metadata text-feedback-error">File tree unavailable.</p>
			<button
				type="button"
				onclick={() => (reloadRevision += 1)}
				class="btn"
				data-variant="ghost"
				data-size="sm"
			>Retry</button>
		</div>
	{:else if nodes === null}
		<p role="status" class="px-xs py-xs tr-text-metadata text-text-muted">Loading files…</p>
	{:else if nodes.length === 0}
		<p class="px-xs py-xs tr-text-metadata text-text-muted">Empty</p>
	{:else if visibleNodes !== null && visibleNodes.length === 0}
		<p class="px-xs py-xs tr-text-metadata text-text-muted">No files match this filter.</p>
	{:else}
		{#if warnings.length > 0}
			<p role="status" class="px-xs py-xs tr-text-metadata text-feedback-warning">
				{warnings.join(" ")}
			</p>
		{/if}
		{#if filterQuery && matchCount === 0}
			<p class="px-xs py-xs tr-text-metadata text-text-muted">No files match this filter.</p>
		{/if}
		<ul class="tree-group flex flex-col">
			{#each visibleNodes ?? [] as node (node.path)}
				<FileNodeRow {node} {projectAreaId} {expandedPaths} {setPathsExpanded} {onOpen} {activePath} filter={filterQuery} onMatchChange={reportMatch} />
			{/each}
		</ul>
	{/if}
</div>
