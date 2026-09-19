<script lang="ts">
import { safeBrowserURL } from "@pixie/shared";
import Icon from "../../../components/icon.svelte";
import type { ToolRenderProps } from "../../render/tool-registry";
import CodeBlock from "../code-block.svelte";
import { countLines } from "../collapsible";
import Collapsible from "../collapsible.svelte";
import { resultText } from "../tool-helpers";
import { firstWebUrl, webHost } from "./web-card";

let { args, result, status }: ToolRenderProps = $props();
let url = $derived(firstWebUrl(args));
let safeURL = $derived(safeBrowserURL(url));
let label = $derived(safeURL ? webHost(safeURL) : url || "fetch");
let output = $derived(resultText(result, status === "error"));
</script>


<div data-testid="tool-fetch_content" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="link" size={14} class="u-shrink-0 u-text-text-muted" />
		{#if safeURL}
			<a href={safeURL} target="_blank" rel="noreferrer" class="u-truncate tool-link" title={safeURL}>{label}</a>
		{:else}
			<span class="u-truncate tool-primary" title={url || undefined}>{label}</span>
		{/if}
	</div>
	{#if status === "running"}<span class="u-text-text-muted tr-text-metadata">Fetching…</span>
	{:else if status === "error"}<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{output}</pre>
	{:else if output}<Collapsible lines={countLines(output)}><CodeBlock code={output} lang="markdown" /></Collapsible>
	{:else}<span class="u-text-text-muted tr-text-metadata tool-italic">(no content)</span>{/if}
</div>

<style>
	.tool-primary, .tool-link { color: var(--primary); }
	.tool-link:hover { text-decoration: underline; }
	.tool-italic { font-style: italic; }
</style>
