<script lang="ts">
import type { ProviderStatus, ProviderStatusReport, WireModel } from "@pixie/contracts";
import { onMount } from "svelte";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";
import { hiddenModelRevision } from "../state";
import {
	cacheText,
	configuredAvailableModels,
	filterModels,
	formatTokenCount,
	providerName,
	rateText,
	refreshModelCatalog,
	shouldLoadModelCatalog,
	shouldReloadModelCatalogRevision,
	tierText,
} from "./models-settings";

let models = $state<WireModel[]>([]);
let report = $state<ProviderStatusReport>({ providers: [] });
let query = $state("");
let loading = $state(true);
let refreshing = $state(false);
let failed = $state(false);
let metadataIncomplete = $state(false);
let busyModel = $state<string | null>(null);
let bulkBusy = $state(false);
let loadSequence = 0;
let forceRefreshSequence = 0;
let forceRefreshInFlight = $state(false);
let visibilityMutationInFlight = false;
let visibilityMutationSequence = 0;
let mounted = false;
let observedCatalogRevision = $state<string | null>(null);
let providerVersion = $derived($appStore.providerVersion);
let catalogRevision = $derived(`${providerVersion}\u0002${hiddenModelRevision($appStore.config)}`);
let providers = $derived(new Map(report.providers.map((provider) => [provider.id, provider])));
let catalog = $derived(configuredAvailableModels(models, providers));
let filtered = $derived(filterModels(catalog, providers, query));
let visibleCount = $derived(catalog.filter((model) => !model.hidden).length);
let groups = $derived.by(() => {
	const grouped = new Map<string, WireModel[]>();
	for (const model of filtered) {
		const group = grouped.get(model.provider);
		if (group) group.push(model);
		else grouped.set(model.provider, [model]);
	}
	return [...grouped.entries()];
});

function notifyError(error: unknown, title: string): void {
	appStoreApi.getState().pushToast({ variant: "error", message: errorText(error), title });
}

async function load(force = false): Promise<void> {
	if (!shouldLoadModelCatalog(force, forceRefreshInFlight)) return;
	const sequence = ++loadSequence;
	const visibilitySequence = visibilityMutationSequence;
	if (force) {
		forceRefreshInFlight = true;
		forceRefreshSequence = sequence;
	}
	refreshing = true;
	try {
		const result = force
			? await refreshModelCatalog(
					() => getTransport().request("model.refresh", { force: true }),
					() => getTransport().request("provider.status", {}),
				)
			: await Promise.all([
					getTransport().request("model.list", {}),
					getTransport().request("provider.status", {}),
				]).then(([catalog, providerReport]) => ({
					models: catalog,
					report: providerReport,
					complete: catalog.every((model) => model.metadataComplete === true),
				}));
		if (
			!mounted ||
			sequence !== loadSequence ||
			visibilitySequence !== visibilityMutationSequence
		) {
			return;
		}
		models = result.models;
		report = result.report;
		metadataIncomplete = !result.complete;
		failed = false;
	} catch (error) {
		if (!mounted || sequence !== loadSequence) return;
		failed = true;
		if (force) notifyError(error, "Couldn't refresh models");
	} finally {
		if (force && forceRefreshSequence === sequence) forceRefreshInFlight = false;
		if (mounted && sequence === loadSequence) {
			loading = false;
			refreshing = false;
		}
	}
}

onMount(() => {
	mounted = true;
	observedCatalogRevision = catalogRevision;
	void load(false);
	return () => {
		mounted = false;
		loadSequence += 1;
	};
});

$effect(() => {
	const nextRevision = catalogRevision;
	const forceRefreshActive = forceRefreshInFlight;
	if (
		!shouldReloadModelCatalogRevision(
			mounted,
			observedCatalogRevision,
			nextRevision,
			forceRefreshActive,
		)
	)
		return;
	observedCatalogRevision = nextRevision;
	void load(false);
});

