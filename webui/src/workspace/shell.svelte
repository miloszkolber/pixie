<script lang="ts">
import { onMount } from "svelte";
import BrandLogo from "../components/brand-logo.svelte";
import Button from "../components/button.svelte";
import Icon from "../components/icon.svelte";
import Toaster from "../components/toaster.svelte";
import { getTransport, logoutController } from "../connection";
import SettingsArea from "../settings/settings-area.svelte";
import {
	appStore,
	appStoreApi,
	selectActiveProjectArea,
	selectContextProject,
	selectPrimaryArea,
} from "../store";
import { initGlobalHotkeys } from "./navigation/global-hotkeys";
import { openSettingsArea } from "./navigation/open-settings-area";
import { focusFirstVisible, panelHasFocusableContent } from "./focus-control";
import ProjectTree from "./projects/project-tree.svelte";
import {
	hasConfiguredProvider,
	resolveShellAvailability,
	resolveShellPrimarySurface,
} from "./shell-state";
import NoProviderWelcome from "./views/no-provider-welcome.svelte";
import { UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS } from "./views/upgrade-recovery";
import WelcomePanel from "./views/welcome-panel.svelte";

const STATUS_LABEL = {
	connected: "Connected",
	connecting: "Connecting…",
	disconnected: "Disconnected",
} as const;
const STATUS_DOT = {
	connected: "positive",
	connecting: "caution",
	disconnected: "negative",
} as const;

let providerError = $state(false);
let providerRefreshTick = $state(0);
type ProjectWorkAreaComponent = typeof import("./views/project-work-area.svelte").default;
let ProjectWorkArea = $state<ProjectWorkAreaComponent | null>(null);
let projectWorkAreaLoadPending = $state(false);
let projectWorkAreaLoadError = $state(false);
let projectWorkAreaReloadAttempts = $state(0);
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
let settingsRequested = $derived($appStore.workspaceSelection.primaryArea === "settings");
let primarySurface = $derived(resolveShellPrimarySurface(hasActiveProjectArea, settingsRequested));

function loadProjectWorkArea(): void {
	if (ProjectWorkArea || projectWorkAreaLoadPending) return;
	projectWorkAreaLoadPending = true;
	projectWorkAreaLoadError = false;
	void import("./views/project-work-area.svelte")
		.then(({ default: component }) => {
			ProjectWorkArea = component;
		})
		.catch(() => {
			projectWorkAreaLoadError = true;
		})
		.finally(() => {
			projectWorkAreaLoadPending = false;
		});
}

function retryProjectWorkArea(): void {
	// A stale deployment chunk must not trigger an unbounded reload loop or
	// re-dispatch any work. Drafts and accepted work remain in the app store.
	if (projectWorkAreaReloadAttempts >= UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS) return;
	projectWorkAreaReloadAttempts = Math.min(
		projectWorkAreaReloadAttempts + 1,
		UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS,
	);
	loadProjectWorkArea();
}

$effect(() => {
	if (hasActiveProjectArea) loadProjectWorkArea();
});

onMount(() =>
	initGlobalHotkeys({
		onProjects: () => {
			appStoreApi.getState().setShellLeftOpen(true);
			queueMicrotask(() => {
				const nav = document.querySelector<HTMLElement>(
					'[data-testid="primary-sidebar"], [data-testid="left-nav"]',
				);
				if (panelHasFocusableContent(nav)) nav?.focus();
				else
					focusFirstVisible(
						'[data-testid="toggle-left-panel"]',
						'[data-testid="expand-left-panel"]',
					);
			});
		},
		onProjectArea: () => {
			appStoreApi.getState().setShellRightOpen(true);
			queueMicrotask(() => {
				const panel = document.querySelector<HTMLElement>('[data-testid="secondary-sidebar"]');
				if (panelHasFocusableContent(panel)) panel?.focus();
				else
					focusFirstVisible(
						'[data-testid="toggle-right-panel"]',
						'[data-testid="expand-right-panel"]',
					);
			});
		},
	}),
);

function setProviderConfigured(configured: boolean | null): void {
	const state = appStoreApi.getState();
	if (state.providerConfigured !== configured) state.setProviderConfigured(configured);
}

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

function closeSettingsArea(): void {
	// Leaving the standalone surface returns to the explanatory panel. The
	// requested section stays in the store, so reopening resumes it.
	appStoreApi.getState().dispatchWorkspaceSelection(selectPrimaryArea("chats"));
}

function signOut(): void {
	void logoutController().finally(() => window.dispatchEvent(new Event("pixie-auth-lost")));
}
</script>

