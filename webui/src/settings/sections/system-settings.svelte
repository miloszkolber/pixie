<script lang="ts">
import type {
	DeletionRecovery,
	RuntimeAgentStatus,
	RuntimeAvailability,
	RuntimeRequestMetrics,
	RuntimeServiceStatus,
	RuntimeStatusReport,
} from "@pixie/shared";
import { onDestroy } from "svelte";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";
import { deletionRecoveryKey } from "../deletion-recovery";
import DeletionRecoverySection from "./deletion-recovery.svelte";
import {
	formatBytes,
	formatCount,
	formatMilliseconds,
	formatUptime,
	STATE_LABEL,
} from "./system-settings";

let report = $state<RuntimeStatusReport | null>(null);
let loading = $state(false);
let failed = $state(false);
let activeRequest: AbortController | null = null;
let loadedGeneration = $state<number | null>(null);
let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let unavailable = $derived(!connected || failed);
let recoveryPendingKey = $state<string | null>(null);
let recoveryError = $state<string | null>(null);
let recoveryAnnouncement = $state<string | null>(null);
let recoveryRecords = $derived($appStore.deletionRecovery);

async function load(generation = connectionGeneration): Promise<void> {
	if (!connected) return;
	activeRequest?.abort();
	const request = new AbortController();
	activeRequest = request;
	loading = true;
	failed = false;
	try {
		const next = await getTransport().request(
			"runtime.status",
			{},
			{ signal: request.signal, timeoutMs: 5_000 },
		);
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation) {
			report = next;
		}
	} catch {
		if (!request.signal.aborted && appStoreApi.getState().connectionGeneration === generation) {
			failed = true;
		}
	} finally {
		if (activeRequest === request) {
			activeRequest = null;
			loading = false;
		}
	}
}

async function loadDeletionRecovery(generation = connectionGeneration): Promise<void> {
	if (!connected) return;
	try {
		const records = await getTransport().request(
			"session.deletionRecovery",
			{},
			{ timeoutMs: 5_000 },
		);
		if (appStoreApi.getState().connectionGeneration === generation)
			appStoreApi.getState().setDeletionRecovery(records);
	} catch {
		// A failed read keeps the last retained list visible instead of clearing
		// tombstones the operator has not reconciled.
	}
}

function refresh(generation = connectionGeneration): void {
	void load(generation);
	void loadDeletionRecovery(generation);
}

async function confirmDeletion(record: DeletionRecovery): Promise<void> {
	const key = deletionRecoveryKey(record);
	const generation = connectionGeneration;
	recoveryPendingKey = key;
	recoveryError = null;
	recoveryAnnouncement = null;
	try {
		await getTransport().request(
			"session.confirmExternalDeletion",
			{ projectId: record.projectId, sessionId: record.sessionId },
			{ timeoutMs: 10_000 },
		);
		if (appStoreApi.getState().connectionGeneration === generation) {
			appStoreApi.getState().removeDeletionRecovery(record.projectId, record.sessionId);
			recoveryAnnouncement = `Confirmed deletion record for ${record.sessionId}.`;
		}
	} catch (cause) {
		recoveryError = errorText(cause);
	} finally {
		if (recoveryPendingKey === key) recoveryPendingKey = null;
	}
}

async function retainDeletion(record: DeletionRecovery): Promise<void> {
	const key = deletionRecoveryKey(record);
	const generation = connectionGeneration;
	recoveryPendingKey = key;
	recoveryError = null;
	recoveryAnnouncement = null;
	try {
		await getTransport().request(
			"session.retainExternalDeletion",
			{ projectId: record.projectId, sessionId: record.sessionId },
			{ timeoutMs: 10_000 },
		);
		if (appStoreApi.getState().connectionGeneration === generation) {
			// A retain acknowledgement validates the existing record but never
			// removes it. Keep the keyed row mounted so keyboard focus remains.
			recoveryAnnouncement = `Retained deletion record for ${record.sessionId}.`;
		}
	} catch (cause) {
		recoveryError = errorText(cause);
	} finally {
		if (recoveryPendingKey === key) recoveryPendingKey = null;
	}
}

$effect(() => {
	if (!connected) {
		activeRequest?.abort();
		activeRequest = null;
		loading = false;
		loadedGeneration = null;
		return;
	}
	if (loadedGeneration === connectionGeneration) return;
	loadedGeneration = connectionGeneration;
	void refresh(connectionGeneration);
});

onDestroy(() => {
	const request = activeRequest;
	activeRequest = null;
	request?.abort();
});
</script>

