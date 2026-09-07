<script lang="ts">
import {
	normalizeProjectName,
	PROJECT_ICONS,
	PROJECT_NAME_MAX_LENGTH,
	type Project,
	type ProjectIcon as ProjectIconName,
} from "@pixie/contracts";
import Button from "../../components/button.svelte";
import Dialog from "../../components/dialog.svelte";
import { errorText, getTransport } from "../../connection";
import { appStoreApi, toast } from "../../store";
import ProjectIcon from "./project-icon.svelte";

const ICON_LABELS: Record<ProjectIconName, string> = {
	folder: "Folder",
	code: "Code",
	book: "Book",
	flask: "Experiment",
	rocket: "Rocket",
	sparkles: "Sparkles",
};

interface Props {
	project: Project;
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
}

let { project, open = $bindable(false), onOpenChange }: Props = $props();
let name = $state("");
let icon = $state<ProjectIconName>("folder");
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
	name = project.name;
	icon = project.icon ?? "folder";
	error = null;
});

function submit(event: SubmitEvent): void {
	event.preventDefault();
	let normalized: string;
	try {
		normalized = normalizeProjectName(name);
	} catch (cause) {
		error = errorText(cause);
		return;
	}
	busy = true;
	error = null;
	void getTransport()
		.request("project.update", { id: project.id, name: normalized, icon })
		.then((updated) => {
			appStoreApi.getState().applyProjectUpdated(updated);
			open = false;
			onOpenChange?.(false);
		})
		.catch((cause) => {
			error = errorText(cause);
			toast.error(errorText(cause), "Couldn't update the project");
		})
		.finally(() => {
			busy = false;
		});
}
</script>

<Dialog
	open={open}
	title="Customize project"
	description="Choose the name and icon shown in Pixie."
	testid="project-customization-dialog"
	class="max-w-[26rem]"
	onOpenChange={setOpen}
>
	<form id="project-customization-form" class="form flex flex-col gap-lg" onsubmit={submit}>
		<label class="text-field">
			<span class="text-field-label">Name</span>
			<!-- svelte-ignore a11y_autofocus (A newly opened customization dialog starts in its primary field.) -->
			<input
					class="text-field-input"
					autofocus
				bind:value={name}
				maxlength={PROJECT_NAME_MAX_LENGTH}
				disabled={busy}
			/>
		</label>
		<fieldset class="radio-group" disabled={busy}>
			<legend class="field-label">Icon</legend>
			<div class="grid grid-cols-3 gap-xs">
				{#each PROJECT_ICONS as candidate}
					<label class="radio-card">
						<input
							type="radio"
							class="radio"
							name="project-icon"
							value={candidate}
							checked={candidate === icon}
							onchange={() => (icon = candidate)}
						/>
						<span class="flex items-center gap-xs">
							<ProjectIcon icon={candidate} size={14} /> {ICON_LABELS[candidate]}
						</span>
					</label>
				{/each}
			</div>
		</fieldset>
		{#if error}<p role="alert" class="field-error">{error}</p>{/if}
	</form>
	{#snippet actions()}
		<Button variant="outline" disabled={busy} onclick={() => setOpen(false)}>Cancel</Button>
		<Button
			type="submit"
			form="project-customization-form"
			disabled={busy || !name.trim()}
			data-testid="project-customization-save"
		>
			{busy ? "Saving…" : "Save"}
		</Button>
	{/snippet}
</Dialog>
