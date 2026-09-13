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
	class="overflow-hidden rounded-[var(--radius-sm)] border border-border-default bg-container-header-bg tr-code-text"
>
	<div class="border-border-default border-b px-sm py-xs">
		<span class="text-feedback-success">$</span>
		<span class="ml-sm text-text-muted">{command}</span>
	</div>
	<pre
		class={`overflow-auto px-sm py-xs tr-code-text leading-relaxed ${isError ? "text-feedback-error" : "text-text-default"}`}
	>{output || (status === "running" ? "Running…" : "(no output)")}</pre>
</div>
