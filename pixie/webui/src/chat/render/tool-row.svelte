<script lang="ts">
import type { ChatRow } from "../runtime/rows";
import McpAppView from "../tools/apps/mcp-app-view.svelte";
import ToolCard from "./tool-card.svelte";
import { getToolChrome, getToolRenderer } from "./tool-registry";

interface Props {
	row: Extract<ChatRow, { kind: "tool" }>;
	projectAreaRoot?: string | undefined;
}
let { row, projectAreaRoot }: Props = $props();
let Renderer = $derived(getToolRenderer(row.toolName));
let renderProps = $derived({
	toolCallId: row.toolCallId,
	toolName: row.toolName,
	args: row.args,
	result: row.tool?.raw,
	app: row.tool?.app,
	subagentActivity: row.tool?.subagentActivity,
	status: row.tool?.status ?? (row.dead ? ("error" as const) : ("running" as const)),
	projectAreaRoot,
	streaming: row.streaming,
});
</script>

{#if getToolChrome(row.toolName) === "bare"}
	<div class="flex flex-col items-start gap-sm tr-text-ui text-text-default">
		<Renderer {...renderProps} />
		<McpAppView {...renderProps} />
	</div>
{:else}
	<ToolCard toolCallId={row.toolCallId} toolName={row.toolName} args={row.args} tool={row.tool} dead={row.dead} streaming={row.streaming} {projectAreaRoot} />
{/if}
