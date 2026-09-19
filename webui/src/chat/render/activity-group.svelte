<script lang="ts">
import { untrack } from "svelte";
import Icon from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import type { ActivityStep } from "../runtime/rows";
import { activityToolRenderProps, summarizeSteps } from "./activity-group";
import ActivityStepRow from "./activity-step.svelte";
import { getToolSummary } from "./tool-registry";

const { readFold, toggleFold } = useFoldState();

interface Props {
	id: string;
	steps: ActivityStep[];
	live: boolean;
	projectAreaRoot?: string | undefined;
}

let { id, steps, live, projectAreaRoot }: Props = $props();
let expanded = $state(untrack(() => readFold(id)));
let single = $derived(steps.length === 1 ? steps[0] : undefined);
let summary = $derived.by(() => {
	if (!live) return summarizeSteps(steps);
	const current = steps.at(-1);
	if (!current) return "Working…";
	if (current.kind === "thinking") return "Thinking…";
	const detail = getToolSummary(
		current.toolName,
		activityToolRenderProps(current, projectAreaRoot),
	);
	return detail ? `${current.toolName} · ${detail}` : `${current.toolName}…`;
});
</script>

{#if single}
	<ActivityStepRow step={single} isCurrent={live} {projectAreaRoot} />
{:else}
	<div
		data-testid="activity-group"
		data-expanded={expanded}
		data-live={live}
		data-steps={steps.length}
		class="u-text-text-muted tr-text-metadata"
	>
		<button
			type="button"
			data-testid="activity-group-toggle"
			aria-expanded={expanded}
			onclick={() => (expanded = toggleFold(id, expanded))}
			class="u-flex u-w-full u-items-center u-gap-xs activity-toggle"
		>
			<Icon name="chevron-right" size={12} class={`u-shrink-0 activity-chevron ${expanded ? "activity-chevron-expanded" : ""}`} />
			<Icon name={live ? "loader-circle" : "layers"} size={12} class={`u-shrink-0 ${live ? "activity-spinning" : ""}`} />
			<span class="u-min-w-0 u-truncate" title={summary}>{summary}</span>
		</button>
		{#if expanded}
			<div class="u-flex u-flex-col activity-steps">
				{#each steps as step, index (step.id)}
					<ActivityStepRow {step} isCurrent={live && index === steps.length - 1} {projectAreaRoot} />
				{/each}
			</div>
		{/if}
	</div>
{/if}

<style>
	.activity-toggle { cursor: pointer; user-select: none; border: 0; border-radius: var(--radius-sm); outline: none; background: transparent; padding: var(--space-xs); text-align: start; }
	.activity-toggle:hover { background: var(--control-bg-hovered); }
	.activity-toggle:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); }
	:global(.activity-chevron) { transition: transform var(--transition-fast); }
	:global(.activity-chevron-expanded) { transform: rotate(90deg); }
	:global(.activity-spinning) { animation: activity-spin 1s linear infinite; }
	.activity-steps { gap: 1px; padding-inline-start: var(--space-md); }
	@keyframes activity-spin { to { transform: rotate(360deg); } }
	@media (prefers-reduced-motion: reduce) { :global(.activity-spinning) { animation: none; } }
</style>
