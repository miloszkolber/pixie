<script lang="ts">
import { toText } from "../tools/tool-helpers";
import ToolOutput from "../tools/tool-output.svelte";
import type { ToolRenderProps } from "./tool-registry";

let { args, result, status, toolName }: ToolRenderProps = $props();
let argsText = $derived(toText(args));
</script>

<div class="flex flex-col gap-xs">
	{#if argsText && argsText !== "{}"}
		<pre class="overflow-auto tr-code-text text-text-muted">{argsText}</pre>
	{/if}
	<ToolOutput {result} error={status === "error"} />
	{#if status === "done" && (toolName === "apps__create_app" || toolName === "apps__iterate_app")}
		<p class="text-text-muted tr-text-metadata">App saved in the agent session.</p>
	{/if}
</div>
