<script lang="ts">
import { highlightCode, languageForPath } from "../../lib/highlighter";

interface Props {
	path: string;
	content: string;
	language?: string | undefined;
	testid?: string;
}

let { path, content, language, testid = "source-preview" }: Props = $props();
let html = $state<string | null>(null);

$effect(() => {
	const source = content;
	const syntax = language ?? languageForPath(path);
	let cancelled = false;
	html = null;
	void highlightCode(source, syntax)
		.then((next) => {
			if (!cancelled) html = next;
		})
		.catch(() => {
			if (!cancelled) html = null;
		});
	return () => {
		cancelled = true;
	};
});
</script>

{#if html === null}
	<pre
		data-testid={testid}
		class="source-preview-surface u-h-full u-overflow-auto u-p-md u-text-text-default tr-code-document"
	>{content}</pre>
{:else}
	<div
		data-testid={testid}
		class="source-preview-highlight u-h-full u-overflow-auto"
	>
		{@html html}
	</div>
{/if}

<style>
	.source-preview-surface,
	.source-preview-highlight { background-color: var(--container-content-bg); }
	.source-preview-highlight :global(.shiki) {
		min-block-size: 100%;
		background-color: transparent !important;
		padding: var(--space-md);
	}
	.source-preview-highlight :global(pre) { margin: 0 !important; }
</style>
