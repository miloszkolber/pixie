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
	class="flex items-center gap-sm py-xs text-text-muted tr-text-metadata"
>
	<span class="flex items-center gap-0.5" aria-hidden="true">
		<span class="size-1.5 animate-pulse rounded-full bg-current"></span>
		<span class="size-1.5 animate-pulse rounded-full bg-current [animation-delay:200ms]"></span>
		<span class="size-1.5 animate-pulse rounded-full bg-current [animation-delay:400ms]"></span>
	</span>
	<span>{phaseLabel(status)}</span>
</div>
