<script lang="ts">
import type { SignetStatus } from "@pixie/contracts";
import { onMount } from "svelte";
import Button from "@/components/button.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";

let enabled = $state($appStore.config.signet.enabled);
let address = $state($appStore.config.signet.address);
let port = $state(String($appStore.config.signet.port));
let status = $state<SignetStatus | null>(null);
let error = $state<string | null>(null);
let saving = $state(false);

function refresh(): void {
	void getTransport()
		.request("signet.status", {})
		.then((next) => {
			status = next;
		})
		.catch(() => {
			status = null;
		});
}

onMount(refresh);

async function save(): Promise<void> {
	saving = true;
	error = null;
	try {
		const numericPort = Number(port);
		if (!Number.isInteger(numericPort) || numericPort < 1 || numericPort > 65_535) {
			throw new Error("Port must be between 1 and 65535.");
		}
		const next = await getTransport().request("settings.update", {
			config: { signet: { enabled, address, port: numericPort } },
		});
		appStoreApi.getState().applyConfig(next);
		refresh();
	} catch (failure) {
		error = errorText(failure);
	} finally {
		saving = false;
	}
}
</script>

<div class="mx-auto flex w-full max-w-[36rem] flex-col gap-lg">
	<div>
		<h2 class="tr-title-entity text-text-default">Signet memory</h2>
		<p class="mt-xs tr-text-ui text-text-muted">Memory tools from your Signet daemon are attached to new and reconnected sessions when the agent supports HTTP MCP. Existing running sessions keep their current tools.</p>
	</div>
	<label
		class="switch-item-block rounded-[var(--radius-sm)] border border-border-default p-md"
	>
		<span class="tr-text-ui text-text-default">Attach Signet memory tools</span>
		<input class="switch" type="checkbox" bind:checked={enabled} />
	</label>
	<div class="grid min-w-0 grid-cols-1 gap-sm sm:grid-cols-[minmax(0,1fr)_8rem]">
		<label class="field tr-text-metadata text-text-muted">
			Address
			<input class="text-field-input" bind:value={address} disabled={!enabled} />
		</label>
		<label class="field tr-text-metadata text-text-muted">
			Port
			<input
				class="text-field-input"
				type="number"
				min={1}
				max={65535}
				bind:value={port}
				disabled={!enabled}
			/>
		</label>
	</div>
	<div class="flex items-center justify-between gap-md">
		<span
			class={`tr-text-metadata ${status?.reachable ? "text-feedback-success" : "text-text-muted"}`}
		>
			{!status
				? "Status not checked"
				: !status.enabled ? "Disabled"
				: status.reachable
					? `Health endpoint reachable at ${status.endpoint}`
					: `Unavailable at ${status.endpoint}`}
		</span>
		<Button onclick={() => void save()} disabled={saving || !address.trim()}>
			{saving ? "Saving…" : "Save"}
		</Button>
	</div>
	<p class="tr-text-metadata text-text-muted">Health does not verify memory operations. Session tools provide on-demand memory; automatic recall and lifecycle hooks require separate Signet harness integration.</p>
	{#if error}
		<p role="alert" class="tr-text-metadata text-feedback-error">{error}</p>
	{/if}
</div>
