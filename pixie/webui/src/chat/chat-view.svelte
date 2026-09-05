<script lang="ts">
import type {
	AgentMentionInfo,
	AskUserQuestionResult,
	PromptHit,
	QueueLane,
	WsResult,
} from "@pixie/contracts";
import { onDestroy, tick, untrack } from "svelte";
import Button from "../components/button.svelte";
import Dialog from "../components/dialog.svelte";
import { errorText, getTransport, wsErrorCode } from "../connection";
import {
	appStore,
	appStoreApi,
	type ChatLocationRequest,
	EMPTY_RUNTIME,
	selectProjectAreaById,
	toast,
} from "../store";
import {
	agentMentionIdentity,
	fileMentionCandidateIdentity,
	type LoadedAgentMentions,
	type LoadedFileMentionCandidates,
	visibleAgentMentions,
	visibleFileMentionCandidates,
} from "./composer/agent-mention-state";
import Composer from "./composer/composer.svelte";
import type { ComposerHandle, MentionCandidate, SubmitBehavior } from "./composer/composer-state";
import { loadTranscriptUntil, type TranscriptLoadOutcome } from "./history/history-loading";
import HistoryOverlay from "./history/history-overlay.svelte";
import {
	createHistorySearch,
	type HistorySearchState,
	type ScopeKind,
} from "./history/history-search";
import { deriveAskStates, setAskStatesContext } from "./runtime/ask-state";
import {
	messagesToRuntime,
	prependTranscriptPage as prependHydratedTranscriptPage,
} from "./runtime/hydrate";
import { setFoldStateContext } from "./runtime/fold-state";
import { createRowDeriver } from "./runtime/rows";
import type { ChatAttachment, ChatSubmission } from "./runtime/types";
import { setChatActionsContext } from "./session/chat-actions";
import ChatHeader from "./session/chat-header.svelte";
import QueueStrip from "./session/queue-strip.svelte";
import { createSessionCommandSync } from "./session/session-command-sync";
import SessionGoalControl from "./session/session-goal-control.svelte";
import { unsupportedLifecycleReason } from "./session/session-lifecycle";
import SessionLineageControl from "./session/session-lineage-control.svelte";
import SessionConfigControls from "./session/session-config-controls.svelte";
import SessionModelControls from "./session/session-model-controls.svelte";
import SessionPlanControl from "./session/session-plan-control.svelte";
import { streamStatus } from "./session/stream-status";
import { setMcpAppSessionContext } from "./tools/apps/mcp-app-context";
import "./tools/register";
import type { ChatTranscriptHandle } from "./view/chat-transcript.svelte";
import ChatTranscript from "./view/chat-transcript.svelte";
import {
	locateChatRow,
	mentionCandidatesForQuery,
	projectAreaNameMap,
	resolveSendBehavior,
	uniqueRecentPrompts,
} from "./view/chat-view-state";

interface Props {
	sessionId: string;
	projectAreaId: string;
	onOpenChanges?: () => void;
}

type TranscriptLoadState = "idle" | "loading" | "error";
interface TranscriptLoadFlight {
	controller: AbortController;
	promise: Promise<TranscriptLoadOutcome>;
}
interface QueueEdit {
	lane: QueueLane;
	index: number;
	original: string;
	text: string;
	revision: string;
	saving: boolean;
	error: string | null;
}

let { sessionId, projectAreaId, onOpenChanges = () => {} }: Props = $props();
let composer = $state<ComposerHandle>();
let transcriptView = $state<ChatTranscriptHandle>();
let mentionQuery = $state<string | null>(null);
let loadedFileMentions = $state<LoadedFileMentionCandidates<MentionCandidate>>({
	identity: null,
	candidates: [],
});
let loadedAgentMentions = $state<LoadedAgentMentions>({ identity: null, mentions: [] });
let queueEdit = $state<QueueEdit | null>(null);
let queueEditor = $state<HTMLTextAreaElement>();
let queueEditorFocusKey = "";
let queueEditRequest = 0;
let transcriptLoadState = $state<TranscriptLoadState>("idle");
let transcriptFlight: TranscriptLoadFlight | null = null;
let flashRowId = $state<string | null>(null);
let chatLocationLoadRevision = $state(0);
let chatLocationLoad: {
	request: ChatLocationRequest;
	active: boolean;
	cursor: string;
} | null = null;

