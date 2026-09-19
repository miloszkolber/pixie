<script lang="ts">
import type { ToolRenderProps } from "../render/tool-registry";
import { resultText, strArg } from "./tool-helpers";

let { args, result, status }: ToolRenderProps = $props();
let command = $derived(strArg(args, "command"));
let isError = $derived(status === "error");
let output = $derived(resultText(result, isError));
</script>

<div
	data-testid="tool-bash"
	class="u-rounded u-border u-border-border-default tool-bash-card tr-code-text"
>
	<div class="u-border-b u-border-border-default u-px-sm u-py-xs">
		<span class="tool-success">$</span>
		<span class="tool-bash-command u-text-text-muted">{command}</span>
	</div>
	<pre
		class={`u-overflow-auto u-px-sm u-py-xs tr-code-text tool-relaxed ${isError ? "u-text-feedback-error" : "u-text-text-default"}`}
	>{output || (status === "running" ? "Running…" : "(no output)")}</pre>
</div>

<style>
	.tool-bash-card { overflow: hidden; background: var(--container-header-bg); }
	.tool-success { color: var(--feedback-success); }
	.tool-bash-command { margin-inline-start: var(--space-sm); }
	.tool-relaxed { line-height: var(--tr-line-height-relaxed); }
</style>
