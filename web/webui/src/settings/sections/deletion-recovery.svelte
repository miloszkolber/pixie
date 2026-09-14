<script lang="ts">
import type { DeletionRecovery } from "@pixie/shared";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { deletionRecoveryKey } from "../deletion-recovery";

interface Props {
	records: readonly DeletionRecovery[];
	pendingKey: string | null;
	error: string | null;
	onConfirm: (record: DeletionRecovery) => void;
	onRetain: (record: DeletionRecovery) => void;
}

let { records, pendingKey, error, onConfirm, onRetain }: Props = $props();
</script>

<section
	data-testid="deletion-recovery"
	aria-labelledby="deletion-recovery-heading"
	class="card u-min-w-0 u-p-md"
>
	<div class="u-min-w-0">
		<h3 id="deletion-recovery-heading" class="tr-text-ui u-text-text-default">Deletion recovery</h3>
		<p class="u-mt-xs u-text-text-muted tr-text-metadata">
			Retained deletion tombstones that could not be resumed. Confirm only when the deletion
			really happened, or retain the record and leave the tombstone in place.
		</p>
	</div>

	{#if error}
		<p role="alert" data-testid="deletion-recovery-error" class="u-mt-sm u-text-feedback-error tr-text-metadata">
			{error}
		</p>
	{/if}

	{#if records.length === 0}
		<p data-testid="deletion-recovery-empty" class="u-mt-sm u-text-text-muted tr-text-metadata">
			No retained deletion records.
		</p>
	{:else}
		<ul
			data-testid="deletion-recovery-list"
			class="deletion-recovery-list u-mt-sm u-flex u-flex-col u-border-border-default u-border-t"
		>
			{#each records as record (deletionRecoveryKey(record))}
				{@const key = deletionRecoveryKey(record)}
				<li
					data-testid="deletion-recovery-row"
					data-project-id={record.projectId}
					data-session-id={record.sessionId}
					data-phase={record.phase}
					class="deletion-recovery-row u-flex u-min-w-0 u-flex-col u-gap-sm u-py-sm"
				>
					<dl
						class="deletion-recovery-details u-min-w-0 tr-text-metadata"
					>
						<dt class="u-text-text-muted">Project</dt>
						<dd class="deletion-recovery-value u-min-w-0 u-text-text-default"><code>{record.projectId}</code></dd>
						<dt class="u-text-text-muted">Session</dt>
						<dd class="deletion-recovery-value u-min-w-0 u-text-text-default"><code>{record.sessionId}</code></dd>
						<dt class="u-text-text-muted">Phase</dt>
						<dd class="deletion-recovery-value u-min-w-0 u-text-text-default">{record.phase}</dd>
						<dt class="u-text-text-muted">Reason</dt>
						<dd class="deletion-recovery-reason u-min-w-0 u-text-text-default">{record.reason}</dd>
					</dl>
					<div class="u-flex u-shrink-0 u-flex-wrap u-gap-sm">
						<Button
							variant="outline"
							size="sm"
							data-testid="deletion-recovery-confirm"
							disabled={pendingKey === key}
							onclick={() => onConfirm(record)}
						>
							<Icon name="check" size={14} />
							{pendingKey === key ? "Confirming…" : "Confirm deletion happened"}
						</Button>
						<Button
							variant="ghost"
							size="sm"
							data-testid="deletion-recovery-retain"
							onclick={() => onRetain(record)}
						>
							<Icon name="lock" size={14} />
							Retain record
						</Button>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<style>
	.deletion-recovery-list > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.deletion-recovery-list {
		border-color: var(--border-muted);
	}

	.deletion-recovery-details {
		display: grid;
		grid-template-columns: minmax(0, auto) minmax(0, 1fr);
		column-gap: var(--space-sm);
		row-gap: var(--space-2xs);
	}

	.deletion-recovery-value {
		word-break: break-all;
	}

	.deletion-recovery-reason {
		overflow-wrap: break-word;
	}

	@media (min-width: 48rem) {
		.deletion-recovery-row {
			flex-direction: row;
			align-items: flex-start;
			justify-content: space-between;
		}
	}
</style>
