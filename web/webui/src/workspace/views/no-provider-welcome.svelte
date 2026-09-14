<script lang="ts">
import Button from "../../components/button.svelte";
import { openSettingsArea } from "../navigation/open-settings-area";
import { SettingsSection } from "../../store";
</script>

<main
	id="main-content"
	data-testid="no-provider-welcome"
	class="app-content u-flex u-h-full u-min-h-0 u-min-w-0 u-flex-col u-items-center u-justify-center u-gap-md u-px-xl u-py-xl u-text-center"
>
	<h1 class="tr-brand-hero no-provider-welcome__title">Pi unavailable</h1>
	<p class="u-max-w-34rem tr-text-reading u-text-text-muted">
		Start the Pi host and configure a provider in Pi, then refresh this page.
	</p>
	<Button
		data-testid="connect-provider"
		onclick={() => void openSettingsArea(SettingsSection.Providers)}
	>
		View Pi providers
	</Button>
</main>

<style>
	.no-provider-welcome__title {
		color: var(--primary);
	}
</style>
