<script lang="ts">
import Icon from "../../components/icon.svelte";
import { projectRelativePath } from "../../lib";
import type { ToolRenderProps } from "../render/tool-registry";
import Collapsible from "./collapsible.svelte";
import { resultText, strArg } from "./tool-helpers";
import ToolOutput from "./tool-output.svelte";

let { args, result, status, projectAreaRoot }: ToolRenderProps = $props();
let path = $derived(strArg(args, "path"));
let displayPath = $derived(projectRelativePath(path, projectAreaRoot));
let oldText = $derived(
	strArg(args, "before") ||
		strArg(args, "oldText") ||
		strArg(args, "old_string") ||
		strArg(args, "old"),
);
let newText = $derived(
	strArg(args, "after") ||
		strArg(args, "newText") ||
		strArg(args, "new_string") ||
		strArg(args, "new"),
);
let message = $derived(resultText(result, status === "error"));
let oldLines = $derived(oldText ? oldText.split("\n") : []);
let newLines = $derived(newText ? newText.split("\n") : []);
</script>

<div data-testid="tool-edit" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="pencil" size={14} class="u-shrink-0 u-text-feedback-warning" />
		<span class="u-truncate u-text-text-default" title={path}>{displayPath}</span>
		<span class="u-shrink-0 u-text-text-muted">
			{status === "running" ? "editing…" : status === "error" ? "edit failed" : "edited"}
		</span>
	</div>
	{#if status === "error"}
		<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text">{message}</pre>
	{:else}
		<Collapsible
			lines={oldLines.length + newLines.length}
			fadeTone="elevated"
		>
			<div class="u-overflow-auto u-rounded u-border u-border-border-default tr-code-text tool-relaxed">
				{#each oldLines as line, index (`old-${index}`)}
					<div class="u-flex tool-diff-error">
						<span class="tool-diff-marker tool-diff-error-marker">−</span>
						<pre class="u-min-w-0 u-flex-1 tool-diff-padding u-text-feedback-error tr-code-text">{line}</pre>
					</div>
				{/each}
				{#each newLines as line, index (`new-${index}`)}
					<div class="u-flex tool-diff-success">
						<span class="tool-diff-marker tool-diff-success-marker">+</span>
						<pre class="u-min-w-0 u-flex-1 tool-diff-padding tool-success tr-code-text">{line}</pre>
					</div>
				{/each}
			</div>
		</Collapsible>
		<ToolOutput {result} />
	{/if}
</div>

<style>
	.tool-relaxed { line-height: var(--tr-line-height-relaxed); }
	.tool-diff-error { background: var(--feedback-error-subtle); }
	.tool-diff-success { background: var(--feedback-success-subtle); }
	.tool-diff-marker { width: 1.5rem; flex-shrink: 0; user-select: none; padding-inline: var(--space-2xs); text-align: end; }
	.tool-diff-error-marker { color: var(--feedback-error-muted); }
	.tool-diff-success-marker { color: var(--feedback-success-muted); }
	.tool-diff-padding { padding-inline: var(--space-2xs); }
	.tool-success { color: var(--feedback-success); }
</style>
