<script lang="ts">
import type { Component } from "svelte";
import Button from "../components/button.svelte";
import Icon from "../components/icon.svelte";
import { resolveWorkspaceSettingsSection } from "../schedules/schedules-workspace";
import { appStore, appStoreApi, selectContextProject, selectPrimary, SettingsSection } from "../store";
import AgentSettings from "./sections/agent-settings.svelte";
import { resolveSettingsSection, settingsTabs } from "./settings-dialog";
import { SETTINGS_SECTION_LOADERS } from "./settings-sections";

interface Props {
	/** Return to the shell state that was visible before Settings was requested. */
	onClose?: () => void;
}

let { onClose }: Props = $props();

let settingsAgentProfile = $derived($appStore.agentProfile);
let settingsProfilePending = $derived(settingsAgentProfile === null);
let settingsGenericAgent = $derived(
	!settingsProfilePending &&
		(!settingsAgentProfile?.pi || settingsAgentProfile.operations.administration === false),
);
let settingsFallbackSection = $derived(
	resolveSettingsSection($appStore.settingsSection, settingsAgentProfile),
);
let settingsActiveSection = $derived(
	resolveWorkspaceSettingsSection(
		$appStore.workspaceSelection.primarySelection,
		settingsFallbackSection,
	),
);
let settingsTabList = $derived(
	settingsTabs(settingsGenericAgent, settingsProfilePending, settingsAgentProfile),
);
let schedulesProject = $derived(selectContextProject($appStore));

// One lifecycle owner: visited sections stay mounted (hidden) so local drafts
// survive tab switches, matching the ready-path ProjectWorkArea settings view.
let settingsVisited = $state<SettingsSection[]>([]);
let settingsModules = $state.raw<Partial<Record<SettingsSection, Component<any>>>>({});
let settingsSectionPending = $state<Partial<Record<SettingsSection, boolean>>>({});
let settingsLoadErrors = $state<Partial<Record<SettingsSection, boolean>>>({});
let settingsLoadGeneration = 0;

async function loadSettingsSection(section: SettingsSection): Promise<void> {
	const loader = SETTINGS_SECTION_LOADERS[section];
	if (!loader || settingsModules[section] || settingsSectionPending[section]) return;
	const current = settingsLoadGeneration;
	settingsSectionPending = { ...settingsSectionPending, [section]: true };
	settingsLoadErrors = { ...settingsLoadErrors, [section]: false };
	try {
		const module = await loader();
		if (current === settingsLoadGeneration)
			settingsModules = { ...settingsModules, [section]: module.default };
	} catch {
		if (current === settingsLoadGeneration)
			settingsLoadErrors = { ...settingsLoadErrors, [section]: true };
	} finally {
		if (current === settingsLoadGeneration)
			settingsSectionPending = { ...settingsSectionPending, [section]: false };
	}
}

function retrySettingsSection(section: SettingsSection): void {
	settingsLoadErrors = { ...settingsLoadErrors, [section]: false };
	void loadSettingsSection(section);
}

$effect(() => {
	const section = settingsActiveSection;
	if (!settingsVisited.includes(section)) settingsVisited = [...settingsVisited, section];
	if (!settingsLoadErrors[section]) void loadSettingsSection(section);
});

function selectSettingsSection(section: SettingsSection): void {
	appStoreApi
		.getState()
		.dispatchWorkspaceSelection(selectPrimary({ kind: "settings", sectionId: section }, "settings"));
	appStoreApi.getState().setSettingsSection(section);
}

function handleSettingsSectionKeydown(event: KeyboardEvent): void {
	if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key))
		return;
	const currentTarget = event.currentTarget as HTMLButtonElement;
	const allTabs = Array.from(
		currentTarget
			.closest('[role="tablist"]')
			?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [],
	);
	const current = allTabs.indexOf(currentTarget);
	if (current < 0 || allTabs.length === 0) return;
	event.preventDefault();
	const step =
		event.key === "ArrowRight" || event.key === "ArrowDown"
			? 1
			: event.key === "ArrowLeft" || event.key === "ArrowUp"
				? -1
				: 0;
	const index =
		event.key === "Home"
			? 0
			: event.key === "End"
				? allTabs.length - 1
				: (current + step + allTabs.length) % allTabs.length;
	const next = allTabs[index];
	next?.focus();
	next?.click();
}
</script>

