<script lang="ts">
import type { QueueLane, SessionQueueState } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";

interface Props {
	queue: SessionQueueState;
	onEdit: (kind: QueueLane, index: number) => void;
	onRemove: (kind: QueueLane, index: number) => void;
	onRetry: (kind: QueueLane, index: number) => void;
}

let { queue, onEdit, onRemove, onRetry }: Props = $props();
let items = $derived(
	[
		...queue.steering.map((text, index) => ({
			kind: "steering" as const,
			index,
			label: "Steering",
			hint: "delivers at the agent's next step",
			text,
		})),
		...queue.followUp.map((text, index) => ({
			kind: "followUp" as const,
			index,
			label: "Follow-up",
			hint: "runs after the agent finishes",
			text,
		})),
	].map((item) => {
		const blocked = queue.blocked?.lane === item.kind && queue.blocked.index === item.index;
		return {
			...item,
			blocked,
			hint: blocked
				? "may already have been delivered; check the transcript before retrying"
				: item.hint,
		};
	}),
);
</script>

{#if items.length > 0}
	<div
		data-testid="queue-strip"
		class="flex w-full shrink-0 flex-col gap-2xs border-border-default border-t bg-container-elevated-bg px-md py-xs text-text-muted tr-text-metadata"
	>
		{#each items as item (`${item.kind}:${item.index}`)}
			<div
				data-testid="queue-item"
				data-kind={item.kind}
				data-index={item.index}
				title={`${item.text} — ${item.hint}`}
				class={`flex w-full items-center gap-sm rounded-[var(--radius-xs)] ${item.blocked ? "bg-feedback-warning-subtle px-xs py-2xs" : ""}`}
			>
				<span class="min-w-0 flex-1 truncate">
					<span class="text-text-default">
						{item.label}{item.blocked ? " — may already be sent" : ""}:
					</span>{" "}{item.text}
				</span>
				{#if item.blocked}
					<button
						type="button"
						data-testid="queue-item-retry"
						aria-label={`Send queued message again (may duplicate): ${item.text}`}
						disabled={!queue.revision}
						onclick={() => onRetry(item.kind, item.index)}
						class="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-xs)] hover:bg-control-bg-hovered hover:text-text-default"
					>
						<Icon name="rotate-ccw" size={12} class="size-3" />
					</button>
				{/if}
				<button
					type="button"
					data-testid="queue-item-edit"
					aria-label={`Edit queued message: ${item.text}`}
					disabled={!queue.revision}
					onclick={() => onEdit(item.kind, item.index)}
					class="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-xs)] hover:bg-control-bg-hovered hover:text-text-default"
				>
					<Icon name="pencil" size={12} class="size-3" />
				</button>
				<button
					type="button"
					data-testid="queue-item-remove"
					aria-label={`Remove queued message: ${item.text}`}
					disabled={!queue.revision}
					onclick={() => onRemove(item.kind, item.index)}
					class="flex size-7 shrink-0 items-center justify-center rounded-[var(--radius-xs)] hover:bg-control-bg-hovered hover:text-text-default"
				>
					<Icon name="x" size={12} class="size-3" />
				</button>
			</div>
		{/each}
	</div>
{/if}
