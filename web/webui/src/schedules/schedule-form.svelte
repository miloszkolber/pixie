<script lang="ts">
import type { Project, Schedule, WsResult } from "@pixie/shared";
import { onMount, untrack } from "svelte";
import Button from "../components/button.svelte";
import { errorText, getTransport } from "../connection";
import { scheduleTime } from "./schedules-model";

let {
	project,
	job,
	disabled,
	onSave,
	onCancel,
}: {
	project: Project;
	job: Schedule | null;
	disabled: boolean;
	onSave: (values: {
		prompt: string;
		cron: string;
		timezone: string;
		maxRuntimeSeconds?: number | null;
	}) => Promise<void>;
	onCancel: () => void;
} = $props();
// The parent keys each edit, so polling never replaces an unsaved draft.
let prompt = $state(untrack(() => job?.prompt ?? ""));
let cron = $state(untrack(() => job?.cron ?? "0 9 * * 1-5"));
let timezone = $state(untrack(() => job?.timezone ?? "UTC"));
let runtimeMode = $state<"default" | "budget" | "unlimited">(
	untrack(() => (job ? (job.maxRuntimeSeconds == null ? "unlimited" : "budget") : "default")),
);
let maxRuntimeHours = $state(untrack(() => (job?.maxRuntimeSeconds ?? 24 * 60 * 60) / 3600));
let preview = $state<WsResult<"schedule.preview"> | null>(null);
let error = $state("");
let checking = $state(false);
let generation = 0;
const id = $props.id();
let promptElement: HTMLTextAreaElement;
onMount(() => promptElement.focus());
$effect(() => {
	void cron;
	void timezone;
	generation++;
	preview = null;
	error = "";
	checking = false;
});
$effect(() => () => {
	generation++;
});
async function checkTiming(): Promise<void> {
	const current = ++generation;
	checking = true;
	error = "";
	try {
		const result = await getTransport().request("schedule.preview", {
			projectId: project.id,
			root: project.roots[0] ?? "",
			cron,
			timezone,
		});
		if (current === generation) preview = result;
	} catch (cause) {
		if (current === generation) {
			preview = null;
			error = errorText(cause);
		}
	} finally {
		if (current === generation) checking = false;
	}
}

function save(): void {
	if (runtimeMode === "budget" && (!Number.isFinite(maxRuntimeHours) || maxRuntimeHours <= 0)) {
		error = "Enter a maximum runtime greater than zero, or select Unlimited.";
		return;
	}
	const maxRuntimeSeconds =
		runtimeMode === "default"
			? undefined
			: runtimeMode === "unlimited"
				? null
				: Math.round(maxRuntimeHours * 3600);
	if (typeof maxRuntimeSeconds === "number" && maxRuntimeSeconds < 1) {
		error = "Enter a maximum runtime of at least one second, or select Unlimited.";
		return;
	}
	error = "";
	void onSave(
		maxRuntimeSeconds === undefined
			? { prompt, cron, timezone }
			: { prompt, cron, timezone, maxRuntimeSeconds },
	);
}
</script>

<form class="u-flex u-min-w-0 u-flex-col u-gap-md" onsubmit={(event) => { event.preventDefault(); save(); }}>
	<h3 class="tr-title-entity">{job ? "Edit schedule" : "Create schedule"}</h3>
	<fieldset {disabled} class="u-flex u-min-w-0 u-flex-col u-gap-md">
		<legend class="u-sr-only">Schedule definition</legend>
		<div class="field"><label for={`${id}-prompt`}>Prompt</label><textarea bind:this={promptElement} id={`${id}-prompt`} name="prompt" class="textarea" rows="5" required bind:value={prompt}></textarea></div>
		<div class="field"><label for={`${id}-cron`}>Cron expression</label><input id={`${id}-cron`} name="cron" class="text-field-input" required bind:value={cron} aria-describedby={`${id}-timing`} /></div>
		<div class="field"><label for={`${id}-zone`}>Timezone</label><input id={`${id}-zone`} name="timezone" class="text-field-input" bind:value={timezone} aria-describedby={`${id}-timing`} /></div>
		<p id={`${id}-timing`} class="tr-text-metadata u-text-text-muted">Five fields: minute, hour, day of month, month, day of week. Use an IANA timezone such as Europe/Warsaw. An empty timezone uses UTC. Pixie validates timing and daylight-saving behavior.</p>
		<fieldset class="field u-flex u-flex-col u-gap-xs"><legend>Maximum runtime</legend>{#if !job}<label class="u-flex u-items-center u-gap-sm"><input type="radio" value="default" bind:group={runtimeMode} /> Use controller default</label>{/if}<label class="u-flex u-items-center u-gap-sm"><input type="radio" value="budget" bind:group={runtimeMode} /> Set a budget</label><label class="u-flex u-items-center u-gap-sm"><input type="radio" value="unlimited" bind:group={runtimeMode} /> Unlimited</label>{#if runtimeMode === "budget"}<input id={`${id}-max-runtime`} name="maxRuntimeHours" class="text-field-input" type="number" min="0.001" step="0.001" bind:value={maxRuntimeHours} aria-describedby={`${id}-max-runtime-help`} />{/if}<p id={`${id}-max-runtime-help`} class="tr-text-metadata u-text-text-muted">A budget applies to this schedule only. Enter hours; Pixie stores whole seconds. The controller default may be configured by the operator. Unlimited does not set a deadline.</p></fieldset>
		<Button variant="outline" disabled={checking} onclick={() => void checkTiming()}>Check next occurrence</Button>
		{#if checking}<p role="status">Checking timing…</p>{/if}
		{#if error}<p role="alert" class="tr-text-ui u-text-feedback-error">{error}</p>{/if}
		{#if preview}<p role="status" class="tr-text-ui">Next occurrence: {scheduleTime(preview.nextRun, preview.timezone)} ({preview.timezone}). Saving recalculates this time.</p>{/if}
		<p class="tr-text-metadata u-text-text-muted">{job?.model ? `Model: ${job.model.provider}/${job.model.id}` : "Uses the native Pi default model."} {job ? "Editing keeps the current pause state and execution history." : "A saved schedule is enabled immediately with the configured runtime budget."}</p>
		<div class="u-flex u-flex-wrap u-gap-sm"><Button variant="outline" onclick={onCancel}>Cancel</Button><Button type="submit">Save schedule</Button></div>
	</fieldset>
</form>
