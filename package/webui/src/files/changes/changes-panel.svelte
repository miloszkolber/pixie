<script lang="ts">
import type { GitDiffScope, GitRepository } from "@pixie/contracts";
import Icon from "../../components/icon.svelte";
import ToggleSegment from "../../components/toggle-segment.svelte";
import { errorText, getTransport } from "../../connection";
import { tupleKey } from "../../lib";
import {
	appStore,
	appStoreApi,
	matchesChangePath,
	selectActiveContentTab,
	selectDiffScope,
	selectProjectAreaTick,
	type TabIntent,
} from "../../store";
import { openDiffInTab } from "../tabs/open-tabs";
import ChangeRowActions from "./change-row-actions.svelte";
import { branchName, scopeKey, splitPath, statusNameClass } from "./changes-model";
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
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));
let activeDiffTab = $derived.by(() => {
	const tab = selectActiveContentTab($appStore, projectAreaId);
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

<div class="app-content flex h-full min-h-0 flex-col">
	<div class="toolbar flex h-panel-header-row shrink-0 items-center gap-xs border-border-default border-b px-sm">
		<div class="mr-auto flex min-w-0 items-center gap-xs tr-text-metadata text-text-muted">
			<Icon name="git-branch" size={14} />
			{#if repositories.length > 1}
				<select
					aria-label="Git repository"
					value={repository?.root ?? ""}
					onchange={(event) => {
						selectedRepository = event.currentTarget.value;
						appStoreApi.getState().setDiffScope(projectAreaId, UNCOMMITTED_SCOPE);
					}}
					class="select min-w-0 border-0 bg-transparent text-text-muted"
				>
					{#each repositories as candidate (candidate.id)}
						<option value={candidate.root}>{candidate.relativePath || candidate.name}</option>
					{/each}
				</select>
			{:else}
				<span class="truncate">
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
		<div class="shrink-0 border-border-default border-b px-xs py-xs">
			<GitScopeMenu
				{projectAreaId}
				repository={repository.root}
				head={repository.head}
				{scope}
				onSelect={(next) => appStoreApi.getState().setDiffScope(projectAreaId, next)}
			/>
		</div>
	{/if}
	<div class="min-h-0 flex-1 overflow-auto">
		{#if warnings.length > 0}
			<p role="status" class="border-border-muted border-b px-sm py-xs tr-text-metadata text-feedback-warning">
				{warnings.join(" ")}
			</p>
		{/if}
		{#if visibleError}
			<div class="flex flex-col items-start gap-xs px-sm py-xs">
				<p class="tr-text-metadata text-feedback-error">Could not read the changes: {visibleError}</p>
				<button type="button" onclick={refresh} class="btn" data-variant="ghost" data-size="sm">Retry</button>
			</div>
		{:else if loadingScope || loadingRepositories}
			<p role="status" class="px-sm py-xs tr-text-metadata text-text-muted">Loading changes…</p>
		{:else if status === null}
			<p class="px-sm py-xs tr-text-metadata text-text-muted">No Git repositories found.</p>
		{:else if status.changes.length === 0}
			<p data-testid="changes-empty" class="px-sm py-xs tr-text-metadata text-text-muted">
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
								class="tree-leaf flex min-w-0 flex-1 items-center gap-sm px-sm py-xs text-left tr-text-ui"
							>
								<span class="flex min-w-0 flex-1 items-baseline">
									{#if parts.dir}<span class="min-w-0 shrink truncate text-text-muted">{parts.dir}</span>{/if}
									<span class={`max-w-full shrink-0 truncate ${statusNameClass(change.status) || "text-text-muted"}`}>
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