let runtime = $derived($appStore.sessions[sessionId] ?? EMPTY_RUNTIME);
let connectionGeneration = $derived($appStore.connectionGeneration);
let agentProfile = $derived($appStore.agentProfile);
let piAgent = $derived(agentProfile?.pi === true);
let isStreaming = $derived(runtime.isStreaming);
let canPromptImage = $derived(agentProfile ? agentProfile.operations.promptImage : null);
let canPromptEmbeddedContext = $derived(
	agentProfile ? agentProfile.operations.promptEmbeddedContext : null,
);
let canSteer = $derived(agentProfile?.operations.steer === true);
let canDelete = $derived(agentProfile?.operations.deleteSession === true);
let canUseHttpMcp = $derived((runtime.capabilities?.mcp ?? agentProfile?.capabilities?.mcp) === 1);
let deleteUnavailableReason = $derived(
	canDelete ? undefined : unsupportedLifecycleReason(agentProfile?.name, "deleting"),
);
let projectId = $derived(
	Object.values($appStore.projectAreas)
		.flat()
		.find((area) => area.id === projectAreaId)?.projectId,
);
let projectAreaRoot = $derived(selectProjectAreaById($appStore, projectAreaId)?.root ?? undefined);
let projectAreaNames = $derived(projectAreaNameMap($appStore.projectAreas));
let parentDeleted = $derived(
	runtime.parentSessionId !== undefined &&
		$appStore.deletedSessionsByProjectArea[projectAreaId]?.[runtime.parentSessionId] === true,
);
const deriveRows = createRowDeriver();
let rows = $derived(deriveRows(runtime.turns, runtime.toolResults, runtime.isStreaming));
let recentPrompts = $derived(uniqueRecentPrompts(runtime.turns));
let projectionId = $derived(runtime.transcript?.projectionId ?? null);
let transcriptStart = $derived(runtime.transcript?.start ?? 0);
let conversationKey = $derived(projectionId ?? `live:${sessionId}`);
let currentStreamStatus = $derived(
	runtime.isStreaming && runtime.turns.at(-1)?.kind !== "retry"
		? streamStatus(runtime.turns, runtime.currentAssistantId)
		: null,
);
let fileMentionIdentity = $derived(
	fileMentionCandidateIdentity(projectAreaId, projectAreaRoot, sessionId, mentionQuery),
);
let fileMentions = $derived(visibleFileMentionCandidates(loadedFileMentions, fileMentionIdentity));
let agentIdentity = $derived(agentMentionIdentity(projectId, sessionId));
let agentMentions = $derived(visibleAgentMentions(loadedAgentMentions, agentIdentity));
let mentionCandidates = $derived(
	mentionCandidatesForQuery(mentionQuery, agentMentions, fileMentions),
);
let queueEditStale = $derived(
	queueEdit !== null && !queueEdit.saving && queueEdit.revision !== runtime.queue.revision,
);
let askStates = $derived(deriveAskStates(runtime.turns, runtime.askAnswers));
function currentHistoryContext() {
	return {
		sessionId,
		projectAreaId,
		...(projectId === undefined ? {} : { projectId }),
	};
}

const focusScope = {};
const history = createHistorySearch(untrack(currentHistoryContext));
let historyState = $state<HistorySearchState>(history.getState());
const unsubscribeHistory = history.subscribe((next) => (historyState = next));
const commandSync = createSessionCommandSync(untrack(() => ({ sessionId, projectAreaId })));

