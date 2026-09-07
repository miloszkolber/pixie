<script lang="ts">
import { untrack } from "svelte";
import DefaultToolRenderer from "./default-tool-renderer.svelte";
import Icon from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import type { ToolResultState } from "../runtime/types";
import McpAppView from "../tools/apps/mcp-app-view.svelte";
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
	app: tool?.app,
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
	class="rounded-[var(--radius-sm)] border border-border-default bg-container-elevated-bg"
>
	<button
		type="button"
		data-testid="tool-card-toggle"
		aria-expanded={expanded}
		onclick={() => (expanded = toggleFold(toolCallId, expanded))}
		class="flex w-full cursor-pointer select-none items-center gap-xs px-sm py-xs text-left tr-text-metadata outline-none focus-visible:ring-2 focus-visible:ring-primary"
	>
		<Icon
			name={status === "running" ? "loader-circle" : isError || status === "interrupted" ? "x" : "check"}
			size={12}
			class={`shrink-0 ${status === "running" ? "animate-spin text-text-muted motion-reduce:animate-none" : isError ? "text-feedback-error" : status === "interrupted" ? "text-text-muted" : "text-feedback-success"}`}
		/>
		<span class="min-w-0 break-words text-text-default">{title || toolName}</span>
		{#if summary}<span class="min-w-0 flex-1 truncate text-text-muted" title={summary}>{summary}</span>
		{:else}<span class="flex-1"></span>{/if}
		<Icon name="chevron-right" size={12} class={`shrink-0 text-text-muted transition-transform ${expanded ? "rotate-90" : ""}`} />
	</button>
	{#if expanded}
		<div class={`flex flex-col items-start gap-sm px-sm pb-xs ${isError ? "text-feedback-error" : ""}`}>
			{#if status === "interrupted"}<DefaultToolRenderer {...renderProps} />{:else}<Renderer {...renderProps} />{/if}
			<McpAppView {...renderProps} />
		</div>
	{/if}
</div>
