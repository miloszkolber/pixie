<script lang="ts">
import { normalizeSessionGoal, SESSION_GOAL_MAX_LENGTH } from "@pixie/contracts";
import { onMount } from "svelte";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as popoverBehavior } from "../../../vendor/mewa-ui/components/popover.js";
import Button from "../../components/button.svelte";
import Icon from "../../components/icon.svelte";
import { errorText, getTransport } from "../../connection";
import { appStore, appStoreApi, type SessionGoalRuntime } from "../../store";

interface Props {
	projectAreaId: string;
	sessionId: string;
	agentCanAccessGoal?: boolean;
	agentName?: string | undefined;
}

const EMPTY_GOAL: SessionGoalRuntime = {
	projectAreaId: null,
	status: "idle",
	goal: null,
	tasks: [],
	updatedAt: null,
	error: null,
};

let { projectAreaId, sessionId, agentCanAccessGoal = true, agentName }: Props = $props();
let open = $state(false);
let draft = $state("");
let editor = $state<HTMLElement>();
let requestGeneration = 0;
const componentId = $props.id();
const inputId = `session-goal-${componentId}`;
const popoverId = `session-goal-editor-${componentId}`;
let goalState = $derived($appStore.sessions[sessionId]?.goal ?? EMPTY_GOAL);
let busy = $derived(goalState.status === "loading" || goalState.status === "saving");
let label = $derived(
	goalState.status === "loading"
		? "Loading goal…"
		: goalState.goal
			? `Goal: ${goalState.goal.replace(/\s+/g, " ")}`
			: "Set goal",
);

function load(id = sessionId, areaId = projectAreaId): void {
	const generation = ++requestGeneration;
	const goalRevision = appStoreApi.getState().sessions[id]?.goalRevision ?? 0;
	appStoreApi.getState().setSessionGoalLoading(id, areaId);
	void getTransport()
		.request("session.goalGet", { projectId: areaId, sessionId: id })
		.then((value) => {
			if (generation !== requestGeneration) return;
			appStoreApi.getState().setSessionGoal(id, value, goalRevision);
			draft = value.goal ?? "";
		})
		.catch((cause) => {
			if (generation !== requestGeneration) return;
			appStoreApi.getState().setSessionGoalError(id, areaId, errorText(cause));
		});
}

onMount(() => {
	const id = sessionId;
	const areaId = projectAreaId;
	const scheduledGeneration = requestGeneration;
	open = false;
	draft = "";
	queueMicrotask(() => {
		if (scheduledGeneration !== requestGeneration) return;
		load(id, areaId);
	});
	return () => {
		requestGeneration += 1;
	};
});

$effect(() => {
	if (!open && editor?.matches(":popover-open")) editor.hidePopover();
});

async function save(): Promise<void> {
	let goal: string;
	try {
		goal = normalizeSessionGoal(draft);
	} catch (cause) {
		appStoreApi.getState().setSessionGoalError(sessionId, projectAreaId, errorText(cause));
		return;
	}
	const generation = ++requestGeneration;
	const goalRevision = appStoreApi.getState().sessions[sessionId]?.goalRevision ?? 0;
	appStoreApi.getState().setSessionGoalSaving(sessionId, projectAreaId);
	try {
		const value = await getTransport().request("session.goalSet", {
			projectId: projectAreaId,
			sessionId,
			goal,
		});
		if (generation !== requestGeneration) return;
		appStoreApi.getState().setSessionGoal(sessionId, value, goalRevision);
		draft = value.goal ?? "";
		open = false;
	} catch (cause) {
		if (generation !== requestGeneration) return;
		appStoreApi.getState().setSessionGoalError(sessionId, projectAreaId, errorText(cause));
	}
}

async function clear(): Promise<void> {
	const generation = ++requestGeneration;
	const goalRevision = appStoreApi.getState().sessions[sessionId]?.goalRevision ?? 0;
	appStoreApi.getState().setSessionGoalSaving(sessionId, projectAreaId);
	try {
		const value = await getTransport().request("session.goalClear", {
			projectId: projectAreaId,
			sessionId,
		});
		if (generation !== requestGeneration) return;
		appStoreApi.getState().setSessionGoal(sessionId, value, goalRevision);
		draft = "";
		open = false;
	} catch (cause) {
		if (generation !== requestGeneration) return;
		appStoreApi.getState().setSessionGoalError(sessionId, projectAreaId, errorText(cause));
	}
}

