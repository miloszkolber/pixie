<script lang="ts">
import type { SessionModeState } from "@pixie/contracts";
import { errorText, getTransport } from "../../connection";
import { appStoreApi } from "../../store";

interface Props {
	sessionId: string;
	modes: SessionModeState | null;
}
let { sessionId, modes }: Props = $props();
let requestedModeId = $state<string | null>(null);
const componentId = $props.id();
const descriptionId = `session-mode-description-${componentId}`;
let currentMode = $derived(modes?.availableModes.find((mode) => mode.id === modes?.currentModeId));

$effect(() => {
	if (requestedModeId === modes?.currentModeId) requestedModeId = null;
});

function changeMode(modeId: string): void {
	if (!modes || modeId === modes.currentModeId) return;
	requestedModeId = modeId;
	void getTransport()
		.request("session.setMode", { sessionId, modeId })
		.catch((cause) => {
			requestedModeId = null;
			appStoreApi.getState().pushToast({
				variant: "error",
				message: errorText(cause),
				title: "Couldn't change the session mode",
			});
		});
}
</script>

{#if modes && modes.availableModes.length > 0}
	<select
		data-testid="session-mode-trigger"
		aria-label="Session mode"
		aria-describedby={currentMode?.description ? descriptionId : undefined}
		aria-busy={requestedModeId !== null}
		title={currentMode?.description ?? "Change the agent mode for this session"}
		value={modes.currentModeId}
		disabled={requestedModeId !== null}
		onchange={(event) => changeMode(event.currentTarget.value)}
		class="min-w-0 max-w-32 rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-xs py-0.5 text-text-default tr-text-metadata outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary disabled:text-text-muted"
	>
		{#each modes.availableModes as mode (mode.id)}
			<option value={mode.id} title={mode.description}>{mode.name}</option>
		{/each}
	</select>
	{#if currentMode?.description}<span id={descriptionId} class="sr-only">{currentMode.description}</span>{/if}
{/if}
