<script lang="ts">
import { untrack } from "svelte";
import Icon from "../../components/icon.svelte";
import type { SkillInvocation } from "../../lib";
import { useFoldState } from "../runtime/fold-state";
import Markdown from "./markdown.svelte";

const { readFold, toggleFold } = useFoldState();

interface Props {
	foldId: string;
	invocation: SkillInvocation;
}
let { foldId, invocation }: Props = $props();
let expanded = $state(untrack(() => readFold(foldId)));
</script>

<div
	data-testid="skill-invocation-card"
	data-expanded={expanded}
	class="max-w-[85%] overflow-hidden rounded-[var(--radius-lg)] border border-bubble-user-border bg-clip-padding bg-bubble-user-bg"
>
	<button
		type="button"
		data-testid="skill-invocation-toggle"
		aria-expanded={expanded}
		aria-label={`${expanded ? "Hide" : "Show"} instructions for ${invocation.name}`}
		onclick={() => (expanded = toggleFold(foldId, expanded))}
		class="flex w-full items-center gap-xs px-md py-sm text-left outline-none transition-colors hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary"
	>
		<Icon name="book-open" size={14} class="shrink-0 text-text-muted" />
		<span class="shrink-0 tr-text-ui text-text-muted">Skill</span><span class="shrink-0 text-text-subtle" aria-hidden="true">·</span>
		<span data-testid="skill-invocation-name" class="min-w-0 flex-1 truncate tr-code-text text-text-default">{invocation.name}</span>
		<Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} class="shrink-0 text-text-muted" />
	</button>
	{#if expanded}
		<div data-testid="skill-invocation-content" class="border-bubble-user-border border-t px-md py-sm text-text-muted">
			<Markdown text={invocation.content} />
		</div>
	{/if}
</div>
