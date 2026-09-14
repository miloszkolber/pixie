<script lang="ts">
import { onMount } from "svelte";
import Button from "../components/button.svelte";
import { type AuthStatus, authRequest } from "./auth";

interface Props {
	onAuthenticated: (authenticationEnabled: boolean) => void;
}

let { onAuthenticated }: Props = $props();
let status = $state<AuthStatus>();
let token = $state("");
let error = $state("");

onMount(() => {
	void authRequest("/auth/status")
		.then(async (response) => (response.ok ? (response.json() as Promise<AuthStatus>) : undefined))
		.then((next) => {
			if (next?.authenticated) onAuthenticated(next.authenticationEnabled === true);
			else status = next;
		})
		.catch(() => {
			error = "Could not reach controller authentication.";
		});
});

async function submit(event: SubmitEvent): Promise<void> {
	event.preventDefault();
	error = "";
	if (!status) return;
	const response = await authRequest("/auth/login", { token }).catch(() => undefined);
	if (!response?.ok) {
		error = "Authentication failed. Check the controller token and try again.";
		return;
	}
	onAuthenticated(true);
}
</script>

<main class="app-content controller-access u-flex u-h-full u-items-center u-justify-center">
	<section class="card controller-access-card u-w-full">
		<header class="card-header">
			<h1 class="card-title">Connect to Pixie</h1>
			<p class="card-description">
				Enter the configured controller token. This browser stays authenticated for the configured
				period.
			</p>
		</header>
		<div class="card-content">
			{#if status}
				<form class="form u-flex u-flex-col u-gap-md" onsubmit={submit}>
					<label class="text-field">
						<span class="text-field-label">Controller token</span>
						<input
							type="password"
							autocomplete="current-password"
							maxlength={256}
							bind:value={token}
							class="text-field-input"
						/>
					</label>
					{#if error}<p role="alert" class="field-error">{error}</p>{/if}
					<div class="u-flex controller-access-actions"><Button type="submit">Connect</Button></div>
				</form>
			{:else}
				<p class="tr-text-ui u-text-text-muted">{error || "Checking controller authentication…"}</p>
			{/if}
		</div>
	</section>
</main>

<style>
	.controller-access-card {
		max-inline-size: 32rem;
	}

	.controller-access {
		padding: var(--space-lg);
	}

	.controller-access-actions {
		justify-content: flex-end;
	}
</style>
