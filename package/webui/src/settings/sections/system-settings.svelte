<script lang="ts">
import type {
	RuntimeAgentStatus,
	RuntimeAvailability,
	RuntimeRequestMetrics,
	RuntimeServiceStatus,
	RuntimeStatusReport,
} from "@pixie/contracts";
import { onDestroy } from "svelte";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import { getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";
import {
	formatBytes,
	formatCount,
	formatMilliseconds,
	formatUptime,
	STATE_CLASS,
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
	void load(connectionGeneration);
});

onDestroy(() => {
	const request = activeRequest;
	activeRequest = null;
	request?.abort();
});
</script>

{#snippet StatusBadge(state: RuntimeAvailability)}
	<span class={`inline-flex items-center gap-xs tr-text-metadata ${STATE_CLASS[state]}`}>
		<span
			aria-hidden="true"
			class={`size-1.5 rounded-full ${
				state === "ready"
					? "bg-feedback-success"
					: state === "degraded"
						? "bg-feedback-warning"
						: "bg-feedback-error"
			}`}
		></span>
		{STATE_LABEL[state]}
	</span>
{/snippet}

{#snippet Metric(label: string, value: string)}
	<div class="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,auto)] gap-sm py-xs">
		<dt class="text-text-muted tr-text-metadata">{label}</dt>
		<dd class="min-w-0 break-words text-right tabular-nums text-text-default tr-text-metadata">
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
		class="card min-w-0 p-md"
	>
		<div class="flex items-center justify-between gap-sm">
			<h3 class="tr-text-ui text-text-default">{name}</h3>
			{@render StatusBadge(status.state)}
		</div>
		{#if status.detail}
			<p class="mt-sm break-words text-text-muted tr-text-metadata">{status.detail}</p>
		{/if}
		{#if status.build || status.process || status.requests}
			<dl class="mt-sm divide-y divide-border-muted border-border-muted border-t">
				{#if status.build}{@render Metric("Version", status.build.version)}{/if}
				{#if status.build?.revision}
					<div class="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,auto)] gap-sm py-xs">
						<dt class="text-text-muted tr-text-metadata">Revision</dt>
						<dd class="min-w-0 text-right tr-text-metadata">
							<code class="break-all" title={status.build.revision}>
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
	<section data-testid="system-card-agent" class="card min-w-0 p-md">
		<div class="flex items-center justify-between gap-sm">
			<h3 class="tr-text-ui text-text-default">Agent</h3>
			{@render StatusBadge(status.state)}
		</div>
		{#if status.detail}
			<p class="mt-sm break-words text-text-muted tr-text-metadata">{status.detail}</p>
		{/if}
		{#if status.name || status.version}
			<dl class="mt-sm divide-y divide-border-muted border-border-muted border-t">
				{#if status.name}{@render Metric("Name", status.name)}{/if}
				{#if status.version}{@render Metric("Version", status.version)}{/if}
			</dl>
		{/if}
	</section>
{/snippet}

<div
	data-testid="system-settings"
	class="mx-auto flex w-full max-w-[56rem] flex-col gap-lg"
>
	<div class="flex items-start justify-between gap-sm">
		<div class="min-w-0">
			<h2 class="tr-title-entity text-text-default">System</h2>
			<p class="mt-xs text-text-muted tr-text-metadata">Local services and build details.</p>
		</div>
		<Button
			variant="ghost"
			size="sm"
			data-testid="system-refresh"
			disabled={!connected || loading}
			onclick={() => void load()}
		>
			<Icon name="refresh-cw" size={14} class={loading ? "animate-spin" : ""} />
			{loading ? "Refreshing…" : "Refresh"}
		</Button>
	</div>

	{#if unavailable}
		<p role="alert" class="text-feedback-error tr-text-metadata">
			{!connected
				? report
					? "Controller disconnected. Showing the last status."
					: "Controller disconnected."
				: report
					? "Couldn't refresh system status. Showing the last status."
					: "Couldn't read system status."}
		</p>
	{:else if loading}
		<p role="status" aria-live="polite" class="text-text-muted tr-text-metadata">
			{report ? "Refreshing system status…" : "Loading system status…"}
		</p>
	{/if}

	{#if report}
		<div class="grid min-w-0 grid-cols-1 gap-sm md:grid-cols-3">
			{@render ServiceCard("Application", report.application)}
			{@render AgentCard(report.agent)}
			{@render ServiceCard("Browser", report.browser)}
		</div>
	{/if}
</div>
