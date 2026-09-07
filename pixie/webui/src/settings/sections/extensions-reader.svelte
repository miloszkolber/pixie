<script lang="ts">
import type { NativeExtensionTarget } from "@pixie/contracts";
import { onMount, onDestroy, untrack } from "svelte";
import { getTransport } from "@/connection";
import ExtensionsInventory from "./extensions-inventory.svelte";
import { ExtensionsModel } from "./extensions-model";
let { target, label }: { target: NativeExtensionTarget; label: string } = $props();
const model = new ExtensionsModel(
	untrack(() => target),
	getTransport(),
);
const state = model.readable;
onMount(() => {
	void model.load();
});
onDestroy(() => model.dispose());
</script>

<ExtensionsInventory {label} inventory={$state.inventory} loading={$state.loading} error={$state.error}
	busy={$state.busy} notice={$state.notice} canRequestReload={Boolean(target.sessionId)}
	onRefresh={() => void model.load()} onReload={() => void model.requestReload()}
	onConfigure={(resource) => void model.configure(resource, (message) => window.confirm(message))} />
