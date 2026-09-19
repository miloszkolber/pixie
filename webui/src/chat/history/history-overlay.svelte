<script lang="ts">
import type { HistoryScope, MessageHit, PromptHit } from "@pixie/shared";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as dropdownBehavior } from "../../../vendor/mewa-ui/components/dropdown-menu.js";
import Icon from "../../components/icon.svelte";
import { activateCheckableMenuItem } from "../../components/menu-keyboard";
import { relativeTime } from "../../lib";
import {
	type ChatLocationRequest,
	type HistorySearchState,
	highlightHistoryText,
	historyOptionKey,
	historySelectionAnnouncement,
	jumpTarget,
	resolveHistorySelection,
	SCOPE_ORDER,
} from "./history-search";

const SCOPE_LABELS: Record<HistoryScope["kind"], string> = {
	chat: "Chat",
	project: "Project",
	all: "All",
};
const SCOPE_MENU_LABELS: Record<HistoryScope["kind"], string> = {
	chat: "This chat",
	project: "Project",
	all: "Everywhere",
};

export interface HistoryOverlayProps {
	state: HistorySearchState;
	projectAreaNames: Record<string, string>;
	onQueryChange: (query: string) => void;
	onToggleStage: () => void;
	onMoveSelection: (delta: number) => void;
	onClose: () => void;
	onInsert: (hit: PromptHit) => void;
	onInsertAndSend: (hit: PromptHit) => void;
	onOpenMessage: (target: ChatLocationRequest) => void;
	onDeleteChat: (projectAreaId: string, sessionId: string) => void;
	deleteUnavailableReason?: string | undefined;
	onSetScope: (kind: HistoryScope["kind"]) => void;
}

let {
	state: historyState,
	projectAreaNames,
	onQueryChange,
	onToggleStage,
	onMoveSelection,
	onClose,
	onInsert,
	onInsertAndSend,
	onOpenMessage,
	onDeleteChat,
	deleteUnavailableReason,
	onSetScope,
}: HistoryOverlayProps = $props();

let input = $state<HTMLInputElement>();
let resultsElement = $state<HTMLDivElement>();
let scopeMenu = $state<HTMLElement>();
let scopeMenuOpen = $state(false);
let focusedOpen = false;
const componentId = $props.id();
const resultsId = `history-results-${componentId}`;
const selectedStatusId = `${resultsId}-selection`;
const scopeMenuId = `history-scope-${componentId}`;

let selectedItem = $derived(
	resolveHistorySelection(historyState.stage, historyState.result, historyState.selected),
);
let selectedKey = $derived(selectedItem ? historyOptionKey(selectedItem) : null);
let selectedProjectAreaName = $derived(
	selectedItem?.hit.projectId ? projectAreaNames[selectedItem.hit.projectId] : undefined,
);
let promptCount = $derived(
	historyState.result
		? Math.min(historyState.result.prompts.length, historyState.result.promptTotal)
		: 0,
);
let messageCount = $derived(
	historyState.result
		? Math.min(historyState.result.messages.length, historyState.result.messageTotal)
		: 0,
);
let hasResults = $derived(
	!!historyState.result &&
		(historyState.result.prompts.length > 0 ||
			(historyState.stage === "zoomed" && historyState.result.messages.length > 0)),
);
let isEmpty = $derived(!!historyState.result && !historyState.result.indexing && !hasResults);
let selectedAnnouncement = $derived(
	historySelectionAnnouncement(historyState.stage, historyState.result, historyState.selected),
);

$effect(() => {
	if (!historyState.open) {
		focusedOpen = false;
		return;
	}
	if (focusedOpen || !input) return;
	focusedOpen = true;
	queueMicrotask(() => {
		if (!input?.isConnected) return;
		input.focus();
		input.select();
	});
});

$effect(() => {
	if (!historyState.open) return;
	const handleEscape = (event: KeyboardEvent) => {
		if (event.key !== "Escape" || scopeMenuOpen) return;
		event.preventDefault();
		event.stopPropagation();
		onClose();
	};
	window.addEventListener("keydown", handleEscape, true);
	return () => window.removeEventListener("keydown", handleEscape, true);
});

