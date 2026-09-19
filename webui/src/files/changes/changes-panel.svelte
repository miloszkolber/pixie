<script lang="ts">
import type { GitDiffScope, GitRepository } from "@pixie/shared";
import Icon from "../../components/icon.svelte";
import ToggleSegment from "../../components/toggle-segment.svelte";
import { errorText, getTransport } from "../../connection";
import { tupleKey } from "../../lib";
import {
	appStore,
	appStoreApi,
	matchesChangePath,
	selectDiffScope,
	selectProjectAreaTick,
	type TabIntent,
} from "../../store";
import { openDiffInTab } from "../tabs/open-tabs";
import ChangeRowActions from "./change-row-actions.svelte";
import {
	branchName,
	repositoryDisplayName,
	scopeKey,
	splitPath,
	statusNameClass,
} from "./changes-model";
import ChangesTree from "./changes-tree.svelte";
import DiffStatBadge from "./diff-stat-badge.svelte";
import GitScopeMenu from "./git-scope-menu.svelte";

const UNCOMMITTED_SCOPE: GitDiffScope = { kind: "uncommitted" };

interface Props {
	projectAreaId: string;
	onOpen?: (() => void) | undefined;
}

let { projectAreaId, onOpen }: Props = $props();
let catalog = $state<{ projectAreaId: string; repositories: GitRepository[] } | null>(null);
let selectedRepository = $state<string | null>(null);
let scoped = $state<{ key: string; status?: GitRepository; error?: string } | null>(null);
let error = $state<string | null>(null);
let warnings = $state<string[]>([]);
let warned = $state(false);
let highlighted = $state<string | null>(null);
let catalogRevision = $state(0);
let scopedRevision = $state(0);
const catalogRead = { identity: "", generation: 0 };
const scopedRead = { generation: 0 };

let repositories = $derived(catalog?.projectAreaId === projectAreaId ? catalog.repositories : []);
let repository = $derived(
	repositories.find((candidate) => candidate.root === selectedRepository) ??
		repositories[0] ??
		null,
);
let scope = $derived(selectDiffScope($appStore, projectAreaId));
let readKey = $derived(tupleKey(projectAreaId, repository?.root ?? "", scopeKey(scope)));
let status = $derived.by(() => {
	if (scope.kind === "uncommitted") return repository;
	return scoped?.key === readKey ? (scoped.status ?? null) : null;
});
let visibleError = $derived(error ?? (scoped?.key === readKey ? (scoped.error ?? null) : null));
let loadingScope = $derived(
	repository !== null && scope.kind !== "uncommitted" && scoped?.key !== readKey,
);
let loadingRepositories = $derived(catalog?.projectAreaId !== projectAreaId && error === null);
let changesView = $derived($appStore.changesView);
let visibleWarnings = $derived([...warnings, ...(status?.warnings ?? [])]);
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));
let activeDiffTab = $derived.by(() => {
	const selection = $appStore.workspaceSelection.secondarySelection;
	if (selection?.kind !== "diff" || selection.projectId !== projectAreaId) return null;
	const tab = ($appStore.tabsByProjectArea[projectAreaId] ?? []).find(
		(candidate) => candidate.kind === "diff" && candidate.id === selection.resourceId,
	);
	return tab?.kind === "diff" &&
		tab.repository === repository?.root &&
		scopeKey(tab.scope) === scopeKey(scope)
		? tab
		: null;
});

$effect(() => {
	const id = projectAreaId;
	const tick = projectTick;
	const revision = catalogRevision;
	void tick;
	void revision;
	if (catalogRead.identity !== id) {
		catalogRead.identity = id;
		catalog = null;
		selectedRepository = null;
		error = null;
		warnings = [];
		highlighted = null;
		warned = false;
	}
	const mine = ++catalogRead.generation;
	void getTransport()
		.request("git.listRepositories", { projectId: id })
		.then((result) => {
			if (mine !== catalogRead.generation || appStoreApi.getState().removedProjectAreaIds[id])
				return;
			catalog = { projectAreaId: id, repositories: result.repositories };
			warnings = result.warnings;
			const next = result.repositories.some((candidate) => candidate.root === selectedRepository)
				? selectedRepository
				: (result.repositories[0]?.root ?? null);
			if (selectedRepository !== null && next !== selectedRepository) {
				appStoreApi.getState().setDiffScope(id, UNCOMMITTED_SCOPE);
			}
			selectedRepository = next;
			error = null;
			warned = false;
		})
		.catch((cause) => {
			if (mine !== catalogRead.generation || appStoreApi.getState().removedProjectAreaIds[id])
				return;
			const detail = errorText(cause);
			if (repositories.length > 0 && !warned) {
				warned = true;
				appStoreApi.getState().pushToast({
					variant: "error",
					message: `Could not refresh the changes: ${detail}`,
				});
			}
			error = detail;
		});
	return () => {
		if (mine === catalogRead.generation) catalogRead.generation += 1;
	};
});

