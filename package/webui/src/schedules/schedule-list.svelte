<script lang="ts">
import type { Project } from "@pixie/contracts";
import { untrack } from "svelte";
import { getTransport } from "../connection";
import { appStore, appStoreApi, selectPrimary } from "../store";
import { activeExecution, SchedulesModel, scheduleTime } from "./schedules-model";
import { filterSchedules } from "./schedules-workspace";

let { project }: { project: Project } = $props();
const model = new SchedulesModel(
	untrack(() => project.id),
	getTransport(),
);
const view = model.readable;
let query = $state("");
let connected = $derived($appStore.status === "connected");
let requestedId = $derived(
	$appStore.workspaceSelection.primarySelection?.kind === "schedule" &&
		$appStore.workspaceSelection.primarySelection.projectId === project.id
		? $appStore.workspaceSelection.primarySelection.scheduleId
		: null,
);
let filtered = $derived(filterSchedules($view.jobs, query));

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

function select(id: string): void {
	appStoreApi
		.getState()
		.dispatchWorkspaceSelection(
			selectPrimary({ kind: "schedule", scheduleId: id, projectId: project.id }, "schedules"),
		);
}
</script>

<section aria-label="Schedules" data-testid="schedules-list" class="flex min-w-0 flex-col gap-sm">
	<p class="tr-text-metadata text-text-muted">Pixie must be running to dispatch schedules. Missed occurrences coalesce into one run. Runs never overlap for the same schedule.</p>
	<label class="flex items-center gap-sm rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-md py-sm">
		<span class="sr-only">Filter schedules</span>
		<input
			type="search"
			data-testid="schedules-filter"
			aria-label="Filter schedules"
			placeholder="Filter schedules…"
			class="min-w-0 flex-1 bg-transparent tr-text-ui outline-none placeholder:text-text-muted"
			bind:value={query}
		/>
	</label>
	{#if $view.error}<p role="alert" class="tr-text-ui text-feedback-error">{$view.error}</p>{/if}
	{#if !$view.loaded && $view.loading}<p role="status">Loading schedules…</p>{/if}
	{#if $view.loaded && $view.jobs.length === 0}<p class="tr-text-ui text-text-muted">No schedules in this project.</p>{/if}
	{#if $view.loaded && $view.jobs.length > 0 && filtered.length === 0}<p class="tr-text-ui text-text-muted">No schedules match this filter.</p>{/if}
	<ul class="min-w-0 divide-y divide-border-muted" aria-label="Schedules">
		{#each filtered as job (job.id)}
			<li>
				<button
					type="button"
					data-testid="schedule-row"
					class="schedule-row flex w-full min-w-0 flex-col gap-xs p-sm text-left"
					aria-pressed={requestedId === job.id}
					onclick={() => select(job.id)}
				>
					<span class="line-clamp-2 break-words tr-text-ui">{job.prompt}</span>
					<span class="tr-text-metadata text-text-muted">{job.paused ? "Paused" : "Enabled"} · {activeExecution(job) ? "Running" : job.runs[0]?.status ?? "Not run yet"}</span>
					<span class="break-all tr-text-metadata text-text-muted">{job.cron} · {job.timezone}</span>
					<span class="tr-text-metadata text-text-muted">{job.paused ? "Next dispatch paused" : `Next: ${scheduleTime(job.nextRun, job.timezone)}`}</span>
				</button>
			</li>
		{/each}
	</ul>
</section>

<style>
	.schedule-row[aria-pressed="true"] { background: var(--surface-selected); }
	.schedule-row:hover { background: var(--surface-hover); }
</style>
