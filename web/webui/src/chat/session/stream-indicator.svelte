<script lang="ts">
import { phaseLabel, type StreamStatus } from "./stream-status";

interface Props {
	status: StreamStatus;
}

let { status }: Props = $props();
</script>

<div
	data-testid="stream-indicator"
	data-phase={status.phase}
	class="u-flex u-items-center u-gap-sm u-py-xs u-text-text-muted tr-text-metadata"
>
	<span class="u-flex u-items-center u-gap-0.5" aria-hidden="true">
		<span class="stream-dot"></span>
		<span class="stream-dot stream-dot-second"></span>
		<span class="stream-dot stream-dot-third"></span>
	</span>
	<span>{phaseLabel(status)}</span>
</div>

<style>
	.stream-dot { width: .375rem; height: .375rem; border-radius: 50%; background: currentColor; animation: stream-pulse 1s cubic-bezier(.4, 0, .6, 1) infinite; }
	.stream-dot-second { animation-delay: 200ms; }
	.stream-dot-third { animation-delay: 400ms; }
	@keyframes stream-pulse { 50% { opacity: .5; } }
	@media (prefers-reduced-motion: reduce) { .stream-dot { animation: none; } }
</style>
