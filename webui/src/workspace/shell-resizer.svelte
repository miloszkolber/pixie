<script lang="ts">
import type { WorkspaceLayout } from "./store/selection-state";
import {
	WORKSPACE_PRIMARY_FRACTION_MAX,
	WORKSPACE_PRIMARY_FRACTION_MIN,
	WORKSPACE_SIDE_MAX,
	WORKSPACE_SIDE_MIN,
} from "./store/selection-state";

type ResizerKind = "left" | "primary" | "right";

interface Props {
	kind: ResizerKind;
	layout: WorkspaceLayout;
	grid: HTMLElement | null;
	onChange: (patch: Partial<WorkspaceLayout>) => void;
}

let { kind, layout, grid, onChange }: Props = $props();
let dragging = $state(false);
let dragStart = $state({ x: 0, value: 0 });

const label = $derived(
	kind === "left"
		? "Primary sidebar width"
		: kind === "right"
			? "Secondary sidebar width"
			: "Primary and secondary content split",
);
const value = $derived(
	kind === "left"
		? layout.leftWidth
		: kind === "right"
			? layout.rightWidth
			: layout.primaryFraction * 100,
);
const min = $derived(
	kind === "primary" ? WORKSPACE_PRIMARY_FRACTION_MIN * 100 : WORKSPACE_SIDE_MIN,
);
const max = $derived(
	kind === "primary" ? WORKSPACE_PRIMARY_FRACTION_MAX * 100 : WORKSPACE_SIDE_MAX,
);

function clamp(valueToClamp: number): number {
	if (!Number.isFinite(valueToClamp)) return value;
	return Math.min(max, Math.max(min, valueToClamp));
}

function setValue(next: number): void {
	const bounded = clamp(next);
	if (kind === "left") onChange({ leftWidth: bounded });
	else if (kind === "right") onChange({ rightWidth: bounded });
	else onChange({ primaryFraction: bounded / 100 });
}

function contentWidth(): number {
	const primary = grid?.querySelector<HTMLElement>('[data-slot="primary-view"]');
	const secondary = grid?.querySelector<HTMLElement>('[data-slot="secondary-view"]');
	const width =
		(primary?.getBoundingClientRect().width ?? 0) + (secondary?.getBoundingClientRect().width ?? 0);
	return width > 0 ? width : (grid?.getBoundingClientRect().width ?? 0);
}

function onKeydown(event: KeyboardEvent): void {
	let next: number | null = null;
	const step = kind === "primary" ? (event.shiftKey ? 10 : 5) : event.shiftKey ? 32 : 8;
	if (event.key === "ArrowLeft" || event.key === "ArrowDown") {
		next = value - (kind === "right" ? -step : step);
	} else if (event.key === "ArrowRight" || event.key === "ArrowUp") {
		next = value + (kind === "right" ? -step : step);
	} else if (event.key === "Home") {
		next = min;
	} else if (event.key === "End") {
		next = max;
	}
	if (next === null) return;
	event.preventDefault();
	setValue(next);
}

function onPointerdown(event: PointerEvent): void {
	if (event.button !== 0) return;
	dragStart = { x: event.clientX, value };
	dragging = true;
	const target = event.currentTarget as HTMLElement;
	target.setPointerCapture?.(event.pointerId);
}

function onPointermove(event: PointerEvent): void {
	if (!dragging) return;
	const delta = event.clientX - dragStart.x;
	if (kind === "primary") {
		const width = contentWidth();
		if (width <= 0) return;
		setValue(dragStart.value + (delta / width) * 100);
	} else {
		setValue(dragStart.value + (kind === "right" ? -delta : delta));
	}
}

function endPointer(event?: PointerEvent): void {
	if (event) (event.currentTarget as HTMLElement).releasePointerCapture?.(event.pointerId);
	dragging = false;
}
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (The separator is an operable pointer and keyboard resize control.) -->
<div
	class={`pixie-resizer pixie-resizer-${kind}`}
	data-testid={`resize-${kind}`}
	data-resizing={dragging ? "" : undefined}
	role="separator"
	tabindex="0"
	aria-orientation="vertical"
	aria-label={label}
	aria-valuemin={min}
	aria-valuemax={max}
	aria-valuenow={Math.round(value)}
	aria-valuetext={`${Math.round(value)} ${kind === "primary" ? "percent primary content" : "pixels"}`}
	onkeydown={onKeydown}
	onpointerdown={onPointerdown}
	onpointermove={onPointermove}
	onpointerup={endPointer}
	onpointercancel={endPointer}
></div>
