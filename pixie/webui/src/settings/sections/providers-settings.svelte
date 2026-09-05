<script lang="ts">
import type { ProviderStatusReport } from "@pixie/contracts";
import { onMount } from "svelte";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import { errorText, getTransport } from "@/connection";
import { appStore, appStoreApi } from "@/store";
import LoginDialog from "../login/login-dialog.svelte";
import ProviderCard from "./provider-card.svelte";

let report = $state<ProviderStatusReport | null>(null);
let failed = $state(false);
let actionError = $state<string | null>(null);
let refreshing = $state(false);
let busyProvider = $state<string | null>(null);
let query = $state("");
let showLegacy = $state(false);
let readinessRevision = $state(0);
let loadSequence = 0;
let loginStartSequence = 0;
let mounted = false;
let observedProviderVersion = $state<number | null>(null);
let observedLoginStatus = $state<string | null>(null);
let activeLogin = $derived($appStore.activeLogin);
let providerVersion = $derived($appStore.providerVersion);
let providers = $derived(report?.providers ?? []);
let filtered = $derived.by(() => {
	const normalized = query.trim().toLocaleLowerCase();
	if (!normalized)
		return providers.filter(
			(provider) => showLegacy || provider.configured || !provider.deprecated,
		);
	return providers.filter(
		(provider) =>
			provider.name.toLocaleLowerCase().includes(normalized) ||
			provider.id.toLocaleLowerCase().includes(normalized),
	);
});
let configured = $derived(filtered.filter((provider) => provider.configured));
let unconfigured = $derived(filtered.filter((provider) => !provider.configured));
let loginProviderName = $derived(
	providers.find((provider) => provider.id === activeLogin?.providerId)?.name ??
		activeLogin?.providerId ??
		"",
);

function notifyError(error: unknown, title: string): void {
	actionError = `${title}: ${errorText(error)}`;
}

function invalidateReadiness(): void {
	readinessRevision += 1;
}

async function load(): Promise<void> {
	invalidateReadiness();
	const sequence = ++loadSequence;
	const version = appStoreApi.getState().providerVersion;
	const isCurrent = () =>
		mounted && sequence === loadSequence && version === appStoreApi.getState().providerVersion;
	refreshing = true;
	try {
		const next = await getTransport().request("provider.status", {});
		if (!isCurrent()) return;
		report = next;
		failed = false;
	} catch {
		if (!isCurrent()) return;
		failed = true;
	} finally {
		if (mounted && sequence === loadSequence) refreshing = false;
	}
}

onMount(() => {
	mounted = true;
	observedProviderVersion = providerVersion;
	observedLoginStatus = activeLogin?.status ?? null;
	void load();
	return () => {
		mounted = false;
		loadSequence += 1;
		loginStartSequence += 1;
	};
});

$effect(() => {
	const nextVersion = providerVersion;
	if (!mounted || observedProviderVersion === null || nextVersion === observedProviderVersion)
		return;
	observedProviderVersion = nextVersion;
	void load();
});

$effect(() => {
	const nextStatus = activeLogin?.status ?? null;
	if (!mounted || nextStatus === observedLoginStatus) return;
	observedLoginStatus = nextStatus;
	if (nextStatus === "success") void load();
});

async function startLogin(providerId: string, type: "oauth" | "api_key"): Promise<void> {
	invalidateReadiness();
	const sequence = ++loginStartSequence;
	const isCurrent = () => mounted && sequence === loginStartSequence;
	busyProvider = providerId;
	try {
		const { loginId, frame } = await getTransport().request("provider.loginStart", {
			providerId,
			type,
		});
		if (!isCurrent()) {
			void getTransport()
				.request("provider.loginCancel", { loginId })
				.catch(() => {});
			return;
		}
		appStoreApi.getState().beginLogin(loginId, providerId);
		appStoreApi.getState().applyLoginFrame({ loginId, providerId, frame });
	} catch (error) {
		if (isCurrent()) notifyError(error, "Couldn't start the connection");
	} finally {
		if (isCurrent()) busyProvider = null;
	}
}

async function logout(providerId: string): Promise<void> {
	invalidateReadiness();
	busyProvider = providerId;
	try {
		await getTransport().request("provider.logout", { providerId });
		appStoreApi.getState().noteProviderChanged();
	} catch (error) {
		notifyError(error, "Couldn't sign out");
		return;
	} finally {
		busyProvider = null;
	}
	await load();
}