setFoldStateContext(() => runtime.disclosures);
setAskStatesContext({ stateFor: (toolCallId) => askStates[toolCallId], focusScope });
setChatActionsContext({
	answerQuestion: async (toolCallId: string, result: AskUserQuestionResult) => {
		try {
			await getTransport().request("session.questionReply", { sessionId, toolCallId, result });
			appStoreApi.getState().setAskAnswer(sessionId, toolCallId, result);
		} catch (cause) {
			toast.error(errorText(cause), "Couldn't send the answer");
			throw cause;
		}
	},
	focusComposer: () => composer?.refocus(),
});
setMcpAppSessionContext({
	get projectId() {
		return projectId;
	},
	get sessionId() {
		return sessionId;
	},
});

onDestroy(() => {
	transcriptFlight?.controller.abort();
	transcriptFlight = null;
	unsubscribeHistory();
	history.destroy();
	commandSync.destroy();
});

$effect(() => {
	history.setContext(currentHistoryContext());
	commandSync.setContext({ sessionId, projectAreaId });
});

$effect(() => {
	const generation = connectionGeneration;
	const projection = projectionId;
	const activeSessionId = sessionId;
	void generation;
	void projection;
	void activeSessionId;
	transcriptLoadState = "idle";
	return () => {
		const flight = transcriptFlight;
		if (!flight) return;
		transcriptFlight = null;
		flight.controller.abort();
	};
});

$effect(() => {
	const id = sessionId;
	const streaming = isStreaming;
	void streaming;
	void getTransport()
		.request("session.getStats", { sessionId: id })
		.then((stats) => appStoreApi.getState().setStats(id, stats))
		.catch(() => {});
});

$effect(() => {
	const identity = agentIdentity;
	const id = sessionId;
	const activeProjectId = projectId;
	loadedAgentMentions = { identity, mentions: [] };
	if (
		!piAgent ||
		(runtime.capabilities?.agents ?? $appStore.agentProfile?.capabilities?.agents) !== 1 ||
		!activeProjectId ||
		!identity
	)
		return;
	let cancelled = false;
	void getTransport()
		.request("session.getAgentMentions", { projectId: activeProjectId, sessionId: id })
		.then((mentions: AgentMentionInfo[]) => {
			if (!cancelled) loadedAgentMentions = { identity, mentions };
		})
		.catch(() => {
			if (!cancelled) loadedAgentMentions = { identity, mentions: [] };
		});
	return () => {
		cancelled = true;
	};
});

$effect(() => {
	const query = mentionQuery;
	const identity = fileMentionIdentity;
	loadedFileMentions = { identity, candidates: [] };
	if (query === null || !identity) return;
	const slash = query.lastIndexOf("/");
	const dir = slash >= 0 ? query.slice(0, slash) : "";
	const prefix = (slash >= 0 ? query.slice(slash + 1) : query).toLocaleLowerCase();
	let cancelled = false;
	const timer = setTimeout(() => {
		void getTransport()
			.request("fs.readDir", { projectId: projectAreaId, path: dir })
			.then(({ nodes }) => {
				if (cancelled) return;
				loadedFileMentions = {
					identity,
					candidates: nodes
						.filter((node) => node.name.toLocaleLowerCase().startsWith(prefix))
						.slice(0, 12)
						.map((node) => ({ path: node.path, name: node.name, kind: node.kind })),
				};
			})
			.catch(() => {
				if (!cancelled) loadedFileMentions = { identity, candidates: [] };
			});
	}, 120);
	return () => {
		cancelled = true;
		clearTimeout(timer);
	};
});

$effect(() => {
	const request = $appStore.historyOpenRequest;
	const open = historyState.open;
	if (request?.sessionId !== sessionId) return;
	if (appStoreApi.getState().historyOpenRequest !== request) return;
	appStoreApi.getState().clearHistoryOpen();
	if (open) history.cycleScope();
	else composer?.openHistory();
});

