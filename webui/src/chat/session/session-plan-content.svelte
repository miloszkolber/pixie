<script lang="ts">
import type { SessionPlanState } from "@pixie/shared";
import PlanStatusIcon from "./plan-status-icon.svelte";
import { planBlockedByLabel, planIconStatus, planProgress, planStatusLabel } from "./session-plan";

interface Props {
	planState: SessionPlanState;
}
let { planState }: Props = $props();
let progress = $derived(planProgress(planState));
let hasEntries = $derived(planState.entries.length > 0);
</script>


<div data-testid="session-plan-content" class="u-flex u-flex-col u-gap-sm u-p-md">
	<div class="u-flex session-plan-items-baseline u-justify-between u-gap-md">
		<h2 class="tr-title-entity u-text-text-default">Session plan</h2>
		{#if hasEntries}
			<span class="u-shrink-0 tr-text-metadata u-text-text-muted">{progress.completed} of {progress.total} complete</span>
		{/if}
	</div>
	{#if hasEntries}
		<ol class="u-flex session-plan-list u-flex-col u-gap-xs u-overflow-y-auto" aria-label="Plan steps">
			{#each planState.entries as entry, index (`${entry.status}:${entry.priority}:${entry.content}:${index}`)}
				<li class="u-flex u-items-start u-gap-xs session-plan-entry u-px-xs session-plan-entry-padding">
					<span class="session-plan-icon-offset" title={planStatusLabel(entry.status)}>
						<PlanStatusIcon kind={planIconStatus(entry.status)} />
					</span>
					<span class={`u-min-w-0 u-flex-1 u-break-words tr-text-ui ${entry.status === "completed" ? "u-text-text-muted session-plan-completed" : "u-text-text-default"}`}>
						<span class="u-sr-only">{planStatusLabel(entry.status)}: </span>{entry.content}
						{#if planBlockedByLabel(entry)}
							<span class="session-plan-blocked tr-text-metadata u-text-text-muted">{planBlockedByLabel(entry)}</span>
						{/if}
					</span>
					<span class="u-shrink-0 session-plan-capitalize tr-text-metadata u-text-text-muted">{entry.priority}</span>
				</li>
			{/each}
		</ol>
	{/if}
	{#if planState.truncated}
		<p role="status" class="tr-text-metadata u-text-feedback-warning">Plan shortened to fit display limits.</p>
	{/if}
</div>

<style>
	.session-plan-items-baseline { align-items: baseline; }
	.session-plan-list { max-height: 18rem; }
	.session-plan-entry { border-radius: var(--radius-xs); }
	.session-plan-entry-padding { padding-block: var(--space-2xs); }
	.session-plan-icon-offset { margin-top: var(--space-2xs); }
	.session-plan-completed { text-decoration: line-through; }
	.session-plan-blocked { display: block; }
	.session-plan-capitalize { text-transform: capitalize; }
</style>
