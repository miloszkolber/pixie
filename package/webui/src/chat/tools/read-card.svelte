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

<div data-testid="tool-read" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="file-text" size={14} class="shrink-0 text-text-muted" />
		<span class="truncate text-primary" title={path}>{displayPath}</span>
		{#if range}<span class="shrink-0 text-text-muted">{range}</span>{/if}
	</div>
	{#if status === "running"}
		<span class="text-text-muted tr-text-metadata">Reading…</span>
	{:else if status === "error"}
		<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{output}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}><CodeBlock code={output} lang={language} /></Collapsible>
	{:else}
		<span class="text-text-muted tr-text-metadata italic">(empty file)</span>
	{/if}
</div>