function beginEdit(): void {
	draft = goalState.goal ?? "";
}
</script>

<span class="contents" {@attach mewa(popoverBehavior)}>
	<button
		type="button"
		popovertarget={popoverId}
		data-testid="session-goal-control"
		aria-label={goalState.goal ? "Edit session goal" : "Set session goal"}
		aria-expanded={open}
		disabled={busy}
		onclick={beginEdit}
		class="flex min-w-0 max-w-[min(36vw,24rem)] items-center gap-xs rounded-[var(--radius-sm)] px-sm py-0.5 text-text-muted tr-text-metadata outline-none transition-colors hover:bg-control-bg-hovered hover:text-text-default focus-visible:ring-2 focus-visible:ring-primary disabled:text-text-muted"
	>
		<Icon name="target" size={14} />
		<span class="truncate">{label}</span>
		{#if goalState.goal}<Icon name="pencil" size={12} />{/if}
	</button>
	<div
		bind:this={editor}
		id={popoverId}
		popover="auto"
		class="popover w-[min(90vw,28rem)] p-md"
		data-align="start"
		ontoggle={(event) => (open = event.newState === "open")}
	>
		<form
			data-testid="session-goal-editor"
			onsubmit={(event) => { event.preventDefault(); void save(); }}
			class="flex flex-col gap-sm"
		>
			<div class="flex items-center justify-between gap-sm">
				<label for={inputId} class="tr-text-ui text-text-default">Session goal</label>
				<span class="text-text-muted tr-text-metadata">{draft.length}/{SESSION_GOAL_MAX_LENGTH}</span>
			</div>
			<!-- svelte-ignore a11y_autofocus -->
			<textarea
				id={inputId}
				autofocus
				data-testid="session-goal-input"
				value={draft}
				oninput={(event) => (draft = event.currentTarget.value)}
				placeholder="What should this session accomplish?"
				maxlength={SESSION_GOAL_MAX_LENGTH + 1}
				rows={3}
				disabled={busy}
				class="input min-h-20 resize-y"
			></textarea>
			{#if goalState.error}
				<div data-testid="session-goal-error" role="alert" class="text-feedback-error tr-text-metadata">{goalState.error}</div>
			{/if}
			<div class="flex items-center justify-end gap-sm">
				{#if goalState.goal}
					<Button variant="ghost" size="sm" data-testid="session-goal-clear" disabled={busy} onclick={() => void clear()}>
						<Icon name="trash-2" size={14} /> Clear
					</Button>
				{/if}
				{#if goalState.status === "error" && !goalState.goal}
					<Button variant="ghost" size="sm" disabled={busy} onclick={() => load()}>Retry</Button>
				{/if}
				<Button type="submit" size="sm" data-testid="session-goal-save" disabled={busy || !draft.trim()}>Save</Button>
			</div>
		</form>
		<div class="mt-md flex flex-col gap-xs border-border-default border-t pt-md">
			<div class="flex items-center justify-between gap-sm">
				<div class="tr-text-ui text-text-default">Agent tasks</div>
				<span class="text-text-muted tr-text-metadata">{agentCanAccessGoal ? "Managed by the agent" : "Agent access unavailable"}</span>
			</div>
			{#each goalState.tasks as task (task.id)}
				<div class="flex items-center gap-xs">
					<span class="text-text-muted" title={task.status}>
						<Icon name={task.status === "done" ? "check" : "circle"} size={14} class={task.status === "active" ? "text-primary" : ""} />
					</span>
					<span class={`min-w-0 flex-1 tr-text-metadata ${task.status === "done" ? "text-text-muted line-through" : "text-text-default"}`}>{task.text}</span>
				</div>
			{/each}
			{#if goalState.tasks.length === 0}
				<div class="text-text-muted tr-text-metadata">
					{agentCanAccessGoal ? "No tasks reported." : `${agentName || "The connected agent"} cannot access or update this goal.`}
				</div>
			{/if}
		</div>
	</div>
</span>
