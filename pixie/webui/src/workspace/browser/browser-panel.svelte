<script lang="ts">
import type { BrowserPanelAction, BrowserPanelResult } from "@pixie/contracts";
import { safeBrowserURL } from "@pixie/contracts";
import { untrack } from "svelte";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, newBrowserPanelViewState } from "../../store";
import { browserPanelScreenState, snapshotReferences } from "./browser-panel-state";

interface Props {
	panelId: string;
	onRestart: () => void | Promise<void>;
}
let { panelId, onRestart }: Props = $props();
let panel = $derived($appStore.browserPanelStateById[panelId] ?? newBrowserPanelViewState());
let references = $derived(snapshotReferences(panel.snapshot));
let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let observedConnectionGeneration = $state(
	untrack(() => appStoreApi.getState().connectionGeneration),
);
let reconnectProbe = 0;
let expired = $state(false);
let restartInFlight = $state(false);
let controlsDisabled = $derived(panel.loading || !connected || expired);
let screenState = $derived(
	browserPanelScreenState(
		panel.loading,
		!connected ? "Controller disconnected" : expired ? "Browser session expired" : panel.error,
		panel.screenshot,
	),
);

function setPanel(patch: Partial<typeof panel>): void {
	appStoreApi.getState().setBrowserPanelState(panelId, patch);
}

async function command(action: BrowserPanelAction): Promise<BrowserPanelResult | null> {
	const generation = appStoreApi.getState().beginBrowserPanelRequest(panelId);
	try {
		const result = await getTransport().request("browser.panelCommand", { panelId, action });
		const completed = appStoreApi.getState().completeBrowserPanelRequest(panelId, generation, {
			...(action.type === "snapshot" ? { snapshot: result.output, reference: "" } : {}),
			...(result.screenshotUrl ? { screenshot: result.screenshotUrl } : {}),
		});
		return completed ? result : null;
	} catch (cause) {
		appStoreApi
			.getState()
			.completeBrowserPanelRequest(panelId, generation, { error: errorText(cause) });
		return null;
	}
}

async function runWithScreenshot(action: BrowserPanelAction): Promise<void> {
	setPanel({ snapshot: "", reference: "" });
	if (!(await command(action))) return;
	await command({ type: "screenshot" });
}

async function restart(): Promise<void> {
	if (restartInFlight) return;
	restartInFlight = true;
	try {
		await onRestart();
	} finally {
		restartInFlight = false;
	}
}

$effect(() => {
	const generation = connectionGeneration;
	if (!connected || generation === observedConnectionGeneration) return;
	observedConnectionGeneration = generation;
	const probe = ++reconnectProbe;
	void command({ type: "screenshot" }).then((result) => {
		if (probe === reconnectProbe && appStoreApi.getState().connectionGeneration === generation) {
			expired = result === null;
		}
	});
});

function open(event: SubmitEvent): void {
	event.preventDefault();
	const url = safeBrowserURL(panel.address.trim());
	if (!url) {
		setPanel({ error: "Enter a plain http:// or https:// URL without credentials." });
		return;
	}
	setPanel({ address: url });
	void runWithScreenshot({ type: "open", url });
}
</script>

<div
	data-testid="browser-panel"
	data-state={screenState}
	class="app-content flex h-full min-h-0 flex-col"
