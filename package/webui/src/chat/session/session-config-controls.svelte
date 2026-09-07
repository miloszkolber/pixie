<script lang="ts">
import type { SessionConfigOption } from "@pixie/contracts";
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
 <label class="flex min-w-0 items-center gap-xs tr-text-metadata">
  <span class="sr-only">{option.name || option.id}</span>
  <select data-testid="session-config-select" data-config-id={option.id} class="min-w-0 max-w-44 rounded-[var(--radius-sm)] border border-border-default bg-control-bg px-xs text-text-default" value={option.currentValue} disabled={busy} onchange={(event) => void change(option.id,event.currentTarget.value)}>
   {#if !option.options?.some(choice => choice.value === option.currentValue)}<option value={String(option.currentValue ?? "")} disabled>{option.currentValue || "Unknown current value"}</option>{/if}
   {#each option.options ?? [] as choice (choice.value)}<option value={choice.value}>{choice.name || choice.value}</option>{/each}
  </select>
 </label>
{/each}
{#if error}<p role="alert" class="text-feedback-error tr-text-metadata">{error}</p>{/if}
