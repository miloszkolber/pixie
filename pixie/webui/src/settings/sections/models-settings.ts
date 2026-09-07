import type {
	ProviderStatus,
	ProviderStatusReport,
	WireModel,
	WireModelCost,
	WireModelCostTier,
} from "@pixie/contracts";

export function formatTokenCount(value: number): string {
	if (!Number.isFinite(value) || value < 0) return "—";
	if (value >= 1_000_000) {
		const millions = value / 1_000_000;
		return `${millions >= 10 || Number.isInteger(millions) ? millions.toFixed(0) : millions.toFixed(1)}M`;
	}
	if (value >= 1_000) {
		const thousands = value / 1_000;
		return `${thousands >= 10 || Number.isInteger(thousands) ? thousands.toFixed(0) : thousands.toFixed(1)}K`;
	}
	return String(value);
}

export function formatModelPrice(value: number, currency = "$"): string {
	if (!Number.isFinite(value) || value < 0) return "—";
	if (value === 0) return `${currency}0`;
	if (value < 0.01) return `${currency}${value.toFixed(4)}`;
	if (value < 1) return `${currency}${value.toFixed(2)}`;
	return `${currency}${value.toLocaleString(undefined, { maximumFractionDigits: 2 })}`;
}

export function rateText(rates: WireModelCost): string {
	return `In ${formatModelPrice(rates.input, rates.currency)} · Out ${formatModelPrice(rates.output, rates.currency)}`;
}

export function cacheText(rates: WireModelCost): string | null {
	const parts: string[] = [];
	if (rates.cacheRead !== undefined)
		parts.push(`Cache read ${formatModelPrice(rates.cacheRead, rates.currency)}`);
	if (rates.cacheWrite !== undefined)
		parts.push(`write ${formatModelPrice(rates.cacheWrite, rates.currency)}`);
	return parts.length > 0 ? parts.join(" · ") : null;
}

export function tierText(tier: WireModelCostTier, currency: string): string {
	return `Over ${formatTokenCount(tier.inputTokensAbove)} input: In ${formatModelPrice(tier.input, currency)} · Out ${formatModelPrice(tier.output, currency)}`;
}

export function providerName(
	provider: string,
	providers: ReadonlyMap<string, ProviderStatus>,
): string {
	return providers.get(provider)?.name ?? provider;
}

export function configuredAvailableModels(
	models: readonly WireModel[],
	providers: ReadonlyMap<string, ProviderStatus>,
): WireModel[] {
	return models.filter((model) => {
		const provider = providers.get(model.provider);
		return provider?.configured === true && provider.available !== false && model.available;
	});
}

export function filterModels(
	models: readonly WireModel[],
	providers: ReadonlyMap<string, ProviderStatus>,
	query: string,
): WireModel[] {
	const normalized = query.trim().toLocaleLowerCase();
	if (!normalized) return [...models];
	return models.filter((model) =>
		[model.name, model.id, model.provider, providerName(model.provider, providers)]
			.join("\n")
			.toLocaleLowerCase()
			.includes(normalized),
	);
}

type ModelCatalogResponse = WireModel[] | { models: WireModel[]; complete?: boolean };

export async function refreshModelCatalog(
	loadCatalog: () => Promise<ModelCatalogResponse>,
	loadProviders: () => Promise<ProviderStatusReport>,
): Promise<{ models: WireModel[]; report: ProviderStatusReport; complete: boolean }> {
	const catalog = await loadCatalog();
	const report = await loadProviders();
	return {
		models: Array.isArray(catalog) ? catalog : catalog.models,
		report,
		complete: Array.isArray(catalog) || catalog.complete !== false,
	};
}

export function shouldLoadModelCatalog(force: boolean, forceRefreshInFlight: boolean): boolean {
	return force || !forceRefreshInFlight;
}

export function shouldReloadModelCatalogRevision(
	mounted: boolean,
	observedRevision: string | null,
	nextRevision: string,
	forceRefreshInFlight: boolean,
): boolean {
	return (
		mounted &&
		observedRevision !== null &&
		observedRevision !== nextRevision &&
		shouldLoadModelCatalog(false, forceRefreshInFlight)
	);
}
