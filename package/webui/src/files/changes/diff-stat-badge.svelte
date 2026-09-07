<script lang="ts">
interface Props {
	added: number;
	removed: number;
	class?: string | undefined;
}

let { added, removed, class: className = "" }: Props = $props();
</script>

{#if added > 0 || removed > 0}
	<span class={`shrink-0 tr-text-metadata tabular-nums ${className}`}>
		<span class="text-feedback-success">+{added}</span>
		{" "}
		<span class="text-feedback-error">−{removed}</span>
	</span>
{/if}
