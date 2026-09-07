import { expect, test } from "bun:test";
import type { ProviderStatus, WireModel } from "@pixie/contracts";
import {
	agentNameError,
	compactionReserveTokensValue,
	defaultModelSuggestions,
	defaultProviderChoices,
	defaultProviderSelectable,
	parseCompactionReserveTokens,
	shouldClearAgentEditorAfterMutation,
	unavailableDefaultProviderOption,
} from "@/settings/sections/pi-settings";

test("default providers come from configured available status, even without visible models", () => {
	const provider: ProviderStatus = {
		id: "private-provider",
		name: "Private provider",
		configured: true,
		available: true,
		modelCount: 0,
		availableModelCount: 0,
		readinessCheck: false,
	};
	const hiddenModel: WireModel = {
		provider: provider.id,
		id: "hidden-model",
		name: "Hidden model",
		available: true,
		hidden: true,
	};

	expect(defaultProviderChoices([provider])).toEqual([provider]);
	expect(defaultModelSuggestions([hiddenModel], provider.id)).toEqual([]);
});

test("retains an unavailable persisted provider as a disabled current selection", () => {
	const provider: ProviderStatus = {
		id: "temporarily-offline",
		name: "Temporarily offline",
		configured: true,
		available: false,
		modelCount: 1,
		availableModelCount: 0,
		readinessCheck: false,
	};

	expect(defaultProviderSelectable(provider.id, [provider])).toBe(false);
	expect(unavailableDefaultProviderOption(provider.id, [provider])).toEqual({
		id: provider.id,
		name: provider.name,
	});
	expect(unavailableDefaultProviderOption("missing-provider", [])).toEqual({
		id: "missing-provider",
		name: "missing-provider",
	});
	expect(defaultProviderSelectable(provider.id, [{ ...provider, available: true }])).toBe(true);
	expect(
		unavailableDefaultProviderOption(provider.id, [{ ...provider, available: true }]),
	).toBeNull();
});

test("an older agent mutation cannot clear a newer editor selection", async () => {
	const olderMutation = { sequence: 4, editingId: "older-agent" };
	await Promise.resolve();
	expect(shouldClearAgentEditorAfterMutation("newer-agent", olderMutation, 5)).toBe(false);
});

test("normalizes Svelte number-input threshold values, including a cleared field", () => {
	expect(parseCompactionReserveTokens(16384)).toEqual({ valid: true, value: 16384 });
	expect(parseCompactionReserveTokens(undefined)).toEqual({ valid: true });
	expect(parseCompactionReserveTokens(0)).toEqual({ valid: false });
	expect(parseCompactionReserveTokens(Number.NaN)).toEqual({ valid: false });
	expect(parseCompactionReserveTokens(100.1)).toEqual({ valid: false });
});

test("reconciles a cleared threshold with the canonical save response", () => {
	expect(parseCompactionReserveTokens(undefined)).toEqual({ valid: true });
	expect(compactionReserveTokensValue({ compactionReserveTokens: 16384 })).toBe(16384);
	expect(compactionReserveTokensValue({})).toBeUndefined();
});

test("validates agent names by UTF-8 bytes and path separators before submitting", () => {
	expect(agentNameError("Reviewer")).toBeNull();
	expect(agentNameError("bad/name")).toContain("80 UTF-8 bytes");
	expect(agentNameError("bad\\name")).toContain("80 UTF-8 bytes");
	expect(agentNameError("🪿".repeat(21))).toContain("80 UTF-8 bytes");
});

test("the Svelte editor retains the default and agent form contracts", async () => {
	const source = await Bun.file(
		new URL("../../../webui/src/settings/sections/pi-settings.svelte", import.meta.url),
	).text();
	for (const testId of [
		"settings-pi",
		"default-provider",
		"default-model",
		"auto-compact-threshold",
		"pi-thinking-effort",
		"agent-catalog-project",
		"agent-row",
		"agent-instructions",
		"agent-model",
	]) {
		expect(source).toContain(`data-testid="${testId}"`);
	}
	expect(source).toContain('confirmTestId="confirm-remove-agent"');
	expect(source).toContain("bind:value={draft.instructions}");
	expect(source).toContain("defaultModelSuggestions(models, defaults.providerId)");
	expect(source).toContain("(current, unavailable)");
	expect(source).toContain(
		"disabled={busy || loading || !defaultsReady || !selectedDefaultProviderAvailable}",
	);
	expect(source).toContain("applyPreferences(saved)");
});