$effect(() => {
	const id = projectAreaId;
	const selected = repository;
	const selectedScope = scope;
	const key = readKey;
	const tick = projectTick;
	const revision = scopedRevision;
	void tick;
	void revision;
	if (!selected || selectedScope.kind === "uncommitted") {
		scoped = null;
		return;
	}
	const mine = ++scopedRead.generation;
	void getTransport()
		.request("git.status", {
			projectId: id,
			repository: selected.root,
			scope: selectedScope,
		})
		.then((result) => {
			if (mine !== scopedRead.generation || appStoreApi.getState().removedProjectAreaIds[id])
				return;
			scoped = { key, status: result };
			if (selectedScope.kind === "branch" && result.comparisonId) {
				appStoreApi
					.getState()
					.noteDiffComparison(id, result.root, selectedScope, result.comparisonId);
			}
		})
		.catch((cause) => {
			if (mine === scopedRead.generation && !appStoreApi.getState().removedProjectAreaIds[id]) {
				scoped = { key, error: errorText(cause) };
			}
		});
	return () => {
		if (mine === scopedRead.generation) scopedRead.generation += 1;
	};
});

$effect(() => {
	const request = $appStore.changesRequest;
	if (request?.projectAreaId !== projectAreaId) return;
	if (appStoreApi.getState().changesRequest !== request) return;
	if (scope.kind !== "uncommitted") {
		appStoreApi.getState().setDiffScope(projectAreaId, UNCOMMITTED_SCOPE);
		return;
	}
	if (!status) return;
	const match = status.changes.find((change) => matchesChangePath(request.path, change.path));
	if (match) openDiff(match.path, "preview");
	else highlighted = request.path;
	appStoreApi.getState().clearChangesRequest();
});

$effect(() => {
	if (activeDiffTab) highlighted = null;
});

function refresh(): void {
	catalogRevision += 1;
	scopedRevision += 1;
}

function openDiff(path: string, intent: TabIntent): void {
	highlighted = path;
	const currentStatus = status;
	if (!currentStatus) return;
	void openDiffInTab(
		projectAreaId,
		scope,
		path,
		intent,
		undefined,
		currentStatus.root,
		currentStatus.comparisonId,
	).then((opened) => {
		if (opened) onOpen?.();
	});
}

function isActive(path: string): boolean {
	return activeDiffTab?.path === path || (!activeDiffTab && highlighted === path);
}
</script>

