<script lang="ts">
import type { Project, Schedule, WsResult } from "@pixie/contracts";
import { onMount, untrack } from "svelte";
import Button from "../components/button.svelte";
import { errorText, getTransport } from "../connection";
import { scheduleTime } from "./schedules-model";

let { project, job, disabled, onSave, onCancel }: {
	project: Project; job: Schedule | null; disabled: boolean;
	onSave: (values: { prompt: string; cron: string; timezone: string }) => Promise<void>;
	onCancel: () => void;
} = $props();
// The parent keys each edit, so polling never replaces an unsaved draft.
let prompt = $state(untrack(() => job?.prompt ?? ""));
let cron = $state(untrack(() => job?.cron ?? "0 9 * * 1-5"));
let timezone = $state(untrack(() => job?.timezone ?? "UTC"));
let preview = $state<WsResult<"schedule.preview"> | null>(null);
let error = $state("");
let checking = $state(false);
let generation = 0;
const id = $props.id();
let promptElement: HTMLTextAreaElement;
onMount(() => promptElement.focus());
$effect(() => {
	void cron; void timezone;
	generation++;
	preview = null;
	error = "";
	checking = false;
});
$effect(() => () => { generation++; });
async function checkTiming(): Promise<void> {
	const current = ++generation;
	checking = true;
	error = "";
	try {
		const result = await getTransport().request("schedule.preview", { projectId: project.id, root: project.roots[0] ?? "", cron, timezone });
		if (current === generation) preview = result;
	} catch (cause) {
		if (current === generation) { preview = null; error = errorText(cause); }
	} finally {
		if (current === generation) checking = false;
	}
}
</script>

<form class="flex min-w-0 flex-col gap-md" onsubmit={(event) => { event.preventDefault(); void onSave({ prompt, cron, timezone }); }}>
	<h3 class="tr-title-entity">{job ? "Edit schedule" : "Create schedule"}</h3>
	<fieldset {disabled} class="flex min-w-0 flex-col gap-md">
		<legend class="sr-only">Schedule definition</legend>
		<div class="field"><label for={`${id}-prompt`}>Prompt</label><textarea bind:this={promptElement} id={`${id}-prompt`} name="prompt" class="textarea" rows="5" required bind:value={prompt}></textarea></div>
		<div class="field"><label for={`${id}-cron`}>Cron expression</label><input id={`${id}-cron`} name="cron" class="text-field-input" required bind:value={cron} aria-describedby={`${id}-timing`} /></div>
		<div class="field"><label for={`${id}-zone`}>Timezone</label><input id={`${id}-zone`} name="timezone" class="text-field-input" bind:value={timezone} aria-describedby={`${id}-timing`} /></div>
		<p id={`${id}-timing`} class="tr-text-metadata text-text-muted">Five fields: minute, hour, day of month, month, day of week. Use an IANA timezone such as Europe/Warsaw. An empty timezone uses UTC. Pixie validates timing and daylight-saving behavior.</p>
		<Button variant="outline" disabled={checking} onclick={() => void checkTiming()}>Check next occurrence</Button>
		{#if checking}<p role="status">Checking timing…</p>{/if}
		{#if error}<p role="alert" class="tr-text-ui text-feedback-error">{error}</p>{/if}
		{#if preview}<p role="status" class="tr-text-ui">Next occurrence: {scheduleTime(preview.nextRun, preview.timezone)} ({preview.timezone}). Saving recalculates this time.</p>{/if}
		<p class="tr-text-metadata text-text-muted">{job?.model ? `Model: ${job.model.provider}/${job.model.id}` : "Uses the native Pi default model."} {job ? "Editing keeps the current pause state and execution history." : "A saved schedule is enabled immediately."}</p>
		<div class="flex flex-wrap gap-sm"><Button variant="outline" onclick={onCancel}>Cancel</Button><Button type="submit">Save schedule</Button></div>
	</fieldset>
</form>
