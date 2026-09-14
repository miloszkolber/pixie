<script lang="ts">
import type { GitDiffScope, GitHead } from "@pixie/shared";
import type { BranchCatalog } from "./git-scope-state";
import { selectedBranch } from "./git-scope-state";

interface Props {
	catalog: BranchCatalog;
	head: GitHead;
	initialSelection: string;
	onSelect: (scope: GitDiffScope) => void;
	onRetry: () => void;
}

let { catalog, head, initialSelection, onSelect, onRetry }: Props = $props();
let selection = $state("");
let previousInitial = $state("");
let selected = $derived(selectedBranch(catalog, head, selection));
let currentRef = $derived(head.kind === "branch" ? `refs/heads/${head.name}` : null);

$effect(() => {
	if (initialSelection === previousInitial) return;
	previousInitial = initialSelection;
	selection = initialSelection;
});
</script>

{#if head.kind === "unborn"}
	<p role="status" class="u-border-border-default u-border-t u-px-sm u-py-sm tr-text-metadata u-text-text-muted">
		Create the first commit before comparing branches.
	</p>
{:else if catalog === null}
	<p role="status" class="u-border-border-default u-border-t u-px-sm u-py-sm tr-text-metadata u-text-text-muted">
		Loading branches…
	</p>
{:else if "error" in catalog}
	<div class="u-border-border-default u-border-t u-px-sm u-py-sm">
		<p role="alert" class="tr-text-metadata u-text-feedback-error">Could not read branches: {catalog.error}</p>
		<button type="button" onclick={onRetry} class="btn u-mt-xs" data-variant="ghost" data-size="sm">Retry</button>
	</div>
{:else if catalog.branches.length === 0}
	<p role="status" class="u-border-border-default u-border-t u-px-sm u-py-sm tr-text-metadata u-text-text-muted">
		No branches found.
	</p>
{:else}
	<div class="u-flex u-flex-col u-gap-sm u-border-border-default u-border-t u-px-sm u-py-sm">
		<label class="field tr-text-metadata u-text-text-muted">
			<span class="field-label">Base branch</span>
			<select
				aria-label="Base branch"
				value={selected?.ref ?? ""}
				onchange={(event) => (selection = event.currentTarget.value)}
				class="select u-mt-xs u-w-full u-min-w-0"
			>
				<option value="" disabled>Choose a branch…</option>
				{#each catalog.branches as branch (branch.ref)}
					<option value={branch.ref} disabled={branch.ref === currentRef}>
						{branch.name}{branch.ref === currentRef ? " (current)" : ""}
					</option>
				{/each}
			</select>
		</label>
		<button
			type="button"
			disabled={!selected}
			onclick={() => selected && onSelect({ kind: "branch", baseRef: selected.ref })}
			class="btn git-scope-action"
			data-variant="ghost"
			data-size="sm"
		>Compare branch</button>
		{#if catalog.truncated}<p class="tr-text-metadata u-text-text-muted">Some branches are not shown.</p>{/if}
	</div>
{/if}

<style>
	.git-scope-action { align-self: flex-start; }
</style>
