<script lang="ts">
import type { RuntimeDiagnosticsReport, SupportSnapshot } from "@pixie/shared";
import { onDestroy } from "svelte";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { getTransport } from "../../connection";
import { appStore, appStoreApi } from "../../store";
import {
	hostHealthSummary,
	supportSnapshotExportAvailability,
	toDiagnosticsViewModel,
} from "../diagnostics";

let report = $state<RuntimeDiagnosticsReport | null>(null);
let loading = $state(false);
let failed = $state(false);
let activeRequest: AbortController | null = null;
let exporting = $state(false);
let exportFailed = $state(false);
let activeExport: AbortController | null = null;
let loadedGeneration = $state<number | null>(null);
let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let exportAvailability = $derived(
	supportSnapshotExportAvailability(connected, $appStore.authenticationEnabled),
);
let model = $derived(toDiagnosticsViewModel(report));
let healthSummary = $derived(hostHealthSummary(model.host));
let capabilityEntries = $derived(
	model.capabilities.capabilities ? Object.entries(model.capabilities.capabilities) : [],
);
let operationEntries = $derived(
	model.capabilities.operationSet ? Object.entries(model.capabilities.operationSet) : [],
);

function booleanLabel(value: boolean | null): string {
	return value === null ? "Unknown" : value ? "Yes" : "No";
}

function countLabel(value: number | null): string {
	return value === null ? "Unknown" : value.toLocaleString();
}

async function load(generation = connectionGeneration): Promise<void> {
	if (!connected) return;
	activeRequest?.abort();
	const request = new AbortController();
	activeRequest = request;
	loading = true;
	failed = false;
	try {
		const next = await getTransport().request(
			"runtime.diagnostics",
			{},
			{ signal: request.signal, timeoutMs: 5_000 },
		);
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation)
			report = next;
	} catch {
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation)
			failed = true;
	} finally {
		if (activeRequest === request) {
			activeRequest = null;
			loading = false;
		}
	}
}

function downloadSupportSnapshot(snapshot: SupportSnapshot): void {
	const blob = new Blob([JSON.stringify(snapshot)], { type: "application/json" });
	const href = URL.createObjectURL(blob);
	const link = document.createElement("a");
	link.href = href;
	link.download = "pixie-support-snapshot.json";
	link.click();
	setTimeout(() => URL.revokeObjectURL(href), 0);
}

async function exportSnapshot(generation = connectionGeneration): Promise<void> {
	if (!exportAvailability.available) return;
	activeExport?.abort();
	const request = new AbortController();
	activeExport = request;
	exporting = true;
	exportFailed = false;
	try {
		const snapshot = await getTransport().request(
			"runtime.supportSnapshot",
			{},
			{ signal: request.signal, timeoutMs: 5_000 },
		);
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation) {
			downloadSupportSnapshot(snapshot);
		}
	} catch {
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation) {
			exportFailed = true;
		}
	} finally {
		if (activeExport === request) {
			activeExport = null;
			exporting = false;
		}
	}
}

$effect(() => {
	if (!connected) {
		activeRequest?.abort();
		activeRequest = null;
		activeExport?.abort();
		activeExport = null;
		loading = false;
		exporting = false;
		loadedGeneration = null;
		return;
	}
	if (loadedGeneration === connectionGeneration) return;
	loadedGeneration = connectionGeneration;
	void load(connectionGeneration);
});

onDestroy(() => {
	const request = activeRequest;
	activeRequest = null;
	request?.abort();
	const exportRequest = activeExport;
	activeExport = null;
	exportRequest?.abort();
});
</script>