$effect(() => {
	const rowId = flashRowId;
	if (rowId === null) return;
	const timer = setTimeout(() => {
		if (flashRowId === rowId) flashRowId = null;
	}, 1600);
	return () => clearTimeout(timer);
});

$effect(() => {
	const key = queueEdit ? `${queueEdit.lane}:${queueEdit.index}:${queueEdit.revision}` : "";
	const editor = queueEditor;
	if (!key) {
		queueEditorFocusKey = "";
		return;
	}
	if (!editor || key === queueEditorFocusKey) return;
	queueEditorFocusKey = key;
	queueMicrotask(() => {
		if (editor.isConnected) editor.focus();
	});
});

function loadEarlierMessages(): Promise<TranscriptLoadOutcome> {
	if (transcriptFlight) return transcriptFlight.promise;
	const state = appStoreApi.getState();
	const transcript = state.sessions[sessionId]?.transcript;
	if (!transcript || transcript.start <= 0) return Promise.resolve("exhausted");
	if (state.status !== "connected") {
		transcriptLoadState = "error";
		return Promise.resolve("failed");
	}

	const generation = state.connectionGeneration;
	const before = transcript.start;
	const expectedProjectionId = transcript.projectionId;
	const controller = new AbortController();
	const anchor = transcriptView?.beginPrepend() ?? null;
	const flight: TranscriptLoadFlight = {
		controller,
		promise: Promise.resolve("ignored"),
	};
	const isCurrent = () => transcriptFlight === flight;
	const isSameRuntime = () => {
		const current = appStoreApi.getState();
		const currentTranscript = current.sessions[sessionId]?.transcript;
		return (
			current.status === "connected" &&
			current.connectionGeneration === generation &&
			currentTranscript?.projectionId === expectedProjectionId &&
			currentTranscript.start === before
		);
	};
	transcriptLoadState = "loading";
	flight.promise = (async (): Promise<TranscriptLoadOutcome> => {
		let restoreAnchor = false;
		let resetToBottom = false;
		try {
			let response: WsResult<"session.getMessages">;
			try {
				response = await getTransport().request(
					"session.getMessages",
					{
						sessionId,
						projectId: projectAreaId,
						before: { projectionId: expectedProjectionId, index: before },
					},
					{ signal: controller.signal },
				);
			} catch (cause) {
				if (wsErrorCode(cause) !== "STALE_TRANSCRIPT_PROJECTION") throw cause;
				if (!isSameRuntime()) return "ignored";
				const snapshot = await getTransport().request(
					"session.getMessages",
					{ sessionId, projectId: projectAreaId },
					{ signal: controller.signal },
				);
				if (snapshot.kind !== "snapshot") throw new Error("invalid chat snapshot");
				if (!isSameRuntime()) return "ignored";
				const hydrated = messagesToRuntime(snapshot.messages, {
					lastSettlement: snapshot.summary.lastSettlement,
					pendingTools: snapshot.pendingTools,
					page: snapshot.page,
					isStreaming: snapshot.summary.isStreaming,
				});
				appStoreApi
					.getState()
					.replaceTranscriptSnapshot(sessionId, snapshot.summary, hydrated, snapshot.planState);
				appStoreApi.getState().setCommands(sessionId, snapshot.commands);
				if (isCurrent()) transcriptLoadState = "idle";
				resetToBottom = true;
				return "reloaded";
			}

			if (response.kind !== "page") throw new Error("invalid transcript page");
			if (!isSameRuntime()) return "ignored";
			const hydrated = messagesToRuntime(response.messages, { page: response.page });
			const currentRuntime = appStoreApi.getState().sessions[sessionId];
			if (!currentRuntime || !prependHydratedTranscriptPage(currentRuntime, hydrated)) {
				return "ignored";
			}
			const applied = appStoreApi.getState().prependTranscriptPage(sessionId, hydrated);
			if (!applied) return "ignored";
			restoreAnchor = true;
			if (isCurrent()) transcriptLoadState = "idle";
			return "loaded";
		} catch (cause) {
			if ((cause as { name?: string }).name === "AbortError") return "ignored";
			if (isCurrent()) transcriptLoadState = "error";
			return "failed";
		} finally {
			if (isCurrent()) transcriptFlight = null;
			await transcriptView?.finishPrepend(anchor, restoreAnchor);
			if (resetToBottom) transcriptView?.scrollToBottom("auto");
		}
	})();
	transcriptFlight = flight;
	return flight.promise;
}

