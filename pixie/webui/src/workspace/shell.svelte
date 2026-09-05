<script lang="ts">
import { onMount } from "svelte";
import BrandLogo from "../components/brand-logo.svelte";
import Button from "../components/button.svelte";
import Icon from "../components/icon.svelte";
import Toaster from "../components/toaster.svelte";
import { getTransport, logoutController } from "../connection";
import { openSettingsFrom } from "../settings/open-settings";
import {
	appStore,
	appStoreApi,
	selectActiveProjectArea,
	selectContextProject,
	toast,
} from "../store";
import { initGlobalHotkeys } from "./navigation/global-hotkeys";
import ProjectTree from "./projects/project-tree.svelte";
import { hasConfiguredProvider, resolveShellAvailability } from "./shell-state";
import NoProviderWelcome from "./views/no-provider-welcome.svelte";
import ProjectWorkArea from "./views/project-work-area.svelte";
import WelcomePanel from "./views/welcome-panel.svelte";

const STATUS_LABEL = {
	connected: "Connected",
	connecting: "Connecting…",
	disconnected: "Disconnected",
} as const;
const STATUS_DOT = {
	connected: "bg-feedback-success",
	connecting: "bg-feedback-warning",
	disconnected: "bg-feedback-error",
} as const;

let providerError = $state(false);
let providerRefreshTick = $state(0);
let SettingsDialog = $state<typeof import("../settings/settings-dialog.svelte").default | null>(
	null,
);
let settingsDialogLoad: Promise<void> | null = null;
let providerProbeStatus = $derived($appStore.status);
let providerProbeAgentProfile = $derived($appStore.agentProfile);
let providerProbeConnectionGeneration = $derived($appStore.connectionGeneration);
let providerProbeVersion = $derived($appStore.providerVersion);
let activeProjectArea = $derived(selectActiveProjectArea($appStore));
let contextProject = $derived(selectContextProject($appStore));
let availability = $derived(
	resolveShellAvailability(
		$appStore.status,
		$appStore.agentProfile,
		$appStore.providerConfigured,
		providerError,
	),
);
let hasActiveProjectArea = $derived(
	availability === "ready" && $appStore.activeProjectAreaId !== null,
);

onMount(() =>
	initGlobalHotkeys({
		onProjects: () => document.querySelector<HTMLElement>('[data-testid="left-nav"]')?.focus(),
		onProjectArea: () =>
			document.querySelector<HTMLElement>('[data-testid="activity-tabs"]')?.focus(),
	}),
);

function setProviderConfigured(configured: boolean | null): void {
	const state = appStoreApi.getState();
	if (state.providerConfigured !== configured) state.setProviderConfigured(configured);
}

$effect(() => {
	if (!$appStore.settingsOpen || SettingsDialog || settingsDialogLoad) return;
	settingsDialogLoad = import("../settings/settings-dialog.svelte")
		.then(({ default: component }) => {
			SettingsDialog = component;
		})
		.catch(() => {
			appStoreApi.getState().closeSettings();
			toast.error("Try opening settings again or reload the page.", "Couldn't open settings");
		})
		.finally(() => {
			settingsDialogLoad = null;
		});
});

$effect(() => {
	void providerRefreshTick;
	const status = providerProbeStatus;
	const agentProfile = providerProbeAgentProfile;
	const generation = `${providerProbeConnectionGeneration}:${providerProbeVersion}`;
	if (status !== "connected") {
		setProviderConfigured(null);
		providerError = false;
		return;
	}
	let current = true;
	setProviderConfigured(null);
	providerError = false;
	if (!agentProfile) {
		void getTransport()
			.request("pi.status", {})
			.then((report) => {
				const live = `${appStoreApi.getState().connectionGeneration}:${appStoreApi.getState().providerVersion}`;
				if (!current || generation !== live) return;
				if (report.agentProfile) appStoreApi.getState().replaceAgentProfile(report.agentProfile);
				else providerError = true;
			})
			.catch(() => {
				if (current) providerError = true;
			});
		return () => {
			current = false;
		};
	}
	if (!agentProfile.compatible || !agentProfile.operations.administration) return;
	void getTransport()
		.request("provider.status", {})
		.then((report) => {
			const live = `${appStoreApi.getState().connectionGeneration}:${appStoreApi.getState().providerVersion}`;
			if (current && generation === live) setProviderConfigured(hasConfiguredProvider(report));
		})
		.catch(() => {
			if (current) {
				setProviderConfigured(null);
				providerError = true;
			}
		});
	return () => {
		current = false;
	};
});

function signOut(): void {
	void logoutController().finally(() => window.dispatchEvent(new Event("pixie-auth-lost")));
}
</script>

