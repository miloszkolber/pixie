<script lang="ts">
	import type { Project, Schedule } from "@pixie/contracts";
	import { untrack } from "svelte";
	import Button from "../components/button.svelte";
	import Dialog from "../components/dialog.svelte";
	import { getTransport } from "../connection";
	import { appStore, appStoreApi, selectPrimary } from "../store";
	import ScheduleForm from "./schedule-form.svelte";
	import {
		activeExecution,
		SchedulesModel,
		scheduleSessionHref,
		scheduleTime,
	} from "./schedules-model";
	import { resolveScheduleSelection } from "./schedules-workspace";

	let { project }: { project: Project } = $props();
	const model = new SchedulesModel(
		untrack(() => project.id),
		getTransport(),
	);
	const view = model.readable;
	let resolved = $derived(
		resolveScheduleSelection(
			$view.jobs,
			$appStore.workspaceSelection.primarySelection,
			project.id,
		),
	);
	let selected = $derived(resolved.job);
	let missing = $derived(resolved.missing);
	let requestedId = $derived(resolved.requestedId);
	let active = $derived(selected ? activeExecution(selected) : undefined);
	let connected = $derived($appStore.status === "connected");
	let locked = $derived(!connected || $view.busy || Boolean($view.pending));
	let editor = $state<{ job: Schedule | null } | null>(null);
	let deleting = $state<Schedule | null>(null);
	let deleteTrigger: HTMLButtonElement | null = null;

	$effect(() => {
		void $appStore.connectionGeneration;
		if (!connected) return;
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		async function poll(): Promise<void> {
			await model.load();
			if (!cancelled) timer = setTimeout(() => void poll(), 5000);
		}
		void poll();
		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	});

	function dispatchSelection(id: string): void {
		appStoreApi
			.getState()
			.dispatchWorkspaceSelection(
				selectPrimary({ kind: "schedule", scheduleId: id, projectId: project.id }, "schedules"),
			);
	}

	async function save(values: { prompt: string; cron: string; timezone: string }): Promise<void> {
		if (locked || !editor) return;
		const ok = editor.job
			? await model.mutate(
					"schedule.update",
					{ scheduleId: editor.job.id, ...values },
					"Schedule saved.",
				)
			: await model.mutate(
					"schedule.create",
					{ root: project.roots[0] ?? "", ...values },
					"Schedule created.",
				);
		if (ok) {
			const current = model.state.getState().selectedId;
			if (current) dispatchSelection(current);
			editor = null;
		}
	}
</script>