>
	<form onsubmit={open} class="toolbar flex shrink-0 flex-wrap items-center gap-xs border-b p-sm">
		<Button variant="ghost" size="icon-sm" aria-label="Back" disabled={controlsDisabled} onclick={() => void runWithScreenshot({ type: "back" })}>
			<Icon name="chevron-left" size={16} />
		</Button>
		<Button variant="ghost" size="icon-sm" aria-label="Forward" disabled={controlsDisabled} onclick={() => void runWithScreenshot({ type: "forward" })}>
			<Icon name="chevron-right" size={16} />
		</Button>
		<Button variant="ghost" size="icon-sm" aria-label="Reload" disabled={controlsDisabled} onclick={() => void runWithScreenshot({ type: "reload" })}>
			<Icon name="refresh-cw" size={16} />
		</Button>
		<label class="visually-hidden" for={`browser-address-${panelId}`}>Requested address</label>
		<input
			id={`browser-address-${panelId}`}
			value={panel.address}
			oninput={(event) => setPanel({ address: event.currentTarget.value })}
			placeholder="https://example.com"
			inputmode="url"
			autocomplete="url"
			disabled={controlsDisabled}
			class="text-field-input min-w-[12rem] flex-1"
		/>
		<Button type="submit" size="sm" disabled={controlsDisabled}>Open</Button>
	</form>
	<div class="toolbar flex shrink-0 flex-wrap items-center gap-xs border-b p-sm">
		<Button variant="outline" size="sm" disabled={controlsDisabled} onclick={() => void command({ type: "snapshot" })}>Snapshot</Button>
		<Button variant="outline" size="sm" disabled={controlsDisabled} onclick={() => void command({ type: "screenshot" })}>
			<Icon name="camera" size={14} /> Screenshot
		</Button>
		<label class="field flex-row items-center tr-text-metadata text-text-muted">
			<span class="field-label">Viewport</span>
			<input
				aria-label="Viewport width"
				type="number"
				min="320"
				max="1920"
				value={panel.viewport.width}
				oninput={(event) => setPanel({ viewport: { ...panel.viewport, width: Number(event.currentTarget.value) } })}
				class="text-field-input w-20"
			/>
			<span aria-hidden="true">×</span>
			<input
				aria-label="Viewport height"
				type="number"
				min="240"
				max="1200"
				value={panel.viewport.height}
				oninput={(event) => setPanel({ viewport: { ...panel.viewport, height: Number(event.currentTarget.value) } })}
				class="text-field-input w-20"
			/>
		</label>
		<Button variant="outline" size="sm" disabled={controlsDisabled} onclick={() => void command({ type: "viewport", ...panel.viewport })}>Apply</Button>
		{#if panel.loading}
			<span role="status" class="inline-flex items-center gap-xs text-text-muted tr-text-metadata">
				<Icon name="loader-circle" size={14} class="animate-spin" /> Working…
			</span>
		{/if}
	</div>
	{#if !connected}
		<div role="alert" class="callout shrink-0" data-variant="warning">
			<div class="callout-content"><p class="callout-description">Controller disconnected. Browser controls will recover after reconnection.</p></div>
		</div>
		{:else if expired}
			<div role="alert" class="callout shrink-0" data-variant="warning">
				<div class="callout-content flex items-center justify-between gap-sm">
					<p class="callout-description">This browser session ended while Pixie was disconnected.</p>
					<Button variant="outline" size="sm" disabled={restartInFlight} onclick={() => void restart()}>Restart browser</Button>
				</div>
		</div>
	{:else if panel.error}
		<div role="alert" class="callout shrink-0" data-variant="destructive">
			<div class="callout-content"><p class="callout-description">{panel.error}</p></div>
		</div>
	{/if}
	<div class="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[minmax(0,1fr)_20rem]">
		<section aria-label="Latest browser screenshot" class="flex min-h-0 items-center justify-center overflow-auto p-md">
			{#if panel.screenshot}
				<img src={panel.screenshot} onerror={() => setPanel({ screenshot: null, error: "The screenshot could not be loaded. Request a new screenshot." })} alt="Latest browser screenshot" class="image max-h-full max-w-full object-contain" />
			{:else}
				<p class="app-empty text-center">Open a URL, then use Screenshot to render the current page.</p>
			{/if}
		</section>
		<section aria-label="Snapshot and interactions" class="min-h-0 overflow-auto border-t p-sm lg:border-t-0 lg:border-l">
			<h2 class="tr-text-ui text-text-default">Snapshot</h2>
			{#if panel.snapshot}
				<textarea readonly aria-label="Browser snapshot output" value={panel.snapshot} rows={8} class="textarea mt-xs max-h-48 w-full resize-none overflow-auto tr-code-text"></textarea>
			{:else}
				<p class="mt-xs text-text-muted tr-text-metadata">Take a snapshot to inspect available element references.</p>
			{/if}
			<div class="mt-md flex flex-col gap-xs">
				<label class="field-label" for={`browser-ref-${panelId}`}>Snapshot reference</label>
				<input
					id={`browser-ref-${panelId}`}
					value={panel.reference}
					oninput={(event) => setPanel({ reference: event.currentTarget.value })}
					placeholder="@element"
					class="text-field-input"
				/>
				<div class="flex gap-xs">
					<Button variant="outline" size="sm" disabled={controlsDisabled || !panel.reference} onclick={() => void runWithScreenshot({ type: "click", ref: panel.reference })}>Click</Button>
					<input
						aria-label="Text to fill"
						value={panel.fillText}
						oninput={(event) => setPanel({ fillText: event.currentTarget.value })}
						placeholder="Text to fill"
						class="text-field-input min-w-0 flex-1"
					/>
					<Button
						variant="outline"
						size="sm"
						disabled={controlsDisabled || !panel.reference}
						onclick={() => void runWithScreenshot({ type: "fill", ref: panel.reference, text: panel.fillText })}
					>Fill</Button>
				</div>
				{#if references.length > 0}
					<fieldset class="flex flex-wrap gap-xs">
						<legend class="visually-hidden">Snapshot references</legend>
						{#each references as reference}
							<Button variant="outline" size="sm" onclick={() => setPanel({ reference })} class="tr-code-text text-primary">{reference}</Button>
						{/each}
					</fieldset>
				{/if}
			</div>
		</section>
	</div>
</div>
