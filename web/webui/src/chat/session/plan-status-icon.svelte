<script lang="ts">
import Icon from "../../components/icon.svelte";
import type { PlanIconStatus } from "./session-plan";

interface Props {
	kind: PlanIconStatus;
}
let { kind }: Props = $props();
</script>

{#if kind === "done"}
	<Icon name="check" size={16} class="plan-status-primary" />
{:else if kind === "active"}
	<Icon name="circle-dot" size={16} class="plan-status-primary" />
{:else}
	<Icon name="circle" size={16} class="plan-status-muted" />
{/if}

<style>
	:global(.plan-status-primary) { flex-shrink: 0; color: var(--primary); }
	:global(.plan-status-muted) { flex-shrink: 0; color: var(--text-muted); }
</style>
