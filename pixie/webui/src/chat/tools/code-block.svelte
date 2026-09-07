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
		class="overflow-auto rounded-[var(--radius-sm)] bg-container-header-bg p-sm tr-code-text text-text-default"
	>{code}</pre>
{:else}
	<div
		class="overflow-auto rounded-[var(--radius-sm)] tr-code-text [&_pre]:!m-0 [&_pre]:!bg-container-header-bg [&_pre]:p-sm"
	>
		{@html html}
	</div>
{/if}
