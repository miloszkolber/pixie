import type { ProviderStatus, ThinkingLevel, WireModel } from "@pixie/contracts";

export function modelKey(model: Pick<WireModel, "provider" | "id">): string {
	return `${model.provider}\u0000${model.id}`;
}

export function sessionSelectableModels(
	models: readonly WireModel[],
	providers: readonly ProviderStatus[],
): WireModel[] {
	const availableProviders = new Set(
		providers
			.filter((provider) => provider.configured && provider.available !== false)
			.map((provider) => provider.id),
	);
	return models.filter(
		(model) => availableProviders.has(model.provider) && model.available && !model.hidden,
	);
}

export function thinkingLevelsForCurrent(
	level: ThinkingLevel,
	reported: readonly ThinkingLevel[],
): ThinkingLevel[] {
	return reported.includes(level) ? [...reported] : [level, ...reported];
}

export function firstModelForProvider(
	models: readonly WireModel[],
	providerId: string,
): WireModel | null {
	return models.find((model) => model.provider === providerId) ?? null;
}

export function modelsForSelectedProvider(
	models: readonly WireModel[],
	providerId: string,
): WireModel[] {
	return models.filter((model) => model.provider === providerId);
}
