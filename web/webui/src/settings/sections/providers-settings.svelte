<script lang="ts">
import type { ProviderStatusReport } from "@pixie/shared";
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

<div data-testid="settings-providers" class="u-flex u-flex-col u-gap-lg">
{#if actionError}<p role="alert" class="u-text-feedback-error tr-text-ui">{actionError}</p>{/if}
	<div class="u-flex u-items-start u-justify-between u-gap-sm">
		<div class="u-flex u-flex-col u-gap-xs">
			<h3 class="u-text-text-default tr-title-section">Providers</h3>
			<p class="u-text-text-muted tr-text-metadata">
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
			<Icon name="refresh-cw" size={14} class={refreshing ? "provider-refresh-icon" : ""} />
			Refresh
		</Button>
	</div>

	<label class="provider-filter u-flex u-items-center u-gap-sm u-rounded u-border u-border-border-default u-bg-control-bg u-px-md u-py-sm">
		<Icon name="search" size={16} class="u-shrink-0 u-text-text-muted" />
		<input
			data-testid="providers-filter"
            aria-label="Filter providers"
			bind:value={query}
			placeholder="Filter providers…"
			class="provider-filter__input u-min-w-0 u-flex-1 tr-text-ui"
		/>
	</label>

	<label class="u-flex u-items-center u-gap-xs tr-text-ui"><input type="checkbox" bind:checked={showLegacy} />Show legacy providers</label>
	{#if report === null && !failed}
		<p class="u-text-text-muted tr-text-ui">Loading providers…</p>
	{:else if failed}
		<p data-testid="providers-error" class="u-text-text-muted tr-text-ui">
			Couldn't read provider status from the controller.
		</p>
	{:else if filtered.length === 0}
		<p class="u-text-text-muted tr-text-ui">No providers match this filter.</p>
	{:else}
		{#if configured.length > 0}
			<section class="u-flex u-flex-col u-gap-sm">
				<h4 class="u-text-text-muted tr-text-eyebrow">Configured in Pi ({configured.length})</h4>
				<div class="u-flex u-flex-col u-gap-xs">
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
			<section class="u-flex u-flex-col u-gap-sm">
				<h4 class="u-text-text-muted tr-text-eyebrow">Not configured ({unconfigured.length})</h4>
				<div class="u-flex u-flex-col u-gap-xs">
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

<style>
	.provider-filter__input {
		background: transparent;
		color: var(--text-default);
		outline: none;
	}

	.provider-filter__input::placeholder {
		color: var(--text-muted);
	}

	.provider-filter:focus-within {
		outline: var(--focus-ring-width, 2px) solid var(--border-focus);
		outline-offset: -1px;
	}

	:global(.provider-refresh-icon) {
		animation: provider-refresh-rotate 1s linear infinite;
	}

	@keyframes provider-refresh-rotate {
		to { transform: rotate(1turn); }
	}

	@media (prefers-reduced-motion: reduce) {
		:global(.provider-refresh-icon) { animation: none; }
	}
</style>
