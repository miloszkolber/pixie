<script lang="ts">
import type { GitDiffScope } from "@pixie/shared";
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
	<p role="status" class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">Loading commits…</p>
{:else if "error" in history}
	<div class="u-px-sm u-py-xs">
		<p role="alert" class="tr-text-metadata u-text-feedback-error">Could not read commits: {history.error}</p>
		<button type="button" onclick={onRetry} class="btn u-mt-xs" data-variant="ghost" data-size="sm">Retry</button>
	</div>
{:else if history.commits.length === 0}
	<p role="status" class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">No commits yet.</p>
{:else}
	<div class="commit-picker u-flex u-flex-col u-gap-sm u-border-border-default u-border-t u-px-sm">
		<label class="field tr-text-metadata u-text-text-muted">
			<span class="field-label">Recent commit</span>
			<select
				aria-label="Recent commit"
				value={selected?.sha ?? ""}
				onchange={(event) => (selection = event.currentTarget.value)}
				class="select u-mt-xs u-w-full u-min-w-0"
			>
				<option value="" disabled>Choose a commit…</option>
				{#each history.commits as commit (commit.sha)}
					<option value={commit.sha}>{commit.shortSha} · {commit.subject}</option>
				{/each}
			</select>
		</label>
		<div class="u-flex u-flex-wrap u-gap-xs">
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
			<p class="tr-text-metadata u-text-text-muted">Showing the latest 200 commits.</p>
		{/if}
	</div>
{/if}

<style>
	.commit-picker { padding-block-start: var(--space-sm); }
</style>
