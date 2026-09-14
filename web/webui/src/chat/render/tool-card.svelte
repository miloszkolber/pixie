<script lang="ts">
import { untrack } from "svelte";
import DefaultToolRenderer from "./default-tool-renderer.svelte";
import Icon from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import type { ToolResultState } from "../runtime/types";
import { getToolRenderer, getToolSummary, resolveProminence } from "./tool-registry";

const { readFold, toggleFold } = useFoldState();

interface Props {
	toolCallId: string;
	toolName: string;
	title?: string;
	args: Record<string, unknown>;
	tool: ToolResultState | undefined;
	dead?: boolean;
	streaming: boolean;
	projectAreaRoot?: string | undefined;
}

let {
	toolCallId,
	toolName,
	title,
	args,
	tool,
	dead = false,
	streaming,
	projectAreaRoot,
}: Props = $props();
let status = $derived(tool?.status ?? (dead ? "error" : "running"));
let isError = $derived(status === "error");
let Renderer = $derived(getToolRenderer(toolName));
let renderProps = $derived({
	toolCallId,
	toolName,
	args,
	result: tool?.raw,
	subagentActivity: tool?.subagentActivity,
	status,
	projectAreaRoot,
	streaming,
});
let summary = $derived(
	status === "interrupted"
		? "Interrupted · final result not reported"
		: getToolSummary(toolName, renderProps),
);
let autoExpand = $derived(
	isError || (resolveProminence(toolName).defaultExpanded && status === "done"),
);
let expanded = $state(untrack(() => readFold(toolCallId, autoExpand)));

$effect(() => {
	if (autoExpand && !expanded && readFold(toolCallId, autoExpand)) expanded = true;
});
</script>

<div
	data-testid="tool-card"
	data-tool={toolName}
	data-status={status}
	data-expanded={expanded}
	class="tool-card"
>
	<button
		type="button"
		data-testid="tool-card-toggle"
		aria-expanded={expanded}
		onclick={() => (expanded = toggleFold(toolCallId, expanded))}
		class="u-flex u-w-full u-items-center u-gap-xs u-px-sm u-py-xs u-text-left tr-text-metadata tool-card-toggle"
	>
		<Icon
			name={status === "running" ? "loader-circle" : isError || status === "interrupted" ? "x" : "check"}
			size={12}
			class={`u-shrink-0 ${status === "running" ? "tool-card-spinning u-text-text-muted" : isError ? "u-text-feedback-error" : status === "interrupted" ? "u-text-text-muted" : "tool-card-success"}`}
		/>
		<span class="u-min-w-0 u-break-words u-text-text-default">{title || toolName}</span>
		{#if summary}<span class="u-min-w-0 u-flex-1 u-truncate u-text-text-muted" title={summary}>{summary}</span>
		{:else}<span class="u-flex-1"></span>{/if}
		<Icon name="chevron-right" size={12} class={`u-shrink-0 u-text-text-muted tool-card-chevron ${expanded ? "tool-card-chevron-expanded" : ""}`} />
	</button>
	{#if expanded}
		<div class={`u-flex u-flex-col u-items-start u-gap-sm tool-card-content u-px-sm ${isError ? "u-text-feedback-error" : ""}`}>
			{#if status === "interrupted"}<DefaultToolRenderer {...renderProps} />{:else}<Renderer {...renderProps} />{/if}
	</div>

<style>
	.tool-card { border: 1px solid var(--border-default); border-radius: var(--radius-sm); background: var(--container-elevated-bg); }
	.tool-card-toggle { cursor: pointer; user-select: none; border: 0; outline: none; background: transparent; }
	.tool-card-toggle:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); outline-offset: -2px; }
	.tool-card-success { color: var(--feedback-success); }
	.tool-card-spinning { animation: tool-card-spin 1s linear infinite; }
	.tool-card-chevron { transition: transform var(--transition-fast); }
	.tool-card-chevron-expanded { transform: rotate(90deg); }
	.tool-card-content { padding-block-end: var(--space-xs); }
	@keyframes tool-card-spin { to { transform: rotate(360deg); } }
	@media (prefers-reduced-motion: reduce) { .tool-card-spinning { animation: none; } }
</style>
	{/if}
</div>
