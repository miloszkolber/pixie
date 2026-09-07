<script lang="ts">
import type {
	PiAgentCatalogEntry,
	PiPreferences,
	PiProviderDefaults,
	ProviderStatus,
	WireModel,
} from "@pixie/contracts";
import { onMount } from "svelte";
import Button from "@/components/button.svelte";
import ConfirmDialog from "@/components/confirm-dialog.svelte";
import Icon from "@/components/icon.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";
import {
	type AgentDraft,
	agentNameError,
	compactionReserveTokensValue,
	defaultModelSuggestions,
	defaultProviderChoices,
	defaultProviderSelectable,
	emptyAgent,
	parseCompactionReserveTokens,
	shouldClearAgentEditorAfterMutation,
	THINKING_EFFORTS,
	unavailableDefaultProviderOption,
} from "./pi-settings";

let preferences = $state<PiPreferences>({});
let reserveTokens = $state<number | undefined>(undefined);
let defaults = $state<PiProviderDefaults>({ providerId: null, modelId: null });
let models = $state<WireModel[]>([]);
let providers = $state<ProviderStatus[]>([]);
let catalogProjectId = $state("");
let agents = $state<PiAgentCatalogEntry[]>([]);
let draft = $state<AgentDraft>(emptyAgent());
let editing = $state<PiAgentCatalogEntry | null>(null);
let loading = $state(true);
let preferencesReady = $state(false);
let defaultsReady = $state(false);
let loadError = $state<string | null>(null);
let actionError = $state<string | null>(null);
let busy = $state(false);
let deleteTarget = $state<PiAgentCatalogEntry | null>(null);
let sequence = 0;
let agentMutationSequence = 0;
let mounted = false;
let observedCatalogKey = $state<string | null>(null);
let agentsAvailable = $derived($appStore.agentProfile?.capabilities?.agents === 1);
let projects = $derived($appStore.projects);
let catalogRoot = $derived(
	projects.find((project) => project.id === catalogProjectId)?.roots[0] ?? "",
);
let catalogKey = $derived(`${catalogProjectId}\0${catalogRoot}`);
let availableModels = $derived(models.filter((model) => model.available && !model.hidden));
let selectableProviders = $derived(defaultProviderChoices(providers));
let selectedDefaultProviderAvailable = $derived(
	defaultProviderSelectable(defaults.providerId, providers),
);
let currentUnavailableProvider = $derived(
	unavailableDefaultProviderOption(defaults.providerId, providers),
);
let defaultSuggestions = $derived(defaultModelSuggestions(models, defaults.providerId));

function notifyError(error: unknown, title: string): void {
	actionError = `${title}: ${errorText(error)}`;
}

function applyPreferences(next: PiPreferences): void {
	preferences = next;
	reserveTokens = compactionReserveTokensValue(next);
}

async function load(
	projectId = catalogProjectId,
	root = catalogRoot,
	catalogOnly = false,
): Promise<void> {
	const current = ++sequence;
	loading = true;
	loadError = null;
	const catalogRequest =
		!agentsAvailable || (projectId && !root)
			? Promise.resolve([] as PiAgentCatalogEntry[])
			: getTransport().request("pi.agentList", projectId ? { projectId, root } : {});
	const results = await Promise.allSettled([
		catalogOnly ? Promise.resolve(null) : getTransport().request("pi.preferencesRead", {}),
		catalogOnly ? Promise.resolve(null) : getTransport().request("pi.defaultsRead", {}),
		catalogOnly ? Promise.resolve(null) : getTransport().request("model.list", {}),
		catalogOnly ? Promise.resolve(null) : getTransport().request("provider.status", {}),
		catalogRequest,
	]);
	if (!mounted || current !== sequence) return;
	const [prefs, savedDefaults, nextModels, nextProviders, nextAgents] = results;
	if (prefs.status === "fulfilled" && prefs.value) {
		applyPreferences(prefs.value);
		preferencesReady = true;
	}
	if (savedDefaults.status === "fulfilled" && savedDefaults.value) {
		defaults = savedDefaults.value;
		defaultsReady = true;
	}
	if (nextModels.status === "fulfilled" && nextModels.value) models = nextModels.value;
	if (nextProviders.status === "fulfilled" && nextProviders.value)
		providers = nextProviders.value.providers;
	if (nextAgents.status === "fulfilled") agents = nextAgents.value;
	if (results.some((result) => result.status === "rejected"))
		loadError =
			"Some Pi settings could not be loaded. Successfully loaded data and drafts are retained.";
	loading = false;
}