async function performSend(
	targetSession: string,
	text: string,
	attachments: ChatAttachment[],
	behavior: Exclude<SubmitBehavior, "interrupt">,
): Promise<boolean> {
	const { effectiveBehavior, heldByQueue } = resolveSendBehavior(
		behavior,
		canSteer,
		(appStoreApi.getState().sessions[targetSession]?.queue.followUp.length ?? 0) > 0,
	);
	const images = attachments.flatMap((attachment) =>
		attachment.kind === "image" ? [attachment.content] : [],
	);
	const resources = attachments.flatMap((attachment) =>
		attachment.kind === "text" ? [attachment.content] : [],
	);
	if (canPromptImage === false && images.length > 0) {
		toast.error(`${agentProfile?.name || "The connected agent"} does not support image prompts.`);
		return false;
	}
	if (canPromptEmbeddedContext === false && resources.length > 0) {
		toast.error(
			`${agentProfile?.name || "The connected agent"} does not support text resource prompts.`,
		);
		return false;
	}
	if (effectiveBehavior === "queue" && attachments.length > 0) {
		toast.error(
			heldByQueue
				? "Resolve the queued follow-ups before sending images."
				: canSteer
					? "Send or steer image attachments directly."
					: "Wait for the current response, then send image attachments directly.",
			"Queued messages are text-only",
		);
		return false;
	}
	if (heldByQueue) toast.info("Queued behind the existing follow-ups.", "Message queued");
	if (effectiveBehavior === "send" && (text || attachments.length > 0)) {
		appStoreApi.getState().appendUserMessage(targetSession, text, attachments);
		const current = appStoreApi.getState().sessions[targetSession];
		if (current?.submission)
			appStoreApi.getState().setSubmission(targetSession, {
				...current.submission,
				optimisticTurnId: current.turns.at(-1)?.id ?? "",
			});
	}
	const params = {
		sessionId: targetSession,
		text,
		...(images.length > 0 ? { images } : {}),
		...(resources.length > 0 ? { resources } : {}),
	};
	const method =
		effectiveBehavior === "steer"
			? "session.steer"
			: effectiveBehavior === "queue"
				? "session.queueAdd"
				: "session.prompt";
	await getTransport().request(method, params);
	return true;
}

function submit(text: string, attachments: ChatAttachment[], behavior: SubmitBehavior): boolean {
	const targetSession = sessionId;
	if (appStoreApi.getState().sessions[targetSession]?.submission) return false;
	const submission: ChatSubmission = { text, attachments, behavior, busy: true };
	appStoreApi.getState().setSubmission(targetSession, submission);
	void (async () => {
		try {
			if (behavior === "interrupt")
				await getTransport().request("session.abort", { sessionId: targetSession });
			const accepted = await performSend(
				targetSession,
				text,
				attachments,
				behavior === "interrupt" ? "send" : behavior,
			);
			if (!accepted)
				throw new Error("Message was not sent. Its text and attachments are retained below.");
			appStoreApi.getState().setSubmission(targetSession, null);
		} catch (cause) {
			const current = appStoreApi.getState().sessions[targetSession]?.submission ?? submission;
			appStoreApi
				.getState()
				.setSubmission(targetSession, { ...current, busy: false, error: errorText(cause) });
		}
	})();
	return true;
}