$effect(() => {
	void selectedKey;
	if (!resultsElement) return;
	resultsElement.querySelector('[data-selected="true"]')?.scrollIntoView({ block: "nearest" });
});

function handleSearchKeydown(event: KeyboardEvent): void {
	if (event.key === "ArrowDown") {
		event.preventDefault();
		onMoveSelection(1);
		return;
	}
	if (event.key === "ArrowUp") {
		event.preventDefault();
		onMoveSelection(-1);
		return;
	}
	if (event.key === "Tab") {
		event.preventDefault();
		onToggleStage();
		return;
	}
	if (event.key !== "Enter") return;
	event.preventDefault();
	const item = resolveHistorySelection(
		historyState.stage,
		historyState.result,
		historyState.selected,
	);
	if (!item) return;
	if (item.kind === "prompt" && !event.shiftKey) {
		if (event.metaKey || event.ctrlKey) onInsertAndSend(item.hit);
		else onInsert(item.hit);
		return;
	}
	const target = jumpTarget(item.hit);
	if (target) onOpenMessage(target);
}

function messageCrumb(hit: MessageHit): string {
	return `${hit.sessionTitle || hit.cwd.split("/").pop() || "session"} · ${hit.role} · ${relativeTime(hit.timestamp)}`;
}

function promptCrumb(hit: PromptHit, projectAreaName: string | undefined): string {
	return [
		hit.sessionTitle,
		hit.projectId ? (projectAreaName ?? "projectArea") : undefined,
		relativeTime(hit.timestamp),
	]
		.filter((part): part is string => !!part)
		.join(" · ");
}

function selectScope(kind: HistoryScope["kind"]): void {
	onSetScope(kind);
	scopeMenu?.hidePopover();
}
</script>