onMount(() => {
	mounted = true;
	observedCatalogKey = catalogKey;
	void load(catalogProjectId, catalogRoot);
	return () => {
		mounted = false;
		sequence += 1;
		agentMutationSequence += 1;
	};
});

$effect(() => {
	const nextKey = catalogKey;
	if (!mounted || observedCatalogKey === null || observedCatalogKey === nextKey) return;
	observedCatalogKey = nextKey;
	void load(catalogProjectId, catalogRoot, true);
});

async function savePreferences(): Promise<void> {
	if (!preferencesReady || busy) return;
	actionError = null;
	const threshold = parseCompactionReserveTokens(reserveTokens);
	if (!threshold.valid) {
		appStoreApi.getState().pushToast({
			variant: "error",
			message: "Use a percentage greater than 0 and no more than 100.",
			title: "Invalid threshold",
		});
		return;
	}
	busy = true;
	try {
		const saved = await getTransport().request("pi.preferencesSave", {
			...(threshold.value !== undefined ? { compactionReserveTokens: threshold.value } : {}),
			...(preferences.piThinkingEffort !== undefined
				? { piThinkingEffort: preferences.piThinkingEffort }
				: {}),
		});
		applyPreferences(saved);
	} catch (error) {
		notifyError(error, "Couldn't save Pi preferences");
	} finally {
		busy = false;
	}
}

async function resetPreference(key: "compactionReserveTokens" | "piThinkingEffort"): Promise<void> {
	busy = true;
	try {
		const reset = await getTransport().request("pi.preferencesReset", { keys: [key] });
		preferences = reset;
		if (key === "compactionReserveTokens") {
			reserveTokens = compactionReserveTokensValue(reset);
		}
	} catch (error) {
		notifyError(error, "Couldn't reset Pi preference");
	} finally {
		busy = false;
	}
}

async function saveDefaults(): Promise<void> {
	if (!defaultsReady || busy) return;
	actionError = null;
	if (!defaults.providerId || !selectedDefaultProviderAvailable) {
		appStoreApi.getState().pushToast({
			variant: "error",
			message: "Choose a configured provider that is currently available.",
			title: "Default provider required",
		});
		return;
	}
	busy = true;
	try {
		defaults = await getTransport().request("pi.defaultsSave", {
			providerId: defaults.providerId,
			modelId: defaults.modelId,
		});
	} catch (error) {
		notifyError(error, "Couldn't save Pi defaults");
	} finally {
		busy = false;
	}
}

async function clearDefaults(): Promise<void> {
	busy = true;
	try {
		defaults = await getTransport().request("pi.defaultsClear", {});
	} catch (error) {
		notifyError(error, "Couldn't clear Pi defaults");
	} finally {
		busy = false;
	}
}

function editAgent(agent: PiAgentCatalogEntry): void {
	editing = agent;
	draft = {
		name: agent.name,
		description: agent.description,
		instructions: agent.instructions,
		scope: agent.scope,
		projectId: catalogProjectId,
		root: catalogRoot,
		modelId: agent.modelId ?? "",
	};
}