function retrySubmission(): void {
	const pending = runtime.submission;
	if (!pending || pending.busy) return;
	appStoreApi.getState().setSubmission(sessionId, null);
	submit(pending.text, pending.attachments, pending.behavior);
}

let stopError = $state<string | null>(null);
let stopping = $state(false);
async function stop(): Promise<void> {
	if (stopping) return;
	stopping = true;
	stopError = null;
	try {
		await getTransport().request("session.abort", { sessionId });
	} catch (cause) {
		stopError = errorText(cause);
	} finally {
		stopping = false;
	}
}

function editQueuedMessage(lane: QueueLane, index: number): void {
	const current = runtime.queue[lane][index];
	const revision = runtime.queue.revision;
	if (!current || !revision) return;
	queueEditRequest += 1;
	queueEdit = {
		lane,
		index,
		original: current,
		text: current,
		revision,
		saving: false,
		error: null,
	};
}

function dismissQueueEdit(): void {
	queueEditRequest += 1;
	queueEdit = null;
}

function saveQueuedMessage(): void {
	const edit = queueEdit;
	if (!edit || edit.saving || queueEditStale) return;
	const text = edit.text.trim();
	if (!text || text === edit.original) {
		dismissQueueEdit();
		return;
	}
	const request = ++queueEditRequest;
	queueEdit = { ...edit, saving: true, error: null };
	void getTransport()
		.request("session.queueEdit", {
			sessionId,
			lane: edit.lane,
			index: edit.index,
			text,
			revision: edit.revision,
		})
		.then(() => {
			if (queueEditRequest === request) queueEdit = null;
		})
		.catch((cause) => {
			if (queueEditRequest === request && queueEdit) {
				queueEdit = { ...queueEdit, saving: false, error: errorText(cause) };
			}
		});
}

function removeQueuedMessage(lane: QueueLane, index: number): void {
	const revision = runtime.queue.revision;
	if (!revision) return;
	void getTransport()
		.request("session.queueRemove", { sessionId, lane, index, revision })
		.catch((cause) => toast.error(errorText(cause), "Couldn't remove queued message"));
}

function retryQueuedMessage(lane: QueueLane, index: number): void {
	const revision = runtime.queue.revision;
	if (!revision) return;
	void getTransport()
		.request("session.queueRetry", { sessionId, lane, index, revision })
		.catch((cause) => toast.error(errorText(cause), "Couldn't retry queued message"));
}

function dismissHistory(): void {
	history.close();
	composer?.refocus();
}

function insertHistoryHit(hit: PromptHit): void {
	composer?.insertText(hit.text);
	history.close();
}

function insertAndSendHistoryHit(hit: PromptHit): void {
	composer?.insertAndSubmit(
		hit.text,
		runtime.isStreaming ? (canSteer ? "steer" : "queue") : "send",
	);
	history.close();
}

async function deleteHistoryChat(targetProjectAreaId: string, targetSessionId: string) {
	if (!canDelete) return;
	try {
		await getTransport().request("session.delete", {
			projectId: targetProjectAreaId,
			sessionId: targetSessionId,
		});
		history.close();
		appStoreApi.getState().deleteChat(targetProjectAreaId, targetSessionId);
	} catch (cause) {
		toast.error(errorText(cause), "Couldn't delete the chat");
	}
}

function revealLocation(request: ChatLocationRequest, rowId: string): void {
	void tick().then(() => {
		if (appStoreApi.getState().chatLocationRequest !== request) return;
		if (!transcriptView?.scrollToRow(rowId)) {
			toast.error("couldn't locate the message — the session may have changed");
			appStoreApi.getState().clearChatLocation();
			return;
		}
		flashRowId = rowId;
		appStoreApi.getState().clearChatLocation();
	});
}

