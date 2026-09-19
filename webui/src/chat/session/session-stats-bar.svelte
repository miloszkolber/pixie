<script lang="ts">
import type { SessionStats } from "@pixie/shared";
import { mewa } from "../../../vendor/mewa-svelte/index.js";
import { behavior as popoverBehavior } from "../../../vendor/mewa-ui/components/popover.js";
import Icon from "../../components/icon.svelte";
import {
	contextPart,
	formatCost,
	formatTokens,
	isUsageReported,
	usageParts,
} from "./session-stats";

interface Props {
	stats: SessionStats | null;
}

let { stats }: Props = $props();
const componentId = $props.id();
const popoverId = `session-stats-${componentId}`;
let view = $derived.by(() => {
	if (!stats) return null;
	const parts = usageParts(stats);
	const context = stats.contextUsage ? contextPart(stats.contextUsage) : null;
	if (parts.length === 0 && !context) return null;
	const percent = stats.contextUsage?.percent;
	const progress = percent === null || percent === undefined ? 0 : percent;
	return { stats, context, progress };
});
</script>

{#snippet usageRow(label: string, value: string)}
	<div class="session-stats-contents">
		<dt class="u-text-text-muted">{label}</dt>
		<dd class="u-text-text-default session-stats-value">{value}</dd>
	</div>
{/snippet}

{#if view}
	<div class="session-stats-contents" {@attach mewa(popoverBehavior)}>
		<button
			type="button"
			popovertarget={popoverId}
			data-testid="usage-tracker"
			class="u-flex u-shrink-0 session-stats-nowrap u-items-center session-stats-justify-end u-gap-xs u-rounded u-px-xs session-stats-trigger-padding u-text-text-muted tr-text-metadata session-stats-trigger"
			aria-label="Open session usage"
		>
			<Icon name="gauge" size={14} class="session-stats-icon" />
			{#if isUsageReported(view.stats, "total", view.stats.tokens.total)}
				<span>{formatTokens(view.stats.tokens.total)} tokens</span>
			{/if}
			{#if view.context}<span>{view.context.text}</span>{/if}
		</button>
		<div
			id={popoverId}
			popover="auto"
			class="popover session-stats-popover u-p-md"
			data-align="end"
		>
			<div data-testid="session-stats" class="u-flex u-flex-col u-gap-md">
				<div>
					<div class="tr-text-ui u-text-text-default">Session usage</div>
					<div class="u-text-text-muted tr-text-metadata">
						Reported by the connected agent for this controller runtime
					</div>
				</div>
				{#if view.stats.contextUsage}
					<div class="u-flex u-flex-col u-gap-xs">
						<div class="u-flex u-items-center u-justify-between tr-text-metadata">
							<span class="u-text-text-default">Context window</span>
							<span class="u-text-text-muted">{view.context?.text}</span>
						</div>
						<div
							role="progressbar"
							aria-label="Context window used"
							aria-valuemin={0}
							aria-valuemax={100}
							aria-valuenow={Math.round(Math.min(100, Math.max(0, view.progress)))}
							class="session-stats-progress-track"
						>
							<div
								class="session-stats-progress"
								style:width={`${Math.min(100, Math.max(0, view.progress))}%`}
							></div>
						</div>
					</div>
				{/if}
				<dl class="session-stats-grid tr-text-metadata">
					{#if isUsageReported(view.stats, "input", view.stats.tokens.input)}
						{@render usageRow("Input", `${view.stats.tokens.input.toLocaleString()} tokens`)}
					{/if}
					{#if isUsageReported(view.stats, "output", view.stats.tokens.output)}
						{@render usageRow("Output", `${view.stats.tokens.output.toLocaleString()} tokens`)}
					{/if}
					{#if isUsageReported(view.stats, "cacheRead", view.stats.tokens.cacheRead)}
						{@render usageRow("Cache read", `${view.stats.tokens.cacheRead.toLocaleString()} tokens`)}
					{/if}
					{#if isUsageReported(view.stats, "cacheWrite", view.stats.tokens.cacheWrite)}
						{@render usageRow("Cache write", `${view.stats.tokens.cacheWrite.toLocaleString()} tokens`)}
					{/if}
					{#if isUsageReported(view.stats, "total", view.stats.tokens.total)}
						{@render usageRow("Total", `${view.stats.tokens.total.toLocaleString()} tokens`)}
					{/if}
					{#if isUsageReported(view.stats, "cost", view.stats.cost)}
						{@render usageRow("Cost", formatCost(view.stats))}
					{/if}
				</dl>
			</div>
		</div>
	</div>
{/if}

<style>
	.session-stats-contents { display: contents; }
	.session-stats-value { text-align: end; font-variant-numeric: tabular-nums; }
	.session-stats-nowrap { flex-wrap: nowrap; }
	.session-stats-justify-end { justify-content: end; }
	.session-stats-trigger-padding { padding-block: var(--space-2xs); }
	.session-stats-trigger { border: 0; background: transparent; }
	.session-stats-trigger:hover { background: var(--control-bg-hovered); color: var(--text-default); }
	:global(.session-stats-icon) { width: 0.875rem; height: 0.875rem; color: var(--primary); }
	.session-stats-popover { width: min(90vw, 22rem); }
	.session-stats-progress-track { height: var(--space-xs); overflow: hidden; border-radius: 999px; background: var(--control-bg-selected); }
	.session-stats-progress { height: 100%; border-radius: 999px; background: var(--primary); }
	.session-stats-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); column-gap: var(--space-lg); row-gap: var(--space-xs); }
</style>
