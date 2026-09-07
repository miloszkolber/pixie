<script lang="ts">
import type { FileNode } from "@pixie/contracts";
import { getTransport } from "../../connection";
import { appStore, selectProjectAreaTick } from "../../store";
import FileNodeRow from "./file-node-row.svelte";

interface Props {
	projectAreaId: string;
	onOpen?: (() => void) | undefined;
}

let { projectAreaId, onOpen }: Props = $props();
let root = $derived(
	$appStore.projects.find((project) => project.id === projectAreaId)?.roots[0] ?? "",
);
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));
let nodes = $state<FileNode[] | null>(null);
let error = $state<string | null>(null);
let warnings = $state<string[]>([]);
let expandedPaths = $state<ReadonlySet<string>>(new Set());
let reloadRevision = $state(0);
const readState = { identity: "", generation: 0 };

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
	{:else}
		{#if warnings.length > 0}
			<p role="status" class="px-xs py-xs tr-text-metadata text-feedback-warning">
				{warnings.join(" ")}
			</p>
		{/if}
		<ul class="tree-group flex flex-col">
			{#each nodes as node (node.path)}
				<FileNodeRow {node} {projectAreaId} {expandedPaths} {setPathsExpanded} {onOpen} />
			{/each}
		</ul>
	{/if}
</div>