<div
	data-testid="settings-area"
	data-settings-surface="standalone"
	class="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden lg:flex-row"
>
	<aside
		data-testid="settings-area-sidebar"
		aria-label="Settings sections"
		class="app-sidebar flex max-h-[45%] w-full shrink-0 flex-col overflow-auto border-b p-md lg:max-h-none lg:w-[clamp(12rem,20vw,16rem)] lg:border-r lg:border-b-0"
	>
		<div class="flex items-center justify-between gap-sm">
			<span class="eyebrow">SETTINGS</span>
			{#if onClose}
				<Button
					variant="ghost"
					size="sm"
					data-testid="settings-area-close"
					aria-label="Back from settings"
					onclick={onClose}
				>
					<Icon name="chevron-left" size={14} /> Back
				</Button>
			{/if}
		</div>
		<ul role="tablist" aria-label="Settings sections" class="mt-sm flex flex-col gap-2xs">
			{#each settingsTabList as tab (tab.section)}
				<li role="presentation">
					<button
						type="button"
						role="tab"
						id={`settings-area-tab-${tab.section}`}
						data-testid="settings-section-row"
						class={`tree-leaf w-full text-left tr-text-ui ${settingsActiveSection === tab.section ? "tree-leaf-active" : ""}`}
						aria-selected={settingsActiveSection === tab.section}
						aria-controls={`settings-area-panel-${tab.section}`}
						tabindex={settingsActiveSection === tab.section ? 0 : -1}
						onkeydown={handleSettingsSectionKeydown}
						onclick={() => selectSettingsSection(tab.section)}
					>
						{tab.label}
					</button>
				</li>
			{/each}
		</ul>
		<p class="mt-sm tr-text-metadata text-text-muted">
			Settings stays available while the agent is unavailable so provider and system
			configuration can be reached.
		</p>
	</aside>

	<main
		id="main-content"
		data-testid="settings-detail"
		class="flex min-h-0 min-w-0 flex-1 flex-col gap-md overflow-y-auto px-lg py-md"
	>
		{#each settingsVisited as section (section)}
			<div
				id={`settings-area-panel-${section}`}
				role="tabpanel"
				aria-labelledby={`settings-area-tab-${section}`}
				hidden={section !== settingsActiveSection}
				class="min-w-0 flex-1"
			>
				{#if section === SettingsSection.Agent && $appStore.agentProfile}
					<AgentSettings profile={$appStore.agentProfile} />
				{:else if section === SettingsSection.Schedules}
					{#if schedulesProject}
						{#key schedulesProject.id}
							{#if settingsModules[section]}
								{@const Section = settingsModules[section]!}<Section project={schedulesProject} />
							{:else if settingsLoadErrors[section]}
								<div role="alert" class="flex flex-col items-start gap-sm">
									<p class="tr-text-ui text-feedback-error">Couldn't load this settings section.</p>
									<Button variant="outline" onclick={() => retrySettingsSection(section)}>Retry loading</Button>
								</div>
							{:else}<p class="tr-text-ui text-text-muted">Loading settings…</p>{/if}
						{/key}
					{:else}
						<p class="tr-text-ui text-text-muted">Select a project to manage its schedules.</p>
					{/if}
				{:else if settingsModules[section]}
					{@const Section = settingsModules[section]!}<Section />
				{:else if settingsLoadErrors[section]}
					<div role="alert" class="flex flex-col items-start gap-sm">
						<p class="tr-text-ui text-feedback-error">Couldn't load this settings section.</p>
						<Button variant="outline" onclick={() => retrySettingsSection(section)}>Retry loading</Button>
					</div>
				{:else}<p class="tr-text-ui text-text-muted">Loading settings…</p>{/if}
			</div>
		{/each}
		{#if settingsVisited.length === 0}<p class="tr-text-ui text-text-muted">Loading settings…</p>{/if}
	</main>
</div>