$effect(() => {
	const request = $appStore.chatLocationRequest;
	const revision = chatLocationLoadRevision;
	const transcript = runtime.transcript;
	const currentRows = rows;
	void revision;
	if (
		!request ||
		request.projectAreaId !== projectAreaId ||
		request.sessionId !== sessionId ||
		appStoreApi.getState().chatLocationRequest !== request
	) {
		return;
	}
	if (transcript && request.messageIndex < transcript.start) {
		const cursor = `${transcript.projectionId}:${transcript.start}`;
		if (
			chatLocationLoad?.request === request &&
			(chatLocationLoad.active || chatLocationLoad.cursor === cursor)
		) {
			return;
		}
		const load = { request, active: true, cursor };
		chatLocationLoad = load;
		void loadTranscriptUntil(
			request.messageIndex,
			() => appStoreApi.getState().sessions[sessionId]?.transcript,
			loadEarlierMessages,
			() => appStoreApi.getState().chatLocationRequest === request,
		).finally(() => {
			if (chatLocationLoad !== load) return;
			const latest = appStoreApi.getState().sessions[sessionId]?.transcript;
			chatLocationLoad = {
				...load,
				active: false,
				cursor: latest ? `${latest.projectionId}:${latest.start}` : "",
			};
			chatLocationLoadRevision += 1;
		});
		return;
	}
	const row = locateChatRow(
		request.messageIndex,
		request.anchorText,
		runtime.turnIdByMessageIndex,
		runtime.turns,
		currentRows,
	);
	if (!row) {
		toast.error("couldn't locate the message — the session may have changed");
		appStoreApi.getState().clearChatLocation();
		return;
	}
	revealLocation(request, row.id);
});

function openChanges(path: string): void {
	appStoreApi.getState().requestChangesView(projectAreaId, path);
	onOpenChanges();
}
</script>

