<script lang="ts">
import { normalizeSessionTitle, SESSION_TITLE_MAX_LENGTH } from "@pixie/contracts";
import Button from "../../components/button.svelte";
import Dialog from "../../components/dialog.svelte";
import { errorText, getTransport } from "../../connection";
import { appStoreApi } from "../../store";
import type { SessionLifecycleTarget } from "./session-lifecycle";

interface Props {
	target: SessionLifecycleTarget;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
}
let { target, open = $bindable(false), onOpenChange }: Props = $props();
let title = $state("");
let error = $state<string | null>(null);
let busy = $state(false);

function setOpen(next: boolean): boolean {
	if (busy && !next) return false;
	open = next;
	onOpenChange?.(next);
	return true;
}

$effect(() => {
	if (!open) return;
	title = target.title;
	error = null;
});

function submit(event: SubmitEvent): void {
	event.preventDefault();
	let normalized: string;
	try {
		normalized = normalizeSessionTitle(title);
	} catch (cause) {
		error = errorText(cause);
		return;
	}
	busy = true;
	error = null;
	void getTransport()
		.request("session.rename", {
			projectId: target.projectId,
			sessionId: target.sessionId,
			title: normalized,
		})
		.then(() => {
			appStoreApi.getState().applySessionLifecycle({
				projectId: target.projectId,
				sessionId: target.sessionId,
				operation: "renamed",
				title: normalized,
			});
			open = false;
			onOpenChange?.(false);
		})
		.catch((cause) => {
			error = errorText(cause);
		})
		.finally(() => {
			busy = false;
		});
}
</script>

	<Dialog open={open} title="Rename chat" description="The title is stored with this agent session." testid="session-rename-dialog" class="max-w-[24rem]" onOpenChange={setOpen}>
	<form id="session-rename-form" class="form" onsubmit={submit}>
		<label class="text-field">
			<span class="text-field-label">Title</span>
			<!-- svelte-ignore a11y_autofocus (A newly opened rename dialog starts in its primary field.) -->
			<input class="text-field-input" bind:value={title} maxlength={SESSION_TITLE_MAX_LENGTH} disabled={busy} autofocus />
		</label>
		{#if error}<p role="alert" class="field-error">{error}</p>{/if}
	</form>
	{#snippet actions()}
		<Button variant="outline" disabled={busy} onclick={() => setOpen(false)}>Cancel</Button>
		<Button type="submit" form="session-rename-form" disabled={busy} data-testid="session-rename-submit">{busy ? "Renaming…" : "Rename"}</Button>
	{/snippet}
</Dialog>