async function saveAgent(): Promise<void> {
	const nameError = agentNameError(draft.name);
	if (nameError) {
		appStoreApi.getState().pushToast({
			variant: "error",
			message: nameError,
			title: "Invalid agent name",
		});
		return;
	}
	if (draft.scope === "project" && (!draft.projectId || !draft.root)) {
		appStoreApi.getState().pushToast({
			variant: "error",
			message: "Choose an admitted project for this agent.",
			title: "Project required",
		});
		return;
	}
	const activeEditing = editing;
	const mutation = {
		sequence: ++agentMutationSequence,
		editingId: activeEditing?.id ?? null,
	};
	busy = true;
	try {
		const modelId = draft.modelId || undefined;
		if (activeEditing) {
			await getTransport().request("pi.agentUpdate", {
				id: activeEditing.id,
				name: draft.name,
				description: draft.description,
				instructions: draft.instructions,
				...(draft.scope === "project" ? { projectId: draft.projectId, root: draft.root } : {}),
				...(modelId !== (activeEditing.modelId ?? "") ? { modelId: modelId ?? null } : {}),
			});
		} else {
			await getTransport().request("pi.agentCreate", {
				name: draft.name,
				description: draft.description,
				instructions: draft.instructions,
				scope: draft.scope,
				...(draft.scope === "project" ? { projectId: draft.projectId, root: draft.root } : {}),
				...(modelId ? { modelId } : {}),
			});
		}
		if (shouldClearAgentEditorAfterMutation(editing?.id ?? null, mutation, agentMutationSequence)) {
			editing = null;
			draft = emptyAgent();
		}
		await load(catalogProjectId, catalogRoot, true);
	} catch (error) {
		notifyError(error, activeEditing ? "Couldn't update Pi agent" : "Couldn't create Pi agent");
	} finally {
		busy = false;
	}
}

async function removeAgent(agent: PiAgentCatalogEntry): Promise<void> {
	const mutation = { sequence: ++agentMutationSequence, editingId: agent.id };
	busy = true;
	try {
		await getTransport().request("pi.agentDelete", {
			id: agent.id,
			...(agent.scope === "project" && catalogProjectId && catalogRoot
				? { projectId: catalogProjectId, root: catalogRoot }
				: {}),
		});
		if (shouldClearAgentEditorAfterMutation(editing?.id ?? null, mutation, agentMutationSequence)) {
			editing = null;
			draft = emptyAgent();
		}
		await load(catalogProjectId, catalogRoot, true);
	} finally {
		busy = false;
	}
}

function changeProjectScope(projectId: string): void {
	sequence += 1;
	catalogProjectId = projectId;
}

function changeDraftProject(projectId: string): void {
	draft = {
		...draft,
		projectId,
		root: projects.find((project) => project.id === projectId)?.roots[0] ?? "",
	};
}
</script>

