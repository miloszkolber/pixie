<script lang="ts">
import type { GitDiffScope, GitHead } from "@pixie/contracts";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as popoverBehavior } from "../../../vendor/mewa-ui/components/popover.js";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, selectProjectAreaTick } from "../../store";
import BranchPicker from "./branch-picker.svelte";
import { scopeLabel } from "./changes-model";
import CommitPicker from "./commit-picker.svelte";
import type { BranchCatalog, CommitHistory } from "./git-scope-state";

interface Props {
	projectAreaId: string;
	repository: string;
	head: GitHead;
	scope: GitDiffScope;
	onSelect: (scope: GitDiffScope) => void;
}

let { projectAreaId, repository, head, scope, onSelect }: Props = $props();
let open = $state(false);
let branches = $state<BranchCatalog>(null);
let history = $state<CommitHistory>(null);
let branchRetry = $state(0);
let commitRetry = $state(0);
let branchGeneration = 0;
let commitGeneration = 0;
let branchIdentity = "";
let commitIdentity = "";
let popover: HTMLElement;
const componentId = $props.id();
const popoverId = `git-scope-${componentId}`;
let projectTick = $derived(selectProjectAreaTick($appStore, projectAreaId));

$effect(() => {
	const active = open && head.kind !== "unborn";
	const id = projectAreaId;
	const root = repository;
	const tick = projectTick;
	const retry = branchRetry;
	void tick;
	void retry;
	if (!active) {
		branches = null;
		return;
	}
	const identity = `${id}\0${root}`;
	if (identity !== branchIdentity) {
		branchIdentity = identity;
		branches = null;
	}
	const mine = ++branchGeneration;
	void getTransport()
		.request("git.listBranches", { projectId: id, repository: root })
		.then((result) => {
			if (mine === branchGeneration && !appStoreApi.getState().removedProjectAreaIds[id]) {
				branches = result;
			}
		})
		.catch((cause) => {
			if (mine === branchGeneration && !appStoreApi.getState().removedProjectAreaIds[id]) {
				branches = { error: errorText(cause) };
			}
		});
	return () => {
		if (mine === branchGeneration) branchGeneration += 1;
	};
});

$effect(() => {
	const active = open;
	const id = projectAreaId;
	const root = repository;
	const tick = projectTick;
	const retry = commitRetry;
	void tick;
	void retry;
	if (!active) {
		history = null;
		return;
	}
	const identity = `${id}\0${root}`;
	if (identity !== commitIdentity) {
		commitIdentity = identity;
		history = null;
	}
	const mine = ++commitGeneration;
	void getTransport()
		.request("git.listCommits", { projectId: id, repository: root })
		.then((result) => {
			if (mine === commitGeneration && !appStoreApi.getState().removedProjectAreaIds[id]) {
				history = result;
			}
		})
		.catch((cause) => {
			if (mine === commitGeneration && !appStoreApi.getState().removedProjectAreaIds[id]) {
				history = { error: errorText(cause) };
			}
		});
	return () => {
		if (mine === commitGeneration) commitGeneration += 1;
	};
});

function select(next: GitDiffScope): void {
	onSelect(next);
	popover?.hidePopover();
}
</script>

<div class="contents" {@attach mewa(popoverBehavior)}>
	<button
		type="button"
		popovertarget={popoverId}
		aria-label={`Review scope: ${scopeLabel(scope)}`}
		class="btn min-w-0"
		data-variant="ghost"
		data-size="sm"
	>
		<span class="truncate">{scopeLabel(scope)}</span>
		<Icon name="chevron-down" size={12} />
	</button>
	<div
		bind:this={popover}
		id={popoverId}
		popover="auto"
		class="popover w-[min(24rem,calc(100vw-2rem))] p-sm"
		data-align="start"
		ontoggle={(event) => (open = event.newState === "open")}
	>
		<!-- svelte-ignore a11y_autofocus -->
		<button
			type="button"
			autofocus
			onclick={() => select({ kind: "uncommitted" })}
			class="btn mb-sm w-full justify-start"
			data-variant="ghost"
		>Uncommitted changes</button>
		<BranchPicker
			catalog={branches}
			{head}
			initialSelection={scope.kind === "branch" ? scope.baseRef : ""}
			onSelect={select}
			onRetry={() => {
				branches = null;
				branchRetry += 1;
			}}
		/>
		<CommitPicker
			{history}
			initialSelection={scope.kind === "commit" ? scope.sha : scope.kind === "pinned" ? scope.baseRef : ""}
			onSelect={select}
			onRetry={() => {
				history = null;
				commitRetry += 1;
			}}
		/>
	</div>
</div>
