<script lang="ts">
import Icon from "../../components/icon.svelte";
import type { PlanIconStatus } from "./session-plan";

interface Props {
	kind: PlanIconStatus;
}
let { kind }: Props = $props();
</script>

{#if kind === "done"}
	<Icon name="check" size={16} class="shrink-0 text-primary" />
{:else if kind === "active"}
	<Icon name="circle-dot" size={16} class="shrink-0 text-primary" />
{:else}
	<Icon name="circle" size={16} class="shrink-0 text-text-muted" />
{/if}
