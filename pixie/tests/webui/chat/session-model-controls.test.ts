import { expect, test } from "bun:test";
import type { ProviderStatus, WireModel } from "@pixie/contracts";
import { compile } from "svelte/compiler";
import {
	firstModelForProvider,
	modelsForSelectedProvider,
	sessionSelectableModels,
	thinkingLevelsForCurrent,
} from "@/chat/session/session-model";

const component = new URL(
	"../../../webui/src/chat/session/session-model-controls.svelte",
	import.meta.url,
);
const model: WireModel = {
	provider: "provider-a",
	id: "model-a",
	name: "Model A",
	available: true,
	hidden: false,
	reasoning: true,
	thinkingLevels: ["low", "medium", "high"],
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

test("session model controls admit only configured, available provider models", () => {
	const providerWithOmittedAvailability: ProviderStatus = {
		id: "provider-c",
		name: "Provider C",
		configured: true,
		modelCount: 1,
		availableModelCount: 1,
		readinessCheck: false,
	};
	const available = sessionSelectableModels(
		[
			model,
			{ ...model, id: "hidden", hidden: true },
			{ ...model, id: "unavailable", available: false },
			{ ...model, provider: "provider-b", id: "other" },
			{ ...model, provider: "provider-c", id: "omitted-provider-availability" },
		],
		[
			provider,
			{ ...provider, id: "provider-b", available: false },
			providerWithOmittedAvailability,
		],
	);
	expect(available).toEqual([
		model,
		{ ...model, provider: "provider-c", id: "omitted-provider-availability" },
	]);
	expect(firstModelForProvider(available, "provider-a")).toEqual(model);
	expect(firstModelForProvider(available, "provider-b")).toBeNull();
	expect(modelsForSelectedProvider(available, "provider-c")).toEqual([
		{ ...model, provider: "provider-c", id: "omitted-provider-availability" },
	]);
});

test("model and thinking selection retain catalog order and the current value", () => {
	const otherModel = { ...model, provider: "provider-b", id: "model-b", name: "Model B" };
	expect(modelsForSelectedProvider([model, otherModel], "provider-a")).toEqual([model]);
	expect(firstModelForProvider([model, otherModel], "provider-b")).toEqual(otherModel);
	expect(thinkingLevelsForCurrent("medium", ["off", "low", "medium", "high"])).toEqual([
		"off",
		"low",
		"medium",
		"high",
	]);
	expect(thinkingLevelsForCurrent("custom", ["off", "high"])).toEqual(["custom", "off", "high"]);
});

test("session model controls compile as Svelte and preserve loading and selection contracts", async () => {
	const source = await Bun.file(component).text();
	expect(source).not.toMatch(/from ["'](?:react|react-dom|lucide-react)/);
	expect(compile(source, { filename: component.pathname, generate: false }).warnings).toEqual([]);
	for (const testId of [
		"session-model-controls",
		"session-provider-select",
		"session-model-select",
		"session-thinking-select",
	])
		expect(source).toContain(`data-testid="${testId}"`);
	expect(source).toContain("disabled={modelControlsDisabled}");
	expect(source).toContain("disabled={thinkingDisabled}");
	expect(source).toContain("Loading providers…");
	expect(source).toContain("Loading models…");
	expect(source).toContain("(current)");
	expect(source).toContain("hiddenModelRevision($appStore.config)");
	expect(source).toContain('getTransport().request("session.setModel"');
	expect(source).toContain('getTransport().request("model.clampThinking"');
	expect(source).toContain('getTransport().request("session.setThinkingLevel"');
	expect(source).toContain("configRevision");
	expect(source).toContain("setCurrentModel(requestedSessionId, nextModel, configRevision)");
	expect(source).toContain("setThinkingLevel(requestedSessionId, clamped.level, configRevision)");
});
