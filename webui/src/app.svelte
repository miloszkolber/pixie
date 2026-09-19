<script lang="ts">
import { initTransport, resetTransport } from "./connection";
import ControllerAccess from "./connection/controller-access.svelte";
import { appStoreApi } from "./store";
import { initSessionLeases } from "./workspace/navigation/session-leases";
import { initProjectExpansionPersistence } from "./workspace/projects/project-expansion";
import { initShellLayoutPersistence } from "./workspace/shell-layout";
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
	const stopShellLayoutPersistence = initShellLayoutPersistence();
	let stopNavigation: (() => void) | null = null;
	let navigationStopped = false;
	// Keep the v2 codec and restore path out of the bootstrap bundle.
	void import("./workspace/navigation").then(({ initNavigation }) => {
		if (!navigationStopped) stopNavigation = initNavigation();
	});
	const stopSessionLeases = initSessionLeases();
	return () => {
		navigationStopped = true;
		stopNavigation?.();
		stopSessionLeases();
		stopShellLayoutPersistence();
		stopExpansionPersistence();
	};
});
</script>

{#if authenticated}<Shell />{:else}<ControllerAccess onAuthenticated={authenticate} />{/if}