async function setVisibility(model: WireModel, hidden: boolean): Promise<void> {
	if (visibilityMutationInFlight) return;
	visibilityMutationInFlight = true;
	visibilityMutationSequence += 1;
	const key = `${model.provider}\0${model.id}`;
	busyModel = key;
	try {
		models = await getTransport().request("model.setVisibility", {
			provider: model.provider,
			id: model.id,
			hidden,
		});
	} catch (error) {
		notifyError(error, hidden ? "Couldn't hide the model" : "Couldn't show the model");
	} finally {
		visibilityMutationInFlight = false;
		busyModel = null;
	}
}

async function setAllVisibility(hidden: boolean): Promise<void> {
	if (visibilityMutationInFlight) return;
	visibilityMutationInFlight = true;
	visibilityMutationSequence += 1;
	bulkBusy = true;
	try {
		models = await getTransport().request("model.setAllVisibility", { hidden });
	} catch (error) {
		notifyError(error, hidden ? "Couldn't hide all models" : "Couldn't show all models");
	} finally {
		visibilityMutationInFlight = false;
		bulkBusy = false;
	}
}
</script>

{#snippet ModelRow(model: WireModel, busy: boolean, withBorder: boolean)}
	{@const cache = model.cost ? cacheText(model.cost) : null}
	<div
		data-testid="model-row"
		data-provider={model.provider}
		data-model={model.id}
		data-available={String(model.available)}
		data-hidden={String(model.hidden)}
		class={`grid min-w-0 grid-cols-1 items-start gap-sm px-md py-sm sm:grid-cols-[minmax(12rem,1fr)_auto_auto] sm:gap-md ${
			withBorder ? "border-border-default border-t" : ""
		} ${!model.available || model.hidden ? "opacity-55" : ""}`}
	>
		<div class="min-w-0">
			<div class="flex min-w-0 items-center gap-sm">
				<span class="truncate text-text-default tr-text-ui">{model.name || model.id}</span>
				{#if !model.available}<span class="badge" data-variant="secondary">Unavailable</span>{/if}
				{#if model.hidden}<span class="badge" data-variant="secondary">Hidden</span>{/if}
			</div>
			<div class="truncate text-text-muted tr-text-metadata">{model.id}</div>
			<div class="mt-xs flex flex-wrap items-center gap-xs text-text-muted tr-text-metadata">
				{#if model.input?.includes("text")}<span class="flex items-center gap-1" title="Text input">
					<Icon name="type" size={12} /> Text
				</span>{/if}
				{#if model.input?.includes("image")}
					<span class="flex items-center gap-1" title="Image input">
						<Icon name="image" size={12} /> Image
					</span>
				{/if}
				{#if model.reasoning}
					<span
						class="flex items-center gap-1"
						title={`Reasoning levels: ${model.thinkingLevels?.join(", ") || "provider default"}`}
					>
						<Icon name="brain-circuit" size={12} />
						Reasoning{model.thinkingLevels?.length ? ` · ${model.thinkingLevels.join("/")}` : ""}
					</span>
				{/if}
			</div>
		</div>

		<div class="flex min-w-0 flex-col items-start gap-0.5 text-left sm:min-w-[9rem] sm:items-end sm:text-right">
			<span class="badge" data-variant="secondary">
				{model.contextWindow === undefined ? "Unknown" : formatTokenCount(model.contextWindow)} ctx ·
				{model.maxTokens === undefined ? "Unknown" : formatTokenCount(model.maxTokens)} out
			</span>
			{#if model.cost}
				<span class="text-text-muted tr-text-metadata">{rateText(model.cost)} / 1M</span>
			{:else}
				<span class="text-text-muted tr-text-metadata">Pricing unavailable</span>
			{/if}
			{#if cache}<span class="text-text-muted tr-text-metadata">{cache}</span>{/if}
			{#each model.cost?.tiers ?? [] as tier (tier.inputTokensAbove)}
				<span class="text-text-muted tr-text-metadata">
					{tierText(tier, model.cost?.currency ?? "")}
				</span>
			{/each}
		</div>

		<Button
			variant="ghost"
			size="icon"
			aria-label={model.hidden ? `Show ${model.name}` : `Hide ${model.name}`}
			title={model.hidden ? "Show model" : "Hide model"}
			disabled={busy}
			onclick={() => void setVisibility(model, !model.hidden)}
		>
			<Icon name={model.hidden ? "eye-off" : "eye"} size={16} />
		</Button>
	</div>
{/snippet}

<div data-testid="settings-models" class="flex flex-col gap-lg">
	<div class="flex flex-wrap items-start justify-between gap-sm">
		<div class="flex min-w-0 flex-1 basis-64 flex-col gap-xs">
			<h3 class="tr-title-section text-text-default">
				Models <span class="font-normal text-text-muted">({catalog.length})</span>
			</h3>
			<p class="text-text-muted tr-text-metadata">
				{catalog.length} available · {visibleCount} shown. Visibility is a Pixie preference.
				Pi keeps the canonical catalog.{metadataIncomplete
					? " Some optional model metadata did not finish loading."
					: ""}
			</p>
		</div>
		<div class="flex flex-wrap items-center gap-xs">
			<Button
				variant="outline"
				size="sm"
				title="Hide every model in Pixie, including models from disconnected providers"
				disabled={bulkBusy || busyModel !== null || models.length === 0}
				onclick={() => void setAllVisibility(true)}
			>
				Hide all
			</Button>
			<Button
				variant="outline"
				size="sm"
				title="Show every model in Pixie, including models from disconnected providers"
				disabled={bulkBusy || busyModel !== null || models.length === 0}
				onclick={() => void setAllVisibility(false)}
			>
				Show all
			</Button>
			<Button
				variant="ghost"
				size="sm"
				aria-label="Refresh model catalog"
				disabled={refreshing || bulkBusy || busyModel !== null}
				onclick={() => void load(true)}
			>
				<Icon name="refresh-cw" size={14} class={refreshing ? "animate-spin" : ""} />
				Refresh
			</Button>
		</div>
	</div>

	<label class="text-field flex-row items-center gap-sm">
		<Icon name="search" size={16} class="text-text-muted" />
		<span class="text-field-label" data-hidden>Filter models</span>
		<input
			class="text-field-input min-w-0 flex-1"
			data-testid="models-filter"
			bind:value={query}
			placeholder="Filter models…"
		/>
	</label>

	{#if loading}
		<p class="text-text-muted tr-text-ui">Loading models…</p>
	{:else if failed}
		<p class="text-text-muted tr-text-ui">Couldn't read the model catalog.</p>
	{:else if filtered.length === 0}
		<p class="text-text-muted tr-text-ui">
			{catalog.length === 0
				? "No available models for configured providers. Connect a provider, then refresh the catalog."
				: "No models match this filter."}
		</p>
	{:else}
		<div class="flex flex-col gap-lg">
			{#each groups as [providerId, providerModels] (providerId)}
				<section class="flex flex-col gap-xs">
					<div class="flex items-baseline justify-between gap-sm px-xs">
						<h4 class="tr-text-eyebrow text-text-muted">{providerName(providerId, providers)}</h4>
						<span class="text-text-muted tr-text-metadata">
							{providerModels.filter((model) => model.available).length}/{providerModels.length}
							available
						</span>
					</div>
					<div class="overflow-hidden rounded-[var(--radius-sm)] border border-border-default bg-control-bg">
						{#each providerModels as model, index (`${model.provider}\0${model.id}`)}
							{@render ModelRow(
								model,
								bulkBusy || busyModel !== null || busyModel === `${model.provider}\0${model.id}`,
								index > 0,
							)}
						{/each}
					</div>
				</section>
			{/each}
		</div>
	{/if}
</div>
