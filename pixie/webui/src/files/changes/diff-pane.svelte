<script lang="ts">
import { onDestroy } from "svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { copyText } from "../../lib/utils";
import { appStore, appStoreApi, type DiffTab, selectDiffTabTargetRef } from "../../store";
import {
	createRefreshAttemptGate,
	createReadSequencer,
	decideLiveTabChange,
	runLiveTabRefresh,
} from "../tabs/use-live-tab-content";
import { splitPath } from "./changes-model";
import { diffIsUnavailable, diffUnavailableNotice } from "./diff-pane-model";
import { simpleUnifiedDiff } from "./line-diff";
import SourceDiff from "./source-diff.svelte";

interface Props {
	tab: DiffTab;
}
let { tab }: Props = $props();
let copied = $state(false);
let copyTimer: ReturnType<typeof setTimeout> | undefined;
let targetRef = $derived(selectDiffTabTargetRef($appStore, tab));
let fsChange = $derived($appStore.fsChangesByProjectArea[tab.projectAreaId]);
let ignoreWhitespace = $derived(tab.ignoreWhitespace ?? false);
let unavailable = $derived(diffIsUnavailable(tab));
let notice = $derived(diffUnavailableNotice(tab));
let pathParts = $derived(splitPath(tab.path));
let diff = $derived(
	unavailable
		? ""
		: simpleUnifiedDiff(tab.path, tab.original, tab.modified, ignoreWhitespace, tab.originalPath),
);
const sequencer = createReadSequencer();
const targetAttempts = createRefreshAttemptGate();
let reloadTabId = $state("");
let liveRefreshError = $state<string | null>(null);
let targetRefreshError = $state<string | null>(null);
let refreshRevision = $state(0);
let refreshError = $derived(targetRefreshError ?? liveRefreshError);

function readDiff() {
	return getTransport().request("git.diffFile", {
		projectId: tab.projectAreaId,
		repository: tab.repository,
		path: tab.path,
		scope: tab.scope,
	});
}

$effect(() => {
	const retry = refreshRevision;
	void retry;
	const decision = decideLiveTabChange(fsChange, tab);
	if (decision.kind === "none") return;
	if (decision.kind === "acknowledge") {
		appStoreApi
			.getState()
			.updateDiffTabContent(tab.projectAreaId, tab.id, tab, decision.tick, tab.loadedTarget);
		return;
	}
	const loadedTarget = targetRef;
	const isCurrent = sequencer.begin();
	let cancelled = false;
	void runLiveTabRefresh(
		readDiff,
		() => !cancelled && isCurrent(),
		(preview) => {
			liveRefreshError = null;
			appStoreApi
				.getState()
				.updateDiffTabContent(
					tab.projectAreaId,
					tab.id,
					preview,
					decision.tick,
					preview.comparisonId ?? loadedTarget,
				);
		},
		(cause) => {
			liveRefreshError = errorText(cause);
		},
	);
	return () => {
		cancelled = true;
	};
});

$effect(() => {
	const currentId = tab.id;
	const reloadKey = targetRef;
	const loadedKey = tab.loadedTarget;
	const retry = refreshRevision;
	if (reloadTabId !== currentId) {
		reloadTabId = currentId;
		targetAttempts.reset(loadedKey ?? reloadKey, retry);
		return;
	}
	if (!reloadKey) return;
	if (reloadKey === loadedKey) {
		targetAttempts.reset(reloadKey, retry);
		targetRefreshError = null;
		return;
	}
	if (!targetAttempts.claim(reloadKey, retry)) return;
	const isCurrent = sequencer.begin();
	let cancelled = false;
	void runLiveTabRefresh(
		readDiff,
		() => !cancelled && isCurrent(),
		(preview) => {
			targetRefreshError = null;
			appStoreApi
				.getState()
				.updateDiffTabContent(
					tab.projectAreaId,
					tab.id,
					preview,
					tab.loadedTick ?? 0,
					preview.comparisonId ?? reloadKey,
				);
		},
		(cause) => {
			targetRefreshError = errorText(cause);
		},
	);
	return () => {
		cancelled = true;
	};
});

async function copy(): Promise<void> {
	if (!(await copyText(diff))) return;
	copied = true;
	if (copyTimer) clearTimeout(copyTimer);
	copyTimer = setTimeout(() => (copied = false), 1_500);
}

onDestroy(() => {
	if (copyTimer) clearTimeout(copyTimer);
});
</script>

<div data-testid="diff-pane" class="app-content flex h-full min-h-0 flex-col">
	{#if refreshError}
		<div
			data-testid="diff-refresh-error"
			role="alert"
			class="flex shrink-0 items-center gap-sm border-feedback-error-muted border-b bg-feedback-error-subtle px-sm py-xs text-feedback-error tr-text-metadata"
		>
			<span class="min-w-0 flex-1 truncate" title={refreshError}>Showing a stale diff. {refreshError}</span>
			<button
				type="button"
				data-testid="diff-refresh-retry"
				onclick={() => (refreshRevision += 1)}
				class="btn"
				data-variant="ghost"
				data-size="sm"
			>Retry</button>
		</div>
	{/if}
	<div class="toolbar flex h-8 shrink-0 items-center gap-xs border-border-default border-b bg-container-header-bg px-sm">
		<span
			data-testid="diff-path"
			title={tab.originalPath ? `${tab.originalPath} → ${tab.path}` : tab.path}
			class="mr-auto flex min-w-0 items-baseline tr-code-text"
		>
			{#if tab.originalPath}
				<span class="min-w-0 truncate text-text-muted">{tab.originalPath} → </span>
			{/if}
			{#if pathParts.dir}<span class="min-w-0 shrink truncate text-text-muted">{pathParts.dir}</span>{/if}
			<span class="max-w-full shrink-0 truncate text-text-muted">{pathParts.base}</span>
		</span>
		<button
			type="button"
			data-testid="diff-toggle-whitespace"
			data-active={ignoreWhitespace || undefined}
			aria-pressed={ignoreWhitespace}
			aria-label="Hide whitespace changes"
			disabled={unavailable}
			title="Hide whitespace changes"
			onclick={() => appStoreApi.getState().setDiffTabIgnoreWhitespace(tab.id, !ignoreWhitespace)}
			class="btn"
			data-variant="ghost"
			data-size="icon-sm"
		>
			<Icon name="pilcrow" size={14} />
		</button>
		<button
			type="button"
			data-testid="diff-copy"
			aria-label="Copy diff"
			title="Copy diff"
			disabled={unavailable}
			onclick={() => void copy()}
			class="btn"
			data-variant="ghost"
			data-size="icon-sm"
		>
			<Icon name={copied ? "check" : "copy"} size={14} class={copied ? "text-feedback-success" : ""} />
		</button>
	</div>
	<div class="min-h-0 flex-1">
		{#if unavailable}
			<p role="status" class="p-lg tr-text-ui text-text-muted">{notice}</p>
		{:else}
			<SourceDiff
				path={tab.path}
				originalPath={tab.originalPath}
				original={tab.original}
				modified={tab.modified}
				{ignoreWhitespace}
			/>
		{/if}
	</div>
</div>
