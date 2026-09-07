import { beforeEach, expect, test } from "bun:test";
import { DEFAULT_CONFIG, type WireModel } from "@pixie/contracts";
import { hiddenModelRevision } from "@/settings/state";
import { appStoreApi } from "@/store/app-store";

beforeEach(() => appStoreApi.setState(appStoreApi.getInitialState(), true));

const model: WireModel = {
	provider: "provider-a",
	id: "model-a",
	name: "Model A",
	contextWindow: 128_000,
	maxTokens: 8_000,
	reasoning: false,
	input: ["text"],
	available: true,
	hidden: false,
};

test("a provider change prevents older model replies from replacing the current refresh", () => {
	const actions = appStoreApi.getState();
	const previousVersion = actions.beginModelsRefresh();
	actions.noteProviderChanged();
	const currentVersion = actions.beginModelsRefresh();
	actions.setProviderConfigured(true);

	actions.setModelsForProviderVersion(previousVersion, [model]);
	actions.finishModelsRefresh(previousVersion, { models: [model], complete: true });
	expect(appStoreApi.getState()).toMatchObject({
		models: [],
		providerVersion: currentVersion,
		providerConfigured: true,
		modelsRefreshing: true,
		modelsFresh: false,
	});

	actions.finishModelsRefresh(currentVersion, { models: [model], complete: true });
	expect(appStoreApi.getState()).toMatchObject({
		models: [model],
		modelsRefreshing: false,
		modelsFresh: true,
	});
	actions.dropModelsFreshness();
	expect(appStoreApi.getState()).toMatchObject({ models: [model], modelsFresh: false });
});

test("provider login keeps the active flow when late frames arrive for another login", () => {
	const actions = appStoreApi.getState();
	actions.beginLogin("current", "provider-a");
	actions.applyLoginFrame({
		loginId: "current",
		providerId: "provider-a",
		frame: { kind: "prompt", message: "Enter the code", secret: true },
	});
	const waitingForInput = appStoreApi.getState().activeLogin;
	actions.beginLogin("current", "provider-a");
	actions.applyLoginFrame({
		loginId: "previous",
		providerId: "provider-b",
		frame: { kind: "error", message: "Expired" },
	});
	expect(appStoreApi.getState().activeLogin).toBe(waitingForInput);

	actions.clearLoginInput();
	expect(appStoreApi.getState().activeLogin).toEqual({
		loginId: "current",
		providerId: "provider-a",
		status: "active",
	});
	actions.applyLoginFrame({
		loginId: "current",
		providerId: "provider-a",
		frame: { kind: "progress", message: "Connecting" },
	});
	actions.applyLoginFrame({
		loginId: "current",
		providerId: "provider-a",
		frame: { kind: "success" },
	});
	expect(appStoreApi.getState().activeLogin).toEqual({
		loginId: "current",
		providerId: "provider-a",
		status: "success",
	});
});

test("settings opens System while agent capabilities are unavailable", () => {
	appStoreApi.setState({ agentProfile: null });
	appStoreApi.getState().openSettings();
	expect(appStoreApi.getState()).toMatchObject({
		settingsOpen: true,
		settingsSection: "system",
	});
});

test("model visibility revision is stable but changes with the hidden catalog", () => {
	expect(hiddenModelRevision(DEFAULT_CONFIG)).toBe("");
	expect(
		hiddenModelRevision({
			hiddenModels: [
				{ provider: "provider-b", id: "model-b" },
				{ provider: "provider-a", id: "model-a" },
			],
		}),
	).toBe(
		hiddenModelRevision({
			hiddenModels: [
				{ provider: "provider-a", id: "model-a" },
				{ provider: "provider-b", id: "model-b" },
			],
		}),
	);
	expect(
		hiddenModelRevision({ hiddenModels: [{ provider: "provider-a", id: "model-a" }] }),
	).not.toBe("");
});
