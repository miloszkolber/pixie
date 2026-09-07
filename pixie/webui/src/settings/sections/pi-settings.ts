import type {
	PiPreferences,
	PiProviderDefaults,
	ProviderStatus,
	WireModel,
} from "@pixie/contracts";

export const THINKING_EFFORTS = ["off", "minimal", "low", "medium", "high", "xhigh"] as const;

export function parseCompactionReserveTokens(
	percent: number | undefined,
): { valid: true; value?: number } | { valid: false } {
	if (percent === undefined) return { valid: true };
	if (!Number.isSafeInteger(percent) || percent < 1024 || percent > 1000000)
		return { valid: false };
	return { valid: true, value: percent };
}

export function compactionReserveTokensValue(preferences: PiPreferences): number | undefined {
	return preferences.compactionReserveTokens === undefined
		? undefined
		: preferences.compactionReserveTokens;
}

export type AgentDraft = {
	name: string;
	description: string;
	instructions: string;
	scope: "global" | "project";
	projectId: string;
	root: string;
	modelId: string;
};

export function emptyAgent(): AgentDraft {
	return {
		name: "",
		description: "",
		instructions: "",
		scope: "global",
		projectId: "",
		root: "",
		modelId: "",
	};
}

export function defaultProviderChoices(providers: readonly ProviderStatus[]): ProviderStatus[] {
	return providers.filter((provider) => provider.configured && provider.available !== false);
}

export function defaultProviderSelectable(
	providerId: string | null,
	providers: readonly ProviderStatus[],
): boolean {
	return (
		providerId !== null &&
		providers.some(
			(provider) =>
				provider.id === providerId && provider.configured && provider.available !== false,
		)
	);
}

export function unavailableDefaultProviderOption(
	providerId: string | null,
	providers: readonly ProviderStatus[],
): Pick<ProviderStatus, "id" | "name"> | null {
	if (!providerId || defaultProviderSelectable(providerId, providers)) return null;
	const current = providers.find((provider) => provider.id === providerId);
	return { id: providerId, name: current?.name ?? providerId };
}

export function defaultModelSuggestions(
	models: readonly WireModel[],
	providerId: string | null,
): WireModel[] {
	return models.filter(
		(model) => model.available && !model.hidden && model.provider === providerId,
	);
}

export function defaultSettingsView(
	defaults: PiProviderDefaults,
	providers: readonly ProviderStatus[],
	models: readonly WireModel[],
) {
	return {
		defaults,
		providers: defaultProviderChoices(providers),
		models: defaultModelSuggestions(models, defaults.providerId),
	};
}

export function shouldClearAgentEditorAfterMutation(
	currentEditingId: string | null,
	mutation: { sequence: number; editingId: string | null },
	currentSequence: number,
): boolean {
	return currentSequence === mutation.sequence && currentEditingId === mutation.editingId;
}

export function agentNameError(value: string): string | null {
	if (
		!value.trim() ||
		value.includes("/") ||
		value.includes("\\") ||
		new TextEncoder().encode(value.trim()).byteLength > 80
	) {
		return "Use a non-empty agent name of at most 80 UTF-8 bytes without / or \\.";
	}
	return null;
}
