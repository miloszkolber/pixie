<script lang="ts">
import type { SessionStats, SessionSummary } from "@pixie/contracts";
import { formatCost, isUsageReported } from "../../chat/session/session-stats";
import Button from "../../components/button.svelte";
import { errorText, getTransport } from "../../connection";
import { releaseSession } from "../../connection/session-release";
import { appStore, appStoreApi, toast } from "../../store";
import {
	describeContextUsage,
	describeTokenCount,
	formatSessionModel,
	formatThinkingLevel,
	releaseAffordanceReason,
	sessionStatusText,
	shouldShowReleaseAffordance,
} from "./details-model";

interface Props {
	projectAreaId: string;
	sessionId: string | null;
}

let { projectAreaId, sessionId }: Props = $props();

let summary = $state<SessionSummary | null>(null);
let summaryError = $state<string | null>(null);
let summaryRevision = $state(0);
let stats = $state<SessionStats | null>(null);
let statsError = $state<string | null>(null);
let statsRevision = $state(0);
let releaseBusy = $state(false);
let releaseError = $state<string | null>(null);

let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);
let catalogVersion = $derived(
	$appStore.sessionCatalogVersionByProjectArea[projectAreaId] ?? 0,
);

// Backend-authoritative session identity for the selected chat. A catalog
// push bumps the version so a release or lifecycle change refetches it.
$effect(() => {
	const area = projectAreaId;
	const id = sessionId;
	const generation = connectionGeneration;
	const version = catalogVersion;
	const online = connected;
	const retry = summaryRevision;
	void version;
	void retry;
	if (!id || !online) {
		if (!id) {
			summary = null;
			summaryError = null;
		}
		return;
	}
	let cancelled = false;
	void getTransport()
		.request("session.list", { projectId: area, archived: "all" })
		.then((catalog) => {
			if (
				cancelled ||
				appStoreApi.getState().connectionGeneration !== generation ||
				appStoreApi.getState().removedProjectAreaIds[area]
			)
				return;
			summary = catalog.find((candidate) => candidate.sessionId === id) ?? null;
			summaryError = null;
		})
		.catch((cause) => {
			if (
				cancelled ||
				appStoreApi.getState().connectionGeneration !== generation ||
				appStoreApi.getState().removedProjectAreaIds[area]
			)
				return;
			summaryError = errorText(cause);
		});
	return () => {
		cancelled = true;
	};
});

// Live backend-authoritative usage for the selected chat. Unknown values stay
// "Unknown" through the details-model helpers, never zero.
$effect(() => {
	const area = projectAreaId;
	const id = sessionId;
	const generation = connectionGeneration;
	const streaming = summary?.isStreaming;
	const online = connected;
	const retry = statsRevision;
	void streaming;
	void retry;
	if (!id || !online) {
		if (!id) {
			stats = null;
			statsError = null;
		}
		return;
	}
	let cancelled = false;
	void getTransport()
		.request("session.getStats", { sessionId: id })
		.then((result) => {
			if (
				cancelled ||
				appStoreApi.getState().connectionGeneration !== generation ||
				appStoreApi.getState().removedProjectAreaIds[area]
			)
				return;
			stats = result;
			statsError = null;
		})
		.catch((cause) => {
			if (
				cancelled ||
				appStoreApi.getState().connectionGeneration !== generation ||
				appStoreApi.getState().removedProjectAreaIds[area]
			)
				return;
			statsError = errorText(cause);
		});
	return () => {
		cancelled = true;
	};
});

let modelText = $derived(formatSessionModel(summary?.model));
let thinkingText = $derived(formatThinkingLevel(summary?.thinkingLevel));
let statusText = $derived(summary ? sessionStatusText(summary) : "Unknown");
let isStreaming = $derived(summary?.isStreaming === true);

// Eligibility is derived only from backend-reported summary fields. Dialog,
// liveness-pin, and lifecycle state are invisible here, so session.release
// stays authoritative and its refusal surfaces verbatim.
let eligibility = $derived.by((): { eligible: boolean | undefined; reason: string | null } => {
	if (!summary) return { eligible: undefined, reason: null };
	if (summary.archived) return { eligible: false, reason: "Archived sessions cannot release runtime" };
	if (!summary.live) return { eligible: false, reason: "Session is not loaded" };
	const steering = summary.queue?.steering ?? [];
	const followUp = summary.queue?.followUp ?? [];
	if (summary.queue?.blocked !== undefined || steering.length > 0 || followUp.length > 0)
		return { eligible: false, reason: "queued work still pending" };
	return { eligible: true, reason: null };
});
let showRelease = $derived(
	shouldShowReleaseAffordance({ backendEligible: eligibility.eligible, isStreaming }),
);
let releaseReason = $derived(
	releaseAffordanceReason({
		backendEligible: eligibility.eligible,
		backendReason: eligibility.reason,
		isStreaming,
	}),
);

let inputTokens = $derived(stats ? describeTokenCount(stats.tokens.input, stats.reported?.input) : "Unknown");
let outputTokens = $derived(stats ? describeTokenCount(stats.tokens.output, stats.reported?.output) : "Unknown");
let cacheReadTokens = $derived(
	stats ? describeTokenCount(stats.tokens.cacheRead, stats.reported?.cacheRead) : "Unknown",
);
let cacheWriteTokens = $derived(
	stats ? describeTokenCount(stats.tokens.cacheWrite, stats.reported?.cacheWrite) : "Unknown",
);
let totalTokens = $derived(stats ? describeTokenCount(stats.tokens.total, stats.reported?.total) : "Unknown");
let costText = $derived(
	stats && isUsageReported(stats, "cost", stats.cost) ? formatCost(stats) : "Unknown",
);
let contextText = $derived(describeContextUsage(stats?.contextUsage));

