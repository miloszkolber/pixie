<script lang="ts">
import type { QueueLane, SessionQueueState } from "@pixie/shared";
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
		class="u-flex u-w-full u-shrink-0 u-flex-col u-gap-2xs queue-strip u-px-md u-py-xs u-text-text-muted tr-text-metadata"
	>
		{#each items as item (`${item.kind}:${item.index}`)}
			<div
				data-testid="queue-item"
				data-kind={item.kind}
				data-index={item.index}
				title={`${item.text} — ${item.hint}`}
				class={`u-flex u-w-full u-items-center u-gap-sm queue-item ${item.blocked ? "queue-item-blocked u-px-xs queue-item-blocked-padding" : ""}`}
			>
				<span class="u-min-w-0 u-flex-1 u-truncate">
					<span class="u-text-text-default">
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
						class="u-flex queue-item-action"
					>
						<Icon name="rotate-ccw" size={12} class="queue-item-icon" />
					</button>
				{/if}
				<button
					type="button"
					data-testid="queue-item-edit"
					aria-label={`Edit queued message: ${item.text}`}
					disabled={!queue.revision}
					onclick={() => onEdit(item.kind, item.index)}
					class="u-flex queue-item-action"
				>
					<Icon name="pencil" size={12} class="queue-item-icon" />
				</button>
				<button
					type="button"
					data-testid="queue-item-remove"
					aria-label={`Remove queued message: ${item.text}`}
					disabled={!queue.revision}
					onclick={() => onRemove(item.kind, item.index)}
					class="u-flex queue-item-action"
				>
					<Icon name="x" size={12} class="queue-item-icon" />
				</button>
			</div>
		{/each}
	</div>
{/if}

<style>
	.queue-strip { border-top: 1px solid var(--border-default); background: var(--container-elevated-bg); }
	.queue-item { border-radius: var(--radius-xs); }
	.queue-item-blocked { background: var(--feedback-warning-subtle); }
	.queue-item-blocked-padding { padding-block: var(--space-2xs); }
	.queue-item-action { width: 1.75rem; height: 1.75rem; flex-shrink: 0; align-items: center; justify-content: center; border: 0; border-radius: var(--radius-xs); background: transparent; }
	.queue-item-action:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	.queue-item-icon { width: var(--space-lg); height: var(--space-lg); }
</style>
