import type { PiPreferences, ProviderStatus, WireModel } from "@pixie/shared";

export const THINKING_EFFORTS = [
	"off",
	"minimal",
	"low",
	"medium",
	"high",
	"xhigh",
	"max",
] as const;

export function parseCompactionReserveTokens(
	tokens: number | undefined,
): { valid: true; value?: number } | { valid: false } {
	if (tokens === undefined) return { valid: true };
	if (!Number.isSafeInteger(tokens) || tokens < 1024 || tokens > 1000000) return { valid: false };
	return { valid: true, value: tokens };
}

export function compactionReserveTokensValue(preferences: PiPreferences): number | undefined {
	return preferences.compactionReserveTokens === undefined
		? undefined
		: preferences.compactionReserveTokens;
}

export type PreferenceSavePayload = {
	piThinkingEffort?: NonNullable<PiPreferences["piThinkingEffort"]>;
	compactionReserveTokens?: number;
};

export type PreferenceSaveResult =
	| { ok: true; payload: PreferenceSavePayload }
	| { ok: false; error: string };

/**
 * Build the Pi preference save payload.
 *
 * A writable compaction reserve is parsed, bounded and included so a writable
 * projection round-trips; an out-of-range value fails here instead of being
 * silently dropped. A cleared field omits the key so the saved value stands.
 * A read-only reserve never joins the payload, preserving the fail-closed
 * controller/host behavior.
 */
export function preferenceSavePayload(
	preferences: PiPreferences,
	reserveTokens: number | undefined,
	reserveWritable: boolean,
): PreferenceSaveResult {
	const payload: PreferenceSavePayload = {};
	if (preferences.piThinkingEffort !== undefined)
		payload.piThinkingEffort = preferences.piThinkingEffort;
	if (reserveWritable && reserveTokens !== undefined) {
		const parsed = parseCompactionReserveTokens(reserveTokens);
		if (!parsed.valid || parsed.value === undefined)
			return {
				ok: false,
				error: "Compaction reserve tokens must be a whole number between 1024 and 1000000.",
			};
		payload.compactionReserveTokens = parsed.value;
	}
	return { ok: true, payload };
}

/**
 * The selected Pi exposes no public setter for the compaction reserve, so the
 * control is disabled unless the projection explicitly marks it writable.
 * Missing metadata fails closed instead of offering a mutation that cannot
 * complete.
 */
export function compactionReserveWritable(preferences: PiPreferences): boolean {
	return (
		preferences.keys?.some(
			(entry) => entry.key === "compactionReserveTokens" && entry.writable === true,
		) === true
	);
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
		!/^[\p{L}\p{N}_ -]+$/u.test(value.trim()) ||
		new TextEncoder().encode(value.trim()).byteLength > 80
	) {
		return "Use a non-empty agent name of at most 80 UTF-8 bytes using letters, numbers, spaces, underscores or hyphens.";
	}
	return null;
}
