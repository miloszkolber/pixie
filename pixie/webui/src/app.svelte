<script lang="ts">
import { initTransport, resetTransport } from "./connection";
import ControllerAccess from "./connection/controller-access.svelte";
import { appStoreApi } from "./store";
import { initNavigation } from "./workspace/navigation";
import { initSessionLeases } from "./workspace/navigation/session-leases";
import { initProjectExpansionPersistence } from "./workspace/projects/project-expansion";
import Shell from "./workspace/shell.svelte";

let authenticated = $state(false);

function authenticate(authenticationEnabled: boolean): void {
	appStoreApi.getState().setAuthenticationEnabled(authenticationEnabled);
	authenticated = true;
}

function signOut(): void {
	resetTransport();
	authenticated = false;
}

$effect(() => {
	window.addEventListener("pixie-auth-lost", signOut);
	return () => window.removeEventListener("pixie-auth-lost", signOut);
});

$effect(() => {
	if (!authenticated) return;
	initTransport();
	const stopExpansionPersistence = initProjectExpansionPersistence();
	const stopNavigation = initNavigation();
	const stopSessionLeases = initSessionLeases();
	return () => {
		stopSessionLeases();
		stopNavigation();
		stopExpansionPersistence();
	};
});
</script>

{#if authenticated}<Shell />{:else}<ControllerAccess onAuthenticated={authenticate} />{/if}
