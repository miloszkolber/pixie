<script lang="ts">
import type { SessionStats } from "@pixie/contracts";
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
	<div class="contents">
		<dt class="text-text-muted">{label}</dt>
		<dd class="text-right text-text-default tabular-nums">{value}</dd>
	</div>
{/snippet}

{#if view}
	<div class="contents" {@attach mewa(popoverBehavior)}>
		<button
			type="button"
			popovertarget={popoverId}
			data-testid="usage-tracker"
			class="flex shrink-0 flex-nowrap items-center justify-end gap-x-xs rounded-[var(--radius-sm)] px-xs py-0.5 text-text-muted tr-text-metadata hover:bg-control-bg-hovered hover:text-text-default"
			aria-label="Open session usage"
		>
			<Icon name="gauge" size={14} class="size-3.5 text-primary" />
			{#if isUsageReported(view.stats, "total", view.stats.tokens.total)}
				<span>{formatTokens(view.stats.tokens.total)} tokens</span>
			{/if}
			{#if view.context}<span>{view.context.text}</span>{/if}
		</button>
		<div
			id={popoverId}
			popover="auto"
			class="popover w-[min(90vw,22rem)] p-md"
			data-align="end"
		>
			<div data-testid="session-stats" class="flex flex-col gap-md">
				<div>
					<div class="tr-text-ui text-text-default">Session usage</div>
					<div class="text-text-muted tr-text-metadata">
						Reported by the connected agent for this controller runtime
					</div>
				</div>
				{#if view.stats.contextUsage}
					<div class="flex flex-col gap-xs">
						<div class="flex items-center justify-between tr-text-metadata">
							<span class="text-text-default">Context window</span>
							<span class="text-text-muted">{view.context?.text}</span>
						</div>
						<div
							role="progressbar"
							aria-label="Context window used"
							aria-valuemin={0}
							aria-valuemax={100}
							aria-valuenow={Math.round(Math.min(100, Math.max(0, view.progress)))}
							class="h-1.5 overflow-hidden rounded-full bg-control-bg-selected"
						>
							<div
								class="h-full rounded-full bg-primary"
								style:width={`${Math.min(100, Math.max(0, view.progress))}%`}
							></div>
						</div>
					</div>
				{/if}
				<dl class="grid grid-cols-2 gap-x-lg gap-y-xs tr-text-metadata">
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
