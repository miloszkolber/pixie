<script lang="ts">
import Icon from "../../../components/icon.svelte";
import { DefaultToolRenderer, type ToolRenderProps } from "../../render/tool-registry";
import { countLines } from "../collapsible";
import Collapsible from "../collapsible.svelte";
import { resultText, strArg } from "../tool-helpers";
import ToolOutput from "../tool-output.svelte";
import { childModelLabel, childStatus, childStatusLabel, subagentDetails } from "./subagent-card";

let props: ToolRenderProps = $props();
let { args, result, status, subagentActivity, toolName } = $derived(props);
let details = $derived(subagentDetails(result));
let useFallback = $derived(
	toolName === "load" &&
		!details.status &&
		!details.results?.length &&
		!subagentActivity?.events.length,
);
let child = $derived(details.results?.[0]);
let task = $derived(
	strArg(args, "task") || strArg(args, "instructions") || strArg(args, "source") || child?.task,
);
let currentStatus = $derived(status === "error" ? ("failed" as const) : childStatus(details));
let output = $derived(resultText(result, status === "error" || currentStatus === "failed"));
let sessionId = $derived(details.childSessionId || details.runId || child?.runId);
let model = $derived(childModelLabel(child?.model, child?.thinkingLevel));
let label = $derived(
	currentStatus
		? childStatusLabel(currentStatus, child?.currentTool, child?.error)
		: status === "done"
			? "Delegation returned"
			: "Subagent running…",
);
let entries = $derived.by(() => {
	const seen = new Map<string, number>();
	return (subagentActivity?.events ?? []).map((event) => {
		const identity = `${event.childSessionId}\0${event.toolName}`;
		const occurrence = (seen.get(identity) ?? 0) + 1;
		seen.set(identity, occurrence);
		return { event, key: `${identity}\0${occurrence}` };
	});
});
let multipleChildren = $derived(
	new Set((subagentActivity?.events ?? []).map((event) => event.childSessionId)).size > 1,
);
let summary = $derived(
	strArg(args, "task") || strArg(args, "instructions") || strArg(args, "source") || "subagent",
);
</script>

{#if useFallback}
	<DefaultToolRenderer {...props} />
{:else}
	<div data-testid="tool-subagent" class="flex flex-col gap-xs">
		<div class="flex items-center gap-xs tr-text-metadata">
			<Icon name="git-fork" size={14} class="shrink-0 text-text-muted" />
			<span class="truncate text-primary" title={task || "subagent"}>{summary}</span>
			{#if sessionId}<span class="truncate text-text-muted">{sessionId}</span>{/if}
		</div>
		{#if task}<p class="truncate text-text-muted tr-text-metadata">{task}</p>{/if}
		<span class="text-text-muted tr-text-metadata">{label}</span>
		{#if model}<span class="text-text-muted tr-text-metadata">{model}</span>{/if}
		{#if entries.length > 0}
			<div class="flex min-w-0 max-w-full flex-col gap-xs">
				<span class="text-text-muted tr-text-metadata">Recent child activity</span>
				<ul class="flex min-w-0 max-w-full flex-col gap-0.5">
					{#each entries as entry (entry.key)}
						<li class="flex min-w-0 max-w-full items-baseline gap-xs tr-text-metadata">
							{#if multipleChildren}
								<span class="max-w-[12rem] shrink truncate text-text-muted" title={entry.event.childSessionId}>{entry.event.childSessionId}</span>
							{/if}
							<span class="min-w-0 break-words text-text-default" title={entry.event.toolName}>{entry.event.toolName}</span>
						</li>
					{/each}
				</ul>
				{#if subagentActivity?.truncated}<span class="text-text-muted tr-text-metadata">Earlier activity omitted</span>{/if}
			</div>
		{/if}
		{#if child?.truncated}<span class="text-text-muted tr-text-metadata">Output truncated</span>{/if}
		{#if status === "error" || currentStatus === "failed"}
			<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{child?.error || output || "Child failed."}</pre>
		{:else}
			<Collapsible lines={countLines(output)}><ToolOutput result={result ?? child?.finalOutput} /></Collapsible>
		{/if}
	</div>
{/if}
