<script lang="ts">
import type { SessionConfigOption } from "@pixie/shared";
import { errorText, getTransport } from "../../connection";
let { sessionId, options }: { sessionId: string; options: readonly SessionConfigOption[] } =
	$props();
let busy = $state(false);
let error = $state<string | null>(null);
let selects = $derived(
	options.filter((option) => option.type === "select" && option.options?.length),
);
async function change(id: string, value: string) {
	if (busy) return;
	busy = true;
	error = null;
	try {
		await getTransport().request("session.setConfigOption", { sessionId, configId: id, value });
	} catch (cause) {
		error = errorText(cause);
	} finally {
		busy = false;
	}
}
</script>
{#each selects as option (option.id)}
 <label class="u-flex u-min-w-0 u-items-center u-gap-xs tr-text-metadata">
  <span class="u-sr-only">{option.name || option.id}</span>
  <select data-testid="session-config-select" data-config-id={option.id} class="u-min-w-0 session-config-select u-rounded u-border u-border-border-default u-bg-control-bg u-px-xs u-text-text-default" value={option.currentValue} disabled={busy} onchange={(event) => void change(option.id,event.currentTarget.value)}>
   {#if !option.options?.some(choice => choice.value === option.currentValue)}<option value={String(option.currentValue ?? "")} disabled>{option.currentValue || "Unknown current value"}</option>{/if}
   {#each option.options ?? [] as choice (choice.value)}<option value={choice.value}>{choice.name || choice.value}</option>{/each}
  </select>
 </label>
{/each}
 {#if error}<p role="alert" class="u-text-feedback-error tr-text-metadata">{error}</p>{/if}

<style>
	.session-config-select { max-width: 11rem; }
</style>
