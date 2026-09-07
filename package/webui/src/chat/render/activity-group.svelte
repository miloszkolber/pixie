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
		class="text-text-muted tr-text-metadata"
	>
		<button
			type="button"
			data-testid="activity-group-toggle"
			aria-expanded={expanded}
			onclick={() => (expanded = toggleFold(id, expanded))}
			class="flex w-full cursor-pointer select-none items-center gap-xs rounded-[var(--radius-sm)] px-xs py-xs text-left outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary"
		>
			<Icon name="chevron-right" size={12} class={`shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} />
			<Icon name={live ? "loader-circle" : "layers"} size={12} class={`shrink-0 ${live ? "animate-spin motion-reduce:animate-none" : ""}`} />
			<span class="min-w-0 truncate" title={summary}>{summary}</span>
		</button>
		{#if expanded}
			<div class="flex flex-col gap-px pl-md">
				{#each steps as step, index (step.id)}
					<ActivityStepRow {step} isCurrent={live && index === steps.length - 1} {projectAreaRoot} />
				{/each}
			</div>
		{/if}
	</div>
{/if}
