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

<div data-testid="chat-compaction" class="flex flex-col gap-sm">
	<button type="button" aria-expanded={open} onclick={() => (open = toggleFold(id, open))} class="flex items-center gap-sm text-text-muted tr-text-metadata hover:text-text-default">
		<span class="h-px flex-1 bg-border-default"></span><Icon name={open ? "chevron-down" : "chevron-right"} size={14} />
		<span>Earlier messages summarized ({formatTokens(tokensBefore)} tokens of context compacted)</span><span class="h-px flex-1 bg-border-default"></span>
	</button>
	{#if open}<div class="tr-text-reading text-text-muted"><Markdown text={summary} /></div>{/if}
</div>
