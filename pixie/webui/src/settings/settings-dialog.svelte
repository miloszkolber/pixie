<script lang="ts">
import type { Component } from "svelte";
import Button from "@/components/button.svelte";
import Dialog from "@/components/dialog.svelte";
import { appStore, appStoreApi, selectActiveProjectArea, selectActiveContentTab } from "@/store";
import { restoreSettingsFocus } from "./open-settings";
import AgentSettings from "./sections/agent-settings.svelte";
import { resolveSettingsSection, settingsTabs } from "./settings-dialog";
import { SettingsSection } from "./state";

const loaders: Partial<Record<SettingsSection, () => Promise<{ default: Component }>>> = {
	pi: () => import("./sections/pi-settings.svelte"),
	tools: () => import("./sections/pi-tools-settings.svelte"),
	models: () => import("./sections/models-settings.svelte"),
	providers: () => import("./sections/providers-settings.svelte"),
	signet: () => import("./sections/signet-settings.svelte"),
	system: () => import("./sections/system-settings.svelte"),
};
let visited = $state<SettingsSection[]>([]);
let modules = $state.raw<Partial<Record<SettingsSection, Component>>>({});
let pending = $state<Partial<Record<SettingsSection, boolean>>>({});
let loadErrors = $state<Partial<Record<SettingsSection, boolean>>>({});
let generation = 0;
async function loadSection(section: SettingsSection): Promise<void> {
	const loader = loaders[section];
	if (!loader || modules[section] || pending[section]) return;
	const current = generation;
	pending = { ...pending, [section]: true };
	loadErrors = { ...loadErrors, [section]: false };
	try {
		const module = await loader();
		if (current === generation) modules = { ...modules, [section]: module.default };
	} catch {
		if (current === generation) loadErrors = { ...loadErrors, [section]: true };
	} finally {
		if (current === generation) pending = { ...pending, [section]: false };
	}
}
$effect(() => {
	if (!$appStore.settingsOpen) {
		if (visited.length) {
			generation++;
			visited = [];
			modules = {};
			pending = {};
			loadErrors = {};
		}
		return;
	}
	if (!visited.includes(activeSection)) visited = [...visited, activeSection];
	if (!loadErrors[activeSection]) void loadSection(activeSection);
});

let activeArea = $derived(selectActiveProjectArea($appStore));
let activeTab = $derived(activeArea ? selectActiveContentTab($appStore, activeArea.id) : null);
let sessionCapabilities = $derived(
	activeTab?.kind === "chat" ? $appStore.sessions[activeTab.sessionId]?.capabilities : undefined,
);
let settingsProfile = $derived(
	$appStore.agentProfile && sessionCapabilities
		? { ...$appStore.agentProfile, capabilities: sessionCapabilities }
		: $appStore.agentProfile,
);
let profilePending = $derived($appStore.agentProfile === null);
let genericAgent = $derived(
	!profilePending &&
		(!$appStore.agentProfile?.pi || $appStore.agentProfile.operations.administration === false),
);
let activeSection = $derived(resolveSettingsSection($appStore.settingsSection, settingsProfile));
let tabs = $derived(settingsTabs(genericAgent, profilePending, settingsProfile));

function selectSection(section: SettingsSection): void {
	appStoreApi.getState().setSettingsSection(section);
}

function handleTabKeydown(event: KeyboardEvent): void {
	if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
	const currentTarget = event.currentTarget as HTMLButtonElement;
	const allTabs = Array.from(
		currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [],
	);
	const current = allTabs.indexOf(currentTarget);
	if (current < 0 || allTabs.length === 0) return;
	event.preventDefault();
	const index =
		event.key === "Home"
			? 0
			: event.key === "End"
				? allTabs.length - 1
				: (current + (event.key === "ArrowRight" ? 1 : -1) + allTabs.length) % allTabs.length;
	allTabs[index]?.focus();
	allTabs[index]?.click();
}
</script>

<Dialog
	open={$appStore.settingsOpen}
	title="Settings"
	testid="settings-dialog"
	class="settings-dialog"
	onOpenChange={(open) => {
		if (!open) appStoreApi.getState().closeSettings();
	}}
	onClosedAutoFocus={() => {
		restoreSettingsFocus();
	}}
>
	{#if $appStore.settingsOpen}
		<div
			role="tablist"
			class="flex flex-wrap gap-xs border-border-default border-b px-md py-sm sm:px-lg"
			aria-label="Settings"
		>
			{#each tabs as tab (tab.section)}
				<button
					type="button"
					role="tab"
					aria-selected={activeSection === tab.section}
					aria-controls={`settings-panel-${tab.section}`}
					tabindex={activeSection === tab.section ? 0 : -1}
					onkeydown={handleTabKeydown}
					onclick={() => selectSection(tab.section)}
					class={`shrink-0 rounded-[var(--radius-sm)] px-md py-xs tr-text-ui ${
						activeSection === tab.section
							? "bg-control-bg-selected text-text-default"
							: "text-text-muted hover:bg-control-bg-hovered hover:text-text-default"
					}`}
				>
					{tab.label}
				</button>
			{/each}
		</div>
  {#each visited as section (section)}
   {#if section === activeSection || section === SettingsSection.Pi || section === SettingsSection.Signet}
    <div id={`settings-panel-${section}`} role="tabpanel" hidden={section !== activeSection} class="min-h-0 min-w-0 flex-1 overflow-y-auto p-md sm:p-lg">
     {#if section === SettingsSection.Agent && $appStore.agentProfile}
      <AgentSettings profile={$appStore.agentProfile} />
     {:else}
      {#if modules[section]}
       {@const Section = modules[section]!}<Section />
      {:else if loadErrors[section]}
       <p role="alert" class="tr-text-ui text-feedback-error">Couldn't load this settings section. Your open form drafts are retained. If retry still fails after a deployment, copy unsaved work before reloading.</p>
       <Button variant="outline" onclick={() => void loadSection(section)}>Retry loading</Button>
       <Button variant="ghost" onclick={() => { if (window.confirm("Reload the application? Unsaved settings, drafts and retained local messages will be lost. Copy anything you need first.")) window.location.reload(); }}>Reload application</Button>
      {:else}<p class="tr-text-ui text-text-muted">Loading settings…</p>{/if}
     {/if}
    </div>
   {/if}
  {/each}
	{/if}
</Dialog>

<style>
	:global(dialog.settings-dialog) {
		width: calc(100vw - 1rem);
		max-width: 64rem;
		height: calc(100dvh - 1rem);
 max-height: calc(100dvh - 1rem);
		overflow: hidden;
	}

	:global(dialog.settings-dialog > .dialog-content) {
		display: flex;
		min-width: 0;
		height: 100%;
 max-height: 100%;
		flex-direction: column;
		padding: 0;
	}

	:global(dialog.settings-dialog > .dialog-content > .dialog-header) {
		margin: 0;
		border-bottom: var(--border-width-025) solid var(--border-default);
		padding: var(--space-400) var(--space-600);
	}

	:global(dialog.settings-dialog > .dialog-content > .dialog-body) {
		display: flex;
		min-height: 0;
		min-width: 0;
		flex: 1;
		flex-direction: column;
		margin: 0;
	}

	@media (min-width: 640px) {
		:global(dialog.settings-dialog),
		:global(dialog.settings-dialog > .dialog-content) {
			height: 88dvh;
 max-height: 88dvh;
		}
	}
</style>