{#snippet HeaderLeft()}
	<div class="flex min-w-0 flex-wrap items-center gap-xs">
		{#if piAgent}
			<SessionModelControls
				sessionId={sessionId}
				model={runtime.model}
				thinkingLevel={runtime.thinkingLevel}
				isStreaming={runtime.isStreaming}
			/>
		{/if}
		<SessionLineageControl
			projectAreaId={projectAreaId}
			parentSessionId={runtime.parentSessionId}
			{parentDeleted}
		/>
		{#if (runtime.capabilities?.mcp ?? agentProfile?.capabilities?.mcp) === 1}
		<SessionGoalControl
			projectAreaId={projectAreaId}
			{sessionId}
			agentCanAccessGoal={canUseHttpMcp}
			agentName={agentProfile?.name}
		/>
		{/if}
		<SessionPlanControl planState={runtime.planState} />
  {#if !piAgent && runtime.configOptions.length}
   <SessionConfigControls {sessionId} options={runtime.configOptions} />
  {/if}

	</div>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-container-project-bg">

	<ChatTranscript
		bind:this={transcriptView}
		{rows}
		{conversationKey}
		{projectAreaRoot}
		{flashRowId}
		status={currentStreamStatus}
		{transcriptStart}
		loadState={transcriptLoadState}
		onLoadEarlier={() => void loadEarlierMessages()}
		onOpenChange={openChanges}
	/>

	<div class="relative shrink-0">
		<HistoryOverlay
			state={historyState}
			{projectAreaNames}
			onQueryChange={(query) => history.setQuery(query)}
			onSetScope={(scope: ScopeKind) => history.setScope(scope)}
			onToggleStage={() => history.toggleStage()}
			onMoveSelection={(delta) => history.moveSelection(delta)}
			onClose={dismissHistory}
			onInsert={insertHistoryHit}
			onInsertAndSend={insertAndSendHistoryHit}
			onOpenMessage={(request) => history.openMessage(request)}
			onDeleteChat={(targetProjectAreaId, targetSessionId) =>
				void deleteHistoryChat(targetProjectAreaId, targetSessionId)}
			{deleteUnavailableReason}
		/>
		{#if runtime.activity}<p role="status" class="px-md py-xs text-text-muted tr-text-ui">{runtime.activity}</p>{/if}
		<QueueStrip
			queue={runtime.queue}
			onEdit={editQueuedMessage}
			onRemove={removeQueuedMessage}
			onRetry={retryQueuedMessage}
		/>
		{#if queueEdit}
			{@const activeQueueEdit = queueEdit}
			<Dialog
				open
				title="Edit queued message"
				description={runtime.queue.blocked?.lane === activeQueueEdit.lane &&
				runtime.queue.blocked.index === activeQueueEdit.index
					? "This may already be in the transcript. Editing changes the text used if you retry it."
					: "Update the text that the agent will receive later."}
				onOpenChange={(open) => {
					if (!open) dismissQueueEdit();
				}}
			>
				{#snippet actions()}
					<Button variant="ghost" onclick={dismissQueueEdit}>Cancel</Button>
					<Button
						disabled={!activeQueueEdit.text.trim() || activeQueueEdit.saving || queueEditStale}
						onclick={saveQueuedMessage}
					>
						{activeQueueEdit.saving ? "Saving…" : "Save"}
					</Button>
				{/snippet}
				<textarea
					bind:this={queueEditor}
					aria-label="Queued message"
					value={activeQueueEdit.text}
					disabled={activeQueueEdit.saving}
					oninput={(event) => {
						if (queueEdit) queueEdit = { ...queueEdit, text: event.currentTarget.value };
					}}
					class="input min-h-28 w-full resize-y"
				></textarea>
				{#if queueEditStale || activeQueueEdit.error}
					<p role="alert" class="text-feedback-warning tr-text-ui">
						{queueEditStale
							? "The queue changed while you were editing. Copy your draft, then reopen the message to edit its current version."
							: `${activeQueueEdit.error} Your draft is still here.`}
					</p>
				{/if}
			</Dialog>
		{/if}
  {#if stopError}<p role="alert" class="px-md py-xs text-feedback-error tr-text-ui">Couldn't stop the response: {stopError}</p>{/if}
  {#if runtime.submission}
   {@const pending = runtime.submission}
   <div class="mx-sm my-xs max-h-[min(40dvh,20rem)] overflow-y-auto rounded border border-border-default p-sm tr-text-ui" role="status">
    {#if pending.busy}<p>Sending message…</p>{:else}
     <p class="text-feedback-error">{pending.error}</p>
     <p class="text-text-muted">Check the transcript before retrying: a lost connection can leave delivery uncertain.</p>
     <textarea aria-label="Retained message" class="input mt-xs w-full" value={pending.text} oninput={(event) => appStoreApi.getState().setSubmission(sessionId, { ...pending, text: event.currentTarget.value })}></textarea>
     {#if pending.attachments.length}<p class="break-words">Attachments: {pending.attachments.map((attachment) => attachment.name).join(", ")}</p>{/if}
     <div class="mt-xs flex flex-wrap gap-xs">
      <Button onclick={retrySubmission}>Retry message</Button>
      <Button variant="ghost" onclick={() => appStoreApi.getState().setSubmission(sessionId, null)}>Discard retained message</Button>
     </div>
    {/if}
   </div>
  {/if}
		<Composer
			bind:this={composer}
			value={runtime.draft}
			onChange={(value) => appStoreApi.getState().setChatDraft(sessionId, value)}
			isStreaming={runtime.isStreaming}
			commands={runtime.commands}
			{mentionCandidates}
			{recentPrompts}
			onMentionQuery={(query) => (mentionQuery = query)}
			onSubmit={submit}
			onAbort={() => void stop()}
			onHistoryOpen={() => history.openOverlay(runtime.draft)}
			supportsImages={canPromptImage}
			supportsTextResources={canPromptEmbeddedContext}
			supportsSteer={canSteer}
		/>
		<ChatHeader stats={runtime.stats} left={HeaderLeft} />
	</div>
</div>
