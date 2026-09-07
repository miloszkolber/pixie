<script lang="ts">
import Icon from "../../../components/icon.svelte";
import type { ToolRenderProps } from "../../render/tool-registry";
import { countLines } from "../collapsible";
import Collapsible from "../collapsible.svelte";
import { resultText, strArg } from "../tool-helpers";
import { browserArtifactUrl, browserDetails, browserImages, browserResponse } from "./browser-card";

let { args, result, status }: ToolRenderProps = $props();
let response = $derived(browserResponse(result));
let details = $derived(browserDetails(response ?? result));
let command = $derived(details.command || strArg(args, "command") || "browser");
let session = $derived(details.session ?? strArg(args, "session"));
let output = $derived(
	response
		? [
				response.stdout,
				response.stderr,
				response.message,
				response.hint,
				...(Array.isArray(response.warnings) ? response.warnings : []),
				...(Array.isArray(response.hints) ? response.hints : []),
			]
				.filter((value): value is string => typeof value === "string" && value.length > 0)
				.join("\n")
		: resultText(result, status === "error"),
);
let failed = $derived(
	status === "error" || response?.outcome === "failed" || response?.outcome === "rejected",
);
let images = $derived(browserImages(result));
let artifact = $derived(details.artifact);
let artifactHref = $derived(browserArtifactUrl(artifact?.url));
</script>

<div data-testid="tool-browser" class="flex flex-col gap-xs">
	<div class="flex items-center gap-xs tr-text-metadata">
		<Icon name="globe" size={14} class="shrink-0 text-text-muted" />
		<span class="text-primary">{command}</span>
		{#if session}<span class="truncate text-text-muted">in {session}</span>{/if}
	</div>
	{#if status === "running"}
		<span class="text-text-muted tr-text-metadata">Running browser command…</span>
	{:else if failed}
		<pre class="overflow-auto px-sm py-xs text-feedback-error tr-code-text"
		>{output || (typeof details.code === "string" ? details.code : "Browser command failed.")}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}>
			<pre class="overflow-auto rounded-[var(--radius-sm)] bg-container-header-bg p-sm tr-code-text text-text-default">{output}</pre>
		</Collapsible>
	{/if}
	{#if artifact}
		<div data-testid="tool-browser-artifact" class="flex items-center gap-xs text-text-muted tr-text-metadata">
			<Icon name="camera" size={14} class="shrink-0" />
			<span>Artifact:</span>
			{#if artifactHref}
				<a href={artifactHref} target="_blank" rel="noreferrer" class="truncate text-primary hover:underline">{artifact.name}</a>
			{:else}<span class="truncate">{artifact.name}</span>{/if}
		</div>
	{/if}
	{#if images.length > 0}
		<div data-testid="tool-browser-images" class="flex flex-wrap gap-sm">
			{#each images as image, index (`${image.mimeType}-${image.data.length}-${image.data.slice(0, 32)}-${index}`)}
				<img
					src={`data:${image.mimeType.toLowerCase()};base64,${image.data}`}
					alt={`${command} screenshot`}
					loading="lazy"
					decoding="async"
					class="max-h-[28rem] max-w-full rounded-[var(--radius-sm)] border border-border-default object-contain"
				/>
			{/each}
		</div>
	{/if}
	{#if status === "done" && !failed && !output && !artifact && images.length === 0}
		<span class="text-text-muted tr-text-metadata italic">No browser output.</span>
	{/if}
</div>
