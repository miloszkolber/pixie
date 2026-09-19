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

<div data-testid="tool-browser" class="u-flex u-flex-col u-gap-xs">
	<div class="u-flex u-items-center u-gap-xs tr-text-metadata">
		<Icon name="globe" size={14} class="u-shrink-0 u-text-text-muted" />
		<span class="tool-primary">{command}</span>
		{#if session}<span class="u-truncate u-text-text-muted">in {session}</span>{/if}
	</div>
	{#if status === "running"}
		<span class="u-text-text-muted tr-text-metadata">Running browser command…</span>
	{:else if failed}
		<pre class="u-overflow-auto u-px-sm u-py-xs u-text-feedback-error tr-code-text"
		>{output || (typeof details.code === "string" ? details.code : "Browser command failed.")}</pre>
	{:else if output}
		<Collapsible lines={countLines(output)}>
			<pre class="u-overflow-auto u-rounded tool-code-surface u-p-md tr-code-text u-text-text-default">{output}</pre>
		</Collapsible>
	{/if}
	{#if artifact}
		<div data-testid="tool-browser-artifact" class="u-flex u-items-center u-gap-xs u-text-text-muted tr-text-metadata">
			<Icon name="camera" size={14} class="u-shrink-0" />
			<span>Artifact:</span>
			{#if artifactHref}
				<a href={artifactHref} target="_blank" rel="noreferrer" class="u-truncate tool-link">{artifact.name}</a>
			{:else}<span class="u-truncate">{artifact.name}</span>{/if}
		</div>
	{/if}
	{#if images.length > 0}
		<div data-testid="tool-browser-images" class="u-flex u-flex-wrap u-gap-sm">
			{#each images as image, index (`${image.mimeType}-${image.data.length}-${image.data.slice(0, 32)}-${index}`)}
				<img
					src={`data:${image.mimeType.toLowerCase()};base64,${image.data}`}
					alt={`${command} screenshot`}
					loading="lazy"
					decoding="async"
					class="u-max-w-full u-rounded u-border u-border-border-default tool-browser-image"
				/>
			{/each}
		</div>
	{/if}
	{#if status === "done" && !failed && !output && !artifact && images.length === 0}
		<span class="u-text-text-muted tr-text-metadata tool-italic">No browser output.</span>
	{/if}
</div>

<style>
	.tool-primary, .tool-link { color: var(--primary); }
	.tool-link:hover { text-decoration: underline; }
	.tool-code-surface { background: var(--container-header-bg); }
	.tool-browser-image { max-block-size: 28rem; object-fit: contain; }
	.tool-italic { font-style: italic; }
</style>
