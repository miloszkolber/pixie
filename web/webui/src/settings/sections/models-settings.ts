import type {
	ProviderStatus,
	ProviderStatusReport,
	RefreshFailure,
	WireModel,
	WireModelCost,
	WireModelCostTier,
} from "@pixie/shared";

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

type ModelCatalogResponse =
	| WireModel[]
	| { models: WireModel[]; complete?: boolean; failed?: readonly RefreshFailure[] | null };

export interface ModelCatalogResult {
	models: WireModel[];
	report: ProviderStatusReport;
	complete: boolean;
	failed: RefreshFailure[];
}

// Older hosts omit `failed` entirely and a future host could send malformed
// entries, so keep only well-formed provider/reason pairs at this boundary.
function normalizeRefreshFailures(value: unknown): RefreshFailure[] {
	if (!Array.isArray(value)) return [];
	return value.filter(isRefreshFailure);
}

function isRefreshFailure(value: unknown): value is RefreshFailure {
	if (typeof value !== "object" || value === null) return false;
	const entry = value as Partial<RefreshFailure>;
	return typeof entry.providerId === "string" && typeof entry.reason === "string";
}

export async function refreshModelCatalog(
	loadCatalog: () => Promise<ModelCatalogResponse>,
	loadProviders: () => Promise<ProviderStatusReport>,
): Promise<ModelCatalogResult> {
	const catalog = await loadCatalog();
	const report = await loadProviders();
	return {
		models: Array.isArray(catalog) ? catalog : catalog.models,
		report,
		complete: Array.isArray(catalog) || catalog.complete !== false,
		failed: Array.isArray(catalog) ? [] : normalizeRefreshFailures(catalog.failed),
	};
}

const MAX_REFRESH_FAILURE_ENTRIES = 3;

// Pi reports a bounded failure code, not the SDK message, but never echo an
// unrecognised reason either: an older or newer host could still send raw
// error text that carries credentials, URLs or absolute paths.
const REFRESH_FAILURE_REASON_LABELS: Readonly<Record<string, string>> = {
	refresh_failed: "refresh failed",
};

function refreshFailureReasonLabel(reason: string): string {
	return REFRESH_FAILURE_REASON_LABELS[reason] ?? "refresh failed";
}

export function refreshFailureWarning(
	failed: readonly RefreshFailure[],
	providers: ReadonlyMap<string, ProviderStatus>,
): string | null {
	if (failed.length === 0) return null;
	const entries: string[] = [];
	for (const failure of failed) {
		const name = providerName(failure.providerId, providers).trim() || "an unknown provider";
		const entry = `${name} (${refreshFailureReasonLabel(failure.reason)})`;
		if (!entries.includes(entry)) entries.push(entry);
	}
	const shown = entries.slice(0, MAX_REFRESH_FAILURE_ENTRIES);
	const remaining = entries.length - shown.length;
	const list = remaining > 0 ? `${shown.join(", ")} and ${remaining} more` : shown.join(", ");
	return `Some providers couldn't refresh: ${list}. Their model lists may be out of date. Refresh again to retry.`;
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
