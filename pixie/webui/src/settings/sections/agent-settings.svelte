<script lang="ts">
import type { AgentProfile } from "@pixie/contracts";
import { agentOperationRows } from "./agent-settings";

interface Props {
	profile: AgentProfile;
}

let { profile }: Props = $props();
let operations = $derived(agentOperationRows(profile));
</script>

<div class="mx-auto flex w-full max-w-[36rem] flex-col gap-lg">
	<div>
		<h2 class="tr-title-entity text-text-default">{profile.name || "Connected agent"}</h2>
		<p class="mt-xs tr-text-ui text-text-muted">
			{profile.version ? `Version ${profile.version} · ` : ""}
			{profile.compatible ? "Compatible with Pixie" : "Missing required capabilities"}
		</p>
	</div>
	{#if profile.missingRequired.length > 0}
		<div class="callout" data-variant="caution">
			<div class="callout-content">
				<h3 class="callout-title">Required capabilities</h3>
				<ul class="mt-xs list-disc pl-lg tr-text-metadata text-text-muted">
					{#each profile.missingRequired as capability (capability)}
						<li><code>{capability}</code></li>
					{/each}
				</ul>
			</div>
		</div>
	{/if}
	<div>
		<h3 class="tr-text-ui text-text-default">Optional capabilities</h3>
		<dl class="mt-sm divide-y divide-border-muted rounded-[var(--radius-sm)] border border-border-default">
			{#each operations as operation (operation.operation)}
				<div class="flex items-center justify-between gap-md px-md py-sm">
					<dt class="tr-text-ui text-text-default">{operation.label}</dt>
					<dd
						class={`tr-text-metadata ${operation.available ? "text-feedback-success" : "text-text-muted"}`}
					>
						{operation.available ? "Available" : "Unavailable"}
					</dd>
				</div>
			{/each}
		</dl>
	</div>
</div>
