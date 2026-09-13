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
		class="h-full overflow-auto bg-container-content-bg p-md text-text-default tr-code-document"
	>{content}</pre>
{:else}
	<div
		data-testid={testid}
		class="h-full overflow-auto bg-container-content-bg [&_.shiki]:min-h-full [&_.shiki]:!bg-transparent [&_.shiki]:p-md [&_pre]:!m-0"
	>
		{@html html}
	</div>
{/if}
