<script lang="ts">
import { onDestroy, untrack } from "svelte";
import type { BrowserMCPStatus } from "@pixie/contracts";
import { getTransport } from "@/connection";
import { appStore } from "@/store";
import {
	type BrowserMcpDraft,
	BrowserMcpModel,
	browserMcpDraftFromStatus,
	emptyBrowserMcpDraft,
} from "./browser-mcp-settings";
import BrowserMcpView from "./browser-mcp-view.svelte";

const model = new BrowserMcpModel(untrack(() => getTransport()));
const view = model.readable;
let draft = $state<BrowserMcpDraft>(emptyBrowserMcpDraft());
let loadedGeneration: number | null = null;

let connected = $derived($appStore.status === "connected");
let connectionGeneration = $derived($appStore.connectionGeneration);

function hydrate(status: BrowserMCPStatus | null): void {
	if (status) draft = browserMcpDraftFromStatus(status);
}

async function load(): Promise<void> {
	if (!connected) return;
	const status = await model.load();
	hydrate(status);
}

async function save(): Promise<void> {
	hydrate(await model.save(draft));
}

async function remove(): Promise<void> {
	hydrate(await model.remove());
}

$effect(() => {
	if (!connected) {
		loadedGeneration = null;
		return;
	}
	if (loadedGeneration === connectionGeneration) return;
	loadedGeneration = connectionGeneration;
	void load();
});

onDestroy(() => model.dispose());
</script>

<BrowserMcpView
	{draft}
	status={$view.status}
	loading={$view.loading}
	busy={$view.busy}
	error={$view.error}
	notice={$view.notice}
	{connected}
	onName={(value) => (draft = { ...draft, name: value })}
	onUrl={(value) => (draft = { ...draft, url: value })}
	onEnabled={(value) => (draft = { ...draft, enabled: value })}
	onSave={() => void save()}
	onRemove={() => void remove()}
	onRefresh={() => void load()}
/>
