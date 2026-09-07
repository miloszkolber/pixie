<script lang="ts">
import type { SessionPlanState } from "@pixie/contracts";
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
</script>

{#if visible && planState}
	<span class="contents" {@attach mewa(popoverBehavior)}>
		<button
			type="button"
			popovertarget={popoverId}
			data-testid="session-plan-trigger"
			aria-label={sessionPlanLabel(planState)}
			class="flex shrink-0 items-center gap-xs rounded-[var(--radius-sm)] px-xs py-0.5 tr-text-metadata text-text-muted outline-none hover:bg-control-bg-hovered hover:text-text-default focus-visible:ring-2 focus-visible:ring-primary"
		>
			<Icon name="list-checks" size={14} />
			<span>{hasEntries ? `${progress.completed}/${progress.total}` : "Limited"}</span>
		</button>
		<div id={popoverId} popover="auto" class="popover w-[min(90vw,30rem)] p-0" data-align="start">
			<SessionPlanContent {planState} />
		</div>
	</span>
{/if}
