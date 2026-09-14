<script lang="ts">
import type { Project } from "@pixie/shared";
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

<section aria-label="Schedules" data-testid="schedules-list" class="u-flex u-min-w-0 u-flex-col u-gap-sm">
	<p class="tr-text-metadata u-text-text-muted">Pixie must be running to dispatch schedules. Missed occurrences coalesce into one run. Runs never overlap for the same schedule.</p>
	<label class="schedule-filter u-flex u-items-center u-gap-sm u-rounded u-border u-border-border-default u-bg-control-bg u-px-md u-py-sm">
		<span class="u-sr-only">Filter schedules</span>
		<input
			type="search"
			data-testid="schedules-filter"
			aria-label="Filter schedules"
			placeholder="Filter schedules…"
			class="schedule-filter__input u-min-w-0 u-flex-1 tr-text-ui"
			bind:value={query}
		/>
	</label>
	{#if $view.error}<p role="alert" class="tr-text-ui u-text-feedback-error">{$view.error}</p>{/if}
	{#if !$view.loaded && $view.loading}<p role="status">Loading schedules…</p>{/if}
	{#if $view.loaded && $view.jobs.length === 0}<p class="tr-text-ui u-text-text-muted">No schedules in this project.</p>{/if}
	{#if $view.loaded && $view.jobs.length > 0 && filtered.length === 0}<p class="tr-text-ui u-text-text-muted">No schedules match this filter.</p>{/if}
	<ul class="schedule-list__items u-min-w-0" aria-label="Schedules">
		{#each filtered as job (job.id)}
			<li>
				<button
					type="button"
					data-testid="schedule-row"
					class="schedule-row u-flex u-w-full u-min-w-0 u-flex-col u-gap-xs u-px-sm u-py-sm u-text-left"
					aria-pressed={requestedId === job.id}
					onclick={() => select(job.id)}
				>
					<span class="schedule-row__prompt tr-text-ui">{job.prompt}</span>
					<span class="tr-text-metadata u-text-text-muted">{job.paused ? "Paused" : "Enabled"} · {activeExecution(job) ? "Running" : job.runs[0]?.status ?? "Not run yet"}</span>
					<span class="schedule-value tr-text-metadata u-text-text-muted">{job.cron} · {job.timezone}</span>
					<span class="tr-text-metadata u-text-text-muted">{job.paused ? "Next dispatch paused" : `Next: ${scheduleTime(job.nextRun, job.timezone)}`}</span>
				</button>
			</li>
		{/each}
	</ul>
</section>

<style>
	.schedule-filter__input {
		background: transparent;
		color: var(--text-default);
		outline: none;
	}

	.schedule-filter__input::placeholder {
		color: var(--text-muted);
	}

	.schedule-filter:focus-within {
		outline: var(--focus-ring-width, 2px) solid var(--border-focus);
		outline-offset: -1px;
	}

	.schedule-list__items > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.schedule-row__prompt {
		display: -webkit-box;
		overflow: hidden;
		overflow-wrap: break-word;
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 2;
		line-clamp: 2;
	}

	.schedule-value {
		word-break: break-all;
	}

	.schedule-row[aria-pressed="true"] { background: var(--surface-selected); }
	.schedule-row:hover { background: var(--surface-hover); }
	.schedule-row:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); outline-offset: -1px; }
</style>
