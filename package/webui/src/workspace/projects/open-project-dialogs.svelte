<script lang="ts">
import type { Project } from "@pixie/contracts";
import NoticeDialog from "../../components/notice-dialog.svelte";
import { errorText, getTransport } from "../../connection";
import { appStoreApi } from "../../store";
import DirectoryPickerDialog from "./directory-picker-dialog.svelte";

interface Props {
	onOpened: (project: Project) => void | Promise<void>;
}

let { onOpened }: Props = $props();
let openError = $state<string | null>(null);
let pickerOpen = $state(false);
let openSequence = 0;

async function adopt(project: Project): Promise<void> {
	appStoreApi.getState().applyProjectUpdated(project);
	await onOpened(project);
}

export async function openProject(rawPath: string): Promise<void> {
	const path = rawPath.trim();
	if (!path) return;
	const sequence = ++openSequence;
	openError = null;
	try {
		const project = await getTransport().request("project.open", { path });
		if (sequence !== openSequence) return;
		await adopt(project);
	} catch (cause) {
		if (sequence === openSequence) openError = errorText(cause, `Couldn't open ${path}.`);
	}
}

export function pickAndOpen(): void {
	pickerOpen = true;
}
</script>

<DirectoryPickerDialog
	bind:open={pickerOpen}
	onSelect={(path) => {
		pickerOpen = false;
		void openProject(path);
	}}
/>
<NoticeDialog
	open={openError !== null}
	onOpenChange={(next) => {
		if (!next) openError = null;
	}}
	title="Couldn't open project"
	description={openError ?? undefined}
	testid="open-error-dialog"
/>
