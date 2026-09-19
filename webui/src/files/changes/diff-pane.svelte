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
import { scopeLabel, splitPath } from "./changes-model";
import { diffIsUnavailable, diffUnavailableNotice, rawPreviewNotice } from "./diff-pane-model";
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
let reviewScope = $derived(scopeLabel(tab.scope));
let rawNotice = $derived(rawPreviewNotice(tab));
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

<div data-testid="diff-pane" class="app-content u-flex u-min-h-0 u-flex-1 u-flex-col">
	{#if refreshError}
		<div
			data-testid="diff-refresh-error"
			role="alert"
			class="diff-pane-refresh-error u-flex u-shrink-0 u-items-center u-gap-sm u-border-b u-px-sm u-py-xs u-text-feedback-error tr-text-metadata"
		>
			<span class="u-min-w-0 u-flex-1 u-truncate" title={refreshError}>Showing a stale diff. {refreshError}</span>
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
	{#if rawNotice}
		<div
			data-testid="diff-raw-notice"
			role="status"
			class="diff-pane-raw-notice u-shrink-0 u-border-b u-px-sm u-py-xs u-text-feedback-warning tr-text-metadata"
		>
			{rawNotice}
		</div>
	{/if}
	<div class="toolbar diff-pane-toolbar u-flex u-shrink-0 u-items-center u-gap-xs u-border-border-default u-border-b u-px-sm">
		<span
			data-testid="diff-path"
			title={tab.originalPath ? `${tab.originalPath} → ${tab.path}` : tab.path}
			class="diff-pane-path u-flex u-min-w-0 tr-code-text"
		>
			{#if tab.originalPath}
				<span class="u-min-w-0 u-truncate u-text-text-muted">{tab.originalPath} → </span>
			{/if}
			{#if pathParts.dir}<span class="u-min-w-0 u-truncate u-text-text-muted">{pathParts.dir}</span>{/if}
			<span class="u-max-w-full u-shrink-0 u-truncate u-text-text-muted">{pathParts.base}</span>
		</span>
		<span
			data-testid="diff-scope"
			title={`${tab.repository} · ${reviewScope}`}
			class="diff-pane-scope u-shrink-0 u-truncate u-text-text-muted tr-text-metadata"
		>{reviewScope}</span>
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
			<span class:diff-pane-copy-success={copied}><Icon name={copied ? "check" : "copy"} size={14} /></span>
		</button>
	</div>
	<div class="u-min-h-0 u-flex-1">
		{#if unavailable}
			<p role="status" class="diff-pane-message tr-text-ui u-text-text-muted">{notice}</p>
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

<style>
	.diff-pane-refresh-error {
		border-color: var(--feedback-error-muted);
		background-color: var(--feedback-error-subtle);
	}
	.diff-pane-raw-notice {
		border-color: var(--border-muted);
		background-color: var(--feedback-warning-subtle);
	}
	.diff-pane-toolbar {
		block-size: calc(var(--space-base) * 32 / 13);
		background-color: var(--container-header-bg);
	}
	.diff-pane-path {
		align-items: baseline;
		margin-inline-end: auto;
	}
	.diff-pane-scope { max-inline-size: 12rem; }
	.diff-pane-copy-success { color: var(--feedback-success); }
	.diff-pane-message { padding: var(--space-lg); }
</style>
