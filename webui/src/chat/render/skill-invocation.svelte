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
	class="skill-invocation-card"
>
	<button
		type="button"
		data-testid="skill-invocation-toggle"
		aria-expanded={expanded}
		aria-label={`${expanded ? "Hide" : "Show"} instructions for ${invocation.name}`}
		onclick={() => (expanded = toggleFold(foldId, expanded))}
		class="u-flex u-w-full u-items-center u-gap-xs u-px-md u-py-sm u-text-left skill-invocation-toggle"
	>
		<Icon name="book-open" size={14} class="u-shrink-0 u-text-text-muted" />
		<span class="u-shrink-0 tr-text-ui u-text-text-muted">Skill</span><span class="u-shrink-0 skill-invocation-subtle" aria-hidden="true">·</span>
		<span data-testid="skill-invocation-name" class="u-min-w-0 u-flex-1 u-truncate tr-code-text u-text-text-default">{invocation.name}</span>
		<Icon name={expanded ? "chevron-down" : "chevron-right"} size={14} class="u-shrink-0 u-text-text-muted" />
	</button>
	{#if expanded}
		<div data-testid="skill-invocation-content" class="skill-invocation-content u-px-md u-py-sm u-text-text-muted">
			<Markdown text={invocation.content} />
		</div>
	{/if}
</div>

<style>
	.skill-invocation-card { max-width: 85%; overflow: hidden; border: 1px solid var(--bubble-user-border); border-radius: var(--radius-lg); background: var(--bubble-user-bg); background-clip: padding-box; }
	.skill-invocation-toggle { border: 0; outline: none; background: transparent; transition: background-color var(--transition-fast); }
	.skill-invocation-toggle:hover { background: var(--control-bg-hovered); }
	.skill-invocation-toggle:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); outline-offset: -2px; }
	.skill-invocation-subtle { color: var(--text-subtle); }
	.skill-invocation-content { border-top: 1px solid var(--bubble-user-border); }
</style>
