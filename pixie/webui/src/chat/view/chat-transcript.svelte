<script module lang="ts">
import type { ChatScrollAnchor as TranscriptScrollAnchor } from "./chat-scroll";

export interface ChatTranscriptHandle {
	beginPrepend: () => TranscriptScrollAnchor | null;
	finishPrepend: (anchor: TranscriptScrollAnchor | null, restore: boolean) => Promise<void>;
	scrollToBottom: (behavior?: ScrollBehavior) => void;
	scrollToRow: (rowId: string) => boolean;
}
</script>

<script lang="ts">
	import { tick } from "svelte";
	import Button from "../../components/button.svelte";
	import Icon from "../../components/icon.svelte";
	import ChatTurnView from "../render/turns.svelte";
	import type { ChatRow } from "../runtime/rows";
	import StreamIndicator from "../session/stream-indicator.svelte";
	import { phaseLabel, type StreamStatus } from "../session/stream-status";
	import {
		captureChatScrollAnchor,
		chatScrollIsAtBottom,
		createChatScroll,
		restoredChatScrollTop,
		type ChatScrollAnchor,
	} from "./chat-scroll";

	interface Props {
		rows: ChatRow[];
		conversationKey: string;
		projectAreaRoot?: string | undefined;
		flashRowId: string | null;
		status: StreamStatus | null;
		transcriptStart: number;
		loadState: "idle" | "loading" | "error";
		onLoadEarlier: () => void;
		onOpenChange: (path: string) => void;
	}

	let {
		rows,
		conversationKey,
		projectAreaRoot,
		flashRowId,
		status,
		transcriptStart,
		loadState,
		onLoadEarlier,
		onOpenChange,
	}: Props = $props();

	let viewport = $state<HTMLDivElement>();
	let content = $state<HTMLOListElement>();
	let showScrollButton = $state(false);
	let suppressFollow = $state(0);
	let observedConversationKey = "";
	let autoHistoryLoadArmed = false;
	let lastAtBottom = true;
	let followRevision = 0;
	const componentId = $props.id();
	const headingId = `chat-transcript-${componentId}`;
	const logId = `chat-transcript-log-${componentId}`;

	function moveToBottom(behavior: ScrollBehavior): void {
		if (!viewport) return;
		const motion = behavior === "smooth" && window.matchMedia("(prefers-reduced-motion: reduce)").matches
			? "auto" : behavior;
		viewport.scrollTo({ top: viewport.scrollHeight, behavior: motion });
		lastAtBottom = true;
	}

	const scroll = createChatScroll(
		{ scrollToBottom: moveToBottom },
		(visible) => (showScrollButton = visible),
	);

	export function scrollToBottom(behavior: ScrollBehavior = "smooth"): void {
		moveToBottom(behavior);
		scroll.handleAtBottom(true);
	}

	export function beginPrepend(): ChatScrollAnchor | null {
		suppressFollow += 1;
		followRevision += 1;
		return viewport ? captureChatScrollAnchor(viewport) : null;
	}

	export async function finishPrepend(
		anchor: ChatScrollAnchor | null,
		restore: boolean,
	): Promise<void> {
		await tick();
		if (restore && anchor && viewport) {
			viewport.scrollTop = restoredChatScrollTop(anchor, viewport.scrollHeight);
			lastAtBottom = chatScrollIsAtBottom(viewport);
			scroll.handleAtBottom(lastAtBottom);
		}
		suppressFollow = Math.max(0, suppressFollow - 1);
	}

	export function scrollToRow(rowId: string): boolean {
		if (!content) return false;
		const row = Array.from(content.querySelectorAll<HTMLElement>("[data-chat-row]")).find(
			(element) => element.dataset.rowId === rowId,
		);
		if (!row) return false;
		followRevision += 1;
		row.scrollIntoView({ block: "center" });
		lastAtBottom = viewport ? chatScrollIsAtBottom(viewport) : false;
		scroll.handleAtBottom(lastAtBottom);
		return true;
	}

	$effect.pre(() => {
		const key = conversationKey;
		const nextRows = rows;
		const element = viewport;
		void nextRows;
		if (!element) return;
		if (observedConversationKey !== key) {
			observedConversationKey = key;
			const revision = ++followRevision;
			void tick().then(() => {
				if (revision === followRevision && observedConversationKey === key) {
					scrollToBottom("auto");
				}
			});
			return;
		}
		if (suppressFollow > 0) return;
		const wasAtBottom = chatScrollIsAtBottom(element);
		lastAtBottom = wasAtBottom;
		scroll.handleAtBottom(wasAtBottom);
		const behavior = scroll.followOutput(wasAtBottom);
		if (!behavior) return;
		const revision = ++followRevision;
		void tick().then(() => {
			if (revision === followRevision) moveToBottom(behavior);
		});
	});

	$effect(() => {
		const element = viewport;
		const list = content;
		if (!element || !list || typeof ResizeObserver !== "function") return;
		const observer = new ResizeObserver(() => {
			if (suppressFollow > 0 || !lastAtBottom || !scroll.followOutput(true)) return;
			moveToBottom("auto");
		});
		observer.observe(element);
		observer.observe(list);
		return () => observer.disconnect();
	});

	function maybeLoadEarlier(): void {
		if (!viewport || viewport.scrollTop > 50 || !autoHistoryLoadArmed) return;
		autoHistoryLoadArmed = false;
		onLoadEarlier();
	}

	function handleScroll(): void {
		if (!viewport) return;
		lastAtBottom = chatScrollIsAtBottom(viewport);
		scroll.handleAtBottom(lastAtBottom);
		maybeLoadEarlier();
	}

	function handleKeydown(event: KeyboardEvent): void {
		if (!["ArrowUp", "PageUp", "Home"].includes(event.key)) return;
		autoHistoryLoadArmed = true;
		scroll.handleWheel(-1);
	}

	let historyLabel = $derived(
		loadState === "loading"
			? "Loading earlier messages…"
			: loadState === "error"
				? "Retry loading earlier messages"
				: "Load earlier messages",
	);
