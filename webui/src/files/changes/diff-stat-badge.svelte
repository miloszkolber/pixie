<script lang="ts">
interface Props {
	added: number;
	removed: number;
	class?: string | undefined;
}

let { added, removed, class: className = "" }: Props = $props();
</script>

{#if added > 0 || removed > 0}
	<span class={`diff-stat-badge u-shrink-0 tr-text-metadata ${className}`}>
		<span class="diff-stat-added">+{added}</span>
		{" "}
		<span class="u-text-feedback-error">−{removed}</span>
	</span>
{/if}

<style>
	.diff-stat-badge { font-variant-numeric: tabular-nums; }
	.diff-stat-added { color: var(--feedback-success); }
</style>