<section aria-label="Schedule details" data-testid="schedule-detail" class="flex min-w-0 flex-col gap-md">
	<div class="flex flex-wrap items-start justify-between gap-sm">
		<h3 class="tr-title-entity">Schedule details</h3>
		<div class="flex flex-wrap gap-sm">
			<Button variant="outline" disabled={!connected || $view.loading} onclick={() => void model.load()}>Refresh</Button>
			<Button data-testid="schedule-create" disabled={locked || Boolean(editor)} onclick={() => { editor = { job: null }; }}>Create schedule</Button>
		</div>
	</div>
	<p class="tr-text-metadata text-text-muted">Pixie must be running to dispatch schedules. Missed occurrences coalesce into one run. Runs never overlap for the same schedule.</p>
	{#if !connected}<p role="status" class="tr-text-ui text-text-muted">Disconnected. Showing last known state. Reconnecting refreshes the ledger and retries in-flight requests.</p>{/if}
	{#if $view.error}<p role="alert" class="tr-text-ui text-feedback-error">{$view.error}</p>{/if}
	{#if $view.healthError}<p role="alert" class="tr-text-ui text-feedback-error">Scheduler persistence or dispatch error: {$view.healthError} Execution claims remain reserved until saving succeeds. A running ledger entry may be waiting for persistence.</p>{/if}
	{#if $view.notice}<p role="status" class="tr-text-ui">{$view.notice}</p>{/if}
	{#if $view.pending}
		<div class="flex flex-col gap-sm">
			<p class="tr-text-ui">{$view.busy ? "Waiting for the schedule action to be confirmed…" : "An unconfirmed action is retained for this project. Retry it before making another change."}</p>
			<div class="flex flex-wrap gap-sm"><Button variant="outline" disabled={!connected || $view.busy} onclick={async () => { if (await model.retry()) { editor = null; deleting = null; } }}>Retry same action</Button><Button variant="ghost" disabled={$view.busy} onclick={() => { if (window.confirm("Discard this retry? The action may already be saved. This does not undo it or stop a run. Inspect the ledger before issuing another action.")) model.abandon(); }}>Discard retry</Button></div>
		</div>
	{/if}
	{#if !$view.loaded && $view.loading}
		<p role="status">Loading schedules…</p>
	{:else if editor}
		{#key editor}<ScheduleForm {project} job={editor.job} disabled={locked} onSave={save} onCancel={() => { editor = null; }} />{/key}
	{:else if missing}
		<div data-testid="schedule-missing" class="flex min-w-0 flex-col gap-sm">
			<p role="alert" class="tr-text-ui">The selected schedule is unavailable. It may have been deleted in another view.</p>
			<p class="tr-text-metadata text-text-muted">The ledger for the remaining schedules is unchanged. Choose another schedule in the list; this view does not redirect.</p>
			{#if requestedId}<p class="break-all tr-text-metadata text-text-muted">Missing schedule: {requestedId}</p>{/if}
		</div>
	{:else if !selected}
		<div data-testid="schedule-empty" class="flex min-w-0 flex-col gap-sm">
			<p class="tr-text-ui text-text-muted">Select a schedule in the list to inspect its timing, next occurrence and run ledger.</p>
			{#if $view.loaded && $view.jobs.length === 0}<p class="tr-text-ui text-text-muted">No schedules in this project.</p>{/if}
		</div>
	{:else}
		<div class="flex min-w-0 flex-col gap-md">
			<p class="whitespace-pre-wrap break-words tr-text-ui">{selected.prompt}</p>
			<dl class="tr-text-ui"><dt>Timing</dt><dd class="break-all text-text-muted">{selected.cron} · {selected.timezone}</dd><dt class="mt-sm">Next occurrence</dt><dd class="text-text-muted">{scheduleTime(selected.nextRun, selected.timezone)}{selected.paused ? " · Dispatch paused" : ""}</dd><dt class="mt-sm">Latest outcome</dt><dd>{selected.runs[0]?.status ?? "Not run yet"}</dd><dt class="mt-sm">Model</dt><dd class="break-all text-text-muted">{selected.model ? `${selected.model.provider}/${selected.model.id}` : "Native Pi default"}</dd></dl>
			<div class="flex flex-wrap gap-sm">
				<Button variant="outline" disabled={locked} onclick={() => selected && void model.mutate("schedule.update", { scheduleId: selected.id, paused: !selected.paused }, selected.paused ? "Schedule resumed. Next occurrence recalculated." : "Schedule paused. Active execution is unchanged.")}>{selected.paused ? "Resume" : "Pause"}</Button>
				<Button variant="outline" disabled={locked || Boolean(active)} onclick={() => selected && void model.mutate("schedule.runNow", { scheduleId: selected.id }, "Run requested. See the execution ledger for its outcome.")}>Run now</Button>
				<Button variant="outline" disabled={locked || !active} onclick={() => selected && void model.mutate("schedule.stop", { scheduleId: selected.id }, "Stop requested. Wait for the active execution to settle. Future dispatch is unchanged.")}>Stop execution</Button>
				<Button variant="outline" disabled={locked || Boolean(active)} onclick={() => { if (selected) editor = { job: selected }; }}>Edit</Button>
				<Button variant="destructive-outline" disabled={locked || Boolean(active)} onclick={(event) => { deleteTrigger = event.currentTarget; if (selected) deleting = selected; }}>Delete</Button>
			</div>
			<p class="tr-text-metadata text-text-muted">Run now also works while paused and keeps the pause state. It recalculates the next occurrence. Pause does not stop execution. Stop does not pause future runs. Wait for execution to settle before editing or deleting.</p>
			<h4 class="tr-text-ui">Execution ledger</h4>
			{#if !selected.runs.length}<p class="tr-text-metadata text-text-muted">No executions yet.</p>{/if}
			<ol class="divide-y divide-border-muted" data-testid="schedule-runs">
				{#each selected.runs as run (run.id)}
					{@const href = scheduleSessionHref(project.id, run)}
					<li class="flex min-w-0 flex-col gap-xs py-sm">
						<span class="tr-text-ui">{run.status === "running" ? "Active execution" : run.status} · {scheduleTime(run.startedAt, selected.timezone)}</span>
						{#if run.finishedAt}<span class="tr-text-metadata text-text-muted">Finished: {scheduleTime(run.finishedAt, selected.timezone)}</span>{/if}
						{#if run.error}<p class="break-words tr-text-ui text-feedback-error">{run.error}</p>{/if}
						{#if run.status === "interrupted"}<p class="tr-text-metadata text-text-muted">Inspect the native session before retrying. An ambiguous execution after restart pauses its schedule.</p>{/if}
						{#if href}<a class="btn self-start break-all text-left" data-variant="link" {href} onclick={() => { appStoreApi.getState().closeSettings(); }}>Open Pi session {run.sessionId}</a>{:else}<span class="tr-text-metadata text-text-muted">No native session recorded yet.</span>{/if}
					</li>
				{/each}
			</ol>
		</div>
	{/if}
</section>

<Dialog open={Boolean(deleting)} title="Delete schedule?" role="alertdialog" description="This removes the schedule and its execution ledger. Native Pi sessions are kept. Delete does not stop execution." onOpenChange={(open) => { if (!open && !$view.busy) deleting = null; return !$view.busy; }} onClosedAutoFocus={() => { deleteTrigger?.focus(); }}>
	<p class="line-clamp-3 break-words tr-text-ui">{deleting?.prompt}</p>
	{#if $view.pending}<p role="status" class="tr-text-ui">{$view.notice || "Waiting for confirmation…"} Close this confirmation to retry the same action if needed.</p>{/if}
	{#snippet actions()}
		<Button variant="outline" disabled={$view.busy} onclick={() => { deleting = null; }}>Cancel</Button>
		<Button variant="destructive" disabled={locked} onclick={async () => { if (deleting && await model.mutate("schedule.delete", { scheduleId: deleting.id }, "Schedule deleted. Native sessions were kept.")) deleting = null; }}>Delete schedule</Button>
	{/snippet}
</Dialog>