</script>

<section
	class="message-scroller chat-scroller"
	data-conversation-key={conversationKey}
	data-default-pinned="true"
>
	<h2 id={headingId} class="message-scroller-label">Conversation</h2>
	<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (This labelled, keyboard-scrollable reading viewport intentionally owns pointer and keyboard interaction state.) -->
	<div
		bind:this={viewport}
		id={logId}
		data-testid="chat-scroll"
		class="message-scroller-viewport chat-viewport"
		role="log"
		aria-live="off"
		aria-labelledby={headingId}
		aria-busy={status !== null}
		tabindex="0"
		onscroll={handleScroll}
		onpointerdown={() => {
			autoHistoryLoadArmed = true;
			scroll.startInteraction();
		}}
		onpointerup={() => scroll.endInteraction()}
		onpointercancel={() => scroll.endInteraction()}
		onwheel={(event) => {
			if (event.deltaY < 0) autoHistoryLoadArmed = true;
			scroll.handleWheel(event.deltaY);
		}}
		ontouchstart={() => {
			autoHistoryLoadArmed = true;
			scroll.startInteraction();
		}}
		ontouchend={() => scroll.endInteraction()}
		onkeydown={handleKeydown}
	>
		{#if transcriptStart > 0 || loadState === "error"}
			<div class="mx-auto flex max-w-3xl justify-center px-md py-sm">
				<Button
					variant="outline"
					size="sm"
					aria-label={historyLabel}
					disabled={loadState === "loading"}
					onclick={onLoadEarlier}
				>
					{historyLabel}
				</Button>
			</div>
		{/if}
		<ol bind:this={content} class="message-scroller-content">
			{#each rows as row (row.id)}
				<li
					data-chat-row
					data-row-id={row.id}
					data-flash={row.id === flashRowId || undefined}
					class="message-scroller-entry chat-row mx-auto w-full max-w-3xl rounded-[var(--radius-sm)] px-md py-xs transition-colors data-[flash]:bg-primary-subtle"
				>
					<ChatTurnView {row} {projectAreaRoot} {onOpenChange} />
				</li>
			{/each}
		</ol>
		{#if status}
			<div class="mx-auto max-w-3xl px-md pb-sm"><StreamIndicator {status} /></div>
		{/if}
	</div>
	<p class="sr-only" role="status" aria-atomic="true" data-testid="chat-announcement">{status ? phaseLabel(status) : ""}</p>
	{#if showScrollButton}
		<button
			type="button"
			data-testid="scroll-to-bottom"
			data-message-scroller-jump
			aria-controls={logId}
			onclick={() => {
				viewport?.focus({ preventScroll: true });
				scrollToBottom();
			}}
			class="message-scroller-jump flex items-center gap-xs"
		>
			<Icon name="arrow-down" size={12} />
			New messages
		</button>
	{/if}
</section>

<style>
	.chat-scroller {
		min-height: 0;
		flex: 1;
		border: 0;
	}

	.chat-viewport {
		min-height: 0;
		max-height: none;
		flex: 1;
		overflow-x: hidden;
	}

	.chat-row {
		content-visibility: auto;
		contain-intrinsic-size: auto 12rem;
		border-block-end: 0;
	}

	.message-scroller-entry.chat-row:last-child {
		padding-block-end: var(--space-200);
	}
</style>
