import { expect, test } from "bun:test";
import type { ProviderStatus, WireModel } from "@pixie/shared";
import {
	agentNameError,
	compactionReserveTokensValue,
	compactionReserveWritable,
	defaultModelSuggestions,
	defaultProviderChoices,
	defaultProviderSelectable,
	parseCompactionReserveTokens,
	preferenceSavePayload,
	shouldClearAgentEditorAfterMutation,
	THINKING_EFFORTS,
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

test("global thinking preferences include the native maximum level", () => {
	expect(THINKING_EFFORTS).toContain("max");
});

test("reconciles a cleared threshold with the canonical save response", () => {
	expect(parseCompactionReserveTokens(undefined)).toEqual({ valid: true });
	expect(compactionReserveTokensValue({ compactionReserveTokens: 16384 })).toBe(16384);
	expect(compactionReserveTokensValue({})).toBeUndefined();
});

test("the compaction reserve is read-only unless the projection marks it writable", () => {
	expect(compactionReserveWritable({ compactionReserveTokens: 16384 })).toBe(false);
	expect(compactionReserveWritable({})).toBe(false);
	expect(
		compactionReserveWritable({
			keys: [{ key: "compactionReserveTokens", writable: false, source: "read-only" }],
		}),
	).toBe(false);
	expect(
		compactionReserveWritable({
			keys: [{ key: "compactionReserveTokens", writable: true, source: "pi" }],
		}),
	).toBe(true);
});

test("a writable compaction reserve joins the save payload once parsed and bounded", () => {
	expect(preferenceSavePayload({ piThinkingEffort: "low" }, 16384, true)).toEqual({
		ok: true,
		payload: { piThinkingEffort: "low", compactionReserveTokens: 16384 },
	});
	// A cleared field omits the key so the saved value stands.
	expect(preferenceSavePayload({}, undefined, true)).toEqual({ ok: true, payload: {} });
	// Out-of-range input fails visibly instead of being dropped.
	expect(preferenceSavePayload({}, 512, true)).toEqual({
		ok: false,
		error: "Compaction reserve tokens must be a whole number between 1024 and 1000000.",
	});
	expect(preferenceSavePayload({}, 100.5, true).ok).toBe(false);
	expect(preferenceSavePayload({}, Number.NaN, true).ok).toBe(false);
});

test("a read-only compaction reserve never joins the save payload", () => {
	expect(preferenceSavePayload({ compactionReserveTokens: 16384 }, 32768, false)).toEqual({
		ok: true,
		payload: {},
	});
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
	// The reserve control is enabled only for a writable projection, and the
	// builder output (never the raw input) is what reaches the save request.
	expect(source).toContain("disabled={busy || !preferencesReady || !reserveWritable}");
	expect(source).toContain("preferenceSavePayload(preferences, reserveTokens, reserveWritable)");
	expect(source).toContain("actionError = result.error");
	expect(source).toContain('getTransport().request("pi.preferencesSave", result.payload)');
});
