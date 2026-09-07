import { expect, test } from "bun:test";
import type { ProviderStatus, WireModel } from "@pixie/contracts";
import {
	cacheText,
	configuredAvailableModels,
	filterModels,
	formatTokenCount,
	rateText,
	refreshModelCatalog,
	shouldLoadModelCatalog,
	shouldReloadModelCatalogRevision,
} from "@/settings/sections/models-settings";

const model: WireModel = {
	provider: "provider-a",
	id: "model-a",
	name: "Model A",
	contextWindow: 1_100_000,
	maxTokens: 128_000,
	reasoning: true,
	thinkingLevels: ["low", "medium", "high"],
	input: ["text", "image"],
	cost: {
		currency: "€",
		input: 1.25,
		output: 5,
		cacheRead: 0.125,
		cacheWrite: 1.5,
		tiers: [{ inputTokensAbove: 200_000, input: 2.5, output: 10, cacheRead: 0.25, cacheWrite: 3 }],
	},
	available: true,
	hidden: false,
};

const provider: ProviderStatus = {
	id: "provider-a",
	name: "Provider A",
	configured: true,
	available: true,
	modelCount: 1,
	availableModelCount: 1,
	readinessCheck: false,
};

test("filters by provider and retains context, modality, cost, and visibility contracts", async () => {
	const providers = new Map([[provider.id, provider]]);
	expect(filterModels([model], providers, "provider a")).toHaveLength(1);
	expect(filterModels([model], providers, "missing")).toHaveLength(0);
	expect(formatTokenCount(model.contextWindow ?? 0)).toBe("1.1M");
	expect(formatTokenCount(model.maxTokens ?? 0)).toBe("128K");
	const cost = model.cost;
	if (!cost) throw new Error("fixture cost is required");
	expect(rateText(cost)).toBe("In €1.25 · Out €5");
	expect(cacheText(cost)).toBe("Cache read €0.13 · write €1.5");

	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/models-settings.svelte", import.meta.url),
	).text();
	for (const value of ["ctx ·", "Image", "Reasoning", "Hidden", "Unavailable", "model-row"]) {
		expect(source).toContain(value);
	}
	expect(source).not.toContain("Routing");
	expect(source).toContain("hiddenModelRevision($appStore.config)");
	expect(source).toContain("observedCatalogRevision");
});

test("does not fabricate cache prices when Pi omits them", () => {
	const rates = { currency: "$", input: 1, output: 4 };
	expect(rateText(rates)).toBe("In $1 · Out $4");
	expect(cacheText(rates)).toBeNull();
});

test("shows only models that Pi reports for configured available providers", () => {
	const providerWithOmittedAvailability: ProviderStatus = {
		id: "omitted",
		name: "Omitted availability",
		configured: true,
		modelCount: 1,
		availableModelCount: 1,
		readinessCheck: false,
	};
	const providers = new Map([
		[provider.id, provider],
		["unavailable", { ...provider, id: "unavailable", available: false }],
		["unconfigured", { ...provider, id: "unconfigured", configured: false }],
		[providerWithOmittedAvailability.id, providerWithOmittedAvailability],
	]);
	const catalog = configuredAvailableModels(
		[
			model,
			{ ...model, id: "model-unavailable", available: false },
			{ ...model, provider: "unavailable", id: "model-provider-unavailable" },
			{ ...model, provider: "unconfigured", id: "model-provider-unconfigured" },
			{ ...model, provider: "omitted", id: "model-provider-omitted-availability" },
		],
		providers,
	);
	expect(catalog).toEqual([
		model,
		{ ...model, provider: "omitted", id: "model-provider-omitted-availability" },
	]);
});

test("labels bulk visibility controls as applying beyond the filtered catalog", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/models-settings.svelte", import.meta.url),
	).text();
	expect(source).toContain("Hide all");
	expect(source).toContain("Show all");
	expect(source).toContain("including models from disconnected providers");
});

test("a forced refresh waits to load provider status and blocks ordinary provider-version loads", async () => {
	let finishRefresh: ((catalog: WireModel[]) => void) | undefined;
	const refresh = new Promise<WireModel[]>((resolve) => (finishRefresh = resolve));
	const calls: string[] = [];
	const forced = refreshModelCatalog(
		async () => {
			calls.push("model.refresh");
			return refresh;
		},
		async () => {
			calls.push("provider.status");
			return { providers: [provider] };
		},
	);
	expect(calls).toEqual(["model.refresh"]);
	expect(shouldLoadModelCatalog(false, true)).toBeFalse();
	finishRefresh?.([model]);
	await expect(forced).resolves.toEqual({
		models: [model],
		report: { providers: [provider] },
		complete: true,
	});
	expect(calls).toEqual(["model.refresh", "provider.status"]);
	expect(shouldLoadModelCatalog(false, false)).toBeTrue();
});

test("a revision blocked by a forced refresh remains unobserved and reloads afterward", async () => {
	let observedRevision: string | null = "provider-1\u0002hidden-1";
	const nextRevision = "provider-2\u0002hidden-2";
	let forceRefreshInFlight = true;
	const reloads: string[] = [];
	const observeRevision = () => {
		if (
			!shouldReloadModelCatalogRevision(true, observedRevision, nextRevision, forceRefreshInFlight)
		)
			return;
		observedRevision = nextRevision;
		reloads.push(nextRevision);
	};

	observeRevision();
	expect(observedRevision).toBe("provider-1\u0002hidden-1");
	expect(reloads).toEqual([]);

	forceRefreshInFlight = false;
	observeRevision();
	expect(observedRevision).toBe(nextRevision);
	expect(reloads).toEqual([nextRevision]);

	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/models-settings.svelte", import.meta.url),
	).text();
	expect(source).toContain("let forceRefreshInFlight = $state(false)");
	expect(source).toContain("const forceRefreshActive = forceRefreshInFlight");
});
