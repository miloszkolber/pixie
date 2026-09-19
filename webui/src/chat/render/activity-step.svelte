<script lang="ts">
import { untrack } from "svelte";
import Icon, { type IconName } from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import DefaultToolRenderer from "./default-tool-renderer.svelte";
import type { ActivityStep } from "../runtime/rows";
import { activityToolRenderProps, formatActivityChars } from "./activity-group";
import { getToolRenderer, getToolSummary } from "./tool-registry";

const { readFold, toggleFold } = useFoldState();

interface Props {
	step: ActivityStep;
	isCurrent?: boolean;
	projectAreaRoot?: string | undefined;
}

let { step, isCurrent = false, projectAreaRoot }: Props = $props();
let expanded = $state(untrack(() => readFold(step.id)));
let renderProps = $derived(
	step.kind === "tool" ? activityToolRenderProps(step, projectAreaRoot) : null,
);
let Renderer = $derived(step.kind === "tool" ? getToolRenderer(step.toolName) : null);
let iconName = $derived.by<IconName>(() => {
	if (step.kind === "thinking") return step.streaming && isCurrent ? "loader-circle" : "brain";
	return renderProps?.status === "running"
		? "loader-circle"
		: renderProps?.status === "error" || renderProps?.status === "interrupted"
			? "x"
			: "check";
});
let iconSpins = $derived(
	step.kind === "thinking" ? step.streaming && isCurrent : renderProps?.status === "running",
);
let iconClass = $derived(
	`${iconSpins ? "activity-spinning" : ""} ${renderProps?.status === "error" ? "activity-error" : renderProps?.status === "done" ? "activity-success" : ""}`,
);
let name = $derived(step.kind === "thinking" ? "thinking" : step.title || step.toolName);
let summary = $derived(
	step.kind === "thinking"
		? `${formatActivityChars(step.text.length)} chars`
		: renderProps
			? renderProps.status === "interrupted"
				? "Interrupted · final result not reported"
				: getToolSummary(step.toolName, renderProps)
			: "",
);

function toggle(): void {
	expanded = toggleFold(step.id, expanded);
}
</script>

<div
	data-testid="activity-step"
	data-step={step.kind}
	data-tool={step.kind === "tool" ? step.toolName : undefined}
	data-status={renderProps?.status}
	data-expanded={expanded}
	class="u-text-text-muted tr-text-metadata"
>
	<button
		type="button"
		data-testid="activity-step-toggle"
		aria-expanded={expanded}
		onclick={toggle}
		class="u-flex u-w-full u-items-center u-gap-xs activity-step-toggle"
	>
		<Icon name={iconName} size={12} class={`u-shrink-0 ${iconClass}`} />
		<span class="u-min-w-0 u-break-words u-text-text-default">{name}</span>
		{#if summary}<span class="u-min-w-0 u-flex-1 u-truncate" title={summary}>{summary}</span>{/if}
		<Icon name="chevron-right" size={12} class={`u-shrink-0 activity-chevron ${expanded ? "activity-chevron-expanded" : ""}`} />
	</button>
	{#if expanded}
		{#if step.kind === "thinking"}
			<div class="activity-step-text u-break-words u-px-sm">{step.text}</div>
		{:else if Renderer && renderProps}
			<div class={`u-flex u-flex-col u-items-start u-gap-sm activity-step-content u-px-sm ${renderProps.status === "error" ? "u-text-feedback-error" : ""}`}>
				{#if renderProps.status === "interrupted"}<DefaultToolRenderer {...renderProps} />{:else}<Renderer {...renderProps} />{/if}
	</div>

<style>
	.activity-step-toggle { cursor: pointer; user-select: none; border: 0; border-radius: var(--radius-sm); outline: none; background: transparent; padding: var(--space-sm) var(--space-xs); text-align: start; }
	.activity-step-toggle:hover { background: var(--control-bg-hovered); }
	.activity-step-toggle:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); }
	.activity-chevron { transition: transform var(--transition-fast); }
	.activity-chevron-expanded { transform: rotate(90deg); }
	.activity-step-text, .activity-step-content { padding-block-end: var(--space-xs); padding-inline-start: var(--space-lg); }
	.activity-step-text { white-space: pre-wrap; }
	.activity-spinning { animation: activity-spin 1s linear infinite; }
	.activity-error { color: var(--feedback-error); }
	.activity-success { color: var(--feedback-success); }
	@keyframes activity-spin { to { transform: rotate(360deg); } }
	@media (min-width: 40rem) { .activity-step-toggle { padding-block: var(--space-2xs); } }
	@media (prefers-reduced-motion: reduce) { .activity-spinning { animation: none; } }
</style>
		{/if}
	{/if}
</div>