<div data-testid="diagnostics-view" class="diagnostics-view u-flex u-w-full u-flex-col u-gap-lg">
	<div class="u-flex u-items-start u-justify-between u-gap-sm">
		<div class="u-min-w-0">
			<h2 class="tr-title-entity u-text-text-default">Diagnostics</h2>
			<p class="u-mt-xs u-text-text-muted tr-text-metadata">
				Negotiated capabilities, active-run count, host health, retained deletion uncertainty and remediation.
			</p>
		</div>
		<Button
			variant="ghost"
			size="sm"
			data-testid="diagnostics-refresh"
			disabled={!connected || loading}
			onclick={() => void load()}
		>
			<Icon name="refresh-cw" size={14} class={loading ? "diagnostics-refresh-icon" : ""} />
			{loading ? "Refreshing…" : "Refresh"}
		</Button>
	</div>

	<div class="u-flex u-items-start u-justify-between u-gap-sm">
		<div class="u-min-w-0">
			<h3 class="tr-text-ui u-text-text-default">Support snapshot</h3>
			<p id="diagnostics-export-status" class="u-mt-xs u-text-text-muted tr-text-metadata">
				Exports bounded controller runtime facts and recent controller request outcomes. It does not collect assistant or system logs.
			</p>
			{#if !exportAvailability.available}
				<p data-testid="diagnostics-export-unavailable" class="u-mt-xs u-text-text-muted tr-text-metadata">
					{exportAvailability.reason}
				</p>
			{:else if exportFailed}
				<p data-testid="diagnostics-export-failed" role="alert" class="u-mt-xs u-text-feedback-error tr-text-metadata">
					Couldn't export the support snapshot. Your current diagnostics are unchanged.
				</p>
			{:else if exporting}
				<p role="status" aria-live="polite" class="u-mt-xs u-text-text-muted tr-text-metadata">Preparing support snapshot…</p>
			{/if}
		</div>
		<Button
			variant="secondary"
			size="sm"
			data-testid="diagnostics-export"
			aria-describedby="diagnostics-export-status"
			disabled={!exportAvailability.available || exporting}
			onclick={() => void exportSnapshot()}
		>
			<Icon name="save" size={14} />
			{exporting ? "Preparing…" : "Export JSON"}
		</Button>
	</div>

	{#if !connected}
		<p role="alert" class="u-text-feedback-error tr-text-metadata">
			{report ? "Controller disconnected. Showing the last diagnostics." : "Controller disconnected."}
		</p>
	{:else if failed}
		<p role="alert" class="u-text-feedback-error tr-text-metadata">
			{report ? "Couldn't refresh diagnostics. Showing the last diagnostics." : "Couldn't read diagnostics."}
		</p>
	{:else if loading}
		<p role="status" aria-live="polite" class="u-text-text-muted tr-text-metadata">
			{report ? "Refreshing diagnostics…" : "Loading diagnostics…"}
		</p>
	{/if}

	<section data-testid="diagnostics-host" aria-labelledby="diagnostics-host-heading" class="card u-min-w-0 u-p-md">
		<h3 id="diagnostics-host-heading" class="tr-text-ui u-text-text-default">Host health</h3>
		<p class="u-mt-xs u-text-text-muted tr-text-metadata">{healthSummary}</p>
		<dl class="diagnostics-metrics u-mt-sm u-border-t">
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Configured</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">{booleanLabel(model.host.configured)}</dd>
			</div>
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Reachable</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">{booleanLabel(model.host.reachable)}</dd>
			</div>
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Application ready</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">{booleanLabel(model.host.applicationReady)}</dd>
			</div>
			{#if model.host.error}
				<div class="metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Host reason</dt>
					<dd class="metric__value u-text-text-default tr-text-metadata">{model.host.error}</dd>
				</div>
			{/if}
			{#if model.host.applicationError}
				<div class="metric u-min-w-0 u-py-xs">
					<dt class="u-text-text-muted tr-text-metadata">Application reason</dt>
					<dd class="metric__value u-text-text-default tr-text-metadata">{model.host.applicationError}</dd>
				</div>
			{/if}
		</dl>
	</section>

	<section data-testid="diagnostics-capabilities" aria-labelledby="diagnostics-capabilities-heading" class="card u-min-w-0 u-p-md">
		<h3 id="diagnostics-capabilities-heading" class="tr-text-ui u-text-text-default">Negotiated capabilities</h3>
		{#if model.capabilities.compatible === null}
			<p class="u-mt-xs u-text-text-muted tr-text-metadata">Capability negotiation is unknown.</p>
		{:else if model.capabilities.missingRequired?.length}
			<p class="u-mt-xs u-text-feedback-error tr-text-metadata">
				Missing required: {model.capabilities.missingRequired.join(", ")}
			</p>
		{:else}
			<p class="u-mt-xs u-text-text-muted tr-text-metadata">
				{model.capabilities.compatible ? "Compatible host contract." : "Host contract is incompatible."}
			</p>
		{/if}
		{#if model.capabilities.operations}
			<p class="u-mt-sm u-text-text-muted tr-text-metadata">
				Administration: {model.capabilities.operations.administration ? "available" : "unavailable"}
			</p>
		{/if}
		{#if capabilityEntries.length > 0}
			<dl class="diagnostics-metrics u-mt-sm u-border-t">
				{#each capabilityEntries as [name, level] (name)}
					<div class="metric u-min-w-0 u-py-xs">
						<dt class="u-text-text-muted tr-text-metadata">{name}</dt>
						<dd class="metric__value u-text-text-default tr-text-metadata">{level}</dd>
					</div>
				{/each}
			</dl>
		{/if}
		{#if operationEntries.length > 0}
			<dl class="diagnostics-metrics u-mt-sm u-border-t">
				{#each operationEntries as [name, supported] (name)}
					<div class="metric u-min-w-0 u-py-xs">
						<dt class="u-text-text-muted tr-text-metadata">{name}</dt>
						<dd class="metric__value u-text-text-default tr-text-metadata">{supported ? "Supported" : "Unsupported"}</dd>
					</div>
				{/each}
			</dl>
		{/if}
	</section>

	<section data-testid="diagnostics-runs" aria-labelledby="diagnostics-runs-heading" class="card u-min-w-0 u-p-md">
		<h3 id="diagnostics-runs-heading" class="tr-text-ui u-text-text-default">Active runs</h3>
		<dl class="diagnostics-metrics u-mt-sm u-border-t">
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Active run count</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">{countLabel(model.runs.activeCount)}</dd>
			</div>
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Schedule health</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">
					{model.schedule.state === "unknown" ? "Unknown" : model.schedule.state === "healthy" ? "Healthy" : model.schedule.reason ?? "Degraded"}
				</dd>
			</div>
			<div class="metric u-min-w-0 u-py-xs">
				<dt class="u-text-text-muted tr-text-metadata">Retained deletions</dt>
				<dd class="metric__value u-text-text-default tr-text-metadata">{countLabel(model.deletionReconciliation.count)}</dd>
			</div>
		</dl>
	</section>

	<section data-testid="diagnostics-reconciliation" aria-labelledby="diagnostics-reconciliation-heading" class="card u-min-w-0 u-p-md">
		<h3 id="diagnostics-reconciliation-heading" class="tr-text-ui u-text-text-default">Deletion reconciliation</h3>
		{#if model.deletionReconciliation.records === null}
			<p class="u-mt-sm u-text-text-muted tr-text-metadata">Deletion reconciliation is unknown.</p>
		{:else if model.deletionReconciliation.records.length === 0}
			<p class="u-mt-sm u-text-text-muted tr-text-metadata">No retained deletion records.</p>
		{:else}
			<ul class="u-mt-sm u-flex u-flex-col u-gap-sm">
				{#each model.deletionReconciliation.records as record (`${record.projectId}\0${record.sessionId}`)}
					<li class="diagnostics-record u-min-w-0 u-border-t u-pt-lg">
						<p class="u-text-text-default tr-text-metadata"><code>{record.sessionId}</code></p>
						<p class="u-mt-xs u-text-text-muted tr-text-metadata">
							{record.uncertain ? "Native deletion outcome is uncertain." : "Native deletion is confirmed; local cleanup may remain."}
						</p>
						<p class="u-mt-xs u-text-text-muted tr-text-metadata">{record.remediation}</p>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	<section data-testid="diagnostics-remediation" aria-labelledby="diagnostics-remediation-heading" class="card u-min-w-0 u-p-md">
		<h3 id="diagnostics-remediation-heading" class="tr-text-ui u-text-text-default">Remediation</h3>
		{#if model.remediation === null}
			<p class="u-mt-sm u-text-text-muted tr-text-metadata">No remediation guidance was reported.</p>
		{:else if model.remediation.length === 0}
			<p class="u-mt-sm u-text-text-muted tr-text-metadata">No remediation guidance is needed.</p>
		{:else}
			<ul class="u-mt-sm u-flex u-flex-col u-gap-xs">
				{#each model.remediation as hint (hint)}
					<li class="u-text-text-default tr-text-metadata">{hint}</li>
				{/each}
			</ul>
		{/if}
	</section>
</div>

<style>
	.diagnostics-view {
		max-inline-size: 56rem;
		margin-inline: auto;
	}

	.metric {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		align-items: baseline;
		column-gap: var(--space-sm);
	}

	.metric__value {
		text-align: right;
		overflow-wrap: break-word;
		font-variant-numeric: tabular-nums;
	}

	.diagnostics-metrics,
	.diagnostics-record {
		border-color: var(--border-muted);
	}

	.diagnostics-metrics > * + * {
		border-top: 1px solid var(--border-muted);
	}

	:global(.diagnostics-refresh-icon) {
		animation: diagnostics-refresh-rotate 1s linear infinite;
	}

	@keyframes diagnostics-refresh-rotate {
		to { transform: rotate(1turn); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.diagnostics-refresh-icon) { animation: none; }
	}
</style>
