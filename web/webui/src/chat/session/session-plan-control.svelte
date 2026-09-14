<script lang="ts">
import type { SessionPlanState } from "@pixie/shared";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as popoverBehavior } from "../../../vendor/mewa-ui/components/popover.js";
import Icon from "../../components/icon.svelte";
import { planProgress, sessionPlanLabel } from "./session-plan";
import SessionPlanContent from "./session-plan-content.svelte";

interface Props {
	planState: SessionPlanState | null;
}
let { planState }: Props = $props();
const componentId = $props.id();
const popoverId = `session-plan-${componentId}`;
let visible = $derived(!!planState && (planState.entries.length > 0 || planState.truncated));
let progress = $derived(planState ? planProgress(planState) : { completed: 0, total: 0 });
let hasEntries = $derived((planState?.entries.length ?? 0) > 0);

// A top-layer popover is sized against the viewport, so clamp it to the chat
// pane it belongs to; otherwise a 30rem surface spills over the details pane
// on wide layouts.
function clampPopoverWidth(event: Event): void {
	const popover = event.currentTarget;
	if (!(popover instanceof HTMLElement)) return;
	const trigger = document.querySelector(`[popovertarget="${popoverId}"]`);
	const pane = trigger?.closest('[data-testid="primary-view"]');
	if (!(trigger instanceof HTMLElement) || !(pane instanceof HTMLElement)) return;
	const available = pane.getBoundingClientRect().right - trigger.getBoundingClientRect().left - 8;
	popover.style.maxInlineSize = `${Math.max(240, Math.min(available, 480))}px`;
}
</script>

{#if visible && planState}
	<span class="session-plan-contents" {@attach mewa(popoverBehavior)}>
		<button
			type="button"
			popovertarget={popoverId}
			data-testid="session-plan-trigger"
			aria-label={sessionPlanLabel(planState)}
			class="u-flex u-shrink-0 u-items-center u-gap-xs u-rounded u-px-xs session-plan-trigger tr-text-metadata u-text-text-muted"
		>
			<Icon name="list-checks" size={14} />
			<span>{hasEntries ? `${progress.completed}/${progress.total}` : "Limited"}</span>
		</button>
		<div
			id={popoverId}
			popover="auto"
			ontoggle={clampPopoverWidth}
			class="session-plan-popover popover session-plan-popover-size"
		>
			<SessionPlanContent {planState} />
		</div>
	</span>
{/if}

<style>
	/* Mewa's `data-align="start"` maps to `position-area: bottom left`, which grows the
	 * surface leftwards so its right edge sits on the trigger's left edge — over the rail
	 * and Projects sidebar. `bottom span-right` instead starts at the trigger's left edge
	 * and grows into the chat pane. When that tile is narrower than the surface (narrow
	 * panes), the browser shifts it back inside the viewport. */
	.session-plan-popover {
		position-area: bottom span-right;
	}
	.session-plan-trigger { border: 0; outline: none; background: transparent; padding-block: var(--space-2xs); }
	.session-plan-trigger:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	.session-plan-trigger:focus-visible { outline: var(--focus-ring-width, 2px) solid var(--border-focus); }
	.session-plan-popover-size { width: min(90vw, 30rem); padding: 0; }
	.session-plan-contents { display: contents; }
</style>
