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

<div data-testid="tool-signet" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="brain" size={14} class="shrink-0 text-text-muted" />
		<span class="text-primary">{signetTitle(toolName)}</span>
		{#if count !== undefined}
			<span class="shrink-0 text-text-muted">{count} result{count === 1 ? "" : "s"}</span>
		{/if}
	</div>
	{#if query}<p class="truncate text-text-muted tr-text-metadata">{query}</p>{/if}
	{#if status === "running"}
		<span class="text-text-muted tr-text-metadata">{signetRunningLabel(toolName)}</span>
	{:else if offline}
		<span data-testid="tool-signet-offline" class="text-text-muted tr-text-metadata">Signet daemon unavailable. Memory integration is disabled for this turn.</span>
	{:else if status === "error"}
		<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{output || "Signet request failed."}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}>
			<pre class="overflow-auto rounded-[var(--radius-sm)] bg-container-header-bg p-sm tr-code-text text-text-default">{output}</pre>
		</Collapsible>
	{/if}
</div>
