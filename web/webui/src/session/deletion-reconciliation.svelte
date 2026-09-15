<script lang="ts">
import type { DeletionRecovery } from "@pixie/shared";
import Button from "../components/button.svelte";
import Icon from "../components/icon.svelte";
import { deletionReconciliationKey, remediationForDeletion } from "./deletion-reconciliation";

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
	data-testid="session-deletion-reconciliation"
	aria-labelledby="session-deletion-reconciliation-heading"
	class="card u-min-w-0 u-p-md"
>
	<div class="u-min-w-0">
		<h3 id="session-deletion-reconciliation-heading" class="tr-text-ui u-text-text-default">Deletion reconciliation</h3>
		<p class="u-mt-xs u-text-text-muted tr-text-metadata">
			Unmatched or uncertain deletion tombstones. Native Pi has no delete command to
			re-check: confirm only when the deletion really happened, or retain the record
			and leave the tombstone in place. Records are never cleared automatically.
		</p>
	</div>

	{#if error}
		<p role="alert" data-testid="session-deletion-error" class="u-mt-sm u-text-feedback-error tr-text-metadata">
			{error}
		</p>
	{/if}

	{#if records.length === 0}
		<p data-testid="session-deletion-empty" class="u-mt-sm u-text-text-muted tr-text-metadata">
			No retained deletion records.
		</p>
	{:else}
		<ul
			data-testid="session-deletion-list"
			class="session-deletion-list u-mt-sm u-flex u-flex-col u-border-border-default u-border-t"
		>
			{#each records as record (deletionReconciliationKey(record))}
				{@const key = deletionReconciliationKey(record)}
				<li
					data-testid="session-deletion-row"
					data-project-id={record.projectId}
					data-session-id={record.sessionId}
					data-phase={record.phase}
					class="session-deletion-row u-flex u-min-w-0 u-flex-col u-gap-sm u-py-sm"
				>
					<dl class="session-deletion-details u-min-w-0 tr-text-metadata">
						<dt class="u-text-text-muted">Project</dt>
						<dd class="session-deletion-value u-min-w-0 u-text-text-default"><code>{record.projectId}</code></dd>
						<dt class="u-text-text-muted">Session</dt>
						<dd class="session-deletion-value u-min-w-0 u-text-text-default"><code>{record.sessionId}</code></dd>
						<dt class="u-text-text-muted">Phase</dt>
						<dd class="session-deletion-value u-min-w-0 u-text-text-default">{record.phase}</dd>
						<dt class="u-text-text-muted">Reason</dt>
						<dd class="session-deletion-reason u-min-w-0 u-text-text-default">{record.reason}</dd>
						<dt class="u-text-text-muted">Next step</dt>
						<dd class="session-deletion-reason u-min-w-0 u-text-text-default">{remediationForDeletion(record)}</dd>
					</dl>
					<div class="u-flex u-shrink-0 u-flex-wrap u-gap-sm">
						<Button
							variant="outline"
							size="sm"
							data-testid="session-deletion-confirm"
							disabled={pendingKey === key}
							onclick={() => onConfirm(record)}
						>
							<Icon name="check" size={14} />
							{pendingKey === key ? "Confirming…" : "Confirm deletion happened"}
						</Button>
						<Button
							variant="ghost"
							size="sm"
							data-testid="session-deletion-retain"
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
	.session-deletion-list > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.session-deletion-list {
		border-color: var(--border-muted);
	}

	.session-deletion-details {
		display: grid;
		grid-template-columns: minmax(0, auto) minmax(0, 1fr);
		column-gap: var(--space-sm);
		row-gap: var(--space-2xs);
	}

	.session-deletion-value {
		word-break: break-all;
	}

	.session-deletion-reason {
		overflow-wrap: break-word;
	}

	@media (min-width: 48rem) {
		.session-deletion-row {
			flex-direction: row;
			align-items: flex-start;
			justify-content: space-between;
		}
	}
</style>
