<script lang="ts">
import { normalizeSessionGoal, SESSION_GOAL_MAX_LENGTH } from "@pixie/shared";
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

<span class="session-goal-contents" {@attach mewa(popoverBehavior)}>
	<button
		type="button"
		popovertarget={popoverId}
		data-testid="session-goal-control"
		aria-label={goalState.goal ? "Edit session goal" : "Set session goal"}
		aria-expanded={open}
		disabled={busy}
		onclick={beginEdit}
		class="u-flex u-min-w-0 session-goal-trigger-size u-items-center u-gap-xs u-rounded u-px-sm session-goal-trigger-padding u-text-text-muted tr-text-metadata session-goal-trigger"
	>
		<Icon name="target" size={14} />
		<span class="u-truncate">{label}</span>
		{#if goalState.goal}<Icon name="pencil" size={12} />{/if}
	</button>
	<div
		bind:this={editor}
		id={popoverId}
		popover="auto"
		class="popover session-goal-popover u-p-md"
		data-align="start"
		ontoggle={(event) => (open = event.newState === "open")}
	>
		<form
			data-testid="session-goal-editor"
			onsubmit={(event) => { event.preventDefault(); void save(); }}
			class="u-flex u-flex-col u-gap-sm"
		>
			<div class="u-flex u-items-center u-justify-between u-gap-sm">
				<label for={inputId} class="tr-text-ui u-text-text-default">Session goal</label>
				<span class="u-text-text-muted tr-text-metadata">{draft.length}/{SESSION_GOAL_MAX_LENGTH}</span>
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
				class="input session-goal-textarea"
			></textarea>
			{#if goalState.error}
				<div data-testid="session-goal-error" role="alert" class="u-text-feedback-error tr-text-metadata">{goalState.error}</div>
			{/if}
			<div class="u-flex u-items-center session-goal-justify-end u-gap-sm">
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
		<div class="session-goal-tasks u-flex u-flex-col u-gap-xs u-border-t u-border-border-default">
			<div class="u-flex u-items-center u-justify-between u-gap-sm">
				<div class="tr-text-ui u-text-text-default">Agent tasks</div>
				<span class="u-text-text-muted tr-text-metadata">{agentCanAccessGoal ? "Managed by the agent" : "Agent access unavailable"}</span>
			</div>
			{#each goalState.tasks as task (task.id)}
				<div class="u-flex u-items-center u-gap-xs">
					<span class="u-text-text-muted" title={task.status}>
						<Icon name={task.status === "done" ? "check" : "circle"} size={14} class={task.status === "active" ? "session-goal-active" : ""} />
					</span>
					<span class={`u-min-w-0 u-flex-1 tr-text-metadata ${task.status === "done" ? "u-text-text-muted session-goal-completed" : "u-text-text-default"}`}>{task.text}</span>
				</div>
			{/each}
			{#if goalState.tasks.length === 0}
				<div class="u-text-text-muted tr-text-metadata">
					{agentCanAccessGoal ? "No tasks reported." : `${agentName || "The connected agent"} cannot access or update this goal.`}
				</div>
			{/if}
		</div>
	</div>
</span>

<style>
	.session-goal-contents { display: contents; }
	.session-goal-trigger-size { max-width: min(36vw, 24rem); }
	.session-goal-trigger { border: 0; outline: none; background: transparent; transition: background-color var(--transition-fast), color var(--transition-fast); }
	.session-goal-trigger-padding { padding-block: var(--space-2xs); }
	.session-goal-trigger:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	.session-goal-trigger:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); }
	.session-goal-trigger:disabled { color: var(--text-muted); }
	.session-goal-popover { width: min(90vw, 28rem); }
	.session-goal-textarea { min-height: 5rem; resize: vertical; }
	.session-goal-justify-end { justify-content: end; }
	.session-goal-tasks { margin-block-start: var(--space-md); padding-block-start: var(--space-md); }
	.session-goal-active { color: var(--primary); }
	.session-goal-completed { text-decoration: line-through; }
</style>
