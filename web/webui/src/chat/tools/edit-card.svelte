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

<div data-testid="tool-edit" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="pencil" size={14} class="shrink-0 text-feedback-warning" />
		<span class="truncate text-text-default" title={path}>{displayPath}</span>
		<span class="shrink-0 text-text-muted">
			{status === "running" ? "editing…" : status === "error" ? "edit failed" : "edited"}
		</span>
	</div>
	{#if status === "error"}
		<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text">{message}</pre>
	{:else}
		<Collapsible
			lines={oldLines.length + newLines.length}
			fadeClass="bg-[linear-gradient(to_top,var(--container-elevated-bg),transparent)]"
		>
			<div class="overflow-auto rounded-[var(--radius-sm)] border border-border-default tr-code-text leading-relaxed">
				{#each oldLines as line, index (`old-${index}`)}
					<div class="flex bg-feedback-error-subtle">
						<span class="w-6 shrink-0 select-none px-1 text-right text-feedback-error-muted">−</span>
						<pre class="min-w-0 flex-1 px-1 text-feedback-error tr-code-text">{line}</pre>
					</div>
				{/each}
				{#each newLines as line, index (`new-${index}`)}
					<div class="flex bg-feedback-success-subtle">
						<span class="w-6 shrink-0 select-none px-1 text-right text-feedback-success-muted">+</span>
						<pre class="min-w-0 flex-1 px-1 text-feedback-success tr-code-text">{line}</pre>
					</div>
				{/each}
			</div>
		</Collapsible>
		<ToolOutput {result} />
	{/if}
</div>