<div data-testid="shell" class="app-shell app-shell-edge pixie-shell">
	<a class="skip-link" href="#main-content">Skip to content</a>
	{#if primarySurface === "project-work-area" && $appStore.activeProjectAreaId}
		<div data-testid="project-shell" class="u-flex u-min-h-0 u-min-w-0 u-flex-1 u-flex-col">
			{#if ProjectWorkArea}
				{#key $appStore.activeProjectAreaId}<ProjectWorkArea projectAreaId={$appStore.activeProjectAreaId} />{/key}
			{:else if projectWorkAreaLoadError}
				<main id="main-content" data-testid="project-work-area-load-error" class="app-empty u-flex-1" role="alert">
					<p>The workspace view needs the current bundle. Open work remains intact.</p>
					<Button
						variant="outline"
						disabled={projectWorkAreaReloadAttempts >= UPGRADE_RECOVERY_MAX_RELOAD_ATTEMPTS}
						onclick={retryProjectWorkArea}
					>
						{projectWorkAreaReloadAttempts === 0 ? "Try loading once" : "Retry loading"}
					</Button>
				</main>
			{:else}
				<main id="main-content" data-testid="project-work-area-loading" class="app-empty u-flex-1" role="status">Loading workspace…</main>
			{/if}
		</div>
	{:else}
		<header class="app-header u-flex u-min-w-0 u-items-center u-justify-between u-gap-sm u-border-b u-px-sm u-py-sm u-sm-px-lg">
			<div class="u-flex u-min-w-0 u-items-center u-gap-md">
				<BrandLogo />
				{#if availability === "ready" && contextProject}
					<div
						data-testid="scope-context"
						data-context={activeProjectArea ? "project" : "project-home"}
						class="u-flex u-min-w-0 u-items-center u-gap-xs u-leading-tight tr-text-ui"
					>
						<span class="u-hidden u-min-w-0 u-items-center u-gap-xs u-sm-flex">
							<span data-testid="scope-project" class="u-max-w-160px u-truncate">{contextProject.name}</span>
							<Icon name="chevron-right" size={12} class="u-text-text-muted" />
						</span>
						<span data-testid="scope-name" class="u-max-w-220px u-truncate">{activeProjectArea?.name ?? "Project home"}</span>
						{#if activeProjectArea}<span class="u-max-w-260px u-truncate u-text-text-muted">{activeProjectArea.root}</span>{/if}
					</div>
				{/if}
			</div>
			<div class="app-header-actions u-flex u-shrink-0 u-items-center u-gap-sm u-sm-gap-md">
				<span data-testid="connection-status" data-status={$appStore.status} role="status" aria-label={STATUS_LABEL[$appStore.status]} class="stat-status u-inline-flex u-items-center u-gap-sm">
					<span aria-hidden="true" class="status-dot" data-state={STATUS_DOT[$appStore.status]}></span>
					<span aria-hidden="true" class="u-hidden u-sm-inline">{STATUS_LABEL[$appStore.status]}</span>
				</span>
				<Button
					variant="ghost"
					size="icon-sm"
					data-testid="open-settings"
					aria-label="Settings"
					title="Settings"
					onclick={() => void openSettingsArea()}
				>
					<Icon name="settings" size={16} />
				</Button>
				{#if $appStore.authenticationEnabled}
					<Button variant="ghost" size="icon-sm" aria-label="Sign out" title="Sign out" onclick={signOut}>
						<Icon name="log-out" size={16} />
					</Button>
				{/if}
			</div>
		</header>
	{/if}
	{#if primarySurface === "project-work-area" && $appStore.activeProjectAreaId}
		<!-- Project chrome renders inside ProjectWorkArea. -->
	{:else if primarySurface === "standalone-settings"}
		<SettingsArea onClose={closeSettingsArea} />
	{:else if availability === "unconfigured"}
		<NoProviderWelcome />
	{:else if availability === "incompatible"}
		<main id="main-content" class="app-content u-flex u-h-full u-min-h-0 u-min-w-0 u-items-center u-justify-center u-px-xl u-py-xl u-text-center">
			<div class="app-status-copy u-max-w-34rem">
				<h1 class="app-status-title">{$appStore.agentProfile?.name || "Connected agent"} is not compatible with Pixie</h1>
				<p role="alert" class="app-status-description">The agent must support the Pi session operations Pixie uses to list and reopen chats.</p>
				{#if $appStore.agentProfile?.missingRequired.length}
					<div class="u-text-left tr-text-ui u-text-text-muted"><p>Missing capabilities:</p><ul class="u-mt-xs u-list-disc u-pl-lg">{#each $appStore.agentProfile.missingRequired as capability}<li><code>{capability}</code></li>{/each}</ul></div>
				{/if}
			</div>
		</main>
	{:else if availability === "disconnected" || availability === "error"}
		<main id="main-content" class="app-content u-flex u-h-full u-min-h-0 u-min-w-0 u-items-center u-justify-center u-px-xl u-py-xl u-text-center">
			<div class="app-status-copy u-max-w-30rem">
				<h1 class="app-status-title">{availability === "disconnected" ? "Controller disconnected" : "Agent status unavailable"}</h1>
				<p role="alert" class="app-status-description">{availability === "disconnected" ? "Pixie will reconnect automatically. Your open work remains in this browser." : "The controller is connected, but the agent status could not be read."}</p>
				{#if availability === "error"}
					<div class="app-status-actions">
						<Button variant="outline" onclick={() => (providerRefreshTick += 1)}>Retry</Button>
						<Button variant="ghost" onclick={() => void openSettingsArea()}>Open settings</Button>
					</div>
				{/if}
			</div>
		</main>
	{:else if availability === "loading"}
		<main id="main-content" data-testid="provider-status-loading" class="app-empty u-h-full" role="status">Checking agent status…</main>
	{:else}
		<div data-testid="welcome-shell" class="u-flex u-min-h-0 u-min-w-0 u-flex-1 u-flex-col u-lg-flex-row">
			<aside aria-label="Projects" data-testid="left-nav" tabindex="-1" class="app-sidebar u-max-h-45pct u-w-full u-shrink-0 u-overflow-auto u-border-b u-p-md u-outline-none u-lg-max-h-none u-lg-w-sidebar u-lg-border-r u-lg-border-b-0"><ProjectTree /></aside>
			<main id="main-content" class="app-content u-min-h-0 u-min-w-0 u-flex-1"><WelcomePanel /></main>
		</div>
	{/if}
	<Toaster />
</div>
