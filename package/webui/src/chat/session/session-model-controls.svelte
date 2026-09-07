<script lang="ts">
import type { ProviderStatus, ThinkingLevel, WireModel } from "@pixie/contracts";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi } from "../../store";
import { hiddenModelRevision } from "../../settings/state";
import {
	firstModelForProvider,
	modelKey,
	modelsForSelectedProvider,
	sessionSelectableModels,
	thinkingLevelsForCurrent,
} from "./session-model";

interface Props {
	sessionId: string;
	model: WireModel | null;
	thinkingLevel: ThinkingLevel;
	isStreaming: boolean;
}

let { sessionId, model, thinkingLevel, isStreaming }: Props = $props();
let catalog = $state<WireModel[]>([]);
let providers = $state<ProviderStatus[]>([]);
let reportedThinkingLevels = $state<ThinkingLevel[]>([]);
let loading = $state(true);
let loadError = $state<string | null>(null);
let refresh = $state(0);
let busy = $state<"model" | "thinking" | null>(null);
let loadSequence = 0;
const componentId = $props.id();
const providerLabelId = `session-provider-${componentId}`;
const modelLabelId = `session-model-${componentId}`;
const thinkingLabelId = `session-thinking-${componentId}`;
let providerVersion = $derived($appStore.providerVersion);
let visibilityRevision = $derived(hiddenModelRevision($appStore.config));

let models = $derived(sessionSelectableModels(catalog, providers));
let providerIds = $derived([...new Set(models.map((candidate) => candidate.provider))]);
let providerNames = $derived(new Map(providers.map((provider) => [provider.id, provider.name])));
let selectedModel = $derived(model ?? null);
let currentModelSelectable = $derived(
	selectedModel
		? models.some((candidate) => modelKey(candidate) === modelKey(selectedModel))
		: false,
);
let currentProviderSelectable = $derived(
	selectedModel ? providerIds.includes(selectedModel.provider) : false,
);
let selectedProvider = $derived(selectedModel?.provider ?? "");
let providerModels = $derived(modelsForSelectedProvider(models, selectedProvider));
let thinkingLevels = $derived(thinkingLevelsForCurrent(thinkingLevel, reportedThinkingLevels));
let modelControlsDisabled = $derived(
	loading || isStreaming || busy !== null || models.length === 0,
);
let thinkingDisabled = $derived(isStreaming || busy !== null);

function notifyError(cause: unknown, title: string): void {
	appStoreApi.getState().pushToast({ variant: "error", message: errorText(cause), title });
}

$effect(() => {
	const id = sessionId;
	void providerVersion;
	void visibilityRevision;
	void refresh;
	const sequence = ++loadSequence;
	loading = true;
	void Promise.allSettled([
		getTransport().request("model.list", {}),
		getTransport().request("provider.status", {}),
		getTransport().request("model.thinkingLevels", { sessionId: id }),
	])
		.then(([nextModels, report, thinking]) => {
			if (sequence !== loadSequence) return;
			if (nextModels.status === "fulfilled") catalog = nextModels.value;
			if (report.status === "fulfilled") providers = report.value.providers;
			if (thinking.status === "fulfilled") reportedThinkingLevels = thinking.value.levels;
			const failures = [nextModels, report, thinking].filter(
				(result) => result.status === "rejected",
			);
			loadError = failures.length ? "Some model controls could not be refreshed." : null;
		})
		.finally(() => {
			if (sequence === loadSequence) loading = false;
		});
	return () => {
		if (sequence === loadSequence) loadSequence += 1;
	};
});

async function changeModel(nextModel: WireModel): Promise<void> {
	const requestedSessionId = sessionId;
	const currentModel = model;
	const requestedThinkingLevel = thinkingLevel;
	if (currentModel && modelKey(nextModel) === modelKey(currentModel)) return;
	busy = "model";
	try {
		try {
			const configRevision =
				appStoreApi.getState().sessions[requestedSessionId]?.configRevision ?? 0;
			await getTransport().request("session.setModel", {
				sessionId: requestedSessionId,
				model: nextModel,
			});
			appStoreApi.getState().setCurrentModel(requestedSessionId, nextModel, configRevision);
		} catch (cause) {
			notifyError(cause, "Couldn't change the session model");
			return;
		}
		try {
			const thinking = await getTransport().request("model.thinkingLevels", {
				sessionId: requestedSessionId,
			});
			reportedThinkingLevels = thinking.levels;
			const { level } = await getTransport().request("model.clampThinking", {
				sessionId: requestedSessionId,
				level: requestedThinkingLevel,
			});
			if (level === requestedThinkingLevel) return;
			const configRevision =
				appStoreApi.getState().sessions[requestedSessionId]?.configRevision ?? 0;
			await getTransport().request("session.setThinkingLevel", {
				sessionId: requestedSessionId,
				level,
			});
			appStoreApi.getState().setThinkingLevel(requestedSessionId, level, configRevision);
		} catch (cause) {
			notifyError(cause, "Couldn't update thinking after changing the model");
		}
	} finally {
		busy = null;
	}
}

