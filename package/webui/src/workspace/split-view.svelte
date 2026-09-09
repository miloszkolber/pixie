<script lang="ts">
import type { Snippet } from "svelte";
import {
	SPLIT_DEFAULT,
	SPLIT_LARGE_STEP,
	SPLIT_MAX,
	SPLIT_MIN,
	SPLIT_STEP,
	clampSplitPercent,
	stepSplitPercent,
} from "./split-range";

interface Props {
	label?: string;
	chatLabel?: string;
	previewLabel?: string;
	splitPercent?: number;
	onSplitChange?: ((percent: number) => void) | undefined;
	chat: Snippet;
	preview: Snippet;
}

let {
	label = "Split workspace",
	chatLabel = "Chat pane",
	previewLabel = "Preview pane",
	splitPercent = 50,
	onSplitChange = undefined,
	chat,
	preview,
}: Props = $props();

let current = $state(SPLIT_DEFAULT);
$effect(() => {
	current = splitPercent;
});
let clamped = $derived(clampSplitPercent(current));
let group: HTMLElement | null = $state(null);
let dragging = $state(false);
let dragStart = { x: 0, percent: 50 };

function setSplit(value: number): void {
	current = clampSplitPercent(value);
	onSplitChange?.(current);
}

function onHandleKeydown(event: KeyboardEvent): void {
	const large = event.shiftKey;
	let next: number | null = null;
	if (event.key === "ArrowLeft" || event.key === "ArrowDown") {
		next = stepSplitPercent(clamped, -1, large);
	} else if (event.key === "ArrowRight" || event.key === "ArrowUp") {
		next = stepSplitPercent(clamped, 1, large);
	} else if (event.key === "Home") {
		next = SPLIT_MIN;
	} else if (event.key === "End") {
		next = SPLIT_MAX;
	}
	if (next === null) return;
	event.preventDefault();
	setSplit(next);
}

function onHandlePointerDown(event: PointerEvent): void {
	if (event.button !== 0) return;
	dragStart = { x: event.clientX, percent: clamped };
	dragging = true;
	(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
}

function onHandlePointerMove(event: PointerEvent): void {
	if (!dragging || !group) return;
	const width = group.getBoundingClientRect().width;
	if (width <= 0) return;
	setSplit(dragStart.percent + ((event.clientX - dragStart.x) / width) * 100);
}

function endDrag(): void {
	dragging = false;
}
</script>

<section class="resizable pixie-split" data-init aria-label={label}>
	<div class="resizable-group pixie-split-group" data-orientation="vertical" bind:this={group}>
		<div
			class="resizable-panel pixie-split-panel"
			role="region"
			aria-label={chatLabel}
			style:flex={`${clamped} ${clamped} 0%`}
		>
			{@render chat()}
		</div>
		<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (This labelled separator is the operable split control: it owns keyboard and pointer resize.) -->
		<div
			class="resizable-handle pixie-split-handle"
			role="separator"
			tabindex="0"
			aria-orientation="vertical"
			aria-label="Chat and preview split"
			aria-valuemin={SPLIT_MIN}
			aria-valuemax={SPLIT_MAX}
			aria-valuenow={clamped}
			aria-valuetext={`${clamped} percent chat pane width`}
			data-value-min={String(SPLIT_MIN)}
			data-value-max={String(SPLIT_MAX)}
			data-value-now={String(clamped)}
			data-value-label="Chat pane width"
			data-step={String(SPLIT_STEP)}
			data-large-step={String(SPLIT_LARGE_STEP)}
			data-resizing={dragging ? "" : undefined}
			title="Chat and preview split (arrow keys resize)"
			onkeydown={onHandleKeydown}
			onpointerdown={onHandlePointerDown}
			onpointermove={onHandlePointerMove}
			onpointerup={endDrag}
			onpointercancel={endDrag}
		></div>
		<div
			class="resizable-panel pixie-split-panel"
			role="region"
			aria-label={previewLabel}
			style:flex={`${100 - clamped} ${100 - clamped} 0%`}
		>
			{@render preview()}
		</div>
	</div>
</section>
