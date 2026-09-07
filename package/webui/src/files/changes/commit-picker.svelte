<script lang="ts">
import type { GitDiffScope } from "@pixie/contracts";
import type { CommitHistory } from "./git-scope-state";
import { selectedCommit } from "./git-scope-state";

interface Props {
	history: CommitHistory;
	initialSelection: string;
	onSelect: (scope: GitDiffScope) => void;
	onRetry: () => void;
}

let { history, initialSelection, onSelect, onRetry }: Props = $props();
let selection = $state("");
let previousInitial = $state("");
let selected = $derived(selectedCommit(history, selection));

$effect(() => {
	if (initialSelection === previousInitial) return;
	previousInitial = initialSelection;
	selection = initialSelection;
});
</script>

{#if history === null}
	<p role="status" class="px-sm py-xs tr-text-metadata text-text-muted">Loading commits…</p>
{:else if "error" in history}
	<div class="px-sm py-xs">
		<p role="alert" class="tr-text-metadata text-feedback-error">Could not read commits: {history.error}</p>
		<button type="button" onclick={onRetry} class="btn mt-xs" data-variant="ghost" data-size="sm">Retry</button>
	</div>
{:else if history.commits.length === 0}
	<p role="status" class="px-sm py-xs tr-text-metadata text-text-muted">No commits yet.</p>
{:else}
	<div class="flex flex-col gap-sm border-border-default border-t px-sm pt-sm">
		<label class="field tr-text-metadata text-text-muted">
			<span class="field-label">Recent commit</span>
			<select
				aria-label="Recent commit"
				value={selected?.sha ?? ""}
				onchange={(event) => (selection = event.currentTarget.value)}
				class="select mt-xs w-full min-w-0"
			>
				<option value="" disabled>Choose a commit…</option>
				{#each history.commits as commit (commit.sha)}
					<option value={commit.sha}>{commit.shortSha} · {commit.subject}</option>
				{/each}
			</select>
		</label>
		<div class="flex flex-wrap gap-xs">
			<button
				type="button"
				disabled={!selected}
				onclick={() => selected && onSelect({ kind: "commit", sha: selected.sha })}
				class="btn"
				data-variant="ghost"
				data-size="sm"
			>View commit</button>
			<button
				type="button"
				disabled={!selected}
				onclick={() => selected && onSelect({ kind: "pinned", baseRef: selected.sha })}
				class="btn"
				data-variant="ghost"
				data-size="sm"
			>Compare with working tree</button>
		</div>
		{#if history.commits.length === 200}
			<p class="tr-text-metadata text-text-muted">Showing the latest 200 commits.</p>
		{/if}
	</div>
{/if}
