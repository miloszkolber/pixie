<script lang="ts">
import Icon from "../../../components/icon.svelte";
import type { ToolRenderProps } from "../../render/tool-registry";
import { countLines } from "../collapsible";
import Collapsible from "../collapsible.svelte";
import { resultText, strArg } from "../tool-helpers";
import { signetDetails, signetRunningLabel, signetTitle } from "./signet-card";

let { toolName, args, result, status }: ToolRenderProps = $props();
let details = $derived(signetDetails(result));
let query = $derived(strArg(args, "query") || strArg(args, "content"));
let output = $derived(resultText(result, status === "error"));
let offline = $derived(details.error === "daemon_offline");
let count = $derived(
	details.memoriesFound ?? details.sourcesFound ?? details.sessionsFound ?? details.memoriesSaved,
);
</script>


<div data-testid="tool-signet" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="brain" size={14} class="u-shrink-0 u-text-text-muted" />
		<span class="tool-primary">{signetTitle(toolName)}</span>
		{#if count !== undefined}
			<span class="u-shrink-0 u-text-text-muted">{count} result{count === 1 ? "" : "s"}</span>
		{/if}
	</div>
	{#if query}<p class="u-truncate u-text-text-muted tr-text-metadata">{query}</p>{/if}
	{#if status === "running"}
		<span class="u-text-text-muted tr-text-metadata">{signetRunningLabel(toolName)}</span>
	{:else if offline}
		<span data-testid="tool-signet-offline" class="u-text-text-muted tr-text-metadata">Signet daemon unavailable. Memory integration is disabled for this turn.</span>
	{:else if status === "error"}
		<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{output || "Signet request failed."}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}>
			<pre class="u-overflow-auto u-rounded tool-code-surface u-p-md tr-code-text u-text-text-default">{output}</pre>
		</Collapsible>
	{/if}
</div>

<style>
	.tool-primary { color: var(--primary); }
	.tool-code-surface { background: var(--container-header-bg); }
</style>
