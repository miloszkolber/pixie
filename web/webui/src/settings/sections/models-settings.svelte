<script lang="ts">
import type { ProviderStatus, ProviderStatusReport, WireModel } from "@pixie/shared";
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
		class={`model-row u-min-w-0 u-px-md u-py-sm ${
			withBorder ? "model-row--bordered" : ""
		} ${!model.available || model.hidden ? "model-row--dimmed" : ""}`}
	>
		<div class="u-min-w-0">
			<div class="u-flex u-min-w-0 u-items-center u-gap-sm">
				<span class="model-row__name u-text-text-default tr-text-ui" title={model.name || model.id}>{model.name || model.id}</span>
				{#if !model.available}<span class="badge" data-variant="secondary">Unavailable</span>{/if}
				{#if model.hidden}<span class="badge" data-variant="secondary">Hidden</span>{/if}
			</div>
			<div class="model-row__name u-text-text-muted tr-text-metadata">{model.id}</div>
			<div class="u-mt-xs u-flex u-flex-wrap u-items-center u-gap-xs u-text-text-muted tr-text-metadata">
				{#if model.input?.includes("text")}<span class="model-row__capability u-flex u-items-center" title="Text input">
					<Icon name="type" size={12} /> Text
				</span>{/if}
				{#if model.input?.includes("image")}
					<span class="model-row__capability u-flex u-items-center" title="Image input">
						<Icon name="image" size={12} /> Image
					</span>
				{/if}
				{#if model.reasoning}
					<span
						class="model-row__capability u-flex u-items-center"
						title={`Reasoning levels: ${model.thinkingLevels?.join(", ") || "provider default"}`}
					>
						<Icon name="brain-circuit" size={12} />
						Reasoning{model.thinkingLevels?.length ? ` · ${model.thinkingLevels.join("/")}` : ""}
					</span>
				{/if}
			</div>
		</div>

		<div class="model-row__stats u-flex u-min-w-0 u-flex-col u-items-start u-gap-0.5 u-text-left">
			<span class="badge model-row__context" data-variant="secondary">
				{model.contextWindow === undefined ? "Unknown" : formatTokenCount(model.contextWindow)} ctx ·
				{model.maxTokens === undefined ? "Unknown" : formatTokenCount(model.maxTokens)} out
			</span>
			{#if model.cost}
				<span class="u-text-text-muted tr-text-metadata">{rateText(model.cost)} / 1M</span>
			{:else}
				<span class="u-text-text-muted tr-text-metadata">Pricing unavailable</span>
			{/if}
			{#if cache}<span class="u-text-text-muted tr-text-metadata">{cache}</span>{/if}
			{#each model.cost?.tiers ?? [] as tier (tier.inputTokensAbove)}
				<span class="u-text-text-muted tr-text-metadata">
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


<div data-testid="settings-models" class="models-settings u-flex u-flex-col u-gap-lg">
	<div class="u-flex u-flex-wrap u-items-start u-justify-between u-gap-sm">
		<div class="models-header__intro u-flex u-min-w-0 u-flex-1 u-flex-col u-gap-xs">
			<h3 class="tr-title-section u-text-text-default">
				Models <span class="models-count u-text-text-muted">({catalog.length})</span>
			</h3>
			<p class="u-text-text-muted tr-text-metadata">
				{catalog.length} available · {visibleCount} shown. Visibility is a Pixie preference.
				Pi keeps the canonical catalog.{metadataIncomplete
					? " Some optional model metadata did not finish loading."
					: ""}
			</p>
		</div>
		<div class="u-flex u-flex-wrap u-items-center u-gap-xs">
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
				<Icon name="refresh-cw" size={14} class={refreshing ? "models-refresh-icon" : ""} />
				Refresh
			</Button>
		</div>
	</div>

	<label class="text-field models-filter u-items-center u-gap-sm">
		<Icon name="search" size={16} class="u-text-text-muted" />
		<span class="text-field-label" data-hidden>Filter models</span>
		<input
			class="text-field-input u-min-w-0 u-flex-1"
			data-testid="models-filter"
			bind:value={query}
			placeholder="Filter models…"
		/>
	</label>

	{#if loading}
		<p class="u-text-text-muted tr-text-ui">Loading models…</p>
	{:else if failed}
		<p class="u-text-text-muted tr-text-ui">Couldn't read the model catalog.</p>
	{:else if filtered.length === 0}
		<p class="u-text-text-muted tr-text-ui">
			{catalog.length === 0
				? "No available models for configured providers. Connect a provider, then refresh the catalog."
				: "No models match this filter."}
		</p>
	{:else}
		<div class="u-flex u-flex-col u-gap-lg">
			{#each groups as [providerId, providerModels] (providerId)}
				<section class="u-flex u-flex-col u-gap-xs">
					<div class="u-flex u-items-baseline u-justify-between u-gap-sm u-px-xs">
						<h4 class="tr-text-eyebrow u-text-text-muted">{providerName(providerId, providers)}</h4>
						<span class="u-text-text-muted tr-text-metadata">
							{providerModels.filter((model) => model.available).length}/{providerModels.length}
							available
						</span>
					</div>
					<div class="models-provider-list u-rounded u-border u-border-border-default u-bg-control-bg">
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

<style>
	.models-settings {
		container-type: inline-size;
	}

	.model-row {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		align-items: start;
		gap: var(--space-sm);
	}

	.model-row--bordered {
		border-top: 1px solid var(--border-default);
	}

	.model-row--dimmed {
		opacity: 0.55;
	}

	.model-row__name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.model-row__capability {
		gap: var(--space-100);
	}

	.model-row__context {
		white-space: nowrap;
	}

	.models-header__intro {
		flex-basis: 16rem;
	}

	.models-count {
		font-weight: var(--tr-font-weight-regular);
	}

	.models-filter {
		flex-direction: row;
	}

	.models-provider-list {
		overflow: hidden;
	}

	:global(.models-refresh-icon) {
		animation: models-refresh-rotate 1s linear infinite;
	}

	@keyframes models-refresh-rotate {
		to { transform: rotate(1turn); }
	}

	@container (min-width: 42rem) {
		.model-row {
			grid-template-columns: minmax(12rem, 1fr) auto auto;
			gap: var(--space-md);
		}

		.model-row__stats {
			min-inline-size: 9rem;
			align-items: flex-end;
			text-align: right;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.models-refresh-icon) { animation: none; }
	}
</style>