async function replyToLogin(value: string): Promise<void> {
	if (!activeLogin) return;
	const loginId = activeLogin.loginId;
	const input = activeLogin.input;
	await getTransport().request("provider.loginReply", { loginId, value });
	// A new challenge can arrive before the reply acknowledgement.
	const current = appStoreApi.getState().activeLogin;
	if (current?.loginId === loginId && current.input === input) {
		appStoreApi.getState().clearLoginInput();
	}
}

async function cancelLogin(): Promise<void> {
	const loginId = activeLogin?.loginId;
	if (!loginId) return;
	await getTransport().request("provider.loginCancel", { loginId });
	if (appStoreApi.getState().activeLogin?.loginId === loginId) appStoreApi.getState().clearLogin();
}

async function closeLogin(): Promise<void> {
	await cancelLogin();
	appStoreApi.getState().noteProviderChanged();
	await load();
}
</script>

<div data-testid="settings-providers" class="flex flex-col gap-lg">
{#if actionError}<p role="alert" class="text-feedback-error tr-text-ui">{actionError}</p>{/if}
	<div class="flex items-start justify-between gap-sm">
		<div class="flex flex-col gap-xs">
			<h3 class="text-text-default tr-title-section">Providers</h3>
			<p class="text-text-muted tr-text-metadata">
				Provider credentials are stored and managed by Pi.
			</p>
		</div>
		<Button
			variant="ghost"
			size="sm"
			data-testid="providers-refresh"
			aria-label="Refresh provider status"
			title="Refresh"
			disabled={refreshing}
			onclick={() => void load()}
		>
			<Icon name="refresh-cw" size={14} class={refreshing ? "animate-spin" : ""} />
			Refresh
		</Button>
	</div>

	<label class="flex items-center gap-sm rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-md py-sm">
		<Icon name="search" size={16} class="shrink-0 text-text-muted" />
		<input
			data-testid="providers-filter"
            aria-label="Filter providers"
			bind:value={query}
			placeholder="Filter providers…"
			class="min-w-0 flex-1 bg-transparent text-text-default outline-none tr-text-ui placeholder:text-text-muted"
		/>
	</label>

	<label class="flex items-center gap-xs tr-text-ui"><input type="checkbox" bind:checked={showLegacy} />Show legacy providers</label>
	{#if report === null && !failed}
		<p class="text-text-muted tr-text-ui">Loading providers…</p>
	{:else if failed}
		<p data-testid="providers-error" class="text-text-muted tr-text-ui">
			Couldn't read provider status from the controller.
		</p>
	{:else if filtered.length === 0}
		<p class="text-text-muted tr-text-ui">No providers match this filter.</p>
	{:else}
		{#if configured.length > 0}
			<section class="flex flex-col gap-sm">
				<h4 class="text-text-muted tr-text-eyebrow">Configured in Pi ({configured.length})</h4>
				<div class="flex flex-col gap-xs">
					{#each configured as provider (provider.id)}
						<ProviderCard
							{provider}
							busy={busyProvider === provider.id || activeLogin !== null}
							{readinessRevision}
							onSignIn={(type) => void startLogin(provider.id, type)}
							onSignOut={() => void logout(provider.id)}
						/>
					{/each}
				</div>
			</section>
		{/if}
		{#if unconfigured.length > 0}
			<section class="flex flex-col gap-sm">
				<h4 class="text-text-muted tr-text-eyebrow">Not configured ({unconfigured.length})</h4>
				<div class="flex flex-col gap-xs">
					{#each unconfigured as provider (provider.id)}
						<ProviderCard
							{provider}
							busy={busyProvider === provider.id || activeLogin !== null}
							{readinessRevision}
							onSignIn={(type) => void startLogin(provider.id, type)}
							onSignOut={() => void logout(provider.id)}
						/>
					{/each}
				</div>
			</section>
		{/if}
	{/if}

	{#if activeLogin}
		{#key activeLogin.loginId}
			<LoginDialog
				state={activeLogin}
				providerName={loginProviderName}
				onReply={replyToLogin}
				onCancel={cancelLogin}
				onClose={closeLogin}
			/>
		{/key}
	{/if}
</div>