{#snippet StatusBadge(state: RuntimeAvailability)}
	<span class={`state-badge state-badge--${state} u-inline-flex u-shrink-0 u-items-center u-gap-xs tr-text-metadata`}>
		<span
			aria-hidden="true"
			class={`status-dot status-dot--${
				state === "ready"
					? "ready"
					: state === "degraded"
						? "degraded"
						: "unavailable"
			}`}
		></span>
		{STATE_LABEL[state]}
	</span>
{/snippet}

{#snippet Metric(label: string, value: string)}
	<div class="metric u-min-w-0 u-py-xs">
		<dt class="u-text-text-muted tr-text-metadata">{label}</dt>
		<dd class="metric__value u-min-w-0 u-text-text-default tr-text-metadata">
			{value}
		</dd>
	</div>
{/snippet}

{#snippet RequestMetrics(requests: RuntimeRequestMetrics)}
	{@render Metric("Requests", formatCount(requests.total))}
	{@render Metric("Failures", formatCount(requests.failures))}
	{@render Metric("Active", formatCount(requests.active))}
	{@render Metric("Average", formatMilliseconds(requests.averageMs))}
	{@render Metric("Maximum", formatMilliseconds(requests.maxMs))}
{/snippet}

{#snippet ServiceCard(name: "Application" | "Browser", status: RuntimeServiceStatus)}
	<section
		data-testid={`system-card-${name.toLowerCase()}`}
		class="card u-min-w-0 u-p-md"
	>
		<div class="u-flex u-flex-wrap u-items-center u-justify-between u-gap-sm">
			<h3 class="tr-text-ui u-text-text-default">{name}</h3>
			{@render StatusBadge(status.state)}
		</div>
		{#if status.detail}
			<p class="service-card__detail u-mt-sm u-text-text-muted tr-text-metadata">{status.detail}</p>
		{/if}
		{#if status.build || status.process || status.requests}
			<dl class="status-metrics u-mt-sm u-border-t">
				{#if status.build}{@render Metric("Version", status.build.version)}{/if}
				{#if status.build?.revision}
					<div class="metric u-min-w-0 u-py-xs">
						<dt class="u-text-text-muted tr-text-metadata">Revision</dt>
						<dd class="metric__value u-min-w-0 tr-text-metadata">
							<code class="metric__code" title={status.build.revision}>
								{status.build.revision.slice(0, 12)}
							</code>
						</dd>
					</div>
				{/if}
				{#if status.process}
					{@render Metric("Uptime", formatUptime(status.process.uptimeSeconds))}
					{@render Metric("Memory", formatBytes(status.process.heapBytes))}
					{@render Metric("Goroutines", formatCount(status.process.goroutines))}
					{@render Metric("GC cycles", formatCount(status.process.gcCycles))}
				{/if}
				{#if status.requests}{@render RequestMetrics(status.requests)}{/if}
			</dl>
		{/if}
	</section>
{/snippet}

{#snippet AgentCard(status: RuntimeAgentStatus)}
	<section data-testid="system-card-agent" class="card u-min-w-0 u-p-md">
		<div class="u-flex u-flex-wrap u-items-center u-justify-between u-gap-sm">
			<h3 class="tr-text-ui u-text-text-default">Agent</h3>
			{@render StatusBadge(status.state)}
		</div>
		{#if status.detail}
			<p class="service-card__detail u-mt-sm u-text-text-muted tr-text-metadata">{status.detail}</p>
		{/if}
		{#if status.name || status.version}
			<dl class="status-metrics u-mt-sm u-border-t">
				{#if status.name}{@render Metric("Name", status.name)}{/if}
				{#if status.version}{@render Metric("Version", status.version)}{/if}
			</dl>
		{/if}
	</section>
{/snippet}

<div
	data-testid="system-settings"
	class="system-settings u-flex u-w-full u-flex-col u-gap-lg"
>
	<div class="u-flex u-items-start u-justify-between u-gap-sm">
		<div class="u-min-w-0">
			<h2 class="tr-title-entity u-text-text-default">System</h2>
			<p class="u-mt-xs u-text-text-muted tr-text-metadata">Local services and build details.</p>
		</div>
		<Button
			variant="ghost"
			size="sm"
			data-testid="system-refresh"
			disabled={!connected || loading}
			onclick={() => refresh()}
		>
			<Icon name="refresh-cw" size={14} class={loading ? "system-refresh-icon" : ""} />
			{loading ? "Refreshing…" : "Refresh"}
		</Button>
	</div>

	{#if unavailable}
		<p role="alert" class="u-text-feedback-error tr-text-metadata">
			{!connected
				? report
					? "Controller disconnected. Showing the last status."
					: "Controller disconnected."
				: report
					? "Couldn't refresh system status. Showing the last status."
					: "Couldn't read system status."}
		</p>
	{:else if loading}
		<p role="status" aria-live="polite" class="u-text-text-muted tr-text-metadata">
			{report ? "Refreshing system status…" : "Loading system status…"}
		</p>
	{/if}

	{#if report}
		<div class="system-service-grid u-min-w-0 u-gap-sm">
			{@render ServiceCard("Application", report.application)}
			{@render AgentCard(report.agent)}
		</div>
	{/if}

	<DeletionRecoverySection
		records={recoveryRecords}
		pendingKey={recoveryPendingKey}
		error={recoveryError}
		announcement={recoveryAnnouncement}
		onConfirm={(record) => void confirmDeletion(record)}
		onRetain={(record) => void retainDeletion(record)}
	/>
</div>

<style>
	.system-settings {
		max-inline-size: 56rem;
		margin-inline: auto;
		container-type: inline-size;
	}

	.state-badge {
		white-space: nowrap;
	}

	.state-badge--ready { color: var(--feedback-success); }
	.state-badge--degraded { color: var(--feedback-warning); }
	.state-badge--unavailable { color: var(--feedback-error); }

	.status-dot {
		inline-size: 0.375rem;
		block-size: 0.375rem;
		border-radius: 50%;
	}

	.status-dot--ready { background: var(--feedback-success); }
	.status-dot--degraded { background: var(--feedback-warning); }
	.status-dot--unavailable { background: var(--feedback-error); }

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

	.metric__code {
		word-break: break-all;
	}

	.service-card__detail {
		overflow-wrap: break-word;
	}

	.status-metrics {
		border-color: var(--border-muted);
	}

	.status-metrics > * + * {
		border-top: 1px solid var(--border-muted);
	}

	.system-service-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
	}

	:global(.system-refresh-icon) {
		animation: system-refresh-rotate 1s linear infinite;
	}

	@keyframes system-refresh-rotate {
		to { transform: rotate(1turn); }
	}

	@container (min-width: 42rem) {
		.system-service-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.system-refresh-icon) { animation: none; }
	}
</style>
