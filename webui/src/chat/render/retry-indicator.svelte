<script lang="ts">
import Icon from "../../components/icon.svelte";

interface Props {
	source: "turn" | "summarization";
	attempt: number;
	maxAttempts: number;
	delayMs: number;
}
let { source, attempt, maxAttempts, delayMs }: Props = $props();
let draining = $state(false);
$effect(() => {
	const frame = requestAnimationFrame(() => (draining = true));
	return () => cancelAnimationFrame(frame);
});
</script>

<div data-testid="retry-indicator" data-source={source} class="u-flex u-flex-col u-gap-xs retry-indicator tr-text-metadata">
	<span class="u-flex u-items-center u-gap-xs"><Icon name="rotate-cw" size={12} />{source === "summarization" ? "Retrying summarization" : "Retrying"} ({attempt}/{maxAttempts})…</span>
	<div class="retry-progress-track">
		<div class="retry-progress" class:retry-progress-draining={draining} style={`transition-duration: ${delayMs}ms`}></div>
	</div>
</div>

<style>
	.retry-indicator { border: 1px solid var(--border-default); border-radius: var(--radius-sm); background: var(--container-elevated-bg); padding: var(--space-xs) var(--space-sm); color: var(--text-muted); }
	.retry-progress-track { width: 100%; height: var(--space-2xs); overflow: hidden; border-radius: 999px; background: var(--border-default); }
	.retry-progress { height: 100%; width: 100%; background: var(--primary); transition-property: width; transition-timing-function: linear; }
	.retry-progress-draining { width: 0; }
</style>