<div data-testid="shell" class="app-shell grid h-full grid-rows-[auto_1fr]">
	<header class="app-header flex min-w-0 items-center justify-between gap-sm border-b px-sm py-sm sm:px-lg">
		<div class="flex min-w-0 items-center gap-md">
			<BrandLogo />
			{#if availability === "ready" && contextProject}
				<div
					data-testid="scope-context"
					data-context={activeProjectArea ? "project" : "project-home"}
					class="flex min-w-0 items-center gap-xs leading-tight tr-text-ui"
				>
					<span class="hidden min-w-0 items-center gap-xs sm:flex">
						<span data-testid="scope-project" class="max-w-[160px] truncate">{contextProject.name}</span>
						<Icon name="chevron-right" size={12} class="text-text-muted" />
					</span>
					<span data-testid="scope-name" class="max-w-[220px] truncate">{activeProjectArea?.name ?? "Project home"}</span>
					{#if activeProjectArea}<span class="max-w-[260px] truncate text-text-muted">{activeProjectArea.root}</span>{/if}
				</div>
			{/if}
		</div>
		<div class="app-header-actions flex shrink-0 items-center gap-sm sm:gap-md">
			<span data-testid="connection-status" data-status={$appStore.status} role="status" aria-label={STATUS_LABEL[$appStore.status]} class="stat-status inline-flex items-center gap-sm">
				<span aria-hidden="true" class={`status-dot ${STATUS_DOT[$appStore.status]}`}></span>
				<span aria-hidden="true" class="hidden sm:inline">{STATUS_LABEL[$appStore.status]}</span>
			</span>
			<Button
				variant="ghost"
				size="icon-sm"
				data-testid="open-settings"
				aria-label="Settings"
				title="Settings"
				onclick={(event) => openSettingsFrom(event.currentTarget)}
			>
				<Icon name="settings" size={16} />
			</Button>
			{#if $appStore.authenticationEnabled}
				<Button variant="ghost" size="icon-sm" aria-label="Sign out" title="Sign out" onclick={signOut}>
					<Icon name="log-out" size={16} />
				</Button>
			{/if}
		</div>
		{#if SettingsDialog}<SettingsDialog />{/if}
	</header>
	{#if hasActiveProjectArea && $appStore.activeProjectAreaId}
		<div data-testid="project-shell" class="h-full min-h-0 min-w-0">
			{#key $appStore.activeProjectAreaId}<ProjectWorkArea projectAreaId={$appStore.activeProjectAreaId} />{/key}
		</div>
	{:else if availability === "unconfigured"}
		<NoProviderWelcome />
	{:else if availability === "incompatible"}
		<main class="app-content flex h-full min-h-0 min-w-0 items-center justify-center px-xl py-xl text-center">
			<div class="app-status-copy max-w-[34rem]">
				<h1 class="app-status-title">{$appStore.agentProfile?.name || "Connected agent"} is not compatible with Pixie</h1>
				<p role="alert" class="app-status-description">The agent must support the Pi session operations Pixie uses to list and reopen chats.</p>
				{#if $appStore.agentProfile?.missingRequired.length}
					<div class="text-left tr-text-ui text-text-muted"><p>Missing capabilities:</p><ul class="mt-xs list-disc pl-lg">{#each $appStore.agentProfile.missingRequired as capability}<li><code>{capability}</code></li>{/each}</ul></div>
				{/if}
			</div>
		</main>
	{:else if availability === "disconnected" || availability === "error"}
		<main class="app-content flex h-full min-h-0 min-w-0 items-center justify-center px-xl py-xl text-center">
			<div class="app-status-copy max-w-[30rem]">
				<h1 class="app-status-title">{availability === "disconnected" ? "Controller disconnected" : "Agent status unavailable"}</h1>
				<p role="alert" class="app-status-description">{availability === "disconnected" ? "Pixie will reconnect automatically. Your open work remains in this browser." : "The controller is connected, but the agent status could not be read."}</p>
				{#if availability === "error"}
					<div class="app-status-actions">
						<Button variant="outline" onclick={() => (providerRefreshTick += 1)}>Retry</Button>
						<Button variant="ghost" onclick={(event) => openSettingsFrom(event.currentTarget)}>Open settings</Button>
					</div>
				{/if}
			</div>
		</main>
	{:else if availability === "loading"}
		<main data-testid="provider-status-loading" class="app-empty h-full" role="status">Checking agent status…</main>
	{:else}
		<div data-testid="welcome-shell" class="flex h-full min-h-0 min-w-0 flex-col lg:flex-row">
			<aside aria-label="Projects" data-testid="left-nav" tabindex="-1" class="app-sidebar max-h-[45%] w-full shrink-0 overflow-auto border-b p-md outline-none lg:max-h-none lg:w-[clamp(12rem,20vw,16rem)] lg:border-r lg:border-b-0"><ProjectTree /></aside>
			<main class="app-content min-h-0 min-w-0 flex-1"><WelcomePanel /></main>
		</div>
	{/if}
	<Toaster />
</div>
