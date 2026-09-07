<script lang="ts">
import Icon from "../../components/icon.svelte";
import { projectRelativePath } from "../../lib";
import type { ToolRenderProps } from "../render/tool-registry";
import CodeBlock from "./code-block.svelte";
import { countLines } from "./collapsible";
import Collapsible from "./collapsible.svelte";
import { languageFromPath, resultText, strArg } from "./tool-helpers";
import ToolOutput from "./tool-output.svelte";

let { args, result, status, projectAreaRoot }: ToolRenderProps = $props();
let path = $derived(strArg(args, "path"));
let displayPath = $derived(projectRelativePath(path, projectAreaRoot));
let content = $derived(strArg(args, "content"));
let language = $derived(languageFromPath(path));
let message = $derived(resultText(result, status === "error"));
</script>

<div data-testid="tool-write" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="file-plus" size={14} class="shrink-0 text-feedback-success" />
		<span class="truncate text-text-default" title={path}>{displayPath}</span>
		<span class="shrink-0 text-text-muted">
			{status === "running" ? "writing…" : status === "error" ? "write failed" : "written"}
		</span>
	</div>
	{#if status === "error"}
		<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{message}</pre>
	{:else if content}
		<Collapsible lines={countLines(content)}><CodeBlock code={content} lang={language} /></Collapsible>
	{:else}
		<span class="text-text-muted tr-text-metadata italic">(empty file)</span>
	{/if}
	{#if status !== "error"}<ToolOutput {result} />{/if}
</div>
