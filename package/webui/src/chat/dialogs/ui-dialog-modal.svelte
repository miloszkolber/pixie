<script lang="ts">
import type { UiDialogRequest } from "@pixie/contracts";
import { onDestroy } from "svelte";
import Button from "../../components/button.svelte";
import Dialog from "../../components/dialog.svelte";
import { errorText, getTransport } from "../../connection";
import { appStoreApi } from "../../store";

interface Props {
	request: UiDialogRequest;
}

let { request }: Props = $props();

let open = $state(true);
let busy = $state(false);
let error = $state<string | null>(null);
let settled = $state(false);
let draft = $state("");
let seenRequestId = $state("");

// Remote settlement, navigation and reconnect remove the presenter, not the
// native request. Only explicit user dismissal may send cancellation.
onDestroy(() => {
	settled = true;
});

function resetFor(requestId: string, prefill: string | undefined): void {
	if (seenRequestId === requestId) return;
	seenRequestId = requestId;
	open = true;
	busy = false;
	error = null;
	settled = false;
	draft = prefill ?? "";
}

$effect(() => {
	resetFor(request.requestId, request.prefill);
});

function done(): void {
	settled = true;
	appStoreApi.getState().dismissUiDialog(request.sessionId, request.requestId);
	open = false;
}

async function reply(result: { value?: string | boolean; cancelled: boolean }): Promise<void> {
	if (settled || busy) return;
	busy = true;
	error = null;
	try {
		await getTransport().request("session.uiReply", {
			sessionId: request.sessionId,
			requestId: request.requestId,
			result,
		});
		done();
	} catch (cause) {
		error = errorText(cause);
	} finally {
		busy = false;
	}
}

function cancel(): void {
	if (settled || busy) return;
	// A dismissed dialog is a cancelled answer. The controller rejects replays,
	// so a lost race with a submitted answer is safe to ignore.
	settled = true;
	const { sessionId, requestId } = request;
	appStoreApi.getState().dismissUiDialog(sessionId, requestId);
	void getTransport()
		.request("session.uiReply", { sessionId, requestId, result: { cancelled: true } })
		.catch(() => {});
}

function setOpen(next: boolean): boolean {
	if (busy && !next) return false;
	if (!next) {
		if (!settled) cancel();
		else open = false;
		return true;
	}
	open = next;
	return true;
}

function submit(event: SubmitEvent): void {
	event.preventDefault();
	void reply({ value: draft, cancelled: false });
}
</script>

{#if request.primitive === "select"}
	<Dialog
		open={open}
		title={request.title}
		description={request.message}
		testid="ui-dialog"
		class="max-w-[28rem]"
		onOpenChange={setOpen}
	>
		{#if error}<p role="alert" class="field-error">{error} You can retry.</p>{/if}
		<div class="option-list">
			{#each (request.options ?? []) as option}
				<Button
					variant="outline"
					disabled={busy}
					data-testid="ui-dialog-option"
					onclick={() => void reply({ value: option, cancelled: false })}
				>
					{option}
				</Button>
			{/each}
		</div>
		{#snippet actions()}
			<Button variant="outline" disabled={busy} data-testid="ui-dialog-cancel" onclick={cancel}>
				Cancel
			</Button>
		{/snippet}
	</Dialog>
{:else if request.primitive === "confirm"}
	<Dialog
		open={open}
		role="alertdialog"
		title={request.title}
		description={request.message}
		hideClose
		testid="ui-dialog"
		class="max-w-[24rem]"
		onOpenChange={setOpen}
	>
		{#if error}<p role="alert" class="field-error">{error} You can retry.</p>{/if}
		{#snippet actions()}
			<Button variant="outline" disabled={busy} data-testid="ui-dialog-cancel" onclick={cancel}>
				Cancel
			</Button>
			<Button
				disabled={busy}
				data-testid="ui-dialog-submit"
				onclick={() => void reply({ value: true, cancelled: false })}
			>
				{busy ? "Working…" : "Confirm"}
			</Button>
		{/snippet}
	</Dialog>
{:else}
	<Dialog
		open={open}
		title={request.title}
		description={request.message}
		testid="ui-dialog"
		class="max-w-[28rem]"
		onOpenChange={setOpen}
	>
		<form id="ui-dialog-form" class="form" onsubmit={submit}>
			<label class="text-field">
				<span class="text-field-label">{request.primitive === "editor" ? "Answer" : "Value"}</span>
				{#if request.primitive === "editor"}
					<!-- svelte-ignore a11y_autofocus (A newly opened dialog starts in its primary field.) -->
					<textarea
						class="text-field-input"
						rows={6}
						maxlength={8000}
						bind:value={draft}
						placeholder={request.placeholder}
						disabled={busy}
						data-testid="ui-dialog-input"
						autofocus
					></textarea>
				{:else}
					<!-- svelte-ignore a11y_autofocus (A newly opened dialog starts in its primary field.) -->
					<input
						maxlength={8000}
						class="text-field-input"
						bind:value={draft}
						placeholder={request.placeholder}
						disabled={busy}
						data-testid="ui-dialog-input"
						autofocus
					/>
				{/if}
			</label>
			{#if error}<p role="alert" class="field-error">{error} You can retry.</p>{/if}
		</form>
		{#snippet actions()}
			<Button variant="outline" disabled={busy} data-testid="ui-dialog-cancel" onclick={cancel}>
				Cancel
			</Button>
			<Button
				type="submit"
				form="ui-dialog-form"
				disabled={busy}
				data-testid="ui-dialog-submit"
			>
				{busy ? "Sending…" : "Send"}
			</Button>
		{/snippet}
	</Dialog>
{/if}

<style>
	.option-list {
		display: flex;
		flex-direction: column;
		gap: var(--space-xs);
	}
	:global(dialog[data-testid="ui-dialog"]) {
		width: calc(100% - 2rem);
		max-height: calc(100dvh - 2rem);
		overflow-y: auto;
		overflow-wrap: anywhere;
	}
	.option-list :global(button) { white-space: normal; overflow-wrap: anywhere; }
</style>
