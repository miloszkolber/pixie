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

<div data-testid="tool-web_search" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="search" size={14} class="u-shrink-0 u-text-text-muted" />
		<span class="u-truncate tool-primary" title={query}>{query}</span>
		{#if provider}<span class="u-shrink-0 u-text-text-muted">via {provider}</span>{/if}
	</div>
	{#if status === "running"}<span class="u-text-text-muted tr-text-metadata">Searching…</span>
	{:else if status === "error"}<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{output}</pre>
	{:else if output}<Collapsible lines={countLines(output)}><CodeBlock code={output} lang="markdown" /></Collapsible>
	{:else}<span class="u-text-text-muted tr-text-metadata tool-italic">No results.</span>{/if}
</div>

<style>
	.tool-primary { color: var(--primary); }
	.tool-italic { font-style: italic; }
</style>