async function changeThinking(level: ThinkingLevel): Promise<void> {
	const requestedSessionId = sessionId;
	const currentThinkingLevel = thinkingLevel;
	if (level === currentThinkingLevel) return;
	busy = "thinking";
	try {
		const clamped = await getTransport().request("model.clampThinking", {
			sessionId: requestedSessionId,
			level,
		});
		if (clamped.level === currentThinkingLevel) return;
		const configRevision = appStoreApi.getState().sessions[requestedSessionId]?.configRevision ?? 0;
		await getTransport().request("session.setThinkingLevel", {
			sessionId: requestedSessionId,
			level: clamped.level,
		});
		appStoreApi.getState().setThinkingLevel(requestedSessionId, clamped.level, configRevision);
	} catch (cause) {
		notifyError(cause, "Couldn't change the thinking level");
	} finally {
		busy = null;
	}
}
</script>

<div
	data-testid="session-model-controls"
	aria-busy={loading || busy !== null}
	class="flex min-w-0 flex-wrap items-center gap-xs"
>
	{#if loadError}<button type="button" class="text-feedback-warning tr-text-metadata" title={loadError} onclick={() => refresh += 1}>Retry model controls</button>{/if}
	<label class="flex min-w-0 items-center gap-2xs">
		<span id={providerLabelId} class="sr-only">Provider</span>
		<select
			data-testid="session-provider-select"
			aria-labelledby={providerLabelId}
			value={selectedProvider}
			disabled={modelControlsDisabled}
			onchange={(event) => {
				const next = firstModelForProvider(models, event.currentTarget.value);
				if (next) void changeModel(next);
			}}
			class="min-w-0 max-w-28 rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-xs py-0.5 text-text-default tr-text-metadata outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary disabled:text-text-muted"
		>
			{#if selectedModel && !currentProviderSelectable}
				<option value={selectedModel.provider} disabled>{providerNames.get(selectedModel.provider) ?? selectedModel.provider} (current)</option>
			{/if}
			<option value="" disabled>{loading ? "Loading providers…" : providerIds.length ? "Choose provider" : "No providers"}</option>
			{#each providerIds as providerId (providerId)}
				<option value={providerId}>{providerNames.get(providerId) ?? providerId}</option>
			{/each}
		</select>
	</label>
	<label class="flex min-w-0 items-center gap-2xs">
		<span id={modelLabelId} class="sr-only">Model</span>
		<select
			data-testid="session-model-select"
			aria-labelledby={modelLabelId}
			value={selectedModel ? modelKey(selectedModel) : ""}
			disabled={modelControlsDisabled}
			onchange={(event) => {
				const next = models.find((candidate) => modelKey(candidate) === event.currentTarget.value);
				if (next) void changeModel(next);
			}}
			class="min-w-0 max-w-44 rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-xs py-0.5 text-text-default tr-text-metadata outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary disabled:text-text-muted"
		>
			{#if selectedModel && !currentModelSelectable}
				<option value={modelKey(selectedModel)} disabled>{selectedModel.name || selectedModel.id} (current)</option>
			{/if}
			<option value="" disabled>{loading ? "Loading models…" : models.length ? "Choose model" : "No available models"}</option>
			{#each providerModels as candidate (modelKey(candidate))}
				<option value={modelKey(candidate)}>{candidate.name || candidate.id}</option>
			{/each}
		</select>
	</label>
	<label class="flex min-w-0 items-center gap-2xs">
		<span id={thinkingLabelId} class="sr-only">Thinking</span>
		<select
			data-testid="session-thinking-select"
			aria-labelledby={thinkingLabelId}
			value={thinkingLevel}
			disabled={thinkingDisabled}
			onchange={(event) => void changeThinking(event.currentTarget.value as ThinkingLevel)}
			class="min-w-0 max-w-24 rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-xs py-0.5 text-text-default tr-text-metadata outline-none hover:bg-control-bg-hovered focus-visible:ring-2 focus-visible:ring-primary disabled:text-text-muted"
		>
			{#each thinkingLevels as level (level)}<option value={level}>{level}</option>{/each}
		</select>
	</label>
</div>
