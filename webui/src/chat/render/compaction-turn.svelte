<script lang="ts">
import { untrack } from "svelte";
import Icon from "../../components/icon.svelte";
import { useFoldState } from "../runtime/fold-state";
import { formatTokens } from "../session/session-stats";
import Markdown from "./markdown.svelte";

const { readFold, toggleFold } = useFoldState();

interface Props {
	id: string;
	summary: string;
	tokensBefore: number;
}
let { id, summary, tokensBefore }: Props = $props();
let open = $state(untrack(() => readFold(id)));
</script>


<div data-testid="chat-compaction" class="u-flex u-flex-col u-gap-sm">
	<button type="button" aria-expanded={open} onclick={() => (open = toggleFold(id, open))} class="u-flex u-items-center u-gap-sm compaction-toggle tr-text-metadata">
		<span class="u-flex-1 compaction-rule"></span><Icon name={open ? "chevron-down" : "chevron-right"} size={14} />
		<span>Earlier messages summarized ({formatTokens(tokensBefore)} tokens of context compacted)</span><span class="u-flex-1 compaction-rule"></span>
	</button>
	{#if open}<div class="tr-text-reading u-text-text-muted"><Markdown text={summary} /></div>{/if}
</div>

<style>
	.compaction-toggle { color: var(--text-muted); }
	.compaction-toggle:hover { color: var(--text-default); }
	.compaction-rule { height: 1px; background: var(--border-default); }
</style>
