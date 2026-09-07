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

<div data-testid="retry-indicator" data-source={source} class="flex flex-col gap-xs rounded-[var(--radius-sm)] border border-border-default bg-container-elevated-bg px-sm py-xs text-text-muted tr-text-metadata">
	<span class="flex items-center gap-xs"><Icon name="rotate-cw" size={12} />{source === "summarization" ? "Retrying summarization" : "Retrying"} ({attempt}/{maxAttempts})…</span>
	<div class="h-1 w-full overflow-hidden rounded-full bg-border-default">
		<div class={`h-full bg-primary transition-[width] ease-linear ${draining ? "w-0" : "w-full"}`} style={`transition-duration: ${delayMs}ms`}></div>
	</div>
</div>
