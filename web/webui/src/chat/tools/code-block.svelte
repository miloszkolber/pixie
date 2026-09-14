<script lang="ts">
import { highlightCode } from "../../lib/highlighter";

interface Props {
	code: string;
	lang: string;
}

let { code, lang }: Props = $props();
let highlighted = $state<{ code: string; html: string; lang: string } | null>(null);
let html = $derived(
	highlighted?.code === code && highlighted.lang === lang ? highlighted.html : null,
);

$effect(() => {
	const source = code;
	const language = lang;
	let cancelled = false;
	highlighted = null;
	if (!language) return;
	void highlightCode(source, language)
		.then((next) => {
			if (!cancelled && next) highlighted = { code: source, html: next, lang: language };
		})
		.catch(() => {
			if (!cancelled) highlighted = null;
		});
	return () => {
		cancelled = true;
	};
});
</script>

{#if html === null}
	<pre
		class="u-overflow-auto u-rounded code-block-surface u-p-md tr-code-text u-text-text-default"
	>{code}</pre>
{:else}
	<div
		class="u-overflow-auto u-rounded code-block-highlighted tr-code-text"
	>
		{@html html}
	</div>
{/if}

<style>
	.code-block-surface { background: var(--container-header-bg); }
	.code-block-highlighted :global(pre) { margin: 0 !important; background: var(--container-header-bg) !important; padding: var(--space-sm); }
</style>
