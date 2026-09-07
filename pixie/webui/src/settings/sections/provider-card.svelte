<script lang="ts">
import type { ProviderStatus } from "@pixie/contracts";
import { onDestroy } from "svelte";
import Button from "@/components/button.svelte";
import Icon from "@/components/icon.svelte";
import { getTransport } from "@/connection";
import {
	KIND_LABEL,
	modelSummary,
	type ProviderReadinessSnapshot,
	providerAvailability,
	readinessStatusText,
	settleProviderReadiness,
} from "./providers-settings";

interface Props {
	provider: ProviderStatus;
	busy: boolean;
	readinessRevision?: number;
	onSignIn: (type: "oauth" | "api_key") => void;
	onSignOut: () => void;
}

let { provider, busy, readinessRevision = 0, onSignIn, onSignOut }: Props = $props();
let readiness = $state<ProviderReadinessSnapshot>({
	revision: 0,
	status: null,
});
let readinessSequence = 0;
let visibleReadiness = $derived(readiness.revision === readinessRevision ? readiness.status : null);
let readinessText = $derived(readinessStatusText(visibleReadiness));
let availability = $derived(providerAvailability(provider, visibleReadiness));
let configuredLabel = $derived(provider.kind ? KIND_LABEL[provider.kind] : "configured");

$effect(() => {
	const revision = readinessRevision;
	if (readiness.revision !== revision) readiness = { revision, status: null };
});

onDestroy(() => {
	readinessSequence += 1;
});

async function checkReadiness(): Promise<void> {
	const sequence = ++readinessSequence;
	const revision = readinessRevision;
	readiness = { revision, status: "checking" };
	try {
		const result = await getTransport().request("provider.readiness", {
			providerId: provider.id,
		});
		if (sequence !== readinessSequence) return;
		readiness = settleProviderReadiness(
			readiness,
			revision,
			result.ready ? (result.hasIssue ? "issue" : "ready") : "not-ready",
		);
	} catch {
		if (sequence !== readinessSequence) return;
		readiness = settleProviderReadiness(readiness, revision, "failed");
	}
}
</script>

<div
	data-testid="provider-row"
	data-provider={provider.id}
	data-configured={String(provider.configured)}
	class="flex flex-wrap items-center gap-md rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-md py-sm sm:flex-nowrap"
>
	<span
		class={`flex size-8 shrink-0 items-center justify-center rounded-[var(--radius-sm)] ${
			availability.usable
				? "bg-feedback-success-subtle text-feedback-success"
				: "bg-control-bg-selected text-text-muted"
		}`}
	>
		<Icon name={availability.usable ? "check" : "boxes"} size={16} />
	</span>
	<div class="min-w-0 flex-1 basis-48">
		<div class="break-words text-text-default tr-text-ui">{provider.name}</div>
		<div class="break-words text-text-muted tr-text-metadata">
			{provider.id} · {modelSummary(provider)}
		</div>
		{#if provider.deprecated}<p class="text-text-muted tr-text-metadata">Legacy provider{provider.replacement ? ` · Replacement: ${provider.replacement}` : ""}</p>{/if}
		{#if provider.configuration === "defaults" || provider.configuration === "unknown"}<p class="text-text-muted tr-text-metadata">{provider.configuration === "defaults" ? "Default connection only · configure through Pi to use" : "Configuration could not be verified"}</p>{/if}
		{#if provider.configured}
			<div class="break-words text-text-muted tr-text-metadata">
				Pi reports {configuredLabel}{provider.available === false
					? " · runtime unavailable"
					: provider.readinessCheck
						? ` · ${availability.qualifier}`
						: " · readiness not checked"}{provider.detail ? ` · ${provider.detail}` : ""}
			</div>
		{/if}
	</div>
	<div class="flex shrink-0 flex-wrap items-center gap-xs">
		{#if provider.readinessCheck}
			<Button
				variant="outline"
				size="sm"
				data-testid="provider-readiness"
				data-provider={provider.id}
				aria-label={`Check configuration for ${provider.name}`}
				disabled={busy || visibleReadiness === "checking"}
				onclick={() => void checkReadiness()}
			>
				Check configuration
			</Button>
			{#if readinessText}
				<span aria-live="polite" class="text-text-muted tr-text-metadata">
					{readinessText}
				</span>
			{/if}
		{/if}
		{#if provider.configured && provider.canLogout}
			<Button
				variant="outline"
				size="sm"
				data-testid="provider-signout"
				data-provider={provider.id}
				disabled={busy}
				onclick={onSignOut}
			>
				<Icon name="log-out" size={14} />
				Sign out
			</Button>
		{/if}
		{#if provider.canApiKey || provider.canConfigure}
			<Button
				variant={provider.canOAuth ? "outline" : "default"}
				size="sm"
				data-testid="provider-apikey"
				data-provider={provider.id}
				disabled={busy}
				onclick={() => onSignIn("api_key")}
			>
				<Icon name="key-round" size={14} />
				{provider.canApiKey ? (provider.configured ? "Change key" : "API key") : "Configure"}
			</Button>
		{/if}
		{#if provider.canOAuth}
			<Button
				size="sm"
				data-testid="provider-signin"
				data-provider={provider.id}
				disabled={busy}
				onclick={() => onSignIn("oauth")}
			>
				<Icon name="log-in" size={14} />
				{provider.configured ? "Reconnect" : "Sign in"}
			</Button>
		{/if}
		{#if (provider.configured && !provider.canLogout) || (!provider.configured && !provider.canApiKey && !provider.canOAuth && !provider.canConfigure)}
			<span
				class="flex shrink-0 items-center gap-xs text-text-muted tr-text-metadata"
				title="Configured through Pi or its environment"
			>
				<Icon name="lock" size={12} />
				Managed by Pi
			</span>
		{/if}
	</div>
</div>
