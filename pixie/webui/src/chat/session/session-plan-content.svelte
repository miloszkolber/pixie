<script lang="ts">
import type { SessionPlanState } from "@pixie/contracts";
import PlanStatusIcon from "./plan-status-icon.svelte";
import { planIconStatus, planProgress, planStatusLabel } from "./session-plan";

interface Props {
	planState: SessionPlanState;
}
let { planState }: Props = $props();
let progress = $derived(planProgress(planState));
let hasEntries = $derived(planState.entries.length > 0);
</script>

<div data-testid="session-plan-content" class="flex flex-col gap-sm p-md">
	<div class="flex items-baseline justify-between gap-md">
		<h2 class="tr-title-entity text-text-default">Session plan</h2>
		{#if hasEntries}
			<span class="shrink-0 tr-text-metadata text-text-muted">{progress.completed} of {progress.total} complete</span>
		{/if}
	</div>
	{#if hasEntries}
		<ol class="flex max-h-72 flex-col gap-xs overflow-y-auto" aria-label="Plan steps">
			{#each planState.entries as entry, index (`${entry.status}:${entry.priority}:${entry.content}:${index}`)}
				<li class="flex items-start gap-xs rounded-[var(--radius-xs)] px-xs py-2xs">
					<span class="mt-0.5" title={planStatusLabel(entry.status)}>
						<PlanStatusIcon kind={planIconStatus(entry.status)} />
					</span>
					<span class={`min-w-0 flex-1 break-words tr-text-ui ${entry.status === "completed" ? "text-text-muted line-through" : "text-text-default"}`}>
						<span class="sr-only">{planStatusLabel(entry.status)}: </span>{entry.content}
					</span>
					<span class="shrink-0 capitalize tr-text-metadata text-text-muted">{entry.priority}</span>
				</li>
			{/each}
		</ol>
	{/if}
	{#if planState.truncated}
		<p role="status" class="tr-text-metadata text-feedback-warning">Plan shortened to fit display limits.</p>
	{/if}
</div>
