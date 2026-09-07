<script lang="ts">
import { untrack } from "svelte";
import Icon, { type IconName } from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import DefaultToolRenderer from "./default-tool-renderer.svelte";
import type { ActivityStep } from "../runtime/rows";
import McpAppView from "../tools/apps/mcp-app-view.svelte";
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
	`${iconSpins ? "animate-spin motion-reduce:animate-none" : ""} ${renderProps?.status === "error" ? "text-feedback-error" : renderProps?.status === "done" ? "text-feedback-success" : ""}`,
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
	class="text-text-muted tr-text-metadata"
>
	<button
		type="button"
		data-testid="activity-step-toggle"
		aria-expanded={expanded}
		onclick={toggle}
		class="flex w-full cursor-pointer select-none items-center gap-xs rounded-[var(--radius-sm)] px-xs py-sm text-left outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary sm:py-0.5"
	>
		<Icon name={iconName} size={12} class={`shrink-0 ${iconClass}`} />
		<span class="min-w-0 break-words text-text-default">{name}</span>
		{#if summary}<span class="min-w-0 flex-1 truncate" title={summary}>{summary}</span>{/if}
		<Icon name="chevron-right" size={12} class={`shrink-0 transition-transform ${expanded ? "rotate-90" : ""}`} />
	</button>
	{#if expanded}
		{#if step.kind === "thinking"}
			<div class="whitespace-pre-wrap break-words px-sm pb-xs pl-lg">{step.text}</div>
		{:else if Renderer && renderProps}
			<div class={`flex flex-col items-start gap-sm px-sm pb-xs pl-lg ${renderProps.status === "error" ? "text-feedback-error" : ""}`}>
				{#if renderProps.status === "interrupted"}<DefaultToolRenderer {...renderProps} />{:else}<Renderer {...renderProps} />{/if}
				<McpAppView {...renderProps} />
			</div>
		{/if}
	{/if}
</div>
