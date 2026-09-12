<script lang="ts">
import type { DeletionRecovery } from "@pixie/contracts";
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
	class="card min-w-0 p-md"
>
	<div class="min-w-0">
		<h3 id="deletion-recovery-heading" class="tr-text-ui text-text-default">Deletion recovery</h3>
		<p class="mt-xs text-text-muted tr-text-metadata">
			Retained deletion tombstones that could not be resumed. Confirm only when the deletion
			really happened, or retain the record and leave the tombstone in place.
		</p>
	</div>

	{#if error}
		<p role="alert" data-testid="deletion-recovery-error" class="mt-sm text-feedback-error tr-text-metadata">
			{error}
		</p>
	{/if}

	{#if records.length === 0}
		<p data-testid="deletion-recovery-empty" class="mt-sm text-text-muted tr-text-metadata">
			No retained deletion records.
		</p>
	{:else}
		<ul
			data-testid="deletion-recovery-list"
			class="mt-sm flex flex-col divide-y divide-border-muted border-border-muted border-t"
		>
			{#each records as record (deletionRecoveryKey(record))}
				{@const key = deletionRecoveryKey(record)}
				<li
					data-testid="deletion-recovery-row"
					data-project-id={record.projectId}
					data-session-id={record.sessionId}
					data-phase={record.phase}
					class="flex min-w-0 flex-col gap-sm py-sm md:flex-row md:items-start md:justify-between"
				>
					<dl
						class="grid min-w-0 grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-sm gap-y-2xs tr-text-metadata"
					>
						<dt class="text-text-muted">Project</dt>
						<dd class="min-w-0 break-all text-text-default"><code>{record.projectId}</code></dd>
						<dt class="text-text-muted">Session</dt>
						<dd class="min-w-0 break-all text-text-default"><code>{record.sessionId}</code></dd>
						<dt class="text-text-muted">Phase</dt>
						<dd class="min-w-0 break-all text-text-default">{record.phase}</dd>
						<dt class="text-text-muted">Reason</dt>
						<dd class="min-w-0 break-words text-text-default">{record.reason}</dd>
					</dl>
					<div class="flex shrink-0 flex-wrap gap-sm">
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