{#snippet Highlight(text: string, query: string)}
	{#each highlightHistoryText(text, query) as part (part.key)}
		{#if part.highlighted}
			<mark class="history-highlight u-text-text-default">{part.text}</mark>
		{:else}
			<span>{part.text}</span>
		{/if}
	{/each}
{/snippet}

{#snippet DeleteChatButton(projectAreaId: string | undefined, sessionId: string, isSelected: boolean)}
	{#if projectAreaId}
		<button
			type="button"
			data-testid="history-delete-chat"
			aria-label={deleteUnavailableReason
				? `Move chat to trash: ${deleteUnavailableReason}`
				: "Move chat to trash"}
			title={deleteUnavailableReason ?? "Move chat to trash"}
			disabled={!!deleteUnavailableReason}
			onclick={(event) => {
				event.stopPropagation();
				onDeleteChat(projectAreaId, sessionId);
			}}
			class={`u-flex u-shrink-0 u-items-center u-justify-center history-row-action ${isSelected ? "history-row-action-visible" : ""}`}
		>
			<Icon name="trash-2" size={14} />
		</button>
	{/if}
{/snippet}

{#snippet PromptRow(hit: PromptHit, index: number)}
	{@const firstLine = hit.text.split("\n")[0] ?? hit.text}
	{@const isSelected = index === historyState.selected}
	{@const showChip = (historyState.scope.kind === "project" || historyState.scope.kind === "all") && !!hit.projectId}
	{@const target = jumpTarget(hit)}
	<li
		data-testid="history-item"
		data-kind="prompt"
		data-selected={isSelected}
		id={`${resultsId}-option-p:${hit.sessionId}:${hit.messageIndex ?? hit.timestamp}`}
		aria-current={isSelected}
		class={`u-flex u-w-full u-items-center u-gap-xs history-prompt-row tr-text-ui ${isSelected ? "history-row-selected u-text-text-default" : "history-row-unselected u-text-text-muted"}`}
	>
		<button type="button" onclick={() => onInsert(hit)} class="u-flex u-min-w-0 u-flex-1 u-items-center u-gap-sm history-row-main">
			<span class="u-min-w-0 u-flex-1 history-single-line">
				{@render Highlight(firstLine, historyState.query)}
			</span>
			{#if showChip}
				<span class="u-shrink-0 history-project-chip u-px-xs u-text-text-muted tr-text-metadata">
					{hit.projectId ? (projectAreaNames[hit.projectId] ?? "projectArea") : "projectArea"}
				</span>
			{/if}
			<span class="u-shrink-0 u-text-text-muted tr-text-metadata">{relativeTime(hit.timestamp)}</span>
		</button>
		{#if target}
			{#if isSelected}<span data-testid="history-jump-shortcut" class="u-shrink-0 u-text-text-muted tr-text-metadata">⇧⏎</span>{/if}
			<button
				type="button"
				data-testid="history-jump"
				aria-label="Go to chat"
				title="⇧⏎ go to chat"
				onclick={(event) => { event.stopPropagation(); onOpenMessage(target); }}
				class={`u-flex u-shrink-0 u-items-center u-justify-center history-row-action ${isSelected ? "history-row-action-visible" : ""}`}
			>
				<Icon name="corner-up-right" size={14} />
			</button>
		{/if}
		{@render DeleteChatButton(hit.projectId, hit.sessionId, isSelected)}
	</li>
{/snippet}

{#snippet MessageRow(hit: MessageHit, index: number)}
	{@const isSelected = historyState.result!.prompts.length + index === historyState.selected}
	{@const unmapped = !hit.projectId}
	<li
		data-testid="history-item"
		data-kind="message"
		data-selected={isSelected}
		id={`${resultsId}-option-m:${hit.sessionId}:${hit.messageIndex}`}
		aria-current={isSelected}
		class={`u-flex u-w-full u-items-center u-gap-xs history-message-row tr-text-ui ${isSelected ? "history-row-selected u-text-text-default" : "history-row-unselected u-text-text-muted"}`}
	>
		<button
			type="button"
			disabled={unmapped}
			onclick={() => { const target = jumpTarget(hit); if (target) onOpenMessage(target); }}
			class="u-flex u-min-w-0 u-flex-1 u-flex-col u-gap-0.5 u-px-sm u-py-xs u-text-left history-message-button"
		>
			<span class="u-flex u-items-center u-gap-xs u-text-text-muted tr-text-metadata">
				<span class="u-truncate">{hit.sessionTitle || hit.cwd.split("/").pop() || "session"}</span>
				<span>·</span><span>{hit.role}</span><span>·</span><span>{relativeTime(hit.timestamp)}</span>
				{#if unmapped}<span>· not a pixie projectArea</span>{/if}
			</span>
			<span class="history-single-line">{@render Highlight(hit.snippet, historyState.query)}</span>
		</button>
		{@render DeleteChatButton(hit.projectId, hit.sessionId, isSelected)}
	</li>
{/snippet}

{#snippet ResultsBody()}
	{#if historyState.error}
		<div data-testid="history-error" class="u-p-md u-text-center u-text-feedback-error tr-text-ui">search unavailable</div>
	{:else if historyState.result}
		{#if historyState.result.indexing}
			<div data-testid="history-indexing" class="u-px-sm history-py-1 u-text-center u-text-text-muted tr-text-metadata">indexing history…</div>
		{/if}
		{#if historyState.result.incomplete}
			<div class="u-px-sm history-py-1 u-text-center u-text-feedback-warning tr-text-metadata">some history could not be indexed</div>
		{/if}
		{#if hasResults}
			<div class="u-flex u-flex-col u-gap-xs history-padding-xs">
				{#if historyState.result.prompts.length > 0}
					<div class="u-flex u-flex-col u-gap-0.5">
						<div class="u-flex u-items-center u-justify-between u-px-sm history-py-half tr-text-eyebrow u-text-text-muted">
							<span>Prompts</span><span data-testid="history-counts">{promptCount}/{historyState.result.promptTotal}</span>
						</div>
						<ul aria-label="Prompt history results" class="u-flex u-flex-col u-gap-0.5">
							{#each historyState.result.prompts as hit, index (`${hit.sessionId}:${hit.messageIndex}`)}
								{@render PromptRow(hit, index)}
							{/each}
						</ul>
					</div>
				{/if}
				{#if historyState.stage === "zoomed" && historyState.result.messages.length > 0}
					<div class="u-flex u-flex-col u-gap-0.5">
						<div class="u-flex u-items-center u-justify-between u-px-sm history-py-half tr-text-eyebrow u-text-text-muted">
							<span>Messages</span><span data-testid="history-counts">{messageCount}/{historyState.result.messageTotal}</span>
						</div>
						<ul aria-label="Conversation history results" class="u-flex u-flex-col u-gap-0.5">
							{#each historyState.result.messages as hit, index (`${hit.sessionId}:${hit.messageIndex}`)}
								{@render MessageRow(hit, index)}
							{/each}
						</ul>
					</div>
				{/if}
			</div>
		{:else if isEmpty}
			<div class="u-p-md u-text-center u-text-text-muted tr-text-ui">no matches</div>
		{/if}
	{/if}
{/snippet}

{#if historyState.open}
	<div
		data-testid="history-overlay"
		data-stage={historyState.stage}
		class="history-overlay u-flex u-flex-col"
	>
		<div class="u-flex u-items-center u-gap-sm u-border-b u-border-border-default history-padding-sm">
			<input
				bind:this={input}
				type="search"
				data-testid="history-query"
				aria-controls={resultsId}
				aria-describedby={selectedStatusId}
				value={historyState.query}
				oninput={(event) => onQueryChange(event.currentTarget.value)}
				onkeydown={handleSearchKeydown}
				placeholder="Search prompts and conversations…"
				class="u-min-w-0 u-flex-1 history-query tr-text-ui u-text-text-default"
			/>
			<span id={selectedStatusId} role="status" aria-live="polite" class="u-sr-only">{selectedAnnouncement}</span>
			<span class="history-contents" {@attach mewa(dropdownBehavior)}>
				<button
					type="button"
					data-dropdown-menu-trigger={scopeMenuId}
					aria-haspopup="menu"
					aria-controls={scopeMenuId}
					aria-expanded="false"
					data-testid="history-scope"
					data-scope={historyState.scope.kind}
					class="u-flex u-shrink-0 u-items-center u-gap-xs history-scope-trigger u-px-sm history-py-half u-text-text-muted tr-text-metadata"
				>
					<span>{SCOPE_LABELS[historyState.scope.kind]}</span><span class="u-text-text-muted">⌃R</span>
				</button>
				<div
					bind:this={scopeMenu}
					id={scopeMenuId}
					popover="auto"
					role="menu"
					class="dropdown-menu-content history-scope-menu"
					ontoggle={(event) => {
						scopeMenuOpen = event.newState === "open";
						if (!scopeMenuOpen) queueMicrotask(() => input?.focus());
					}}
				>
					{#each SCOPE_ORDER as kind}
						<button
							type="button"
							role="menuitemradio"
							aria-checked={kind === historyState.scope.kind}
							data-testid="history-scope-option"
							data-scope={kind}
							class="dropdown-menu-item"
							onkeydown={activateCheckableMenuItem}
							onclick={() => selectScope(kind)}
						>
							<Icon name="check" size={14} class={kind === historyState.scope.kind ? "" : "history-scope-icon-hidden"} />
							<span>{SCOPE_MENU_LABELS[kind]}</span>
						</button>
					{/each}
				</div>
			</span>
		</div>
		{#if deleteUnavailableReason}
			<p class="u-border-b u-border-border-default u-px-sm u-py-xs u-text-text-muted tr-text-metadata">{deleteUnavailableReason}</p>
		{/if}
		{#if historyState.stage === "zoomed"}
			<div class="history-zoomed-layout u-flex u-flex-col">
				<div bind:this={resultsElement} data-testid="history-results" id={resultsId} class="history-results-zoomed">
					{@render ResultsBody()}
				</div>
				<div data-testid="history-preview" class="history-preview u-flex u-flex-col">
					{#if selectedItem}
						<div class="u-min-h-0 u-flex-1 u-overflow-y-auto history-pre-wrap u-break-words history-padding-sm tr-text-ui u-text-text-default">{@render Highlight(selectedItem.hit.text, historyState.query)}</div>
						<div class="u-shrink-0 u-border-t u-border-border-default u-px-sm u-py-xs u-text-text-muted tr-text-metadata">
							{selectedItem.kind === "prompt" ? promptCrumb(selectedItem.hit, selectedProjectAreaName) : messageCrumb(selectedItem.hit)}
						</div>
					{/if}
				</div>
			</div>
		{:else}
			<div bind:this={resultsElement} data-testid="history-results" id={resultsId} class="history-results-compact">
				{@render ResultsBody()}
			</div>
		{/if}
		{#if historyState.stage === "compact" && !historyState.error && historyState.result && !historyState.result.indexing && historyState.result.messageTotal > 0}
			<button type="button" data-testid="history-expand-hint" onclick={onToggleStage} class="u-border-t u-border-border-default history-padding-xs u-text-center u-text-text-muted tr-text-metadata history-expand-hint">
				{historyState.result.messageTotal} matches in conversations · ⇥ expand
			</button>
		{/if}
	</div>
{/if}

<style>
	.history-scope-menu { left: auto; right: anchor(right); }
	.history-highlight { border-radius: var(--radius-xs); background: var(--primary-soft); }
	.history-row-action { border: 0; border-radius: var(--radius-sm); background: transparent; padding: var(--space-xs); color: var(--text-muted); opacity: 0; transition: opacity var(--transition-fast), background-color var(--transition-fast), color var(--transition-fast); }
	.history-prompt-row:hover .history-row-action, .history-message-row:hover .history-row-action, .history-row-action-visible { opacity: 1; }
	.history-row-action:hover { background: var(--container-elevated-bg); color: var(--text-default); }
	.history-row-action[aria-label^="Move"]:hover { color: var(--feedback-error); }
	.history-prompt-row, .history-message-row { border-inline-start: 2px solid transparent; border-radius: var(--radius-sm); padding-inline-end: var(--space-xs); text-align: start; }
	.history-prompt-row { padding-block: var(--space-xs); padding-inline-start: var(--space-sm); }
	.history-row-selected { border-inline-start-color: var(--primary); background: var(--control-bg-selected); }
	.history-row-unselected { border-inline-start-color: transparent; }
	.history-row-main { overflow: hidden; border: 0; background: transparent; text-align: start; }
	.history-single-line { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
	.history-project-chip { border: 1px solid var(--border-default); border-radius: 999px; background: var(--container-project-bg); }
	.history-message-button { border: 0; background: transparent; }
	.history-message-button:disabled { cursor: default; }
	.history-py-1 { padding-block: var(--space-2xs); }
	.history-py-half { padding-block: var(--space-2xs); }
	.history-padding-xs { padding: var(--space-xs); }
	.history-padding-sm { padding: var(--space-sm); }
	.history-overlay { position: absolute; inset-block-end: 100%; inset-inline: var(--space-sm); margin-block-end: var(--space-xs); overflow: hidden; border: 1px solid var(--border-default); border-radius: var(--radius-lg); background: var(--container-elevated-bg); box-shadow: var(--shadow-md); }
	.history-query { border: 0; outline: none; background: transparent; }
	.history-query::placeholder { color: var(--text-muted); }
	.history-contents { display: contents; }
	.history-scope-trigger { border: 1px solid var(--border-default); border-radius: 999px; outline: none; background: var(--container-project-bg); }
	.history-scope-trigger:hover, .history-expand-hint:hover { background: var(--control-bg-hovered); }
	.history-zoomed-layout { overflow: hidden; }
	.history-results-zoomed, .history-preview { max-height: 37.5vh; }
	.history-results-zoomed, .history-results-compact { overflow-y: auto; }
	.history-preview { overflow: hidden; border-top: 1px solid var(--border-default); }
	.history-pre-wrap { white-space: pre-wrap; }
	@media (min-width: 48rem) { .history-zoomed-layout { flex-direction: row; } .history-results-zoomed { max-height: 75vh; width: 55%; } .history-preview { max-height: 75vh; width: 45%; border-top: 0; border-inline-start: 1px solid var(--border-default); } }
	.history-results-compact { max-height: 40vh; }
	.history-scope-icon-hidden { visibility: hidden; }
</style>