<div data-testid="settings-pi" class="flex flex-col gap-xl">
 {#if loadError}<p role="alert" class="text-feedback-warning tr-text-ui">{loadError} Close and reopen settings to retry the initial load.</p>{/if}
 {#if actionError}<p role="alert" class="text-feedback-error tr-text-ui">{actionError}</p>{/if}
	<section class="flex flex-col gap-sm">
		<div>
			<h3 class="tr-title-section">Pi preferences</h3>
			<p class="text-text-muted tr-text-metadata">
				These are the only Pi preferences available here. Pi persists them.
			</p>
		</div>
		<label class="flex flex-col gap-xs">
			Compaction reserve tokens
			<span class="text-text-muted tr-text-metadata">
				Tokens Pi reserves for a response before compacting. Clearing the field keeps the saved value; reset restores Pi’s default.
			</span>
			<input
				data-testid="auto-compact-threshold"
				type="number"
				min="1024"
				max="1000000"
				step="1"
				bind:value={reserveTokens}
				disabled={busy}
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			/>
		</label>
		<label class="flex flex-col gap-xs">
			Thinking effort
			<select
				data-testid="pi-thinking-effort"
				value={preferences.piThinkingEffort ?? ""}
				disabled={busy}
				onchange={(event) => {
					const effort = event.currentTarget.value as
						| PiPreferences["piThinkingEffort"]
						| "";
					if (effort) preferences = { ...preferences, piThinkingEffort: effort };
					else {
						const { piThinkingEffort: _unset, ...unset } = preferences;
						preferences = unset;
					}
				}}
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			>
				<option value="">Pi default</option>
				{#each THINKING_EFFORTS as effort (effort)}
					<option value={effort}>{effort}</option>
				{/each}
			</select>
		</label>
		<div class="flex flex-wrap gap-xs">
			<Button size="sm" disabled={busy || loading || !preferencesReady} onclick={() => void savePreferences()}>
				<Icon name="save" size={14} />
				Save preferences
			</Button>
			<Button
				size="sm"
				variant="outline"
				disabled={busy}
				onclick={() => void resetPreference("compactionReserveTokens")}
			>
				<Icon name="rotate-ccw" size={14} />
				Reset reserve
			</Button>
			<Button
				size="sm"
				variant="outline"
				disabled={busy}
				onclick={() => void resetPreference("piThinkingEffort")}
			>
				Reset thinking
			</Button>
		</div>
	</section>

	<section class="flex flex-col gap-sm border-border-default border-t pt-lg">
		<div>
			<h3 class="tr-title-section">New session defaults</h3>
			<p class="text-text-muted tr-text-metadata">
				Pi persists this provider and model default. New sessions inherit Pi’s saved
				default.
			</p>
		</div>
		<label class="flex flex-col gap-xs">
			Provider
			<select
				data-testid="default-provider"
				value={defaults.providerId ?? ""}
				disabled={busy}
				onchange={(event) => {
					defaults = { providerId: event.currentTarget.value || null, modelId: null };
				}}
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			>
				<option value="">Choose provider</option>
				{#if currentUnavailableProvider}
					<option value={currentUnavailableProvider.id} disabled>
						{currentUnavailableProvider.name} (current, unavailable)
					</option>
				{/if}
				{#each selectableProviders as provider (provider.id)}
					<option value={provider.id}>{provider.name}</option>
				{/each}
			</select>
		</label>
		<label class="flex flex-col gap-xs">
			Model
			<input
				data-testid="default-model"
				value={defaults.modelId ?? ""}
				disabled={!selectedDefaultProviderAvailable || busy}
				list="default-model-suggestions"
				oninput={(event) => {
					defaults = { ...defaults, modelId: event.currentTarget.value || null };
				}}
				placeholder="Provider default"
				class="rounded border border-border-default bg-control-bg px-sm py-xs"
			/>
			<datalist id="default-model-suggestions">
				{#each defaultSuggestions as model (`${model.provider}\0${model.id}`)}
					<option value={model.id}>{model.name} ({model.id})</option>
				{/each}
			</datalist>
		</label>
		<div class="flex gap-xs">
			<Button
				size="sm"
				disabled={busy || loading || !defaultsReady || !selectedDefaultProviderAvailable}
				onclick={() => void saveDefaults()}
			>
				Save defaults
			</Button>
			<Button size="sm" variant="outline" disabled={busy} onclick={() => void clearDefaults()}>
				Clear defaults
			</Button>
		</div>
	</section>

	{#if agentsAvailable}
	<section class="flex flex-col gap-sm border-border-default border-t pt-lg">
		<div class="flex flex-wrap items-end justify-between gap-sm">
			<div>
				<h3 class="tr-title-section">Agent catalog</h3>
				<p class="text-text-muted tr-text-metadata">
					Definitions live in Pi’s agent folders. Use a model ID or provider/model.
				</p>
			</div>
			<label class="flex flex-col gap-xs text-text-muted tr-text-metadata">
				Project scope
				<select
					data-testid="agent-catalog-project"
					value={catalogProjectId}
					disabled={busy}
					onchange={(event) => changeProjectScope(event.currentTarget.value)}
					class="rounded border border-border-default bg-control-bg px-sm py-xs text-text-default"
				>
					<option value="">Global agents</option>
					{#each projects as project (project.id)}
						<option value={project.id}>{project.name}</option>
					{/each}
				</select>
			</label>
		</div>
		{#if loading}
			<p class="text-text-muted">Loading Pi settings…</p>
		{:else}
			<div class="flex flex-col gap-xs">
				{#if agents.length === 0}
					<p class="text-text-muted tr-text-ui">No agents in this scope.</p>
				{:else}
					{#each agents as agent (agent.id)}
						<div
							data-testid="agent-row"
							class="flex items-center gap-sm rounded border border-border-default bg-control-bg px-sm py-xs"
						>
							<Icon name="bot" size={16} class="text-text-muted" />
							<div class="min-w-0 flex-1">
								<div>
									{agent.name}
									<span class="text-text-muted tr-text-metadata">
										{agent.scope} · {agent.writable ? "Writable" : "Read-only"}
									</span>
								</div>
								<div class="truncate text-text-muted tr-text-metadata">{agent.description}</div>
								{#if agent.modelId}
									<div class="text-text-muted tr-text-metadata">Model ID: {agent.modelId}</div>
								{/if}
							</div>
							{#if agent.writable}
								<div class="flex gap-xs">
									<Button
										size="sm"
										variant="outline"
										disabled={busy}
										onclick={() => editAgent(agent)}
									>
										Edit
									</Button>
									<Button
										size="icon"
										variant="ghost"
										aria-label={`Remove ${agent.name}`}
										disabled={busy}
										onclick={() => (deleteTarget = agent)}
									>
										<Icon name="trash-2" size={14} />
									</Button>
								</div>
							{/if}
						</div>
					{/each}
				{/if}
			</div>
		{/if}

		<form
			class="flex flex-col gap-sm rounded border border-border-default p-md"
			onsubmit={(event) => {
				event.preventDefault();
				void saveAgent();
			}}
		>
			<h4 class="tr-text-ui">{editing ? `Edit ${editing.name}` : "Add agent"}</h4>
			<label class="flex flex-col gap-xs">
				Name
				<span class="text-text-muted tr-text-metadata">
					Up to 80 UTF-8 bytes. Slashes are not allowed.
				</span>
				<input
					bind:value={draft.name}
					maxlength="80"
					disabled={busy}
					class="rounded border border-border-default bg-control-bg px-sm py-xs"
				/>
			</label>
			<label class="flex flex-col gap-xs">
				Description
				<input
					bind:value={draft.description}
					maxlength="1000"
					disabled={busy}
					class="rounded border border-border-default bg-control-bg px-sm py-xs"
				/>
			</label>
			<label class="flex flex-col gap-xs">
				Instructions (Markdown as plain text)
				<textarea
					data-testid="agent-instructions"
					bind:value={draft.instructions}
					maxlength={64 * 1024}
					rows="7"
					disabled={busy}
					class="rounded border border-border-default bg-control-bg px-sm py-xs"
				></textarea>
			</label>
			{#if !editing}
				<label class="flex flex-col gap-xs">
					Scope
					<select
						value={draft.scope}
						disabled={busy}
						onchange={(event) => {
							draft = { ...draft, scope: event.currentTarget.value as AgentDraft["scope"] };
						}}
						class="rounded border border-border-default bg-control-bg px-sm py-xs"
					>
						<option value="global">Global</option>
						<option value="project">Project</option>
					</select>
				</label>
				{#if draft.scope === "project"}
					<label class="flex flex-col gap-xs">
						Admitted project
						<select
							value={draft.projectId}
							disabled={busy}
							onchange={(event) => changeDraftProject(event.currentTarget.value)}
							class="rounded border border-border-default bg-control-bg px-sm py-xs"
						>
							<option value="">Choose project</option>
							{#each projects as project (project.id)}
								<option value={project.id}>{project.name}</option>
							{/each}
						</select>
					</label>
				{/if}
			{/if}
			<label class="flex flex-col gap-xs">
				Preferred model ID
				<input
					data-testid="agent-model"
					bind:value={draft.modelId}
					list="agent-model-suggestions"
					disabled={busy}
					placeholder="Inherit provider model"
					class="rounded border border-border-default bg-control-bg px-sm py-xs"
				/>
				<datalist id="agent-model-suggestions">
					{#each availableModels as model (`${model.provider}\0${model.id}`)}
						<option value={model.id}>{model.provider}: {model.id}</option>
					{/each}
				</datalist>
			</label>
			<div class="flex gap-xs">
				<Button size="sm" type="submit" disabled={busy}>
					{editing ? "Save agent" : "Add agent"}
				</Button>
				{#if editing}
					<Button
						size="sm"
						type="button"
						variant="outline"
						disabled={busy}
						onclick={() => {
							editing = null;
							draft = emptyAgent();
						}}
					>
						Cancel
					</Button>
				{/if}
			</div>
		</form>

		<ConfirmDialog
			open={deleteTarget !== null}
			title="Remove Pi agent?"
			description={deleteTarget ? `Remove ${deleteTarget.name} from Pi.` : ""}
			confirmLabel="Remove agent"
			confirmTestId="confirm-remove-agent"
			destructive
			onOpenChange={(open) => {
				if (!open) deleteTarget = null;
			}}
			onConfirm={async () => {
				if (deleteTarget) await removeAgent(deleteTarget);
			}}
		/>
	</section>
	{/if}
</div>
