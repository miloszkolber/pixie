<script lang="ts">
import Icon from "../../components/icon.svelte";
import { projectRelativePath } from "../../lib";
import { languageForPath } from "../../lib/language";
import type { ToolRenderProps } from "../render/tool-registry";
import CodeBlock from "./code-block.svelte";
import { countLines } from "./collapsible";
import Collapsible from "./collapsible.svelte";
import { resultText, strArg } from "./tool-helpers";
import ToolOutput from "./tool-output.svelte";

let { args, result, status, projectAreaRoot }: ToolRenderProps = $props();
let path = $derived(strArg(args, "path"));
let displayPath = $derived(projectRelativePath(path, projectAreaRoot));
let content = $derived(strArg(args, "content"));
let language = $derived(languageForPath(path));
let message = $derived(resultText(result, status === "error"));
</script>

<div data-testid="tool-write" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="file-plus" size={14} class="tool-success-icon" />
		<span class="u-truncate u-text-text-default" title={path}>{displayPath}</span>
		<span class="u-shrink-0 u-text-text-muted">
			{status === "running" ? "writing…" : status === "error" ? "write failed" : "written"}
		</span>
	</div>
	{#if status === "error"}
		<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{message}</pre>
	{:else if content}
		<Collapsible lines={countLines(content)}><CodeBlock code={content} lang={language} /></Collapsible>
	{:else}
		<span class="u-text-text-muted tr-text-metadata tool-italic">(empty file)</span>
	{/if}
	{#if status !== "error"}<ToolOutput {result} />{/if}
</div>

<style>
	:global(.tool-success-icon) { flex-shrink: 0; color: var(--feedback-success); }
	.tool-italic { font-style: italic; }
</style>
