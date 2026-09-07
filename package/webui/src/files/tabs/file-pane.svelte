<script lang="ts">
import { untrack } from "svelte";
import ToggleSegment from "../../components/toggle-segment.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, type FileTab } from "../../store";
import { projectFileUrl } from "../markdown/markdown-links";
import { BINARY_FILE_NOTICE, filePreviewKind } from "./file-pane-model";
import SourcePreview from "./source-preview.svelte";
import {
	createReadSequencer,
	decideLiveTabChange,
	runLiveTabRefresh,
} from "./use-live-tab-content";

interface Props {
	tab: FileTab;
}
let { tab }: Props = $props();
let failedImageSource = $state<string | null>(null);
let imageRevision = $state(untrack(() => tab.loadedTick ?? 0));
let imageRequestRevision = $state(0);
let pendingImageTick = $state<number | null>(null);
let refreshError = $state<string | null>(null);
let refreshRevision = $state(0);
let kind = $derived(filePreviewKind(tab.path, tab.content));
let view = $derived(tab.view ?? "rendered");
let fsChange = $derived($appStore.fsChangesByProjectArea[tab.projectAreaId]);
let imageSource = $derived.by(() => {
	if (kind !== "image") return undefined;
	const url = projectFileUrl(getTransport().httpBase(), tab.projectAreaId, "", tab.path);
	return url ? `${url}?v=${imageRevision}-${imageRequestRevision}` : undefined;
});
const sequencer = createReadSequencer();
let markdownModule: Promise<typeof import("../markdown/markdown-preview.svelte")> | undefined;

function loadMarkdown() {
	markdownModule ??= import("../markdown/markdown-preview.svelte");
	return markdownModule;
}

$effect(() => {
	const retry = refreshRevision;
	void retry;
	const decision = decideLiveTabChange(fsChange, tab);
	if (decision.kind === "none") return;
	if (decision.kind === "acknowledge") {
		appStoreApi
			.getState()
			.updateFileTabContent(tab.projectAreaId, tab.id, tab.content, decision.tick);
		return;
	}
	const currentKind = kind;
	if (currentKind === "image") {
		pendingImageTick = decision.tick;
		imageRevision = decision.tick;
		imageRequestRevision += 1;
		failedImageSource = null;
		return;
	}
	const isCurrent = sequencer.begin();
	let cancelled = false;
	const read = getTransport().request("fs.readFile", {
		projectId: tab.projectAreaId,
		path: tab.path,
	});
	void runLiveTabRefresh(
		() => read,
		() => !cancelled && isCurrent(),
		({ content }) => {
			refreshError = null;
			appStoreApi
				.getState()
				.updateFileTabContent(tab.projectAreaId, tab.id, content, decision.tick);
		},
		(cause) => {
			refreshError = errorText(cause);
		},
	);
	return () => {
		cancelled = true;
	};
});

function imageRequestMatches(event: Event): boolean {
	return (event.currentTarget as HTMLImageElement).getAttribute("src") === imageSource;
}

function imageLoaded(event: Event): void {
	if (!imageRequestMatches(event)) return;
	failedImageSource = null;
	const loadedTick = pendingImageTick;
	if (loadedTick === null) return;
	pendingImageTick = null;
	refreshError = null;
	appStoreApi.getState().updateFileTabContent(tab.projectAreaId, tab.id, tab.content, loadedTick);
}

function imageFailed(event: Event): void {
	if (!imageRequestMatches(event)) return;
	failedImageSource = imageSource ?? null;
	if (pendingImageTick !== null) refreshError = "The updated image could not be loaded.";
}

function retryRefresh(): void {
	if (kind === "image" && (pendingImageTick !== null || failedImageSource === imageSource)) {
		failedImageSource = null;
		imageRequestRevision += 1;
		return;
	}
	refreshRevision += 1;
}
</script>

{#snippet readOnlyToolbar()}
	<div class="toolbar flex h-8 shrink-0 items-center gap-sm border-border-default border-b bg-container-header-bg px-sm">
		<span class="min-w-0 flex-1 truncate text-text-muted tr-text-metadata" title={tab.path}>{tab.path}</span>
		<span class="text-text-subtle tr-text-metadata">Read-only</span>
	</div>
{/snippet}

<div class="app-content flex h-full min-h-0 flex-col">
	{#if refreshError}
		<div
			data-testid="file-refresh-error"
			role="alert"
			class="flex shrink-0 items-center gap-sm border-feedback-error-muted border-b bg-feedback-error-subtle px-sm py-xs text-feedback-error tr-text-metadata"
		>
			<span class="min-w-0 flex-1 truncate" title={refreshError}>Refresh failed; the previous content may be shown. {refreshError}</span>
			<button
				type="button"
				data-testid="file-refresh-retry"
				onclick={retryRefresh}
				class="btn"
				data-variant="ghost"
				data-size="sm"
			>Retry</button>
		</div>
	{/if}
	{#if kind === "markdown"}
		<div
			data-testid="markdown-view-toggle"
			role="toolbar"
			aria-label="Markdown view mode"
			class="toolbar flex h-8 shrink-0 items-center gap-xs border-border-default border-b bg-container-header-bg px-sm"
		>
			<span class="mr-auto min-w-0 truncate text-text-muted tr-text-metadata" title={tab.path}>{tab.path}</span>
			<span class="text-text-subtle tr-text-metadata">Read-only</span>
			<ToggleSegment
				testid="md-toggle-preview"
				label="Preview"
				active={view === "rendered"}
				onclick={() => appStoreApi.getState().setFileTabView(tab.id, "rendered")}
			/>
			<ToggleSegment
				testid="md-toggle-source"
				label="Source"
				active={view === "source"}
				onclick={() => appStoreApi.getState().setFileTabView(tab.id, "source")}
			/>
		</div>
		<div class="min-h-0 flex-1">
			{#if view === "rendered"}
				{#await loadMarkdown()}
					<div class="flex h-full items-center justify-center text-text-muted">Loading…</div>
				{:then module}
					<module.default content={tab.content} projectAreaId={tab.projectAreaId} path={tab.path} />
				{:catch}
					<p role="alert" class="p-lg tr-text-ui text-feedback-error">Markdown preview is unavailable.</p>
				{/await}
			{:else}
				<SourcePreview path={tab.path} content={tab.content} />
			{/if}
		</div>
	{:else}
		{@render readOnlyToolbar()}
		{#if kind === "image"}
			{#if !imageSource || failedImageSource === imageSource}
				<div role="alert" class="flex flex-col items-start gap-sm p-lg tr-text-ui text-text-muted">
					<p>Image preview is unavailable. The file may have changed, exceeded the preview limit, or left the project roots.</p>
					{#if imageSource}
						<button type="button" data-testid="image-preview-retry" onclick={retryRefresh} class="btn" data-variant="ghost" data-size="sm">Retry</button>
					{/if}
				</div>
			{:else}
				<div class="flex min-h-0 flex-1 items-center justify-center overflow-auto p-md">
					<img
						src={imageSource}
						alt={tab.name}
						onload={imageLoaded}
						onerror={imageFailed}
						class="image max-h-full max-w-full object-contain"
					/>
				</div>
			{/if}
		{:else if kind === "binary"}
			<p class="p-lg tr-text-ui text-text-muted">{BINARY_FILE_NOTICE}</p>
		{:else}
			<div class="min-h-0 flex-1"><SourcePreview path={tab.path} content={tab.content} /></div>
		{/if}
	{/if}
</div>