<div class="app-content u-flex u-h-full u-min-h-0 u-flex-col">
	<div class="toolbar changes-panel-toolbar u-flex u-shrink-0 u-items-center u-gap-xs u-border-border-default u-border-b u-px-sm">
		<div class="changes-panel-repository u-flex u-min-w-0 u-items-center u-gap-xs tr-text-metadata u-text-text-muted">
			<Icon name="git-branch" size={14} />
			{#if repositories.length > 1}
				<select
					aria-label="Git repository"
					data-testid="git-repository-select"
					value={repository?.root ?? ""}
					onchange={(event) => {
						selectedRepository = event.currentTarget.value;
						appStoreApi.getState().setDiffScope(projectAreaId, UNCOMMITTED_SCOPE);
					}}
					class="select changes-panel-repository-select u-min-w-0"
				>
					{#each repositories as candidate (candidate.id)}
						<option value={candidate.root}>{repositoryDisplayName(candidate)}</option>
					{/each}
				</select>
			{:else}
				<span class="u-truncate">
					{repository
						? repository.head.kind === "branch"
							? branchName(`refs/heads/${repository.head.name}`)
							: repository.head.kind === "detached"
								? repository.head.oid.slice(0, 8)
								: "Unborn repository"
						: "Git changes"}
				</span>
			{/if}
		</div>
		<button
			type="button"
			aria-label="Refresh changes"
			title="Refresh changes"
			onclick={refresh}
			class="btn"
			data-variant="ghost"
			data-size="icon-sm"
		><Icon name="refresh-cw" size={14} /></button>
		<ToggleSegment
			testid="changes-toggle-list"
			label="List"
			active={changesView === "list"}
			onclick={() => appStoreApi.getState().setChangesView("list")}
		/>
		<ToggleSegment
			testid="changes-toggle-tree"
			label="Tree"
			active={changesView === "tree"}
			onclick={() => appStoreApi.getState().setChangesView("tree")}
		/>
	</div>
	{#if repository}
		<div class="u-shrink-0 u-border-border-default u-border-b u-px-xs u-py-xs">
			<GitScopeMenu
				{projectAreaId}
				repository={repository.root}
				head={repository.head}
				{scope}
				onSelect={(next) => appStoreApi.getState().setDiffScope(projectAreaId, next)}
			/>
		</div>
	{/if}
	<div class="u-min-h-0 u-flex-1 u-overflow-auto">
		{#if visibleWarnings.length > 0}
			<p
				role="status"
				data-testid="git-warnings"
				class="changes-panel-warning u-border-b u-px-sm u-py-xs tr-text-metadata u-text-feedback-warning"
			>
				{visibleWarnings.join(" ")}
			</p>
		{/if}
		{#if visibleError}
			<div class="u-flex u-flex-col u-items-start u-gap-xs u-px-sm u-py-xs">
				<p class="tr-text-metadata u-text-feedback-error">Could not read the changes: {visibleError}</p>
				<button type="button" onclick={refresh} class="btn" data-variant="ghost" data-size="sm">Retry</button>
			</div>
		{:else if loadingScope || loadingRepositories}
			<p role="status" class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">Loading changes…</p>
		{:else if status === null}
			<p class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">No Git repositories found.</p>
		{:else if status.changes.length === 0}
			<p data-testid="changes-empty" class="u-px-sm u-py-xs tr-text-metadata u-text-text-muted">
				{scope.kind === "uncommitted"
					? "Working tree is clean."
					: scope.kind === "commit"
						? "No file changes in this commit."
						: scope.kind === "branch"
							? `No committed changes from ${branchName(scope.baseRef)}.`
							: "No changes since this commit."}
			</p>
		{:else if changesView === "tree"}
			<ChangesTree changes={status.changes} {isActive} onOpen={openDiff} />
		{:else}
			<ul class="tree tree-group">
				{#each status.changes as change (change.path)}
					{@const parts = splitPath(change.path)}
					<li class="tree-item">
						{#snippet row(oncontextmenu: (event: MouseEvent) => void)}
							<button
								type="button"
								{oncontextmenu}
								data-testid="change-item"
								data-status={change.status}
								data-active={isActive(change.path) || undefined}
								onclick={() => openDiff(change.path, "preview")}
								ondblclick={() => openDiff(change.path, "keep")}
								title={change.path}
								class="tree-leaf changes-panel-row u-flex u-min-w-0 u-flex-1 u-items-center u-gap-sm u-px-sm u-py-xs u-text-left tr-text-ui"
							>
								<span class="changes-panel-path u-flex u-min-w-0 u-flex-1">
									{#if parts.dir}<span class="u-min-w-0 u-truncate u-text-text-muted">{parts.dir}</span>{/if}
									<span class={`u-max-w-full u-shrink-0 u-truncate ${statusNameClass(change.status)}`}>
										{parts.base}
									</span>
								</span>
								<DiffStatBadge added={change.added ?? 0} removed={change.removed ?? 0} />
							</button>
						{/snippet}
						<ChangeRowActions
							path={change.path}
							active={isActive(change.path)}
							onView={() => openDiff(change.path, "preview")}
							children={row}
						/>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</div>

<style>
	.changes-panel-toolbar { block-size: var(--panel-header-row-height); }
	.changes-panel-repository { margin-inline-end: auto; }
	.changes-panel-repository-select {
		border: 0;
		background-color: transparent;
		color: var(--text-muted);
	}
	.changes-panel-warning { border-color: var(--border-muted); }
	.changes-panel-path { align-items: baseline; }
	.tree-row-status-added { color: var(--feedback-success); }
	.tree-row-status-deleted {
		color: var(--feedback-error);
		text-decoration: line-through;
	}
	.tree-row-status-renamed { color: var(--feedback-info); }
	.tree-row-status-default { color: var(--text-muted); }
</style>
