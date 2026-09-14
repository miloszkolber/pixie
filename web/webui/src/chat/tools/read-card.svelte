<script lang="ts">
import Icon from "../../components/icon.svelte";
import { projectRelativePath } from "../../lib";
import type { ToolRenderProps } from "../render/tool-registry";
import CodeBlock from "./code-block.svelte";
import { countLines } from "./collapsible";
import Collapsible from "./collapsible.svelte";
import { languageFromPath, numArg, resultText, strArg } from "./tool-helpers";

let { args, result, status, projectAreaRoot }: ToolRenderProps = $props();
let path = $derived(strArg(args, "path"));
let displayPath = $derived(projectRelativePath(path, projectAreaRoot));
let offset = $derived(numArg(args, "offset"));
let limit = $derived(numArg(args, "limit"));
let output = $derived(resultText(result, status === "error"));
let language = $derived(languageFromPath(path));
let range = $derived.by(() => {
	if (offset != null && offset > 1) {
		return limit != null ? `lines ${offset}–${offset + limit - 1}` : `from line ${offset}`;
	}
	return limit != null ? `first ${limit} lines` : "";
});
</script>

<div data-testid="tool-read" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="file-text" size={14} class="u-shrink-0 u-text-text-muted" />
		<span class="u-truncate tool-primary" title={path}>{displayPath}</span>
		{#if range}<span class="u-shrink-0 u-text-text-muted">{range}</span>{/if}
	</div>
	{#if status === "running"}
		<span class="u-text-text-muted tr-text-metadata">Reading…</span>
	{:else if status === "error"}
		<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{output}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}><CodeBlock code={output} lang={language} /></Collapsible>
	{:else}
		<span class="u-text-text-muted tr-text-metadata tool-italic">(empty file)</span>
	{/if}
</div>

<style>
	.tool-primary { color: var(--primary); }
	.tool-italic { font-style: italic; }
</style>
