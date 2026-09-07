<script lang="ts">
import type { HistoryScope, MessageHit, PromptHit } from "@pixie/contracts";
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
			<mark class="rounded-[var(--radius-xs)] bg-primary-soft text-text-default">{part.text}</mark>
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
			class={`flex shrink-0 items-center justify-center rounded-[var(--radius-sm)] p-xs text-text-muted opacity-0 transition hover:bg-container-elevated-bg hover:text-feedback-error group-hover:opacity-100 ${isSelected ? "opacity-100" : ""}`}
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
		class={`group flex w-full items-center gap-xs rounded-[var(--radius-sm)] border-l-2 py-xs pl-sm pr-xs text-left tr-text-ui ${isSelected ? "border-l-primary bg-control-bg-selected text-text-default" : "border-l-transparent text-text-muted"}`}
	>
		<button type="button" onclick={() => onInsert(hit)} class="flex min-w-0 flex-1 items-center gap-sm overflow-hidden text-left">
			<span class="min-w-0 flex-1 overflow-hidden whitespace-nowrap text-ellipsis">
				{@render Highlight(firstLine, historyState.query)}
			</span>
			{#if showChip}
				<span class="shrink-0 rounded-full border border-border-default bg-container-project-bg px-xs text-text-muted tr-text-metadata">
					{hit.projectId ? (projectAreaNames[hit.projectId] ?? "projectArea") : "projectArea"}
				</span>
			{/if}
			<span class="shrink-0 text-text-muted tr-text-metadata">{relativeTime(hit.timestamp)}</span>
		</button>
		{#if target}
			{#if isSelected}<span data-testid="history-jump-shortcut" class="shrink-0 text-text-muted tr-text-metadata">⇧⏎</span>{/if}
			<button
				type="button"
				data-testid="history-jump"
				aria-label="Go to chat"
				title="⇧⏎ go to chat"
				onclick={(event) => { event.stopPropagation(); onOpenMessage(target); }}
				class={`flex shrink-0 items-center justify-center rounded-[var(--radius-sm)] p-xs text-text-muted opacity-0 transition hover:bg-container-elevated-bg hover:text-text-default group-hover:opacity-100 ${isSelected ? "opacity-100" : ""}`}
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
		class={`group flex w-full items-center gap-xs rounded-[var(--radius-sm)] border-l-2 pr-xs tr-text-ui ${isSelected ? "border-l-primary bg-control-bg-selected text-text-default" : "border-l-transparent text-text-muted"}`}
	>
		<button
			type="button"
			disabled={unmapped}
			onclick={() => { const target = jumpTarget(hit); if (target) onOpenMessage(target); }}
			class="flex min-w-0 flex-1 flex-col gap-0.5 px-sm py-xs text-left disabled:cursor-default"
		>
			<span class="flex items-center gap-xs text-text-muted tr-text-metadata">
				<span class="truncate">{hit.sessionTitle || hit.cwd.split("/").pop() || "session"}</span>
				<span>·</span><span>{hit.role}</span><span>·</span><span>{relativeTime(hit.timestamp)}</span>
				{#if unmapped}<span>· not a pixie projectArea</span>{/if}
			</span>
			<span class="overflow-hidden whitespace-nowrap text-ellipsis">{@render Highlight(hit.snippet, historyState.query)}</span>
		</button>
		{@render DeleteChatButton(hit.projectId, hit.sessionId, isSelected)}
	</li>
{/snippet}

{#snippet ResultsBody()}
	{#if historyState.error}
		<div data-testid="history-error" class="p-md text-center text-feedback-error tr-text-ui">search unavailable</div>
	{:else if historyState.result}
		{#if historyState.result.indexing}
			<div data-testid="history-indexing" class="px-sm py-1 text-center text-text-muted tr-text-metadata">indexing history…</div>
		{/if}
		{#if historyState.result.incomplete}
			<div class="px-sm py-1 text-center text-feedback-warning tr-text-metadata">some history could not be indexed</div>
		{/if}
		{#if hasResults}
			<div class="flex flex-col gap-xs p-xs">
				{#if historyState.result.prompts.length > 0}
					<div class="flex flex-col gap-0.5">
						<div class="flex items-center justify-between px-sm py-0.5 tr-text-eyebrow text-text-muted">
							<span>Prompts</span><span data-testid="history-counts">{promptCount}/{historyState.result.promptTotal}</span>
						</div>
						<ul aria-label="Prompt history results" class="flex flex-col gap-0.5">
							{#each historyState.result.prompts as hit, index (`${hit.sessionId}:${hit.messageIndex}`)}
								{@render PromptRow(hit, index)}
							{/each}
						</ul>
					</div>
				{/if}
				{#if historyState.stage === "zoomed" && historyState.result.messages.length > 0}
					<div class="flex flex-col gap-0.5">
						<div class="flex items-center justify-between px-sm py-0.5 tr-text-eyebrow text-text-muted">
							<span>Messages</span><span data-testid="history-counts">{messageCount}/{historyState.result.messageTotal}</span>
						</div>
						<ul aria-label="Conversation history results" class="flex flex-col gap-0.5">
							{#each historyState.result.messages as hit, index (`${hit.sessionId}:${hit.messageIndex}`)}
								{@render MessageRow(hit, index)}
							{/each}
						</ul>
					</div>
				{/if}
			</div>
		{:else if isEmpty}
			<div class="p-md text-center text-text-muted tr-text-ui">no matches</div>
		{/if}
	{/if}
{/snippet}

{#if historyState.open}
	<div
		data-testid="history-overlay"
		data-stage={historyState.stage}
		class="absolute bottom-full left-sm right-sm mb-xs flex flex-col overflow-hidden rounded-[var(--radius-lg)] border border-border-default bg-container-elevated-bg shadow-[var(--shadow-md)]"
	>
		<div class="flex items-center gap-sm border-b border-border-default p-sm">
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
				class="min-w-0 flex-1 bg-transparent tr-text-ui text-text-default outline-none placeholder:text-text-muted"
			/>
			<span id={selectedStatusId} role="status" aria-live="polite" class="sr-only">{selectedAnnouncement}</span>
			<span class="contents" {@attach mewa(dropdownBehavior)}>
				<button
					type="button"
					data-dropdown-menu-trigger={scopeMenuId}
					aria-haspopup="menu"
					aria-controls={scopeMenuId}
					aria-expanded="false"
					data-testid="history-scope"
					data-scope={historyState.scope.kind}
					class="flex shrink-0 items-center gap-xs rounded-full border border-border-default bg-container-project-bg px-sm py-0.5 text-text-muted tr-text-metadata outline-none hover:bg-control-bg-hovered"
				>
					<span>{SCOPE_LABELS[historyState.scope.kind]}</span><span class="text-text-muted">⌃R</span>
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
							<Icon name="check" size={14} class={kind === historyState.scope.kind ? "" : "invisible"} />
							<span>{SCOPE_MENU_LABELS[kind]}</span>
						</button>
					{/each}
				</div>
			</span>
		</div>
		{#if deleteUnavailableReason}
			<p class="border-border-default border-b px-sm py-xs text-text-muted tr-text-metadata">{deleteUnavailableReason}</p>
		{/if}
		{#if historyState.stage === "zoomed"}
			<div class="flex flex-col overflow-hidden md:flex-row">
				<div bind:this={resultsElement} data-testid="history-results" id={resultsId} class="max-h-[37.5vh] overflow-y-auto md:max-h-[75vh] md:w-[55%]">
					{@render ResultsBody()}
				</div>
				<div data-testid="history-preview" class="flex max-h-[37.5vh] flex-col overflow-hidden border-border-default border-t md:max-h-[75vh] md:w-[45%] md:border-t-0 md:border-l">
					{#if selectedItem}
						<div class="min-h-0 flex-1 overflow-y-auto whitespace-pre-wrap break-words p-sm tr-text-ui text-text-default">{@render Highlight(selectedItem.hit.text, historyState.query)}</div>
						<div class="shrink-0 border-t border-border-default px-sm py-xs text-text-muted tr-text-metadata">
							{selectedItem.kind === "prompt" ? promptCrumb(selectedItem.hit, selectedProjectAreaName) : messageCrumb(selectedItem.hit)}
						</div>
					{/if}
				</div>
			</div>
		{:else}
			<div bind:this={resultsElement} data-testid="history-results" id={resultsId} class="max-h-[40vh] overflow-y-auto">
				{@render ResultsBody()}
			</div>
		{/if}
		{#if historyState.stage === "compact" && !historyState.error && historyState.result && !historyState.result.indexing && historyState.result.messageTotal > 0}
			<button type="button" data-testid="history-expand-hint" onclick={onToggleStage} class="border-t border-border-default p-xs text-center text-text-muted tr-text-metadata hover:bg-control-bg-hovered">
				{historyState.result.messageTotal} matches in conversations · ⇥ expand
			</button>
		{/if}
	</div>
{/if}

<style>
	.history-scope-menu { left: auto; right: anchor(right); }
</style>
