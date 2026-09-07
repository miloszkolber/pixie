<script lang="ts">
import { safeBrowserURL } from "@pixie/contracts";
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

<div data-testid="tool-fetch_content" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="link" size={14} class="shrink-0 text-text-muted" />
		{#if safeURL}
			<a href={safeURL} target="_blank" rel="noreferrer" class="truncate text-primary hover:underline" title={safeURL}>{label}</a>
		{:else}
			<span class="truncate text-primary" title={url || undefined}>{label}</span>
		{/if}
	</div>
	{#if status === "running"}<span class="text-text-muted tr-text-metadata">Fetching…</span>
	{:else if status === "error"}<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{output}</pre>
	{:else if output}<Collapsible lines={countLines(output)}><CodeBlock code={output} lang="markdown" /></Collapsible>
	{:else}<span class="text-text-muted tr-text-metadata italic">(no content)</span>{/if}
</div>