function retrySummary(): void {
	summaryRevision += 1;
}

function retryStats(): void {
	statsRevision += 1;
}

async function release(): Promise<void> {
	const id = sessionId;
	if (!id || releaseBusy) return;
	releaseBusy = true;
	releaseError = null;
	try {
		await releaseSession({ projectId: projectAreaId, sessionId: id });
		toast.success("Idle runtime released");
	} catch (cause) {
		releaseError = errorText(cause);
	} finally {
		releaseBusy = false;
	}
}
</script>

<div data-testid="details-panel" class="flex min-h-0 flex-1 flex-col gap-md">
	{#if sessionId === null}
		<p class="tr-text-metadata text-text-muted">Select a chat to inspect its status and usage.</p>
	{:else}
		{#if !connected}
			<p
				role="status"
				data-testid="details-stale"
				class="tr-text-metadata text-feedback-warning"
			>
				Offline — showing last reported values.
			</p>
		{/if}
		<section aria-label="Session" class="flex flex-col gap-2xs">
			<p class="tr-text-metadata text-text-muted">Session details</p>
			<p data-testid="details-session" class="mt-xs break-all tr-code-text">{sessionId}</p>
			{#if summaryError}
				<p role="alert" data-testid="details-error" class="tr-text-metadata text-feedback-error">
					Could not load session details: {summaryError}
				</p>
				<div>
					<Button
						variant="ghost"
						size="sm"
						data-testid="details-retry"
						onclick={retrySummary}
					>
						Retry
					</Button>
				</div>
			{:else if !summary}
				<p role="status" class="tr-text-metadata text-text-muted">Loading session details…</p>
			{:else}
				<dl class="grid grid-cols-2 gap-x-lg gap-y-xs tr-text-metadata">
					<dt class="text-text-muted">Model</dt>
					<dd data-testid="details-model" class="text-right text-text-default">{modelText}</dd>
					<dt class="text-text-muted">Thinking</dt>
					<dd data-testid="details-thinking" class="text-right text-text-default">{thinkingText}</dd>
					<dt class="text-text-muted">Status</dt>
					<dd data-testid="details-status" class="text-right text-text-default">{statusText}</dd>
				</dl>
			{/if}
		</section>
		<section aria-label="Usage" class="flex flex-col gap-2xs">
			<p class="tr-text-metadata text-text-muted">Usage</p>
			{#if statsError}
				<p role="alert" data-testid="details-stats-error" class="tr-text-metadata text-feedback-error">
					Could not load usage: {statsError}
				</p>
				<div>
					<Button
						variant="ghost"
						size="sm"
						data-testid="details-stats-retry"
						onclick={retryStats}
					>
						Retry
					</Button>
				</div>
			{:else if !stats}
				<p role="status" class="tr-text-metadata text-text-muted">Loading usage…</p>
			{:else}
				<dl data-testid="details-usage" class="grid grid-cols-2 gap-x-lg gap-y-xs tr-text-metadata">
					<dt class="text-text-muted">Input</dt>
					<dd data-testid="details-tokens-input" class="text-right text-text-default tabular-nums">{inputTokens}</dd>
					<dt class="text-text-muted">Output</dt>
					<dd data-testid="details-tokens-output" class="text-right text-text-default tabular-nums">{outputTokens}</dd>
					<dt class="text-text-muted">Cache read</dt>
					<dd data-testid="details-tokens-cache-read" class="text-right text-text-default tabular-nums">{cacheReadTokens}</dd>
					<dt class="text-text-muted">Cache write</dt>
					<dd data-testid="details-tokens-cache-write" class="text-right text-text-default tabular-nums">{cacheWriteTokens}</dd>
					<dt class="text-text-muted">Total</dt>
					<dd data-testid="details-tokens-total" class="text-right text-text-default tabular-nums">{totalTokens}</dd>
					<dt class="text-text-muted">Cost</dt>
					<dd data-testid="details-cost" class="text-right text-text-default tabular-nums">{costText}</dd>
					<dt class="text-text-muted">Context</dt>
					<dd data-testid="details-context" class="text-right text-text-default tabular-nums">{contextText}</dd>
				</dl>
			{/if}
		</section>
		{#if summary && !summaryError}
			<section aria-label="Idle runtime" class="flex flex-col items-start gap-xs">
				<p class="tr-text-metadata text-text-muted">Idle runtime</p>
				{#if showRelease}
					<Button
						variant="outline"
						size="sm"
						data-testid="details-release"
						disabled={releaseBusy}
						onclick={() => void release()}
					>
						{releaseBusy ? "Releasing…" : "Release idle runtime"}
					</Button>
					<p class="tr-text-metadata text-text-muted">
						The controller verifies idleness; active or pinned work stays. History and drafts are retained.
					</p>
				{:else if releaseReason}
					<p data-testid="details-release-reason" class="tr-text-metadata text-text-muted">{releaseReason}</p>
				{/if}
				{#if releaseError}
					<p role="alert" data-testid="details-release-error" class="tr-text-metadata text-feedback-error">{releaseError}</p>
				{/if}
			</section>
		{/if}
		<p class="tr-text-metadata text-text-muted">Read-only inspection. Releasing frees runtime capacity only.</p>
	{/if}
</div>
