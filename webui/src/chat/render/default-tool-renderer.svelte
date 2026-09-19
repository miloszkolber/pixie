<script lang="ts">
import { toText } from "../tools/tool-helpers";
import ToolOutput from "../tools/tool-output.svelte";
import type { ToolRenderProps } from "./tool-registry";

let { args, result, status, toolName }: ToolRenderProps = $props();
let argsText = $derived(toText(args));
</script>

<div class="u-flex u-flex-col u-gap-xs">
	{#if argsText && argsText !== "{}"}
		<pre class="u-overflow-auto tr-code-text u-text-text-muted">{argsText}</pre>
	{/if}
	<ToolOutput {result} error={status === "error"} />
	{#if status === "done" && (toolName === "apps__create_app" || toolName === "apps__iterate_app")}
		<p class="u-text-text-muted tr-text-metadata">App saved in the agent session.</p>
	{/if}
</div>
