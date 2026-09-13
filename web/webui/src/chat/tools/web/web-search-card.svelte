<script lang="ts">
import Icon from "../../../components/icon.svelte";
import type { ToolRenderProps } from "../../render/tool-registry";
import CodeBlock from "../code-block.svelte";
import { countLines } from "../collapsible";
import Collapsible from "../collapsible.svelte";
import { resultText } from "../tool-helpers";
import { firstWebQuery, webSearchProvider } from "./web-card";

let { args, result, status }: ToolRenderProps = $props();
let query = $derived(firstWebQuery(args));
let provider = $derived(webSearchProvider(result));
let output = $derived(resultText(result, status === "error"));
</script>

<div data-testid="tool-web_search" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="search" size={14} class="shrink-0 text-text-muted" />
		<span class="truncate text-primary" title={query}>{query}</span>
		{#if provider}<span class="shrink-0 text-text-muted">via {provider}</span>{/if}
	</div>
	{#if status === "running"}<span class="text-text-muted tr-text-metadata">Searching…</span>
	{:else if status === "error"}<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{output}</pre>
	{:else if output}<Collapsible lines={countLines(output)}><CodeBlock code={output} lang="markdown" /></Collapsible>
	{:else}<span class="text-text-muted tr-text-metadata italic">No results.</span>{/if}
</div>
